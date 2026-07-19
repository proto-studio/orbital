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
      this._endAfterHeaders = false;
      this._sentHeaders = null;
      this._sentTrailers = null;
      this._sentInfoHeaders = [];
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

    get rstCode() {
      return this._rstCode || constants.NGHTTP2_NO_ERROR;
    }

    setTimeout(msecs, callback) {
      if (callback) this.once('timeout', callback);
      return this;
    }

    close(code, callback) {
      if (this._closed) return;
      this._closed = true;
      this._state = 'closed';

      if (callback) this.once('close', callback);

      if (this._session && this._session._id) {
        internal.closeStream(this._session._id, this._id, code || 0);
      }

      this.emit('close');
    }

    destroy(error) {
      if (this._destroyed) return;
      this._destroyed = true;
      this.close();
      if (error) this.emit('error', error);
    }

    priority(options) {
      // Priority hints - simplified
      return this;
    }
  }

  /**
   * ClientHttp2Stream - Client-side HTTP/2 stream.
   */
  class ClientHttp2Stream extends Http2Stream {
    constructor(session, id, headers) {
      super(session, id);
      this._headers = headers;
      this._response = null;
      this._data = [];
    }

    _handleResponse(headers, flags) {
      this._response = headers;
      this.emit('response', headers, flags);
    }

    _handleData(chunk) {
      this._data.push(chunk);
      this.emit('data', Buffer.from(chunk, 'binary'));
    }

    _handleEnd() {
      this._state = 'half-closed (remote)';
      this.emit('end');
    }

    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
      }

      if (data) {
        this.write(data, encoding);
      }

      this._state = 'half-closed (local)';

      if (callback) this.once('finish', callback);
      this.emit('finish');

      return this;
    }

    write(chunk, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (this._session && this._session._id) {
        const data = Buffer.isBuffer(chunk) ? chunk.toString('binary') : chunk;
        internal.writeStream(this._session._id, this._id, data);
      }

      if (callback) process.nextTick(callback);
      return true;
    }

    setEncoding(encoding) {
      return this;
    }

    read(size) {
      if (this._data.length === 0) return null;
      return Buffer.from(this._data.shift(), 'binary');
    }

    pipe(destination, options) {
      this.on('data', (chunk) => destination.write(chunk));
      this.on('end', () => {
        if (!options || options.end !== false) destination.end();
      });
      return destination;
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
      this._alpnProtocol = 'h2';
      this._originSet = new Set();
    }

    get alpnProtocol() { return this._alpnProtocol; }
    get closed() { return this._closed; }
    get connecting() { return this._state === 'connecting'; }
    get destroyed() { return this._destroyed; }
    get encrypted() { return true; }
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
      this._closed = true;

      for (const stream of this._streams.values()) {
        stream.destroy(error);
      }
      this._streams.clear();

      if (this._id) {
        internal.destroySession(this._id);
        this._id = null;
      }

      if (error) this.emit('error', error);
      this.emit('close');
    }

    close(callback) {
      if (this._closed) return;
      this._closed = true;

      if (callback) this.once('close', callback);

      this.goaway();

      process.nextTick(() => {
        this.destroy();
      });
    }

    setTimeout(msecs, callback) {
      if (callback) this.once('timeout', callback);
      return this;
    }

    ref() { return this; }
    unref() { return this; }

    setLocalWindowSize(windowSize) {
      this._settings.initialWindowSize = windowSize;
    }
  }

  /**
   * ClientHttp2Session - Client HTTP/2 session.
   */
  class ClientHttp2Session extends Http2Session {
    constructor() {
      super();
      this._type = 'client';
      this._nextStreamId = 1; // Odd numbers for client
    }

    request(headers, options) {
      if (this._destroyed || this._closed) {
        throw new Error('Session is closed');
      }

      const streamId = this._nextStreamId;
      this._nextStreamId += 2;

      const stream = new ClientHttp2Stream(this, streamId, headers);
      this._streams.set(streamId, stream);

      // Make the request
      if (this._id) {
        internal.request(this._id, streamId, JSON.stringify(headers), (err, respHeaders, data, finished) => {
          if (err) {
            stream.emit('error', new Error(err));
            return;
          }

          if (respHeaders) {
            try {
              const parsedHeaders = JSON.parse(respHeaders);
              stream._handleResponse(parsedHeaders, 0);
            } catch (e) {
              // Ignore parse errors
            }
          }

          if (data) {
            stream._handleData(data);
          }

          if (finished) {
            stream._handleEnd();
            stream.close();
          }
        });
      }

      return stream;
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

  /**
   * Connect to an HTTP/2 server.
   */
  function connect(authority, options, listener) {
    if (typeof options === 'function') {
      listener = options;
      options = {};
    }

    options = options || {};

    const session = new ClientHttp2Session();

    if (listener) {
      session.once('connect', listener);
    }

    // Parse authority
    let url;
    try {
      url = new URL(authority);
    } catch (e) {
      url = new URL('https://' + authority);
    }

    const host = url.hostname;
    const port = parseInt(url.port) || (url.protocol === 'http:' ? 80 : 443);

    session._id = internal.connect(host, port, (err) => {
      if (err) {
        session.emit('error', new Error(err));
        return;
      }

      session._state = 'connected';
      session.emit('connect', session);
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
