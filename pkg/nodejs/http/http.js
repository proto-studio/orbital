// http - HTTP/1.1 client and server over node:net sockets.
(function(global) {
  'use strict';

  const http = global.__http_module;
  const net = global.__net_module;
  const EventEmitter = global.__events_module;
  const StreamModule = global.__stream_module;
  const { Readable, Stream } = StreamModule;
  const nativeRequest = http._doRequest;
  const useSockets = http._useSockets;
  delete http._doRequest;
  delete http._useSockets;

  const STATUS_CODES = http.STATUS_CODES;
  const kMaxHeaderSize = 16384;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  const tokenRe = /^[\^_`a-zA-Z\-0-9!#$%&'*+.|~]+$/;
  const invalidHeaderValueRe = /[^\t\x20-\x7e\x80-\xff]/;
  const invalidPathRe = /[^!-~\x80-\xff]/;
  const requestTargetRe = /^(?:\/|\*|[a-zA-Z]+:\/\/)/;
  const validVersions = new Set(['0.9', '1.0', '1.1', '2.0']);
  // RFC 9112 chunk-ext, with no whitespace around its separators.
  const chunkExtRe = /^(?:;[!#$%&'*+\-.^_`|~0-9A-Za-z]+(?:=(?:[!#$%&'*+\-.^_`|~0-9A-Za-z]+|"(?:[\t \x21\x23-\x5b\x5d-\x7e\x80-\xff]|\\[\t\x20-\x7e\x80-\xff])*"))?)*$/;

  function isToken(s) {
    return typeof s === 'string' && tokenRe.test(s);
  }

  function validateHeaderName(name) {
    if (!isToken(name)) {
      throw codedError(TypeError, 'ERR_INVALID_HTTP_TOKEN', `Header name must be a valid HTTP token ["${name}"]`);
    }
  }

  function validateHeaderValue(name, value) {
    if (value === undefined) {
      throw codedError(TypeError, 'ERR_HTTP_INVALID_HEADER_VALUE', `Invalid value "${value}" for header "${name}"`);
    }
    const check = (v) => {
      if (invalidHeaderValueRe.test(String(v))) {
        throw codedError(TypeError, 'ERR_INVALID_CHAR', `Invalid character in header content ["${name}"]`);
      }
    };
    if (Array.isArray(value)) value.forEach(check);
    else check(value);
  }

  // Message parser. It is strict about everything that decides where a message ends,
  // because a disagreement with another hop about that is request smuggling.
  function parseError(code, message) {
    const err = new Error(`Parse Error: ${message}`);
    err.code = code;
    err.reason = message;
    return err;
  }

  const CR = 13;
  const LF = 10;

  // Handlers run user callbacks while the parser is on the stack. Their exceptions are
  // theirs, and must not be mistaken for a malformed message.
  function isParseError(err) {
    return !!err && typeof err.code === 'string' && err.code.startsWith('HPE_');
  }

  class Parser {
    constructor(type, handlers, options) {
      this.type = type;
      this.handlers = handlers;
      this.maxHeaderSize = (options && options.maxHeaderSize) || kMaxHeaderSize;
      this.pending = null;
      this.failed = null;
      this.reset();
    }

    reset() {
      this.state = 'head';
      this.remaining = 0;
      this.chunked = false;
      this.untilClose = false;
      this.closeAfter = false;
      this.paused = false;
    }

    // Consumes data, and returns any bytes left over after a protocol switch. Once a message
    // has been rejected nothing after it can be trusted, so the rejection is final.
    execute(data) {
      if (this.failed) throw this.failed;
      try {
        return this.consume(data);
      } catch (err) {
        if (isParseError(err)) this.failed = err;
        throw err;
      }
    }

    consume(data) {
      this.pending = this.pending && this.pending.length ? Buffer.concat([this.pending, data]) : data;
      while (this.pending && this.pending.length > 0) {
        if (this.state === 'head') {
          if (!this.parseHead()) return null;
        } else if (this.state === 'body') {
          this.parseBody();
        } else if (this.state === 'chunk-size') {
          if (!this.parseChunkSize()) return null;
        } else if (this.state === 'chunk-data') {
          this.parseChunkData();
        } else if (this.state === 'chunk-end') {
          if (!this.parseChunkEnd()) return null;
        } else if (this.state === 'trailers') {
          if (!this.parseTrailers()) return null;
        } else if (this.state === 'upgraded') {
          const rest = this.pending;
          this.pending = null;
          return rest;
        } else if (this.state === 'closed') {
          // The last request has been read. Whatever follows is not a request.
          this.pending = null;
        }
      }
      return this.state === 'upgraded' ? Buffer.alloc(0) : null;
    }

    // The peer has closed. A body with no framing ends here.
    finish() {
      if (this.failed) return null;
      if (this.state === 'body' && this.untilClose) {
        this.complete();
        return null;
      }
      if (this.state === 'head' && (!this.pending || this.pending.length === 0)) return null;
      if (this.state === 'upgraded' || this.state === 'closed') return null;
      return parseError('HPE_INVALID_EOF_STATE', 'Invalid EOF state');
    }

    findLineEnd(from) {
      return this.pending.indexOf(LF, from);
    }

    parseHead() {
      // Ignore empty lines before a request, as HTTP allows for robustness.
      if (this.type === 'request') {
        let start = 0;
        while (start < this.pending.length && (this.pending[start] === CR || this.pending[start] === LF)) start++;
        if (start > 0) this.pending = this.pending.slice(start);
        if (this.pending.length === 0) return false;
      }

      const end = this.findHeadEnd();
      if (end === -1) {
        if (this.pending.length > this.maxHeaderSize) {
          throw parseError('HPE_HEADER_OVERFLOW', 'Header overflow');
        }
        return false;
      }
      if (end > this.maxHeaderSize) {
        throw parseError('HPE_HEADER_OVERFLOW', 'Header overflow');
      }

      const text = this.pending.slice(0, end).toString('latin1');
      this.pending = this.pending.slice(end + 4);
      const lines = this.splitLines(text);
      const first = lines.shift();
      const info = this.type === 'request' ? this.parseRequestLine(first) : this.parseStatusLine(first);
      info.rawHeaders = this.parseHeaderLines(lines);
      this.applyFraming(info);
      return true;
    }

    // Finds the end of the head. Lines must end in CRLF, so no other hop can read different
    // lines from the same bytes.
    findHeadEnd() {
      const end = this.pending.indexOf('\r\n\r\n', 0, 'latin1');
      const scan = end === -1 ? this.pending.length : end;
      for (let at = this.pending.indexOf(LF); at !== -1 && at < scan; at = this.pending.indexOf(LF, at + 1)) {
        if (at === 0 || this.pending[at - 1] !== CR) {
          throw parseError('HPE_INVALID_HEADER_TOKEN', 'Missing expected CR after header value');
        }
      }
      return end;
    }

    splitLines(text) {
      const lines = text.split('\r\n');
      for (const line of lines) {
        if (line.includes('\r')) {
          throw parseError('HPE_INVALID_HEADER_TOKEN', 'Invalid header token');
        }
      }
      return lines;
    }

    parseRequestLine(line) {
      const m = /^([^ ]+) ([^ ]+) HTTP\/(\d)\.(\d)$/.exec(line);
      if (!m) {
        if (!/^[^ ]+ /.test(line)) throw parseError('HPE_INVALID_METHOD', 'Invalid method encountered');
        if (!/^[^ ]+ [^ ]+ /.test(line)) throw parseError('HPE_INVALID_VERSION', 'Invalid HTTP version');
        throw parseError('HPE_INVALID_VERSION', 'Expected HTTP/');
      }
      if (!http.METHODS.includes(m[1])) {
        throw parseError('HPE_INVALID_METHOD', 'Invalid method encountered');
      }
      if (invalidPathRe.test(m[2])) {
        throw parseError('HPE_INVALID_URL', 'Invalid characters in url');
      }
      if (m[1] !== 'CONNECT' && !requestTargetRe.test(m[2])) {
        throw parseError('HPE_INVALID_URL', 'Unexpected start char in url');
      }
      if (!validVersions.has(`${m[3]}.${m[4]}`)) {
        throw parseError('HPE_INVALID_VERSION', 'Invalid HTTP version');
      }
      return { method: m[1], url: m[2], major: +m[3], minor: +m[4] };
    }

    parseStatusLine(line) {
      const m = /^HTTP\/(\d)\.(\d) (\d{3})(?: (.*))?$/.exec(line);
      if (!m) throw parseError('HPE_INVALID_CONSTANT', 'Expected HTTP/');
      if (!validVersions.has(`${m[1]}.${m[2]}`)) {
        throw parseError('HPE_INVALID_VERSION', 'Invalid HTTP version');
      }
      return { statusCode: +m[3], statusMessage: m[4] || '', major: +m[1], minor: +m[2] };
    }

    parseHeaderLines(lines) {
      const raw = [];
      for (const line of lines) {
        if (line === '') continue;
        if (line[0] === ' ' || line[0] === '\t') {
          // Folded lines are obsolete, and another hop might not unfold them the same way.
          throw parseError('HPE_INVALID_HEADER_TOKEN', 'Invalid header token');
        }
        const colon = line.indexOf(':');
        if (colon <= 0) throw parseError('HPE_INVALID_HEADER_TOKEN', 'Invalid header token');
        const name = line.slice(0, colon);
        if (!tokenRe.test(name)) throw parseError('HPE_INVALID_HEADER_TOKEN', 'Invalid header token');
        const value = line.slice(colon + 1).replace(/^[ \t]+|[ \t]+$/g, '');
        if (invalidHeaderValueRe.test(value)) {
          throw parseError('HPE_INVALID_HEADER_TOKEN', 'Invalid header value char');
        }
        // Hops disagree about a trailing tab on the headers that decide where a message ends.
        if (/\t[ \t]*$/.test(line)) {
          const lower = name.toLowerCase();
          if (lower === 'content-length') {
            throw parseError('HPE_INVALID_CONTENT_LENGTH', 'Invalid character in Content-Length');
          }
          if (lower === 'transfer-encoding') {
            throw parseError('HPE_INVALID_TRANSFER_ENCODING', 'Invalid `Transfer-Encoding` header value');
          }
        }
        raw.push(name, value);
      }
      return raw;
    }

    // Decides how this message's body is framed.
    applyFraming(info) {
      let contentLength = null;
      let transferEncoding = null;
      let connection = '';
      let upgradeHeader = false;
      const raw = info.rawHeaders;
      for (let i = 0; i < raw.length; i += 2) {
        const name = raw[i].toLowerCase();
        const value = raw[i + 1];
        if (name === 'content-length') {
          if (!/^\d+$/.test(value)) {
            throw parseError('HPE_INVALID_CONTENT_LENGTH', 'Invalid character in Content-Length');
          }
          if (contentLength !== null) {
            throw parseError('HPE_UNEXPECTED_CONTENT_LENGTH', 'Content-Length can\'t be present more than once');
          }
          contentLength = Number(value);
          // Past this, two different lengths could look equal and a body could not be counted.
          if (!Number.isSafeInteger(contentLength)) {
            throw parseError('HPE_INVALID_CONTENT_LENGTH', 'Content-Length overflow');
          }
        } else if (name === 'transfer-encoding') {
          transferEncoding = transferEncoding === null ? value : `${transferEncoding}, ${value}`;
        } else if (name === 'connection') {
          connection = connection ? `${connection}, ${value}` : value;
        } else if (name === 'upgrade') {
          upgradeHeader = true;
        }
      }

      const connectionTokens = connection.toLowerCase().split(',').map((t) => t.trim());
      // Asked for both, close wins: the safe reading if another hop saw only that.
      info.shouldKeepAlive = !connectionTokens.includes('close')
        && ((info.major === 1 && info.minor >= 1) || connectionTokens.includes('keep-alive'));
      const wantsUpgrade = upgradeHeader && connectionTokens.includes('upgrade');

      let chunked = false;
      let unframedResponse = false;
      if (transferEncoding !== null) {
        const codings = transferEncoding.toLowerCase().split(',').map((t) => t.trim());
        const lastIsChunked = codings[codings.length - 1] === 'chunked';
        if (this.type === 'request' && (!lastIsChunked || codings.indexOf('chunked') !== codings.length - 1)) {
          throw parseError('HPE_INVALID_TRANSFER_ENCODING', 'Request has invalid `Transfer-Encoding`');
        }
        if (contentLength !== null) {
          throw parseError('HPE_UNEXPECTED_CONTENT_LENGTH', 'Content-Length can\'t be present with Transfer-Encoding');
        }
        chunked = lastIsChunked;
        unframedResponse = !lastIsChunked;
      }

      if (this.type === 'request') {
        info.upgrade = info.method === 'CONNECT' || wantsUpgrade;
      } else {
        info.upgrade = (info.statusCode === 101 && wantsUpgrade);
      }

      const skipBody = this.handlers.onHeaders(info);
      if (skipBody === 'upgrade') {
        this.state = 'upgraded';
        return;
      }
      this.closeAfter = this.type === 'request' && !info.shouldKeepAlive;
      if (info.statusCode >= 100 && info.statusCode < 200 && info.statusCode !== 101) {
        this.state = 'head';
        return;
      }
      if (skipBody === true || info.statusCode === 204 || info.statusCode === 304 || info.statusCode === 101) {
        this.complete();
        return;
      }
      if (chunked) {
        this.chunked = true;
        this.state = 'chunk-size';
      } else if (contentLength !== null && contentLength > 0) {
        this.remaining = contentLength;
        this.state = 'body';
      } else if (contentLength === 0) {
        this.complete();
      } else if (this.type === 'response' || unframedResponse) {
        this.untilClose = true;
        this.state = 'body';
        info.shouldKeepAlive = false;
      } else {
        this.complete();
      }
    }

    parseBody() {
      if (this.untilClose) {
        const chunk = this.pending;
        this.pending = null;
        this.handlers.onBody(chunk);
        return;
      }
      const take = Math.min(this.remaining, this.pending.length);
      const chunk = this.pending.slice(0, take);
      this.pending = this.pending.slice(take);
      this.remaining -= take;
      this.handlers.onBody(chunk);
      if (this.remaining === 0) this.complete();
    }

    parseChunkSize() {
      const end = this.findLineEnd(0);
      if (end === -1) {
        if (this.pending.length > 8192) throw parseError('HPE_CHUNK_EXTENSIONS_OVERFLOW', 'Chunk extensions overflow');
        return false;
      }
      let line = this.pending.slice(0, end).toString('latin1');
      if (!line.endsWith('\r')) throw parseError('HPE_CR_EXPECTED', 'Expected CRLF after chunk size');
      line = line.slice(0, -1);
      const m = /^0*([0-9a-fA-F]+)(;.*)?$/.exec(line);
      if (!m) throw parseError('HPE_INVALID_CHUNK_SIZE', 'Invalid character in chunk size');
      if (m[1].length > 12) throw parseError('HPE_INVALID_CHUNK_SIZE', 'Chunk size overflow');
      if (m[2] && !chunkExtRe.test(m[2])) throw parseError('HPE_STRICT', 'Invalid character in chunk extensions');
      this.remaining = parseInt(m[1], 16);
      this.pending = this.pending.slice(end + 1);
      this.state = this.remaining === 0 ? 'trailers' : 'chunk-data';
      this.trailerText = [];
      return true;
    }

    parseChunkData() {
      const take = Math.min(this.remaining, this.pending.length);
      const chunk = this.pending.slice(0, take);
      this.pending = this.pending.slice(take);
      this.remaining -= take;
      this.handlers.onBody(chunk);
      if (this.remaining === 0) this.state = 'chunk-end';
    }

    parseChunkEnd() {
      if (this.pending.length < 2) {
        if (this.pending[0] !== CR) throw parseError('HPE_STRICT', 'Expected CRLF after chunk');
        return false;
      }
      if (this.pending[0] !== CR || this.pending[1] !== LF) {
        throw parseError('HPE_STRICT', 'Expected CRLF after chunk');
      }
      this.pending = this.pending.slice(2);
      this.state = 'chunk-size';
      return true;
    }

    parseTrailers() {
      while (true) {
        const end = this.findLineEnd(0);
        if (end === -1) {
          if (this.pending.length > this.maxHeaderSize) throw parseError('HPE_HEADER_OVERFLOW', 'Header overflow');
          return false;
        }
        let line = this.pending.slice(0, end).toString('latin1');
        if (!line.endsWith('\r')) throw parseError('HPE_CR_EXPECTED', 'Expected CRLF after trailer');
        line = line.slice(0, -1);
        this.pending = this.pending.slice(end + 1);
        if (line === '') {
          const raw = this.parseHeaderLines(this.trailerText);
          this.checkTrailers(raw);
          this.handlers.onTrailers(raw);
          this.complete();
          return true;
        }
        this.trailerText.push(line);
        if (this.pending.length === 0) return false;
      }
    }

    // A trailer must not frame a message, and whatever reads these later might believe one.
    checkTrailers(raw) {
      for (let i = 0; i < raw.length; i += 2) {
        const name = raw[i].toLowerCase();
        if (name === 'transfer-encoding') {
          throw parseError('HPE_INVALID_TRANSFER_ENCODING', 'Transfer-Encoding can\'t be a trailer');
        }
        if (name === 'content-length' && !/^\d+$/.test(raw[i + 1])) {
          throw parseError('HPE_INVALID_CONTENT_LENGTH', 'Invalid character in Content-Length');
        }
      }
    }

    complete() {
      const wasUpgraded = this.state === 'upgraded';
      this.state = this.closeAfter ? 'closed' : 'head';
      this.remaining = 0;
      this.chunked = false;
      this.untilClose = false;
      if (!wasUpgraded) this.handlers.onComplete();
    }
  }

  // Header lists for message.headers follow Node's rules: set-cookie stays an array, a
  // fixed set of single-value headers keep the first occurrence, cookie joins with "; ",
  // and everything else joins with ", ".
  const singleValueHeaders = new Set([
    'age', 'authorization', 'content-length', 'content-type', 'etag', 'expires', 'from',
    'host', 'if-modified-since', 'if-unmodified-since', 'last-modified', 'location',
    'max-forwards', 'proxy-authorization', 'referer', 'retry-after', 'server', 'user-agent'
  ]);

  function hasOwn(obj, key) {
    return Object.prototype.hasOwnProperty.call(obj, key);
  }

  function headersFromRaw(raw, joinDuplicates) {
    const headers = {};
    for (let i = 0; i < raw.length; i += 2) {
      const name = raw[i].toLowerCase();
      const value = raw[i + 1];
      // A peer picks these names, and 'constructor' must not find Object.prototype's.
      const seen = hasOwn(headers, name);
      if (joinDuplicates) {
        headers[name] = seen ? `${headers[name]},${value}` : value;
      } else if (name === 'set-cookie') {
        if (seen) headers[name].push(value);
        else headers[name] = [value];
      } else if (!seen) {
        headers[name] = value;
      } else if (singleValueHeaders.has(name)) {
        continue;
      } else if (name === 'cookie') {
        headers[name] += `; ${value}`;
      } else {
        headers[name] += `, ${value}`;
      }
    }
    return headers;
  }

  function distinctFromRaw(raw) {
    const out = {};
    for (let i = 0; i < raw.length; i += 2) {
      const name = raw[i].toLowerCase();
      // Assigning an array here would replace the object's prototype.
      if (name === '__proto__') continue;
      if (hasOwn(out, name)) out[name].push(raw[i + 1]);
      else out[name] = [raw[i + 1]];
    }
    return out;
  }

  /**
   * IncomingMessage: a request on a server, or a response on a client.
   */
  class IncomingMessage extends Readable {
    constructor(socket) {
      super({
        autoDestroy: false,
        highWaterMark: socket && socket.readableHighWaterMark
      });
      this.socket = socket;
      this.httpVersionMajor = null;
      this.httpVersionMinor = null;
      this.httpVersion = null;
      this.complete = false;
      this.rawHeaders = [];
      this.rawTrailers = [];
      this.joinDuplicateHeaders = false;
      this.aborted = false;
      this.upgrade = null;
      this.url = '';
      this.method = null;
      this.statusCode = null;
      this.statusMessage = null;
      this.client = socket;
      this._headers = null;
      this._trailers = null;
      this._consuming = false;
      this._dumped = false;
    }

    get connection() { return this.socket; }
    set connection(v) { this.socket = v; }

    get headers() {
      if (!this._headers) this._headers = headersFromRaw(this.rawHeaders, this.joinDuplicateHeaders);
      return this._headers;
    }
    set headers(v) { this._headers = v; }
    get headersDistinct() { return distinctFromRaw(this.rawHeaders); }
    get trailers() {
      if (!this._trailers) this._trailers = headersFromRaw(this.rawTrailers, this.joinDuplicateHeaders);
      return this._trailers;
    }
    set trailers(v) { this._trailers = v; }
    get trailersDistinct() { return distinctFromRaw(this.rawTrailers); }

    setTimeout(msecs, callback) {
      if (callback) this.on('timeout', callback);
      if (this.socket) this.socket.setTimeout(msecs);
      return this;
    }

    _read(n) {
      if (!this._consuming) {
        this._consuming = true;
      }
      // Backpressure: the socket was paused when this message's buffer filled.
      if (this.socket && this.socket.readable) readStart(this.socket);
    }

    _destroy(err, cb) {
      if (!this.readableEnded || !this.complete) {
        this.aborted = true;
        this.emit('aborted');
      }
      if (this.socket && !this.socket.destroyed && this.aborted) this.socket.destroy(err);
      // As in Node, a premature close is only an error if someone is listening for it.
      cb(this.listenerCount('error') === 0 ? null : err);
    }

    // Discards the body, so the connection can move on to the next message.
    _dump() {
      if (!this._dumped) {
        this._dumped = true;
        this.removeAllListeners('data');
        this.resume();
      }
    }
  }

  function readStart(socket) {
    if (socket && socket.readable) socket.resume();
  }

  function readStop(socket) {
    if (socket) socket.pause();
  }

  function isCookieField(name) {
    return name.length === 6 && name.toLowerCase() === 'cookie';
  }

  const kNoBodyMethods = new Set(['GET', 'HEAD', 'DELETE', 'OPTIONS', 'TRACE', 'CONNECT']);
  const kChunkEnd = '\r\n';
  const kLastChunk = '0\r\n';

  /**
   * OutgoingMessage: a response on a server, or a request on a client.
   */
  class OutgoingMessage extends Stream {
    constructor(options) {
      super();
      this.outputData = [];
      this.outputSize = 0;
      this.writable = true;
      this.destroyed = false;
      this._last = false;
      this.chunkedEncoding = false;
      this.shouldKeepAlive = true;
      this.maxRequestsOnConnectionReached = false;
      this._defaultKeepAlive = true;
      this.useChunkedEncodingByDefault = true;
      this.sendDate = false;
      this._removedConnection = false;
      this._removedContLen = false;
      this._removedTE = false;
      this.strictContentLength = false;
      this._contentLength = null;
      this._hasBody = true;
      this._trailer = '';
      this._headerSent = false;
      this._header = null;
      this._headers = null;
      this._rawHeaderNames = null;
      this.finished = false;
      this._onPendingData = null;
      this.socket = null;
      this._keepAliveTimeout = 0;
      this._maxRequestsPerSocket = 0;
      this._writableFinished = false;
      this._corked = 0;
      this._highWaterMark = (options && options.highWaterMark) || StreamModule.getDefaultHighWaterMark();
      this._needDrain = false;
      this.errored = null;
      this._closed = false;
    }

    get connection() { return this.socket; }
    set connection(v) { this.socket = v; }
    get headersSent() { return !!this._header; }
    get writableFinished() {
      return this.finished && this.outputSize === 0 && (!this.socket || this.socket.writableLength === 0);
    }
    get writableObjectMode() { return false; }
    get writableLength() { return this.outputSize + (this.socket ? this.socket.writableLength : 0); }
    get writableHighWaterMark() { return this.socket ? this.socket.writableHighWaterMark : this._highWaterMark; }
    get writableCorked() { return this.socket ? this.socket.writableCorked : this._corked; }
    get writableEnded() { return this.finished; }
    get writableNeedDrain() { return !this.destroyed && !this.finished && this._needDrain; }
    get closed() { return this._closed; }

    cork() {
      if (this.socket) this.socket.cork();
      else this._corked++;
    }

    uncork() {
      if (this.socket) this.socket.uncork();
      else if (this._corked) this._corked--;
    }

    setTimeout(msecs, callback) {
      if (callback) this.on('timeout', callback);
      if (this.socket) this.socket.setTimeout(msecs);
      else this.once('socket', (socket) => socket.setTimeout(msecs));
      return this;
    }

    destroy(error) {
      if (this.destroyed) return this;
      this.destroyed = true;
      this.errored = error || null;
      if (this.socket) this.socket.destroy(error);
      else {
        this.once('socket', (socket) => socket.destroy(error));
      }
      return this;
    }

    setHeader(name, value) {
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot set headers after they are sent to the client');
      }
      validateHeaderName(name);
      validateHeaderValue(name, value);
      if (!this._headers) this._headers = Object.create(null);
      this._headers[name.toLowerCase()] = [name, value];
      return this;
    }

    setHeaders(headers) {
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot set headers after they are sent to the client');
      }
      const entries = typeof headers.entries === 'function' ? headers.entries() : Object.entries(headers);
      for (const [name, value] of entries) this.setHeader(name, value);
      return this;
    }

    appendHeader(name, value) {
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot set headers after they are sent to the client');
      }
      validateHeaderName(name);
      validateHeaderValue(name, value);
      const key = name.toLowerCase();
      if (!this._headers) this._headers = Object.create(null);
      const existing = this._headers[key];
      if (!existing) {
        this._headers[key] = [name, value];
      } else {
        const current = Array.isArray(existing[1]) ? existing[1] : [existing[1]];
        this._headers[key] = [existing[0], current.concat(value)];
      }
      return this;
    }

    getHeader(name) {
      if (typeof name !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.');
      }
      if (!this._headers) return undefined;
      const entry = this._headers[name.toLowerCase()];
      return entry === undefined ? undefined : entry[1];
    }

    getHeaderNames() {
      return this._headers ? Object.keys(this._headers) : [];
    }

    getRawHeaderNames() {
      return this._headers ? Object.values(this._headers).map((e) => e[0]) : [];
    }

    getHeaders() {
      const out = Object.create(null);
      if (this._headers) {
        for (const key of Object.keys(this._headers)) out[key] = this._headers[key][1];
      }
      return out;
    }

    hasHeader(name) {
      if (typeof name !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.');
      }
      return !!this._headers && this._headers[name.toLowerCase()] !== undefined;
    }

    removeHeader(name) {
      if (typeof name !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "name" argument must be of type string.');
      }
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot remove headers after they are sent to the client');
      }
      const key = name.toLowerCase();
      if (key === 'connection') this._removedConnection = true;
      else if (key === 'content-length') this._removedContLen = true;
      else if (key === 'transfer-encoding') this._removedTE = true;
      else if (key === 'date') this.sendDate = false;
      if (this._headers) delete this._headers[key];
    }

    flushHeaders() {
      if (!this._header) this._implicitHeader();
      this._send('');
    }

    // Writes one framed piece: to the socket if this message is current, or to a queue.
    _send(data, encoding, callback) {
      if (!this._headerSent) {
        const header = this._header;
        if (typeof data === 'string' && (encoding === 'utf8' || encoding === 'latin1' || !encoding)) {
          data = header + data;
        } else {
          this.outputData.unshift({ data: header, encoding: 'latin1', callback: null });
          this.outputSize += header.length;
          if (this._onPendingData) this._onPendingData(header.length);
        }
        this._headerSent = true;
      }
      return this._writeRaw(data, encoding, callback);
    }

    _writeRaw(data, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = null;
      }
      const socket = this.socket;
      if (socket && socket._httpMessage === this && socket.writable) {
        if (this.outputData.length) this._flushOutput(socket);
        return socket.write(data, encoding, callback);
      }
      if (socket && socket.destroyed) {
        if (callback) process.nextTick(callback, codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed'));
        return false;
      }
      this.outputData.push({ data, encoding, callback });
      this.outputSize += data.length;
      if (this._onPendingData) this._onPendingData(data.length);
      return this.outputSize < this.writableHighWaterMark;
    }

    _flushOutput(socket) {
      while (this._corked) {
        socket.cork();
        this._corked--;
      }
      const pending = this.outputData;
      this.outputData = [];
      let ret;
      socket.cork();
      for (const { data, encoding, callback } of pending) {
        ret = socket.write(data, encoding, callback);
      }
      socket.uncork();
      if (this._onPendingData) this._onPendingData(-this.outputSize);
      this.outputSize = 0;
      return ret;
    }

    _flush() {
      const socket = this.socket;
      if (socket && socket.writable) {
        const ret = this._flushOutput(socket);
        if (this.finished) {
          this._finish();
        } else if (ret && this._needDrain) {
          this._needDrain = false;
          this.emit('drain');
        }
      }
    }

    _storeHeader(firstLine, headers) {
      const state = { connection: false, contLen: false, te: false, date: false, expect: false, trailer: false, header: firstLine };

      const addLine = (name, value) => {
        if (Array.isArray(value)) {
          if (value.length < 2 || !isCookieField(name)) {
            for (const v of value) processLine(name, v);
          } else {
            processLine(name, value.join('; '));
          }
        } else {
          processLine(name, value);
        }
      };
      const processLine = (name, value) => {
        // Every form of headers ends up here, so nothing reaches the wire unchecked.
        validateHeaderName(name);
        validateHeaderValue(name, value);
        const lower = name.toLowerCase();
        if (lower === 'connection') {
          state.connection = true;
          this._removedConnection = false;
          if (/(^|[\s,])close($|[\s,])/i.test(value)) this._last = true;
          else this.shouldKeepAlive = true;
        } else if (lower === 'transfer-encoding') {
          state.te = true;
          this._removedTE = false;
          if (/(^|,)[ \t]*chunked[ \t]*$/i.test(value)) this.chunkedEncoding = true;
        } else if (lower === 'content-length') {
          state.contLen = true;
          this._contentLength = +value;
          this._removedContLen = false;
        } else if (lower === 'date' || lower === 'expect' || lower === 'trailer') {
          state[lower] = true;
        } else if (lower === 'keep-alive') {
          this._defaultKeepAlive = false;
        }
        state.header += `${name}: ${value}\r\n`;
      };

      if (headers) {
        if (headers === this._headers) {
          for (const key of Object.keys(headers)) addLine(headers[key][0], headers[key][1]);
        } else if (Array.isArray(headers)) {
          if (headers.length && Array.isArray(headers[0])) {
            for (const entry of headers) addLine(entry[0], entry[1]);
          } else {
            for (let i = 0; i < headers.length; i += 2) addLine(headers[i], headers[i + 1]);
          }
        } else {
          for (const key of Object.keys(headers)) addLine(key, headers[key]);
        }
      }

      let { header } = state;
      if (this.sendDate && !state.date) header += `Date: ${new Date().toUTCString()}\r\n`;

      if ((this.statusCode === 204 || this.statusCode === 304) && this.chunkedEncoding) {
        this.chunkedEncoding = false;
      }

      if (this._removedConnection) {
        this._last = !this.shouldKeepAlive;
      } else if (!state.connection) {
        const shouldSendKeepAlive = this.shouldKeepAlive && (state.contLen || this.useChunkedEncodingByDefault || this.agent);
        if (this._last) {
          header += 'Connection: close\r\n';
        } else if (shouldSendKeepAlive) {
          header += 'Connection: keep-alive\r\n';
          if (this._keepAliveTimeout && this._defaultKeepAlive) {
            const timeoutSeconds = Math.floor(this._keepAliveTimeout / 1000);
            let max = '';
            if (this._maxRequestsPerSocket > 0) max = `, max=${this._maxRequestsPerSocket}`;
            header += `Keep-Alive: timeout=${timeoutSeconds}${max}\r\n`;
          }
        } else {
          this._last = true;
          header += 'Connection: close\r\n';
        }
      }

      if (!state.contLen && !state.te) {
        if (!this._hasBody) {
          this.chunkedEncoding = false;
        } else if (!this.useChunkedEncodingByDefault) {
          this._last = true;
        } else if (!state.trailer && !this._removedContLen && typeof this._contentLength === 'number') {
          header += `Content-Length: ${this._contentLength}\r\n`;
        } else if (!this._removedTE) {
          header += 'Transfer-Encoding: chunked\r\n';
          this.chunkedEncoding = true;
        } else {
          this._last = true;
        }
      }

      this._header = `${header}\r\n`;
      this._headerSent = false;
      if (state.expect) this._send('');
    }

    write(chunk, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = null;
      }
      const ret = this._writeChunk(chunk, encoding, callback, false);
      if (!ret) this._needDrain = true;
      return ret;
    }

    _writeChunk(chunk, encoding, callback, fromEnd) {
      if (chunk === null) {
        throw codedError(TypeError, 'ERR_STREAM_NULL_VALUES', 'May not write null values to stream');
      }
      let len;
      if (typeof chunk === 'string') {
        len = Buffer.byteLength(chunk, encoding);
      } else if (chunk instanceof Uint8Array) {
        len = chunk.length;
      } else {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
          'The "chunk" argument must be of type string or an instance of Buffer or Uint8Array.');
      }

      let err;
      if (this.finished) err = codedError(Error, 'ERR_STREAM_WRITE_AFTER_END', 'write after end');
      else if (this.destroyed) err = codedError(Error, 'ERR_STREAM_DESTROYED', 'Cannot call write after a stream was destroyed');
      if (err) {
        if (!this.destroyed) {
          process.nextTick(() => {
            this.emit('error', err);
          });
        }
        if (typeof callback === 'function') process.nextTick(callback, err);
        return false;
      }

      if (!this._header) {
        if (fromEnd) this._contentLength = len;
        this._implicitHeader();
      }

      if (!this._hasBody) {
        if (typeof callback === 'function') process.nextTick(callback);
        return true;
      }

      if (!fromEnd && this.socket && !this.socket.writableCorked) {
        this.socket.cork();
        process.nextTick(uncorkSocket, this.socket);
      }

      if (this.chunkedEncoding && chunk.length !== 0) {
        this._send(`${len.toString(16)}\r\n`, 'latin1');
        this._send(chunk, encoding);
        return this._send(kChunkEnd, 'latin1', callback);
      }
      return this._send(chunk, encoding, callback);
    }

    addTrailers(headers) {
      this._trailer = '';
      const entries = Array.isArray(headers) ? headers : Object.entries(headers);
      for (const [field, value] of entries) {
        if (!isToken(field)) {
          throw codedError(TypeError, 'ERR_INVALID_HTTP_TOKEN', `Trailer name must be a valid HTTP token ["${field}"]`);
        }
        if (invalidHeaderValueRe.test(String(value))) {
          throw codedError(TypeError, 'ERR_INVALID_CHAR', `Invalid character in trailer content ["${field}"]`);
        }
        this._trailer += `${field}: ${value}\r\n`;
      }
    }

    end(chunk, encoding, callback) {
      if (typeof chunk === 'function') {
        callback = chunk;
        chunk = null;
        encoding = null;
      } else if (typeof encoding === 'function') {
        callback = encoding;
        encoding = null;
      }

      if (chunk) {
        if (this.finished) {
          const err = codedError(Error, 'ERR_STREAM_WRITE_AFTER_END', 'write after end');
          if (typeof callback === 'function') process.nextTick(callback, err);
          return this;
        }
        if (this.socket) this.socket.cork();
        this._writeChunk(chunk, encoding, null, true);
      } else if (this.finished) {
        if (typeof callback === 'function') {
          if (!this.writableFinished) {
            this.on('finish', callback);
          } else {
            process.nextTick(callback, codedError(Error, 'ERR_STREAM_ALREADY_FINISHED', 'Cannot call end after a stream was finished'));
          }
        }
        return this;
      } else if (!this._header) {
        if (this.socket) this.socket.cork();
        this._contentLength = 0;
        this._implicitHeader();
      }

      if (typeof callback === 'function') this.once('finish', callback);

      const finish = () => this._onLastWritten();
      if (this._hasBody && this.chunkedEncoding) {
        this._send(`${kLastChunk}${this._trailer}\r\n`, 'latin1', finish);
      } else if (!this._headerSent || this.writableLength || chunk) {
        this._send('', 'latin1', finish);
      } else {
        process.nextTick(finish);
      }

      if (this.socket) this.socket.uncork();
      this._needDrain = false;
      this.finished = true;

      if (this.outputData.length === 0 && this.socket && this.socket._httpMessage === this) {
        this._finish();
      }
      return this;
    }

    _onLastWritten() {
      if (this._finishEmitted) return;
      this._finishEmitted = true;
      this.emit('finish');
    }

    _finish() {
      this.emit('prefinish');
    }
  }

  function uncorkSocket(socket) {
    if (socket) socket.uncork();
  }

  OutgoingMessage.prototype._implicitHeader = function() {
    throw codedError(Error, 'ERR_METHOD_NOT_IMPLEMENTED', 'The _implicitHeader() method is not implemented');
  };

  /**
   * Agent keeps connections alive for reuse.
   */
  class Agent extends EventEmitter {
    constructor(options) {
      super();
      this.defaultPort = 80;
      this.protocol = 'http:';
      this.options = { __proto__: null, ...options };
      if (this.options.noDelay === undefined) this.options.noDelay = true;
      this.options.path = null;
      this.requests = { __proto__: null };
      this.sockets = { __proto__: null };
      this.freeSockets = { __proto__: null };
      this.keepAliveMsecs = this.options.keepAliveMsecs || 1000;
      this.keepAlive = this.options.keepAlive || false;
      this.maxSockets = this.options.maxSockets || Agent.defaultMaxSockets;
      this.maxFreeSockets = this.options.maxFreeSockets || 256;
      this.scheduling = this.options.scheduling || 'lifo';
      this.maxTotalSockets = this.options.maxTotalSockets || Infinity;
      this.totalSocketCount = 0;

      this.on('free', (socket, freeOptions) => this._onFree(socket, freeOptions));
    }

    _onFree(socket, options) {
      const name = this.getName(options);
      if (!socket.writable) {
        socket.destroy();
        return;
      }
      const requests = this.requests[name];
      if (requests && requests.length) {
        const req = requests.shift();
        if (requests.length === 0) delete this.requests[name];
        setRequestSocket(this, req, socket);
        return;
      }
      if (!req0Keep(socket)) {
        socket.destroy();
        return;
      }
      const freeSockets = this.freeSockets[name] || [];
      const freeLen = freeSockets.length;
      let count = freeLen;
      if (this.sockets[name]) count += this.sockets[name].length;

      if (this.keepAlive && count <= this.maxSockets && freeLen < this.maxFreeSockets && this.keepSocketAlive(socket)) {
        this.freeSockets[name] = freeSockets;
        socket._httpMessage = null;
        this.removeSocket(socket, options);
        // removeSocket counted this socket out. It stays in the pool.
        this.totalSocketCount++;
        freeSockets.push(socket);
      } else {
        socket.destroy();
      }
    }

    createConnection(...args) {
      return net.createConnection(...args);
    }

    getName(options) {
      let name = options.host || 'localhost';
      name += ':';
      if (options.port) name += options.port;
      name += ':';
      if (options.localAddress) name += options.localAddress;
      if (options.family === 4 || options.family === 6) name += `:${options.family}`;
      if (options.socketPath) name += `:${options.socketPath}`;
      return name;
    }

    addRequest(req, options, port, localAddress) {
      if (typeof options === 'string') {
        options = { __proto__: null, host: options, port, localAddress };
      }
      options = { __proto__: null, ...options, ...this.options };
      if (options.socketPath) options.path = options.socketPath;
      normalizeServerName(options, req);

      const name = this.getName(options);
      this.sockets[name] = this.sockets[name] || [];
      const freeSockets = this.freeSockets[name];
      let socket;
      if (freeSockets) {
        while (freeSockets.length && freeSockets[0].destroyed) freeSockets.shift();
        socket = this.scheduling === 'fifo' ? freeSockets.shift() : freeSockets.pop();
        if (!freeSockets.length) delete this.freeSockets[name];
      }
      const sockLen = this.sockets[name].length + (freeSockets ? freeSockets.length : 0);

      if (socket) {
        this.reuseSocket(socket, req);
        setRequestSocket(this, req, socket);
        this.sockets[name].push(socket);
      } else if (sockLen < this.maxSockets && this.totalSocketCount < this.maxTotalSockets) {
        this.createSocket(req, options, (err, newSocket) => {
          if (err) req.onSocket(newSocket, err);
          else setRequestSocket(this, req, newSocket);
        });
      } else {
        if (!this.requests[name]) this.requests[name] = [];
        req[kRequestOptions] = options;
        this.requests[name].push(req);
      }
    }

    createSocket(req, options, cb) {
      options = { __proto__: null, ...options, ...this.options };
      if (options.socketPath) options.path = options.socketPath;
      normalizeServerName(options, req);
      const name = this.getName(options);
      options._agentKey = name;
      options.encoding = null;

      const oncreate = (err, socket) => {
        if (called) return;
        called = true;
        if (err) {
          cb(err);
          return;
        }
        this.sockets[name] = this.sockets[name] || [];
        this.sockets[name].push(socket);
        this.totalSocketCount++;
        installListeners(this, socket, options);
        cb(null, socket);
      };
      let called = false;
      const created = this.createConnection(options, oncreate);
      if (created) oncreate(null, created);
    }

    removeSocket(socket, options) {
      const name = this.getName(options);
      for (const sets of [this.sockets, this.freeSockets]) {
        const list = sets[name];
        if (list) {
          const index = list.indexOf(socket);
          if (index !== -1) {
            list.splice(index, 1);
            this.totalSocketCount--;
            if (list.length === 0) delete sets[name];
          }
        }
      }
      const requests = this.requests[name];
      if (requests && requests.length) {
        const req = requests[0];
        const reqOptions = req[kRequestOptions] || options;
        this.createSocket(req, reqOptions, (err, newSocket) => {
          if (err) {
            req.onSocket(newSocket, err);
            return;
          }
          requests.shift();
          if (requests.length === 0) delete this.requests[name];
          setRequestSocket(this, req, newSocket);
        });
      }
    }

    keepSocketAlive(socket) {
      socket.setKeepAlive(true, this.keepAliveMsecs);
      socket.unref();
      let agentTimeout = this.options.timeout || 0;
      if (socket._httpMessage && socket._httpMessage.res) {
        const keepAliveHint = socket._httpMessage.res.headers['keep-alive'];
        if (keepAliveHint) {
          const hint = /timeout=(\d+)/.exec(keepAliveHint);
          if (hint) {
            const serverHintTimeout = Number(hint[1]) * 1000 - 1000;
            if (serverHintTimeout > 0) agentTimeout = agentTimeout ? Math.min(agentTimeout, serverHintTimeout) : serverHintTimeout;
          }
        }
      }
      if (socket.timeout !== agentTimeout) socket.setTimeout(agentTimeout);
      return true;
    }

    reuseSocket(socket, req) {
      req.reusedSocket = true;
      socket.ref();
    }

    destroy() {
      for (const sets of [this.freeSockets, this.sockets]) {
        for (const name of Object.keys(sets)) {
          for (const socket of sets[name].slice()) socket.destroy();
        }
      }
    }
  }
  Agent.defaultMaxSockets = Infinity;

  const kRequestOptions = Symbol('kRequestOptions');

  function req0Keep(socket) {
    const req = socket._httpMessage;
    return !!req && !!req.shouldKeepAlive && !socket.destroyed;
  }

  function normalizeServerName(options, req) {
    if (!options.servername && options.servername !== '') {
      const hostHeader = req && req.getHeader && req.getHeader('host');
      let name = options.host;
      if (hostHeader && typeof hostHeader === 'string') {
        name = hostHeader[0] === '[' ? hostHeader.slice(1, hostHeader.indexOf(']')) : hostHeader.split(':')[0];
      }
      if (name && !net.isIP(name)) options.servername = name;
    }
  }

  function installListeners(agent, socket, options) {
    function onFree() {
      agent.emit('free', socket, options);
    }
    socket.on('free', onFree);

    function onClose() {
      agent.removeSocket(socket, options);
    }
    socket.on('close', onClose);

    function onTimeout() {
      const sockets = agent.freeSockets;
      if (Object.keys(sockets).some((name) => sockets[name].includes(socket))) {
        socket.destroy();
        agent.removeSocket(socket, options);
      }
    }
    socket.on('timeout', onTimeout);

    function onRemove() {
      agent.removeSocket(socket, options);
      socket.removeListener('close', onClose);
      socket.removeListener('free', onFree);
      socket.removeListener('timeout', onTimeout);
      socket.removeListener('agentRemove', onRemove);
    }
    socket.on('agentRemove', onRemove);
  }

  function setRequestSocket(agent, req, socket) {
    req.onSocket(socket);
    const agentTimeout = agent.options.timeout || 0;
    if (req.timeout === undefined || req.timeout === agentTimeout) return;
    socket.setTimeout(req.timeout);
  }

  // Lets a request go through an embedder's HTTPClient instead of a connection. Whatever
  // ClientRequest writes is parsed back into a request, and the answer is presented as
  // bytes on a socket, so the rest of the client is the same on both paths.
  class HTTPClientSocket extends StreamModule.Duplex {
    constructor(options) {
      super({ allowHalfOpen: true });
      this.connecting = false;
      this._options = options;
      this.encrypted = options.protocol === 'https:';
      this._sent = false;
      this._parts = [];
      this._parser = new Parser('request', {
        onHeaders: (info) => {
          this._info = info;
          return info.method === 'CONNECT' || (info.upgrade) ? 'upgrade' : false;
        },
        onBody: (chunk) => this._parts.push(chunk),
        onTrailers: () => {},
        onComplete: () => this._dispatch()
      });
      process.nextTick(() => {
        this.emit('connect');
        this.emit('ready');
      });
    }

    get timeout() { return 0; }
    setTimeout() { return this; }
    setNoDelay() { return this; }
    setKeepAlive() { return this; }
    ref() { return this; }
    unref() { return this; }
    address() { return {}; }

    _read() {}

    _write(chunk, encoding, callback) {
      const buf = typeof chunk === 'string' ? Buffer.from(chunk, encoding) : chunk;
      try {
        const leftover = this._parser.execute(buf);
        if (leftover !== null && !this._sent) {
          this._sent = true;
          callback(codedError(Error, 'ERR_HTTP_TRANSPORT_UNAVAILABLE',
            'the configured HTTPClient cannot switch protocols or open tunnels; set Config.SocketFactory and leave HTTPClient unset'));
          return;
        }
      } catch (err) {
        callback(err);
        return;
      }
      callback();
    }

    _destroy(err, cb) {
      this._cancelled = true;
      cb(err);
      process.nextTick(() => this.emit('close', !!err));
    }

    _dispatch() {
      if (this._sent) return;
      this._sent = true;
      const info = this._info;
      const options = this._options;
      const raw = info.rawHeaders;
      const headers = [];
      for (let i = 0; i < raw.length; i += 2) {
        const name = raw[i].toLowerCase();
        // These describe the connection, and the embedder's client manages its own.
        if (name === 'connection' || name === 'transfer-encoding') continue;
        headers.push([raw[i], raw[i + 1]]);
      }
      let host = options.host || options.hostname || 'localhost';
      // This text becomes a URL, where "allowed.test@other.test" would name one host to a
      // policy reading it and another to whatever connects.
      if (!net.isIP(host) && !/^[a-zA-Z0-9._-]+$/.test(host)) {
        const err = new Error(`getaddrinfo ENOTFOUND ${host}`);
        err.code = 'ENOTFOUND';
        err.syscall = 'getaddrinfo';
        err.hostname = host;
        this.destroy(err);
        return;
      }
      if (net.isIPv6(host)) host = `[${host}]`;
      const defaultPort = options.protocol === 'https:' ? 443 : 80;
      const port = options.port && +options.port !== defaultPort ? `:${options.port}` : '';
      const url = `${options.protocol || 'http:'}//${host}${port}${info.url}`;
      nativeRequest(JSON.stringify({
        method: info.method,
        url,
        headers,
        body: Buffer.concat(this._parts).toString('base64'),
        timeout: options.timeout || 0
      }), (error, encoded) => {
        if (this._cancelled) return;
        if (error) {
          const match = /^([A-Z][A-Z0-9_]+):/.exec(error);
          const err = new Error(error);
          if (match) err.code = match[1];
          this.destroy(err);
          return;
        }
        const response = JSON.parse(encoded);
        const body = Buffer.from(response.body, 'base64');
        let head = `HTTP/1.1 ${response.statusCode} ${response.statusMessage}\r\n`;
        let hasLength = false;
        for (const [name, value] of response.headers) {
          const lower = name.toLowerCase();
          if (lower === 'transfer-encoding' || lower === 'connection') continue;
          if (lower === 'content-length') {
            hasLength = true;
            head += `${name}: ${info.method === 'HEAD' ? value : body.length}\r\n`;
            continue;
          }
          head += `${name}: ${String(value).replace(/[\r\n]/g, ' ')}\r\n`;
        }
        if (!hasLength && response.statusCode !== 204 && response.statusCode !== 304) {
          head += `Content-Length: ${body.length}\r\n`;
        }
        head += 'Connection: close\r\n\r\n';
        this.push(Buffer.concat([Buffer.from(head, 'latin1'), body]));
        this.push(null);
      });
    }
  }

  /**
   * ClientRequest: an outgoing request.
   */
  class ClientRequest extends OutgoingMessage {
    constructor(input, options, cb) {
      super();

      if (typeof input === 'string') {
        input = urlToOptions(new URL(input));
      } else if (input instanceof URL) {
        input = urlToOptions(input);
      } else {
        cb = options;
        options = input;
        input = null;
      }
      if (typeof options === 'function') {
        cb = options;
        options = input || {};
      } else {
        options = { ...input, ...options };
      }

      let agent = options.agent;
      const defaultAgent = options._defaultAgent || http.globalAgent;
      if (agent === false) {
        agent = new defaultAgent.constructor();
      } else if (agent === null || agent === undefined) {
        if (typeof options.createConnection !== 'function') agent = defaultAgent;
      } else if (typeof agent.addRequest !== 'function') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.agent" property must be one of Agent-like Object, undefined, or false.');
      }
      this.agent = agent;

      const protocol = options.protocol || defaultAgent.protocol;
      let expectedProtocol = defaultAgent.protocol;
      if (agent && agent.protocol) expectedProtocol = agent.protocol;
      if (options.path) {
        const path = String(options.path);
        if (invalidPathRe.test(path)) {
          throw codedError(TypeError, 'ERR_UNESCAPED_CHARACTERS', 'Request path contains unescaped characters');
        }
      }
      if (protocol !== expectedProtocol) {
        throw codedError(TypeError, 'ERR_INVALID_PROTOCOL', `Protocol "${protocol}" not supported. Expected "${expectedProtocol}"`);
      }

      const defaultPort = options.defaultPort || (agent && agent.defaultPort);
      const port = options.port = options.port || defaultPort || 80;
      const host = options.host = validateHost(options.hostname, 'hostname') || validateHost(options.host, 'host') || 'localhost';
      const setHost = options.setHost === undefined || Boolean(options.setHost);

      this.socketPath = options.socketPath;
      if (options.timeout !== undefined) this.timeout = getTimerDuration(options.timeout);

      const method = options.method;
      if (method !== null && method !== undefined && typeof method !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.method" property must be of type string.');
      }
      if (method && !isToken(method)) {
        throw codedError(TypeError, 'ERR_INVALID_HTTP_TOKEN', `Method must be a valid HTTP token ["${method}"]`);
      }
      this.method = method ? method.toUpperCase() : 'GET';
      this.path = options.path || '/';
      if (cb) this.once('response', cb);

      if (kNoBodyMethods.has(this.method)) this.useChunkedEncodingByDefault = false;
      else this.useChunkedEncodingByDefault = true;
      this._ended = false;
      this.res = null;
      this.aborted = false;
      this.timeoutCb = null;
      this.upgradeOrConnect = false;
      this.parser = null;
      this.maxHeadersCount = null;
      this.reusedSocket = false;
      this.host = host;
      this.protocol = protocol;
      this.maxHeaderSize = options.maxHeaderSize;
      this.joinDuplicateHeaders = options.joinDuplicateHeaders;

      if (this.agent) {
        if (!this.agent.keepAlive && !Number.isFinite(this.agent.maxSockets)) {
          this._last = true;
          this.shouldKeepAlive = false;
        } else {
          this._last = false;
          this.shouldKeepAlive = true;
        }
      }

      const headersArray = Array.isArray(options.headers);
      if (!headersArray) {
        if (options.headers) {
          for (const key of Object.keys(options.headers)) {
            this.setHeader(key, options.headers[key]);
          }
        }
        if (host && !this.getHeader('host') && setHost) {
          let hostHeader = host;
          const posColon = hostHeader.indexOf(':');
          if (posColon !== -1 && hostHeader.includes(':', posColon + 1) && hostHeader.charCodeAt(0) !== 91) {
            hostHeader = `[${hostHeader}]`;
          }
          if (port && +port !== defaultPort) hostHeader += `:${port}`;
          this.setHeader('Host', hostHeader);
        }
        if (options.auth && !this.getHeader('Authorization')) {
          this.setHeader('Authorization', `Basic ${Buffer.from(options.auth).toString('base64')}`);
        }
        if (this.getHeader('expect')) {
          if (this._header) {
            throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot render headers after they are sent to the client');
          }
          this._storeHeader(`${this.method} ${this.path} HTTP/1.1\r\n`, this._headers);
        }
      } else {
        this._storeHeader(`${this.method} ${this.path} HTTP/1.1\r\n`, options.headers);
      }

      if (options.signal) this._addSignal(options.signal);

      const finalOptions = { ...options };

      // Without connection-level access, the embedder's HTTPClient does all the work. A
      // connection the script brings would go around it, and sending the request through
      // the client instead could deliver it somewhere the script did not ask for.
      if (!useSockets) {
        this.agent = undefined;
        if (typeof options.createConnection === 'function') {
          this._last = true;
          this.shouldKeepAlive = false;
          this.onSocket(null, codedError(Error, 'ERR_HTTP_TRANSPORT_UNAVAILABLE',
            'the configured HTTPClient cannot use createConnection; set Config.SocketFactory and leave HTTPClient unset'));
          return;
        }
        const socket = new HTTPClientSocket({ ...finalOptions, protocol });
        this._last = true;
        this.shouldKeepAlive = false;
        this.onSocket(socket);
        return;
      }

      if (this.agent) {
        this.agent.addRequest(this, finalOptions);
      } else {
        this._last = true;
        this.shouldKeepAlive = false;
        let opts = finalOptions;
        if (this.socketPath) opts = { ...opts, path: this.socketPath };
        let called = false;
        const oncreate = (err, socket) => {
          if (called) return;
          called = true;
          if (err) {
            process.nextTick(() => this.emit('error', err));
            return;
          }
          this.onSocket(socket);
        };
        const created = options.createConnection(opts, oncreate);
        if (created) oncreate(null, created);
      }
    }

    _addSignal(signal) {
      const onAbort = () => {
        const err = codedError(Error, 'ABORT_ERR', 'The operation was aborted');
        err.name = 'AbortError';
        err.cause = signal.reason;
        this.destroy(err);
      };
      if (signal.aborted) {
        process.nextTick(onAbort);
      } else {
        signal.addEventListener('abort', onAbort, { once: true });
        this.once('close', () => signal.removeEventListener('abort', onAbort));
      }
    }

    _implicitHeader() {
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot render headers after they are sent to the client');
      }
      this._storeHeader(`${this.method} ${this.path} HTTP/1.1\r\n`, this._headers);
    }

    abort() {
      if (this.aborted) return;
      this.aborted = true;
      process.nextTick(() => this.emit('abort'));
      this.destroy();
    }

    destroy(err) {
      if (this.destroyed) return this;
      this.destroyed = true;
      if (this.res) this.res._dump();
      this[kError] = err;
      if (this.socket) this.socket.destroy(err);
      return this;
    }

    onSocket(socket, err) {
      process.nextTick(() => this._onSocketNT(socket, err));
    }

    _onSocketNT(socket, err) {
      if (this.destroyed || err) {
        this[kError] = err || this[kError];
        if (this.agent) {
          if (socket) {
            socket.emit('free');
          }
        } else if (socket) {
          socket.destroy(this[kError]);
        }
        const error = this[kError] || (this.destroyed ? connResetException('socket hang up') : null);
        if (socket && !err) {
          socket.destroy();
        }
        if (error) this.emit('error', error);
        this._closed = true;
        this.emit('close');
        return;
      }
      this._attachSocket(socket);
    }

    _attachSocket(socket) {
      this.socket = socket;
      socket._httpMessage = this;
      this.emit('socket', socket);

      const req = this;
      const parser = new Parser('response', {
        onHeaders: (info) => this._onHeaders(info),
        onBody: (chunk) => {
          const res = this.res;
          if (res && !res._dumped) {
            if (!res.push(chunk)) readStop(socket);
          }
        },
        onTrailers: (raw) => { if (this.res) this.res.rawTrailers = raw; },
        onComplete: () => this._onMessageComplete()
      }, { maxHeaderSize: this.maxHeaderSize });
      this.parser = parser;

      const onData = (data) => {
        if (socket._httpMessage !== req) return;
        let leftover;
        try {
          leftover = parser.execute(data);
        } catch (err) {
          if (!isParseError(err)) throw err;
          err.rawPacket = data;
          socket.destroy();
          socket._hadError = true;
          req.emit('error', err);
          return;
        }
        if (leftover !== null && req.upgradeOrConnect) {
          this._finishUpgrade(leftover);
        }
      };
      const onEnd = () => {
        const finishErr = parser.finish();
        if (!req.res && !socket._hadError && finishErr) {
          socket._hadError = true;
          req.emit('error', connResetException('socket hang up'));
        }
        if (socket.writable) socket.end();
      };
      const onClose = () => {
        const res = req.res;
        req.destroyed = true;
        if (res) {
          if (!res.complete) {
            res.destroy(connResetException('aborted'));
          } else {
            closeWhenRead(res);
          }
          req._closed = true;
          process.nextTick(() => req.emit('close'));
        } else {
          if (!socket._hadError) {
            socket._hadError = true;
            req.emit('error', req[kError] || connResetException('socket hang up'));
          }
          req._closed = true;
          req.emit('close');
        }
        cleanup();
      };
      const onError = (err) => {
        if (req.res) return;
        socket._hadError = true;
        req.emit('error', err);
      };
      const onDrain = () => {
        if (req._needDrain) {
          req._needDrain = false;
          req.emit('drain');
        }
      };
      const onTimeout = () => {
        if (req.timeoutCb || req.listenerCount('timeout')) req.emit('timeout');
      };
      const cleanup = () => {
        socket.removeListener('data', onData);
        socket.removeListener('end', onEnd);
        socket.removeListener('close', onClose);
        socket.removeListener('error', onError);
        socket.removeListener('drain', onDrain);
        socket.removeListener('timeout', onTimeout);
      };
      this._detachSocket = cleanup;

      socket.on('data', onData);
      socket.on('end', onEnd);
      socket.on('close', onClose);
      socket.on('error', onError);
      socket.on('drain', onDrain);
      socket.on('timeout', onTimeout);

      if (socket.destroyed) {
        process.nextTick(onClose);
        return;
      }

      if (this.timeout !== undefined) {
        // Agents may have set their own timeout on a reused socket.
        const start = () => socket.setTimeout(this.timeout);
        if (socket.connecting) socket.once('connect', start);
        else start();
      }
      this._flush();
    }

    _onHeaders(info) {
      const socket = this.socket;
      const res = new this._IncomingMessageClass(socket);
      res.httpVersionMajor = info.major;
      res.httpVersionMinor = info.minor;
      res.httpVersion = `${info.major}.${info.minor}`;
      res.rawHeaders = info.rawHeaders;
      res.statusCode = info.statusCode;
      res.statusMessage = info.statusMessage;
      res.upgrade = info.upgrade;
      res.joinDuplicateHeaders = !!this.joinDuplicateHeaders;

      if (info.statusCode === 100) {
        this.emit('continue');
        return false;
      }
      if (info.statusCode > 100 && info.statusCode < 200 && info.statusCode !== 101) {
        this.emit('information', {
          statusCode: info.statusCode,
          statusMessage: info.statusMessage,
          httpVersion: res.httpVersion,
          httpVersionMajor: info.major,
          httpVersionMinor: info.minor,
          headers: res.headers,
          rawHeaders: res.rawHeaders
        });
        return false;
      }

      if (this.res) {
        // A second response on one request means the stream can't be trusted.
        socket.destroy();
        return true;
      }
      this.res = res;
      res.req = this;
      res.socket = socket;

      if (this.method === 'CONNECT') {
        res.upgrade = true;
        this.upgradeOrConnect = true;
        this._pendingUpgrade = res;
        return 'upgrade';
      }
      if (info.upgrade) {
        this.upgradeOrConnect = true;
        this._pendingUpgrade = res;
        return 'upgrade';
      }

      if (this.shouldKeepAlive && !info.shouldKeepAlive) this.shouldKeepAlive = false;

      const handled = this.emit('response', res);
      if (!handled) res._dump();

      res.on('end', () => this._onResponseEnd(res));
      if (this.method === 'HEAD') return true;
      return false;
    }

    _finishUpgrade(head) {
      const socket = this.socket;
      const res = this._pendingUpgrade;
      this._pendingUpgrade = null;
      this._detachSocket();
      socket.emit('agentRemove');
      socket._httpMessage = null;
      socket.readableFlowing = null;

      const event = this.method === 'CONNECT' ? 'connect' : 'upgrade';
      if (this.listenerCount(event) > 0) {
        this.upgradeOrConnect = true;
        res.complete = true;
        this.emit(event, res, socket, head);
        this.destroyed = true;
        this._closed = true;
        this.emit('close');
      } else {
        socket.destroy();
      }
    }

    _onMessageComplete() {
      const res = this.res;
      if (!res) return;
      res.complete = true;
      if (res.rawTrailers.length) res._trailers = null;
      res.push(null);
    }

    _onResponseEnd(res) {
      const socket = this.socket;
      if (!this.shouldKeepAlive) {
        if (socket.writable) {
          if (typeof socket.destroySoon === 'function') socket.destroySoon();
          else socket.end();
        }
      } else if (this.writableFinished && !this.timeoutCb) {
        this._releaseSocket();
      } else {
        this.once('finish', () => this._releaseSocket());
      }
    }

    // Hands a kept-alive socket back to its agent.
    _releaseSocket() {
      const socket = this.socket;
      if (socket._httpMessage !== this) return;
      this._detachSocket();
      this.destroyed = true;
      this._closed = true;
      // The response no longer owns the connection, which may serve another request.
      this.res.socket = null;
      closeWhenRead(this.res);
      process.nextTick(() => this.emit('close'));
      socket.removeAllListeners('timeout');
      socket.setTimeout(0);
      if (this.agent) {
        socket.emit('free');
      }
    }

    setTimeout(msecs, callback) {
      if (this._ended) return this;
      msecs = getTimerDuration(msecs);
      if (callback) this.once('timeout', callback);
      this.timeout = msecs;
      if (this.socket) {
        const start = () => this.socket.setTimeout(msecs);
        if (this.socket.connecting) this.socket.once('connect', start);
        else start();
      }
      return this;
    }

    setNoDelay(noDelay) {
      this._withSocket((socket) => socket.setNoDelay(noDelay));
    }

    setSocketKeepAlive(enable, initialDelay) {
      this._withSocket((socket) => socket.setKeepAlive(enable, initialDelay));
    }

    clearTimeout(callback) {
      this.setTimeout(0, callback);
    }

    _withSocket(fn) {
      if (this.socket) fn(this.socket);
      else this.once('socket', fn);
    }
  }

  // Emits a complete response's 'close' once its reader has finished with it.
  function closeWhenRead(res) {
    if (res.destroyed) return;
    if (res.readableEnded) res.destroy();
    else res.once('end', () => res.destroy());
  }

  const kError = Symbol('kError');
  const kResponse = Symbol('kResponse');
  ClientRequest.prototype._IncomingMessageClass = IncomingMessage;

  function getTimerDuration(msecs) {
    const value = Number(msecs);
    if (!(value >= 0)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE', `The value of "timeout" is out of range. Received ${msecs}`);
    }
    return value;
  }

  function validateHost(host, name) {
    if (host !== null && host !== undefined && typeof host !== 'string') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', `The "options.${name}" property must be of type string or null or undefined`);
    }
    return host;
  }

  function connResetException(message) {
    const err = new Error(message);
    err.code = 'ECONNRESET';
    return err;
  }

  function urlToOptions(url) {
    const options = {
      protocol: url.protocol,
      hostname: typeof url.hostname === 'string' && url.hostname.startsWith('[') ? url.hostname.slice(1, -1) : url.hostname,
      hash: url.hash,
      search: url.search,
      pathname: url.pathname,
      path: `${url.pathname || ''}${url.search || ''}`,
      href: url.href
    };
    if (url.port !== '') options.port = Number(url.port);
    if (url.username || url.password) {
      options.auth = `${decodeURIComponent(url.username)}:${decodeURIComponent(url.password)}`;
    }
    return options;
  }

  /**
   * ServerResponse: an outgoing response.
   */
  class ServerResponse extends OutgoingMessage {
    constructor(req, options) {
      super(options);
      this.statusCode = 200;
      this.statusMessage = undefined;
      this.req = req;
      this._req = req;
      if (req.method === 'HEAD') this._hasBody = false;
      this.sendDate = true;
      this._sent100 = false;
      this._expect_continue = false;
      if (req.httpVersionMajor < 1 || req.httpVersionMinor < 1) {
        this.useChunkedEncodingByDefault = false;
        this.shouldKeepAlive = false;
      }
    }

    assignSocket(socket) {
      if (socket._httpMessage) {
        throw codedError(Error, 'ERR_HTTP_SOCKET_ASSIGNED', 'ServerResponse has an already assigned socket');
      }
      socket._httpMessage = this;
      this.socket = socket;
      this.emit('socket', socket);
      this._flush();
    }

    detachSocket(socket) {
      socket._httpMessage = null;
      this.socket = null;
    }

    writeContinue(cb) {
      this._writeRaw('HTTP/1.1 100 Continue\r\n\r\n', 'ascii', cb);
      this._sent100 = true;
    }

    writeProcessing(cb) {
      this._writeRaw('HTTP/1.1 102 Processing\r\n\r\n', 'ascii', cb);
    }

    writeEarlyHints(hints, cb) {
      let head = 'HTTP/1.1 103 Early Hints\r\n';
      if (hints.link === null || hints.link === undefined) return;
      const link = Array.isArray(hints.link) ? hints.link.join(', ') : hints.link;
      if (invalidHeaderValueRe.test(link)) {
        throw codedError(TypeError, 'ERR_INVALID_CHAR', 'Invalid character in header content ["Link"]');
      }
      head += `Link: ${link}\r\n`;
      for (const key of Object.keys(hints)) {
        if (key !== 'link') {
          validateHeaderName(key);
          validateHeaderValue(key, hints[key]);
          head += `${key}: ${hints[key]}\r\n`;
        }
      }
      head += '\r\n';
      this._writeRaw(head, 'ascii', cb);
    }

    _implicitHeader() {
      this.writeHead(this.statusCode);
    }

    writeHead(statusCode, reason, obj) {
      if (this._header) {
        throw codedError(Error, 'ERR_HTTP_HEADERS_SENT', 'Cannot write headers after they are sent to the client');
      }
      const originalStatusCode = statusCode;
      statusCode |= 0;
      if (statusCode < 100 || statusCode > 999) {
        throw codedError(RangeError, 'ERR_HTTP_INVALID_STATUS_CODE', `Invalid status code: ${originalStatusCode}`);
      }

      if (typeof reason === 'string') {
        this.statusMessage = reason;
      } else {
        this.statusMessage = this.statusMessage || STATUS_CODES[statusCode] || 'unknown';
        obj = reason;
      }
      this.statusCode = statusCode;

      let headers;
      if (this._headers) {
        if (obj) {
          if (Array.isArray(obj)) {
            if (obj.length % 2 !== 0 && !Array.isArray(obj[0])) {
              throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', 'The argument \'headers\' is invalid.');
            }
            if (Array.isArray(obj[0])) {
              for (const [k, v] of obj) this.appendHeader(k, v);
            } else {
              for (let n = 0; n < obj.length; n += 2) this.appendHeader(obj[n], obj[n + 1]);
            }
          } else {
            for (const k of Object.keys(obj)) {
              if (k) this.setHeader(k, obj[k]);
            }
          }
        }
        headers = this._headers;
      } else {
        headers = obj;
        if (headers && !Array.isArray(headers)) {
          for (const k of Object.keys(headers)) {
            validateHeaderName(k);
            validateHeaderValue(k, headers[k]);
          }
        } else if (Array.isArray(headers)) {
          const flat = Array.isArray(headers[0]) ? headers.flat() : headers;
          for (let i = 0; i < flat.length; i += 2) {
            validateHeaderName(flat[i]);
            validateHeaderValue(flat[i], flat[i + 1]);
          }
        }
      }

      if (invalidHeaderValueRe.test(this.statusMessage) || /[\r\n]/.test(this.statusMessage)) {
        throw codedError(TypeError, 'ERR_INVALID_CHAR', 'Invalid character in statusMessage');
      }

      const statusLine = `HTTP/1.1 ${statusCode} ${this.statusMessage}\r\n`;
      if (statusCode === 204 || statusCode === 304 || (statusCode >= 100 && statusCode <= 199)) {
        this._hasBody = false;
      }
      if (this._expect_continue && !this._sent100) this.shouldKeepAlive = false;
      this._storeHeader(statusLine, headers);
      return this;
    }
  }

  // Everything an HTTP server does on a connection. https.Server shares it over TLS.
  function setupServer(server, options) {
    options = options || {};
    server.IncomingMessage = options.IncomingMessage || IncomingMessage;
    server.ServerResponse = options.ServerResponse || ServerResponse;
    server.maxHeaderSize = options.maxHeaderSize;
    server.joinDuplicateHeaders = options.joinDuplicateHeaders;
    server.requireHostHeader = options.requireHostHeader === undefined ? true : !!options.requireHostHeader;
    server.timeout = 0;
    server.maxHeadersCount = null;
    server.maxRequestsPerSocket = options.maxRequestsPerSocket || 0;
    server.headersTimeout = options.headersTimeout === undefined ? 60000 : options.headersTimeout;
    server.keepAliveTimeout = options.keepAliveTimeout === undefined ? 5000 : options.keepAliveTimeout;
    server.requestTimeout = options.requestTimeout === undefined ? 300000 : options.requestTimeout;
    server._connectionsCheckingInterval = options.connectionsCheckingInterval === undefined ? 30000 : options.connectionsCheckingInterval;
    server._httpConnections = new Set();
    server._checkTimer = null;
    server.on('listening', () => {
      if (server._checkTimer || (!server.headersTimeout && !server.requestTimeout)) return;
      server._checkTimer = setInterval(() => checkConnections(server), server._connectionsCheckingInterval);
      server._checkTimer.unref();
    });
    server.on('close', () => {
      if (server._checkTimer) clearInterval(server._checkTimer);
      server._checkTimer = null;
    });
  }

  const serverMethods = {
    setTimeout(msecs, callback) {
      this.timeout = msecs;
      if (callback) this.on('timeout', callback);
      return this;
    },

    close(callback) {
      const result = net.Server.prototype.close.call(this, callback);
      this.closeIdleConnections();
      if (this._checkTimer) clearInterval(this._checkTimer);
      this._checkTimer = null;
      return result;
    },

    closeAllConnections() {
      for (const conn of this._httpConnections) conn.socket.destroy();
    },

    closeIdleConnections() {
      for (const conn of this._httpConnections) {
        if (conn.outgoing.length === 0 && !conn.incoming) conn.socket.destroy();
      }
    }
  };

  function checkConnections(server) {
    const now = Date.now();
    for (const conn of server._httpConnections) {
      if (conn.upgraded) continue;
      const expired =
        (server.headersTimeout > 0 && conn.headersStartedAt && now - conn.headersStartedAt > server.headersTimeout) ||
        (server.requestTimeout > 0 && conn.requestStartedAt && now - conn.requestStartedAt > server.requestTimeout);
      if (expired) {
        const err = codedError(Error, 'ERR_HTTP_REQUEST_TIMEOUT', 'Request timeout');
        conn.parserError(err, 408);
      }
    }
  }

  function connectionListener(server, socket) {
    const conn = {
      socket,
      outgoing: [],
      incoming: null,
      requests: 0,
      headersStartedAt: Date.now(),
      requestStartedAt: Date.now(),
      pendingBytes: 0,
      active: new Set(),
      upgraded: false,
      parserError: null
    };
    server._httpConnections.add(conn);

    if (server.timeout && typeof socket.setTimeout === 'function') socket.setTimeout(server.timeout);

    const parser = new Parser('request', {
      onHeaders: (info) => onRequestHeaders(info),
      onBody: (chunk) => {
        const req = conn.incoming;
        if (req && !req._dumped) {
          if (!req.push(chunk)) readStop(socket);
        }
      },
      onTrailers: (raw) => { if (conn.incoming) conn.incoming.rawTrailers = raw; },
      onComplete: () => {
        const req = conn.incoming;
        conn.incoming = null;
        conn.requestStartedAt = 0;
        conn.headersStartedAt = 0;
        if (req) {
          req.complete = true;
          req.push(null);
        }
      }
    }, { maxHeaderSize: server.maxHeaderSize });

    function onRequestHeaders(info) {
      conn.headersStartedAt = 0;
      const req = new server.IncomingMessage(socket);
      req.httpVersionMajor = info.major;
      req.httpVersionMinor = info.minor;
      req.httpVersion = `${info.major}.${info.minor}`;
      req.method = info.method;
      req.url = info.url;
      req.rawHeaders = info.rawHeaders;
      req.joinDuplicateHeaders = !!server.joinDuplicateHeaders;
      req.upgrade = info.upgrade;
      conn.incoming = req;
      conn.requests++;
      conn.active.add(req);

      if (info.upgrade) {
        const event = info.method === 'CONNECT' ? 'connect' : 'upgrade';
        if (server.listenerCount(event) > 0) {
          conn.upgraded = true;
          conn.pendingUpgrade = { req, event };
          return 'upgrade';
        }
        if (info.method === 'CONNECT') {
          socket.destroy();
          return true;
        }
        // No one wants the upgrade, so this is an ordinary request.
        req.upgrade = false;
      }

      if (server.maxRequestsPerSocket > 0 && conn.requests > server.maxRequestsPerSocket) {
        server.emit('dropRequest', req, socket);
        socket.end('HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\n\r\n', 'latin1');
        return true;
      }

      const res = new server.ServerResponse(req, { highWaterMark: socket.writableHighWaterMark });
      req[kResponse] = res;
      res.shouldKeepAlive = info.shouldKeepAlive && res.shouldKeepAlive;
      res._keepAliveTimeout = server.keepAliveTimeout;
      res._maxRequestsPerSocket = server.maxRequestsPerSocket;
      if (server.maxRequestsPerSocket > 0 && conn.requests >= server.maxRequestsPerSocket) {
        res.shouldKeepAlive = false;
      }
      res._onPendingData = (delta) => {
        // Responses waiting behind an earlier one stop further pipelined requests.
        conn.pendingBytes += delta;
        if (conn.pendingBytes > socket.writableHighWaterMark) readStop(socket);
        else readStart(socket);
      };

      res.on('finish', () => onResponseFinish(req, res));

      if (socket._httpMessage) {
        conn.outgoing.push(res);
      } else {
        res.assignSocket(socket);
      }

      if (info.major === 1 && info.minor === 1 && server.requireHostHeader && !hasOwn(req.headers, 'host')) {
        info.shouldKeepAlive = false;
        res.writeHead(400, ['Connection', 'close']);
        res.end();
        return false;
      }

      const expect = req.headers.expect;
      let handled = false;
      if (expect !== undefined && info.minor >= 1) {
        if (/(^|\W)100-continue(\W|$)/i.test(expect)) {
          res._expect_continue = true;
          if (server.listenerCount('checkContinue') > 0) {
            server.emit('checkContinue', req, res);
            handled = true;
          } else {
            res.writeContinue();
          }
        } else if (server.listenerCount('checkExpectation') > 0) {
          server.emit('checkExpectation', req, res);
          handled = true;
        } else {
          res.writeHead(417);
          res.end();
          handled = true;
        }
      }
      if (!handled) server.emit('request', req, res);
      return false;
    }

    function onResponseFinish(req, res) {
      // Whatever the handler left unread must not block the next request.
      if (!req._consuming) req._dump();

      conn.active.delete(req);
      res.detachSocket(socket);
      process.nextTick(() => {
        res.destroyed = true;
        res._closed = true;
        res.emit('close');
        // As in Node, the request closes after its response, and not before it has been read.
        if (req.readableEnded) req.destroy();
        else req.once('end', () => req.destroy());
      });

      if (res._last) {
        if (typeof socket.destroySoon === 'function') socket.destroySoon();
        else socket.end();
        return;
      }
      if (conn.outgoing.length) {
        conn.outgoing.shift().assignSocket(socket);
      } else if (server.keepAliveTimeout && typeof socket.setTimeout === 'function') {
        socket.setTimeout(server.keepAliveTimeout);
        conn.keepAliveTimeoutSet = true;
      }
      if (server._id === null && !conn.incoming && conn.outgoing.length === 0) socket.destroy();
    }

    // Requests whose responses have not finished end with their connection.
    function abortActive() {
      for (const req of conn.active) {
        if (req.destroyed) continue;
        if (!req.complete || !req.readableEnded) {
          req.destroy(connResetException('aborted'));
          continue;
        }
        // A fully read request has nothing to abort; Node has already closed it.
        const res = req[kResponse];
        if (res && res.finished && !res._closed) {
          // Its response has ended and only waits for the write callback, which the peer's FIN
          // can beat. Node's 'finish' comes at once for such a write, so the response closes first.
          res.once('close', () => req.destroy());
        } else {
          req.destroy();
        }
      }
      conn.active.clear();
    }

    conn.parserError = (err, status) => {
      if (socket.destroyed) return;
      if (server.listenerCount('clientError') > 0) {
        server.emit('clientError', err, socket);
      } else if (socket.writable && (!socket._httpMessage || !socket._httpMessage._headerSent)) {
        socket.end(`HTTP/1.1 ${status} ${STATUS_CODES[status]}\r\nConnection: close\r\n\r\n`, 'latin1');
        socket.once('finish', () => socket.destroy());
      } else {
        socket.destroy(err);
      }
    };

    socket.on('data', (data) => {
      if (conn.upgraded && !conn.pendingUpgrade) return;
      if (conn.keepAliveTimeoutSet) {
        socket.setTimeout(server.timeout || 0);
        conn.keepAliveTimeoutSet = false;
      }
      if (!conn.incoming && !conn.headersStartedAt) {
        conn.headersStartedAt = Date.now();
        conn.requestStartedAt = Date.now();
      }
      let leftover;
      try {
        leftover = parser.execute(data);
      } catch (err) {
        if (!isParseError(err)) throw err;
        err.bytesParsed = data.length;
        err.rawPacket = data;
        let status = 400;
        if (err.code === 'HPE_HEADER_OVERFLOW') status = 431;
        conn.parserError(err, status);
        return;
      }
      if (leftover !== null && conn.pendingUpgrade) {
        const { req, event } = conn.pendingUpgrade;
        conn.pendingUpgrade = null;
        conn.incoming = null;
        conn.active.delete(req);
        server._httpConnections.delete(conn);
        socket.removeAllListeners('data');
        socket.removeAllListeners('end');
        socket.removeAllListeners('close');
        socket.removeAllListeners('drain');
        socket.removeAllListeners('timeout');
        socket.removeAllListeners('error');
        socket.setTimeout(0);
        req.complete = true;
        req.upgrade = true;
        server.emit(event, req, socket, leftover);
      }
    });

    socket.on('end', () => {
      const finishErr = parser.finish();
      if (finishErr) {
        conn.parserError(finishErr, 400);
        return;
      }
      if (!server.httpAllowHalfOpen) {
        abortActive();
        if (socket.writable) socket.end();
      } else if (conn.outgoing.length) {
        conn.outgoing[conn.outgoing.length - 1]._last = true;
      } else if (socket._httpMessage) {
        socket._httpMessage._last = true;
      } else if (socket.writable) {
        socket.end();
      }
    });

    socket.on('close', () => {
      server._httpConnections.delete(conn);
      abortActive();
      const res = socket._httpMessage;
      if (res) {
        res.destroyed = true;
        res._closed = true;
        res.emit('close');
      }
      for (const queued of conn.outgoing.splice(0)) {
        queued.destroyed = true;
        queued._closed = true;
        queued.emit('close');
      }
    });

    socket.on('error', (err) => {
      if (!server.emit('clientError', err, socket)) socket.destroy(err);
    });
    socket.on('drain', () => {
      const res = socket._httpMessage;
      if (res && res._needDrain) {
        res._needDrain = false;
        res.emit('drain');
      }
    });
    socket.on('timeout', () => {
      const reqTimeout = conn.incoming && conn.incoming.emit('timeout');
      const res = socket._httpMessage;
      const resTimeout = res && res.emit('timeout', socket);
      const serverTimeout = server.emit('timeout', socket);
      if (!reqTimeout && !resTimeout && !serverTimeout) socket.destroy();
    });

  }

  class HTTPServer extends net.Server {
    constructor(options, requestListener) {
      if (typeof options === 'function') {
        requestListener = options;
        options = {};
      } else if (options === null || options === undefined) {
        options = {};
      } else if (typeof options !== 'object') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.');
      }
      super({
        allowHalfOpen: true,
        noDelay: options.noDelay === undefined ? true : options.noDelay,
        keepAlive: options.keepAlive,
        keepAliveInitialDelay: options.keepAliveInitialDelay,
        highWaterMark: options.highWaterMark
      });
      setupServer(this, options);
      if (requestListener) this.on('request', requestListener);
      this.httpAllowHalfOpen = false;
      this.on('connection', (socket) => connectionListener(this, socket));
    }
  }
  Object.assign(HTTPServer.prototype, serverMethods);

  // Node's http.Server can be called without new, and inherited from with util.inherits.
  function Server(options, requestListener) {
    return Reflect.construct(HTTPServer, [options, requestListener], new.target || HTTPServer);
  }
  Server.prototype = HTTPServer.prototype;
  HTTPServer.prototype.constructor = Server;
  Object.setPrototypeOf(Server, net.Server);

  function createServer(options, requestListener) {
    return new HTTPServer(options, requestListener);
  }

  function request(url, options, cb) {
    return new ClientRequest(url, options, cb);
  }

  function get(url, options, cb) {
    const req = request(url, options, cb);
    req.end();
    return req;
  }

  http.Agent = Agent;
  http.globalAgent = new Agent({ keepAlive: true, scheduling: 'lifo', timeout: 5000 });
  http.ClientRequest = ClientRequest;
  http.IncomingMessage = IncomingMessage;
  http.OutgoingMessage = OutgoingMessage;
  http.Server = Server;
  http.ServerResponse = ServerResponse;
  http.createServer = createServer;
  http.request = request;
  http.get = get;
  http.maxHeaderSize = kMaxHeaderSize;
  http.validateHeaderName = validateHeaderName;
  http.validateHeaderValue = validateHeaderValue;
  http.setMaxIdleHTTPParsers = () => {};
  // For node:https, which shares all of this over TLS.
  http._setupServer = setupServer;
  http._serverMethods = serverMethods;
  http._connectionListener = connectionListener;
})(globalThis);
