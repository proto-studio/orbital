// http2 - HTTP/2 module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const internal = global.__http2_internal;

  // HTTP/2 constants
  const constants = {
    // Error codes
    NGHTTP2_NO_ERROR: 0x00,
    NGHTTP2_PROTOCOL_ERROR: 0x01,
    NGHTTP2_INTERNAL_ERROR: 0x02,
    NGHTTP2_FLOW_CONTROL_ERROR: 0x03,
    NGHTTP2_SETTINGS_TIMEOUT: 0x04,
    NGHTTP2_STREAM_CLOSED: 0x05,
    NGHTTP2_FRAME_SIZE_ERROR: 0x06,
    NGHTTP2_REFUSED_STREAM: 0x07,
    NGHTTP2_CANCEL: 0x08,
    NGHTTP2_COMPRESSION_ERROR: 0x09,
    NGHTTP2_CONNECT_ERROR: 0x0a,
    NGHTTP2_ENHANCE_YOUR_CALM: 0x0b,
    NGHTTP2_INADEQUATE_SECURITY: 0x0c,
    NGHTTP2_HTTP_1_1_REQUIRED: 0x0d,

    // Settings
    NGHTTP2_SETTINGS_HEADER_TABLE_SIZE: 0x01,
    NGHTTP2_SETTINGS_ENABLE_PUSH: 0x02,
    NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS: 0x03,
    NGHTTP2_SETTINGS_INITIAL_WINDOW_SIZE: 0x04,
    NGHTTP2_SETTINGS_MAX_FRAME_SIZE: 0x05,
    NGHTTP2_SETTINGS_MAX_HEADER_LIST_SIZE: 0x06,

    // Default values
    DEFAULT_SETTINGS_HEADER_TABLE_SIZE: 4096,
    DEFAULT_SETTINGS_ENABLE_PUSH: 1,
    DEFAULT_SETTINGS_MAX_CONCURRENT_STREAMS: 100,
    DEFAULT_SETTINGS_INITIAL_WINDOW_SIZE: 65535,
    DEFAULT_SETTINGS_MAX_FRAME_SIZE: 16384,
    DEFAULT_SETTINGS_MAX_HEADER_LIST_SIZE: 65535,
    MAX_MAX_FRAME_SIZE: 16777215,
    MIN_MAX_FRAME_SIZE: 16384,
    MAX_INITIAL_WINDOW_SIZE: 2147483647,

    // Padding strategy
    PADDING_STRATEGY_NONE: 0,
    PADDING_STRATEGY_ALIGNED: 1,
    PADDING_STRATEGY_MAX: 2,
    PADDING_STRATEGY_CALLBACK: 1
  };

  // Sensitive headers that should not be indexed
  const sensitiveHeaders = Symbol('sensitiveHeaders');

  const kNoError = constants.NGHTTP2_NO_ERROR;
  const kCancel = constants.NGHTTP2_CANCEL;
  const kInternalError = constants.NGHTTP2_INTERNAL_ERROR;

  // Names reported in ERR_HTTP2_STREAM_ERROR messages, indexed by error code.
  const errorCodeNames = [
    'NGHTTP2_NO_ERROR', 'NGHTTP2_PROTOCOL_ERROR', 'NGHTTP2_INTERNAL_ERROR',
    'NGHTTP2_FLOW_CONTROL_ERROR', 'NGHTTP2_SETTINGS_TIMEOUT', 'NGHTTP2_STREAM_CLOSED',
    'NGHTTP2_FRAME_SIZE_ERROR', 'NGHTTP2_REFUSED_STREAM', 'NGHTTP2_CANCEL',
    'NGHTTP2_COMPRESSION_ERROR', 'NGHTTP2_CONNECT_ERROR', 'NGHTTP2_ENHANCE_YOUR_CALM',
    'NGHTTP2_INADEQUATE_SECURITY', 'NGHTTP2_HTTP_1_1_REQUIRED'
  ];

  // Connection-specific headers are not permitted in HTTP/2 (RFC 9113, 8.2.2).
  const connectionHeaders = new Set([
    'connection', 'upgrade', 'http2-settings', 'keep-alive', 'proxy-connection', 'transfer-encoding'
  ]);

  // Requests with these methods carry no body, so END_STREAM goes with the headers.
  const payloadlessMethods = new Set(['GET', 'HEAD', 'DELETE']);

  const kHighWaterMark = 16384;

  function makeError(message, code, ErrorClass) {
    const error = new (ErrorClass || Error)(message);
    if (code) error.code = code;
    return error;
  }

  function streamCancelError(cause) {
    const message = 'The pending stream has been canceled' +
      (cause ? ' (caused by: ' + cause.message + ')' : '');
    const error = makeError(message, 'ERR_HTTP2_STREAM_CANCEL');
    if (cause) error.cause = cause;
    return error;
  }

  function abortError(signal) {
    const error = makeError('The operation was aborted', 'ABORT_ERR');
    error.name = 'AbortError';
    if (signal && signal.reason !== undefined) error.cause = signal.reason;
    return error;
  }

  // Inactivity timer behind stream.setTimeout and session.setTimeout. Like Node it
  // only emits 'timeout'; the application decides what to do.
  class IdleTimer {
    constructor(owner) {
      this._owner = owner;
      this._ms = 0;
      this._timer = null;
      this._lastActive = 0;
    }

    set(ms) {
      this.clear();
      this._ms = ms;
      this.touch();
    }

    // Called for every chunk, so it only records the time. The one running timer
    // re-arms itself for the remainder when it finds there was activity.
    touch() {
      if (!this._ms) return;
      this._lastActive = Date.now();
      if (this._timer === null) this._arm(this._ms);
    }

    _arm(ms) {
      this._timer = setTimeout(() => {
        this._timer = null;
        const remaining = this._lastActive + this._ms - Date.now();
        if (remaining > 0) {
          this._arm(remaining);
          return;
        }
        this._owner.emit('timeout');
      }, ms);
    }

    clear() {
      this._ms = 0;
      if (this._timer !== null) clearTimeout(this._timer);
      this._timer = null;
    }
  }

  /**
   * Http2Stream - Base class for HTTP/2 streams.
   */
  class Http2Stream extends EventEmitter {
    constructor(session, id) {
      super();
      this._session = session;
      this._id = id;
      this._state = 'open';
      this._destroyed = false;
      this._closed = false;
      this._aborted = false;
      this._rstCode = kNoError;
      this._endAfterHeaders = false;
      this._sentHeaders = null;
      this._sentTrailers = null;
      this._sentInfoHeaders = [];
      this._idle = new IdleTimer(this);
    }

    get id() { return this._id; }
    get session() { return this._session; }
    get state() { return this._state; }
    get destroyed() { return this._destroyed; }
    get closed() { return this._closed; }
    get aborted() { return this._aborted; }
    get pending() { return this._id === undefined; }
    get sentHeaders() { return this._sentHeaders; }
    get sentTrailers() { return this._sentTrailers; }
    get sentInfoHeaders() { return this._sentInfoHeaders; }
    get rstCode() { return this._rstCode; }

    setTimeout(msecs, callback) {
      if (this._destroyed) return this;
      if (callback) this.once('timeout', callback);
      this._idle.set(msecs);
      return this;
    }

    close(code, callback) {
      if (typeof code === 'function') {
        callback = code;
        code = undefined;
      }
      if (this._closed) return;
      if (callback) this.once('close', callback);
      this._teardown(null, code === undefined ? kNoError : code);
    }

    destroy(error) {
      if (this._destroyed) return this;
      this._destroyCalled = true;
      this._teardown(error || null, undefined);
      return this;
    }

    // Single exit for every way a stream ends. The events it schedules follow Node:
    // a stream ended without error still ends its readable side, a non-zero code
    // other than CANCEL is an error, and 'close' comes last.
    _teardown(error, code) {
      this._destroyed = true;
      this._closed = true;
      this._state = 'closed';
      this._idle.clear();

      if (code === undefined) {
        code = !error ? kNoError : (error.code === 'ABORT_ERR' ? kCancel : kInternalError);
      }
      this._rstCode = code;
      if (!error && code !== kNoError && code !== kCancel) {
        error = makeError('Stream closed with error code ' + (errorCodeNames[code] || code),
          'ERR_HTTP2_STREAM_ERROR');
      }

      this._releaseNative();
      process.nextTick(() => {
        this._onTeardown(error);
        if (error) this.emit('error', error);
        this.emit('close');
        if (this._session) this._session._streamClosed(this);
      });
    }

    _releaseNative() {
      if (this._session && this._session._id && this._id !== undefined) {
        internal.closeStream(this._session._id, this._id);
      }
    }

    _onTeardown(error) {}

    priority(options) {
      // Priority hints - simplified
      return this;
    }
  }

  /**
   * ClientHttp2Stream - Client-side HTTP/2 stream.
   *
   * Reads pull one chunk at a time from native code and writes wait for HTTP/2 to
   * take the bytes, so a slow reader or writer applies real backpressure.
   */
  class ClientHttp2Stream extends Http2Stream {
    constructor(session, headers, endStream) {
      super(session, undefined);
      this._headers = headers;
      this._sentHeaders = headers;
      this._response = null;
      this.readable = true;
      this.writable = !endStream;

      // Readable side
      this._chunks = [];
      this._buffered = 0;
      this._flowing = null;
      this._decoder = null;
      this._fetching = false;
      this._remoteEnded = false;
      this._endEmitted = false;

      // Writable side
      this._endStream = endStream;
      this._writes = [];
      this._writing = false;
      this._writeLength = 0;
      this._needDrain = false;
      this._ending = endStream;
      this._finished = false;
      this._endCallbacks = [];

      this._started = false;
    }

    // ---- request lifecycle (driven by ClientHttp2Session) ----

    _start(id) {
      this._id = id;
      this._started = true;
      this._idle.touch();
      internal.request(this._session._id, id, JSON.stringify(this._headers), this._endStream,
        (message, code, rstCode, responseHeaders) => {
          if (this._destroyed) return;
          if (message || rstCode) {
            this._failNative(message, code, rstCode);
            return;
          }
          this._idle.touch();
          this._session._activity();
          let parsed = {};
          try {
            parsed = JSON.parse(responseHeaders);
          } catch (e) {
            // Native code produced these; this cannot fail in practice.
          }
          this._response = parsed;
          this.emit('response', parsed, 0);
          this._fetch();
        });
      this.emit('ready');
      this._pumpWrites();
      if (this._endStream) this._markFinished();
    }

    _failNative(message, code, rstCode) {
      if (this._destroyed) return;
      if (message) {
        this._teardown(makeError(message, code), rstCode || kInternalError);
      } else {
        this._teardown(null, rstCode);
      }
    }

    // ---- readable side ----

    // Attaching a 'data' listener starts the flow unless paused explicitly.
    addListener(event, listener) {
      super.addListener(event, listener);
      if (event === 'data' && this._flowing !== false) this.resume();
      return this;
    }

    once(event, listener) {
      super.once(event, listener);
      if (event === 'data' && this._flowing !== false) this.resume();
      return this;
    }

    prependListener(event, listener) {
      super.prependListener(event, listener);
      if (event === 'data' && this._flowing !== false) this.resume();
      return this;
    }

    prependOnceListener(event, listener) {
      super.prependOnceListener(event, listener);
      if (event === 'data' && this._flowing !== false) this.resume();
      return this;
    }

    _fetch() {
      if (this._fetching || this._remoteEnded || this._destroyed || !this._response) return;
      if (this._buffered >= kHighWaterMark) return;
      this._fetching = true;
      internal.readStream(this._session._id, this._id, (message, code, rstCode, chunk, done, trailers) => {
        this._fetching = false;
        if (this._destroyed) return;
        if (message || rstCode) {
          this._failNative(message, code, rstCode);
          return;
        }
        if (chunk) {
          this._idle.touch();
          this._session._activity();
          const buffer = Buffer.from(chunk, 'latin1');
          this._chunks.push(buffer);
          this._buffered += buffer.length;
        }
        if (done) {
          this._state = 'half-closed (remote)';
          if (trailers) {
            let parsed = {};
            try {
              parsed = JSON.parse(trailers);
            } catch (e) {
              // Native code produced these; this cannot fail in practice.
            }
            this._deliverChunks();
            this.emit('trailers', parsed, 0);
          }
          this._remoteEnded = true;
          // Nothing will read a stream nobody is listening to, and it must still
          // finish so that its session can close.
          if (this._flowing === null && this.listenerCount('readable') === 0) this.resume();
        }
        this._deliverChunks();
        if (chunk && this.listenerCount('readable') > 0) this.emit('readable');
        this._fetch();
      });
    }

    _decode(buffer) {
      return this._decoder ? this._decoder.write(buffer) : buffer;
    }

    _deliverChunks() {
      while (this._flowing && this._chunks.length > 0) {
        const buffer = this._chunks.shift();
        this._buffered -= buffer.length;
        const data = this._decode(buffer);
        if (data.length > 0) this.emit('data', data);
      }
      this._maybeEmitEnd();
    }

    _maybeEmitEnd() {
      if (this._endEmitted || !this._remoteEnded || this._chunks.length > 0) return;
      if (this._flowing === false) return;
      this._endEmitted = true;
      this.readable = false;
      this.emit('end');
      this._maybeComplete();
    }

    resume() {
      if (this._flowing !== true) {
        this._flowing = true;
        // Deferred as in Node, so listeners attached right after resume() see data.
        process.nextTick(() => {
          this._deliverChunks();
          this._fetch();
        });
      }
      return this;
    }

    pause() {
      this._flowing = false;
      return this;
    }

    isPaused() {
      return this._flowing === false;
    }

    setEncoding(encoding) {
      const StringDecoder = global.__string_decoder_module && global.__string_decoder_module.StringDecoder;
      this._decoder = StringDecoder ? new StringDecoder(encoding) : { write: (b) => b.toString(encoding) };
      return this;
    }

    read(size) {
      if (this._chunks.length === 0) {
        this._maybeEmitEnd();
        return null;
      }
      const buffer = this._chunks.shift();
      this._buffered -= buffer.length;
      if (this._chunks.length === 0 && this._remoteEnded) process.nextTick(() => this._maybeEmitEnd());
      this._fetch();
      return this._decode(buffer);
    }

    pipe(destination, options) {
      this.on('data', (chunk) => {
        if (destination.write(chunk) === false) {
          this.pause();
          destination.once('drain', () => this.resume());
        }
      });
      this.on('end', () => {
        if (!options || options.end !== false) destination.end();
      });
      return destination;
    }

    [Symbol.asyncIterator]() {
      const queue = [];
      let waiting = null;
      let finished = false;
      let failure = null;
      const wake = () => {
        if (!waiting) return;
        const { resolve, reject } = waiting;
        if (queue.length > 0) {
          waiting = null;
          resolve({ value: queue.shift(), done: false });
        } else if (failure) {
          waiting = null;
          reject(failure);
        } else if (finished) {
          waiting = null;
          resolve({ value: undefined, done: true });
        }
      };
      this.on('data', (chunk) => { queue.push(chunk); wake(); });
      this.on('error', (error) => { failure = error; wake(); });
      this.on('end', () => { finished = true; wake(); });
      this.on('close', () => { finished = true; wake(); });
      const stream = this;
      return {
        next() {
          return new Promise((resolve, reject) => {
            waiting = { resolve, reject };
            wake();
          });
        },
        return() {
          stream.destroy();
          return Promise.resolve({ value: undefined, done: true });
        },
        [Symbol.asyncIterator]() { return this; }
      };
    }

    // ---- writable side ----

    write(chunk, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }
      if (chunk === null) {
        throw makeError('May not write null values to stream', 'ERR_STREAM_NULL_VALUES', TypeError);
      }
      if (typeof chunk !== 'string' && !ArrayBuffer.isView(chunk)) {
        throw makeError('The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.',
          'ERR_INVALID_ARG_TYPE', TypeError);
      }

      const failure = (this._destroyed || this._ending)
        ? makeError('write after end', 'ERR_STREAM_WRITE_AFTER_END')
        : null;
      if (failure) {
        process.nextTick(() => {
          if (callback) callback(failure);
          if (!this._destroyed) this.emit('error', failure);
        });
        return false;
      }

      let buffer;
      if (typeof chunk === 'string') {
        buffer = Buffer.from(chunk, encoding || 'utf8');
      } else if (Buffer.isBuffer(chunk)) {
        buffer = chunk;
      } else {
        buffer = Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
      }

      this._writes.push({ buffer, callback });
      this._writeLength += buffer.length;
      this._pumpWrites();
      if (this._writeLength >= kHighWaterMark) {
        this._needDrain = true;
        return false;
      }
      return true;
    }

    _pumpWrites() {
      if (this._writing || !this._started || this._destroyed) return;
      const entry = this._writes.shift();
      if (!entry) {
        if (this._ending && !this._endStream) {
          internal.endStream(this._session._id, this._id);
          this._markFinished();
        }
        return;
      }
      this._writing = true;
      this._idle.touch();
      internal.writeStream(this._session._id, this._id, entry.buffer.toString('base64'), (message, code) => {
        this._writing = false;
        this._writeLength -= entry.buffer.length;
        if (this._destroyed) {
          if (entry.callback) {
            entry.callback(makeError('Cannot call write after a stream was destroyed', 'ERR_STREAM_DESTROYED'));
          }
          return;
        }
        this._idle.touch();
        this._session._activity();
        if (message) {
          const error = makeError(message, code);
          if (entry.callback) entry.callback(error);
          this.destroy(error);
          return;
        }
        if (entry.callback) entry.callback();
        if (this._needDrain && this._writeLength < kHighWaterMark) {
          this._needDrain = false;
          this.emit('drain');
        }
        this._pumpWrites();
      });
    }

    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
        encoding = undefined;
      } else if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (data !== undefined && data !== null) this.write(data, encoding);

      if (callback) {
        if (this._finished) process.nextTick(callback);
        else this._endCallbacks.push(callback);
      }
      if (!this._ending) {
        this._ending = true;
        this.writable = false;
        this._state = 'half-closed (local)';
        this._pumpWrites();
      }
      return this;
    }

    _markFinished() {
      if (this._finished) return;
      this._finished = true;
      this.writable = false;
      process.nextTick(() => {
        if (this._destroyed && this._writes.length > 0) return;
        for (const callback of this._endCallbacks.splice(0)) callback();
        this.emit('finish');
        this._maybeComplete();
      });
    }

    // A stream is complete once both directions are done.
    _maybeComplete() {
      if (!this._destroyed && this._finished && this._endEmitted) {
        this._teardown(null, kNoError);
      }
    }

    // ---- teardown ----

    _onTeardown(error) {
      // The request was cut off before the client finished sending it.
      if (this._aborted) this.emit('aborted');
      if (!this._finished && !this._destroyCalled) {
        this._finished = true;
        for (const callback of this._endCallbacks.splice(0)) callback();
        this.emit('finish');
      }

      // Fail anything still queued.
      const pending = this._writes;
      this._writes = [];
      for (const entry of pending) {
        if (entry.callback) {
          entry.callback(makeError('Cannot call write after a stream was destroyed', 'ERR_STREAM_DESTROYED'));
        }
      }

      if (error || (this._rstCode !== kNoError && this._rstCode !== kCancel)) {
        this.readable = false;
        return;
      }
      // Ended without error: the readable side still finishes for its listeners.
      this._remoteEnded = true;
      if (this._flowing === null) this._flowing = true;
      this._deliverChunks();
    }

    _releaseNative() {
      super._releaseNative();
      if (this._response && !this._finished) this._aborted = true;
    }
  }

  /**
   * ServerHttp2Stream - Server-side HTTP/2 stream.
   */
  class ServerHttp2Stream extends Http2Stream {
    constructor(session, id, headers) {
      super(session, id);
      this._headers = headers;
      this._headersSent = false;
      this._pushAllowed = true;
    }

    get headersSent() { return this._headersSent; }
    get pushAllowed() { return this._pushAllowed; }

    respond(headers, options) {
      if (this._headersSent) {
        throw new Error('Response has already been initiated');
      }

      this._headersSent = true;
      this._sentHeaders = headers;

      if (this._session && this._session._id) {
        internal.respond(this._session._id, this._id, JSON.stringify(headers), options);
      }

      return this;
    }

    respondWithFile(path, headers, options) {
      // Simplified - would read file and respond
      this.respond(headers || {});
      this.end();
    }

    respondWithFD(fd, headers, options) {
      this.respond(headers || {});
      this.end();
    }

    pushStream(headers, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }

      if (!this._pushAllowed) {
        if (callback) callback(new Error('Push streams disabled'));
        return;
      }

      // Simplified push stream
      if (callback) {
        const pushStream = new ServerHttp2Stream(this._session, this._id + 1, headers);
        callback(null, pushStream, headers);
      }
    }

    additionalHeaders(headers) {
      this._sentInfoHeaders.push(headers);
      return this;
    }

    write(chunk, encoding, callback) {
      if (!this._headersSent) {
        this.respond({ ':status': 200 });
      }

      if (this._session && this._session._id) {
        const data = Buffer.isBuffer(chunk) ? chunk.toString('binary') : chunk;
        internal.writeStream(this._session._id, this._id, data);
      }

      if (callback) process.nextTick(callback);
      return true;
    }

    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
      }

      if (!this._headersSent) {
        this.respond({ ':status': 200 });
      }

      if (data) {
        this.write(data, encoding);
      }

      this._state = 'half-closed (local)';

      if (this._session && this._session._id) {
        internal.endStream(this._session._id, this._id);
      }

      if (callback) this.once('finish', callback);
      this.emit('finish');

      return this;
    }
  }

  /**
   * Http2Session - Base class for HTTP/2 sessions.
   */
  class Http2Session extends EventEmitter {
    constructor() {
      super();
      this._id = null;
      this._state = 'idle';
      this._destroyed = false;
      this._closed = false;
      this._streams = new Map();
      this._nextStreamId = 1;
      this._settings = {
        headerTableSize: constants.DEFAULT_SETTINGS_HEADER_TABLE_SIZE,
        enablePush: true,
        maxConcurrentStreams: constants.DEFAULT_SETTINGS_MAX_CONCURRENT_STREAMS,
        initialWindowSize: constants.DEFAULT_SETTINGS_INITIAL_WINDOW_SIZE,
        maxFrameSize: constants.DEFAULT_SETTINGS_MAX_FRAME_SIZE,
        maxHeaderListSize: constants.DEFAULT_SETTINGS_MAX_HEADER_LIST_SIZE
      };
      this._remoteSettings = { ...this._settings };
      this._pendingSettingsAck = false;
      this._socket = null;
      this._alpnProtocol = undefined;
      this._encrypted = undefined;
      this._originSet = new Set();
      this._idle = new IdleTimer(this);
    }

    get alpnProtocol() { return this._alpnProtocol; }
    get closed() { return this._closed; }
    get connecting() { return this._state === 'connecting'; }
    get destroyed() { return this._destroyed; }
    get encrypted() { return this._encrypted; }
    get localSettings() { return { ...this._settings }; }
    get remoteSettings() { return { ...this._remoteSettings }; }
    get originSet() { return this._originSet; }
    get pendingSettingsAck() { return this._pendingSettingsAck; }
    get socket() { return this._socket; }
    get state() {
      return {
        effectiveLocalWindowSize: 65535,
        effectiveRecvDataLength: 0,
        nextStreamID: this._nextStreamId,
        localWindowSize: 65535,
        lastProcStreamID: 0,
        remoteWindowSize: 65535,
        outboundQueueSize: 0,
        deflateDynamicTableSize: 4096,
        inflateDynamicTableSize: 4096
      };
    }
    get type() { return this._type; }

    settings(settings, callback) {
      if (typeof settings === 'function') {
        callback = settings;
        settings = {};
      }

      Object.assign(this._settings, settings);
      this._pendingSettingsAck = true;

      if (callback) this.once('localSettings', callback);

      // Would send SETTINGS frame
      process.nextTick(() => {
        this._pendingSettingsAck = false;
        this.emit('localSettings', this._settings);
      });

      return this;
    }

    ping(payload, callback) {
      if (typeof payload === 'function') {
        callback = payload;
        payload = Buffer.alloc(8);
      }

      const duration = 1; // Would measure actual RTT
      if (callback) process.nextTick(() => callback(null, duration, payload));
      return true;
    }

    goaway(code, lastStreamID, opaqueData) {
      if (this._id) {
        internal.goaway(this._id, code || 0, lastStreamID || 0);
      }
    }

    destroy(error, code) {
      if (this._destroyed) return;
      this._destroyed = true;
      this._state = 'closed';
      this._idle.clear();

      for (const stream of Array.from(this._streams.values())) {
        if (stream.pending) stream.destroy(streamCancelError(error));
        else stream.destroy(error);
      }

      if (this._id) {
        internal.destroySession(this._id, !error && this._closed);
      }

      process.nextTick(() => {
        if (error) this.emit('error', error);
        this.emit('close');
      });
    }

    close(callback) {
      if (this._closed || this._destroyed) return;
      this._closed = true;

      if (callback) this.once('close', callback);

      this.goaway();
      this._maybeFinishClose();
    }

    _streamClosed(stream) {
      this._streams.delete(stream);
      this._maybeFinishClose();
    }

    // A closing session stays open until its last stream has finished.
    _maybeFinishClose() {
      if (this._closed && !this._destroyed && this._streams.size === 0) this.destroy();
    }

    _activity() {
      this._idle.touch();
    }

    setTimeout(msecs, callback) {
      if (this._destroyed) return this;
      if (callback) this.once('timeout', callback);
      this._idle.set(msecs);
      return this;
    }

    ref() { return this; }
    unref() { return this; }

    setLocalWindowSize(windowSize) {
      this._settings.initialWindowSize = windowSize;
    }
  }

  /**
   * ClientHttp2Session - Client HTTP/2 session. One session is one connection.
   */
  class ClientHttp2Session extends Http2Session {
    constructor() {
      super();
      this._type = 'client';
      this._nextStreamId = 1; // Odd numbers for client
      this._scheme = 'https';
      this._authority = '';
      this._pending = [];
    }

    request(headers, options) {
      if (this._destroyed) {
        throw makeError('The session has been destroyed', 'ERR_HTTP2_INVALID_SESSION');
      }
      if (this._closed) {
        throw makeError('New streams cannot be created after receiving a GOAWAY', 'ERR_HTTP2_GOAWAY_SESSION');
      }

      options = options || {};
      const outgoing = { ...(headers || {}) };
      for (const name of Object.keys(outgoing)) {
        const lower = name.toLowerCase();
        if (connectionHeaders.has(lower)) {
          throw makeError('HTTP/1 Connection specific headers are forbidden: "' + lower + '"',
            'ERR_HTTP2_INVALID_CONNECTION_HEADERS', TypeError);
        }
        if (lower !== name) {
          outgoing[lower] = outgoing[name];
          delete outgoing[name];
        }
        const value = outgoing[lower];
        if (Array.isArray(value)) outgoing[lower] = value.map(String);
        else if (value === undefined || value === null) delete outgoing[lower];
        else outgoing[lower] = String(value);
      }

      if (outgoing[':method'] === undefined) outgoing[':method'] = 'GET';
      if (outgoing[':method'] !== 'CONNECT') {
        if (outgoing[':scheme'] === undefined) outgoing[':scheme'] = this._scheme;
        if (outgoing[':path'] === undefined) outgoing[':path'] = '/';
      }
      if (outgoing[':authority'] === undefined) outgoing[':authority'] = outgoing.host || this._authority;

      const endStream = options.endStream === undefined
        ? payloadlessMethods.has(outgoing[':method'])
        : !!options.endStream;

      const stream = new ClientHttp2Stream(this, outgoing, endStream);
      this._streams.set(stream, stream);

      const signal = options.signal;
      if (signal) {
        if (signal.aborted) {
          process.nextTick(() => stream.destroy(abortError(signal)));
          return stream;
        }
        const onAbort = () => stream.destroy(abortError(signal));
        signal.addEventListener('abort', onAbort);
        stream.once('close', () => signal.removeEventListener('abort', onAbort));
      }

      if (this.connecting) {
        this._pending.push(stream);
      } else {
        process.nextTick(() => this._startStream(stream));
      }
      return stream;
    }

    _startStream(stream) {
      if (stream.destroyed) return;
      const id = this._nextStreamId;
      this._nextStreamId += 2;
      stream._start(id);
    }

    ping(payload, callback) {
      if (typeof payload === 'function') {
        callback = payload;
        payload = Buffer.alloc(8);
      }
      if (this._destroyed) {
        throw makeError('The session has been destroyed', 'ERR_HTTP2_INVALID_SESSION');
      }
      const send = () => {
        internal.ping(this._id, (message, code, duration) => {
          if (!callback) return;
          if (message) callback(makeError(message, code), 0, payload);
          else callback(null, duration, payload);
        });
      };
      if (this.connecting) this.once('connect', send);
      else send();
      return true;
    }

    ref() {
      if (this._id) internal.setRef(this._id, true);
      return this;
    }

    unref() {
      if (this._id) internal.setRef(this._id, false);
      return this;
    }
  }

  /**
   * ServerHttp2Session - Server HTTP/2 session.
   */
  class ServerHttp2Session extends Http2Session {
    constructor() {
      super();
      this._type = 'server';
      this._nextStreamId = 2; // Even numbers for server
    }

    origin(...origins) {
      for (const origin of origins) {
        this._originSet.add(origin);
      }
    }

    altsvc(alt, originOrStream) {
      // Alt-Svc support - simplified
    }
  }

  /**
   * Http2Server - HTTP/2 server.
   */
  class Http2Server extends EventEmitter {
    constructor(options, onRequestHandler) {
      super();

      if (typeof options === 'function') {
        onRequestHandler = options;
        options = {};
      }

      this._options = options || {};
      this._id = null;
      this._listening = false;
      this._sessions = new Map();

      if (onRequestHandler) {
        this.on('stream', onRequestHandler);
      }
    }

    get listening() { return this._listening; }

    listen(port, host, callback) {
      if (typeof host === 'function') {
        callback = host;
        host = '0.0.0.0';
      }

      if (callback) this.once('listening', callback);

      this._id = internal.createServer(port, host || '0.0.0.0', (err, sessionId, streamId, headersJson) => {
        if (err) {
          this.emit('error', new Error(err));
          return;
        }

        // Get or create session
        let session = this._sessions.get(sessionId);
        if (!session) {
          session = new ServerHttp2Session();
          session._id = sessionId;
          this._sessions.set(sessionId, session);
          this.emit('session', session);
        }

        // Create stream
        let headers = {};
        try {
          headers = JSON.parse(headersJson);
        } catch (e) {}

        const stream = new ServerHttp2Stream(session, streamId, headers);
        session._streams.set(streamId, stream);

        this.emit('stream', stream, headers, 0);
      });

      if (this._id) {
        this._listening = true;
        this.emit('listening');
      }

      return this;
    }

    close(callback) {
      if (!this._listening) return;
      this._listening = false;

      if (callback) this.once('close', callback);

      for (const session of this._sessions.values()) {
        session.close();
      }
      this._sessions.clear();

      if (this._id) {
        internal.closeServer(this._id);
        this._id = null;
      }

      this.emit('close');
    }

    setTimeout(msecs, callback) {
      if (callback) this.once('timeout', callback);
      return this;
    }

    updateSettings(settings) {
      Object.assign(this._options, settings);
    }
  }

  /**
   * Http2SecureServer - Secure HTTP/2 server.
   */
  class Http2SecureServer extends Http2Server {
    constructor(options, onRequestHandler) {
      super(options, onRequestHandler);
    }
  }

  function pemList(value) {
    if (value === undefined || value === null) return [];
    return (Array.isArray(value) ? value : [value]).map((item) =>
      typeof item === 'string' ? item : Buffer.from(item).toString('utf8'));
  }

  function pemText(value) {
    return pemList(value).join('\n');
  }

  /**
   * Connect to an HTTP/2 server.
   */
  function connect(authority, options, listener) {
    if (typeof options === 'function') {
      listener = options;
      options = {};
    }

    options = options || {};

    // Parse authority
    let url = authority;
    if (typeof authority === 'string') {
      try {
        url = new URL(authority);
      } catch (e) {
        url = new URL('https://' + authority);
      }
    }

    const protocol = url.protocol || options.protocol || 'https:';
    if (protocol !== 'http:' && protocol !== 'https:') {
      throw makeError('protocol "' + protocol + '" is unsupported.', 'ERR_HTTP2_UNSUPPORTED_PROTOCOL');
    }

    let host = 'localhost';
    if (url.hostname) {
      host = url.hostname;
    } else if (url.host) {
      host = url.host;
    }
    // Native code receives the bare address, without URL brackets.
    if (host[0] === '[') host = host.slice(1, -1);

    const port = parseInt(url.port) || (protocol === 'http:' ? 80 : 443);
    const encrypted = protocol === 'https:';

    const session = new ClientHttp2Session();
    session._scheme = encrypted ? 'https' : 'http';
    const authorityHost = options.servername || host;
    session._authority = (authorityHost.indexOf(':') !== -1 ? '[' + authorityHost + ']' : authorityHost) + ':' + port;
    session._state = 'connecting';

    if (listener) {
      session.once('connect', listener);
    }

    let rejectUnauthorized = options.rejectUnauthorized;
    if (rejectUnauthorized === undefined) {
      const env = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
      rejectUnauthorized = env !== '0';
    }

    const nativeOptions = {
      scheme: session._scheme,
      rejectUnauthorized: rejectUnauthorized !== false,
      ca: pemList(options.ca),
      cert: pemText(options.cert),
      key: pemText(options.key),
      servername: options.servername || ''
    };

    session._id = internal.connect(host, port, JSON.stringify(nativeOptions), (message, code) => {
      if (session._destroyed) return;
      if (message) {
        session.destroy(makeError(message, code));
        return;
      }

      session._state = 'connected';
      session._encrypted = encrypted;
      session._alpnProtocol = encrypted ? 'h2' : 'h2c';
      session._idle.touch();
      session.emit('connect', session, null);

      const pending = session._pending;
      session._pending = [];
      for (const stream of pending) session._startStream(stream);
      if (session._closed) session._maybeFinishClose();
    }, (message, code) => {
      // The connection ended underneath the session.
      if (session._destroyed) return;
      session.destroy(message ? makeError(message, code) : undefined);
    });

    return session;
  }

  /**
   * Create an HTTP/2 server.
   */
  function createServer(options, onRequestHandler) {
    return new Http2Server(options, onRequestHandler);
  }

  /**
   * Create a secure HTTP/2 server.
   */
  function createSecureServer(options, onRequestHandler) {
    return new Http2SecureServer(options, onRequestHandler);
  }

  /**
   * Get default settings.
   */
  function getDefaultSettings() {
    return {
      headerTableSize: constants.DEFAULT_SETTINGS_HEADER_TABLE_SIZE,
      enablePush: true,
      maxConcurrentStreams: constants.DEFAULT_SETTINGS_MAX_CONCURRENT_STREAMS,
      initialWindowSize: constants.DEFAULT_SETTINGS_INITIAL_WINDOW_SIZE,
      maxFrameSize: constants.DEFAULT_SETTINGS_MAX_FRAME_SIZE,
      maxHeaderListSize: constants.DEFAULT_SETTINGS_MAX_HEADER_LIST_SIZE
    };
  }

  /**
   * Get packed settings.
   */
  function getPackedSettings(settings) {
    // Would return binary representation
    return Buffer.alloc(0);
  }

  /**
   * Get unpacked settings.
   */
  function getUnpackedSettings(buffer) {
    return getDefaultSettings();
  }

  // Export module
  const http2 = {
    constants,
    sensitiveHeaders,
    connect,
    createServer,
    createSecureServer,
    getDefaultSettings,
    getPackedSettings,
    getUnpackedSettings,
    Http2Session,
    ClientHttp2Session,
    ServerHttp2Session,
    Http2Stream,
    ClientHttp2Stream,
    ServerHttp2Stream,
    Http2Server,
    Http2SecureServer
  };

  global.__http2_module = http2;

})(globalThis);
