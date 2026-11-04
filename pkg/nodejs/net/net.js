// net - TCP and Unix domain sockets
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module;
  const { Duplex, addAbortSignal } = global.__stream_module;
  const internal = global.__net_internal;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // Builds an Error from the description native code reports.
  function nodeError(desc) {
    const err = new Error(desc.message);
    if (desc.errno) err.errno = desc.errno;
    if (desc.code) err.code = desc.code;
    if (desc.syscall) err.syscall = desc.syscall;
    if (desc.hostname) err.hostname = desc.hostname;
    if (desc.address) err.address = desc.address;
    if (desc.port) err.port = desc.port;
    return err;
  }

  function parseError(encoded) {
    return nodeError(JSON.parse(encoded));
  }

  const kListenMessages = {
    EADDRINUSE: 'address already in use',
    EACCES: 'permission denied',
    EADDRNOTAVAIL: 'address not available'
  };

  function listenError(desc, address, port) {
    const text = kListenMessages[desc.code];
    if (text) {
      const where = port === undefined ? address : `${address}:${port}`;
      desc.message = `listen ${desc.code}: ${text} ${where}`;
    }
    return nodeError(desc);
  }

  function toNumber(x) {
    x = Number(x);
    return x >= 0 ? x : false;
  }

  function isPipeName(s) {
    return typeof s === 'string' && toNumber(s) === false;
  }

  function validatePort(port) {
    const valid = (typeof port === 'number' || (typeof port === 'string' && port.trim() !== '')) &&
      +port === (+port >>> 0) && port <= 0xFFFF;
    if (!valid) {
      throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', `Port should be >= 0 and < 65536. Received ${String(port)}.`);
    }
    return +port;
  }

  // The (options[, cb]), (port[, host][, cb]) and (path[, cb]) argument forms.
  function normalizeArgs(args) {
    if (args.length === 0) {
      throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "options", "port", or "path" argument must be specified');
    }
    // An array is an already normalized [options, callback].
    if (Array.isArray(args[0]) && args[0].normalized) return args[0];

    const first = args[0];
    let options = {};
    if (typeof first === 'object' && first !== null) {
      options = first;
    } else if (isPipeName(first)) {
      options.path = first;
    } else {
      options.port = first;
      if (args.length > 1 && typeof args[1] === 'string') options.host = args[1];
    }
    const cb = args[args.length - 1];
    const result = [options, typeof cb === 'function' ? cb : null];
    result.normalized = true;
    return result;
  }

  /**
   * Socket class - Represents a TCP or Unix domain socket.
   */
  class Socket extends Duplex {
    constructor(options) {
      if (typeof options === 'number') options = { fd: options };
      options = { ...options };
      if (options.fd !== undefined) {
        throw codedError(Error, 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM', 'Opening a socket from a file descriptor is not supported');
      }
      const netId = options._netId;
      delete options._netId;
      super({
        ...options,
        allowHalfOpen: !!options.allowHalfOpen,
        // The socket emits 'close' itself, with its hadError argument.
        emitClose: false,
        autoDestroy: true,
        decodeStrings: false
      });
      this.connecting = false;
      this._id = netId === undefined ? null : netId;
      this._hadError = false;
      this._ioQueue = [];
      this._ioBlocked = false;
      this._readPending = false;
      this._refed = true;
      this._server = null;
      this._peername = null;
      this._sockname = null;
      this._bytesRead = 0;
      this._bytesDispatched = 0;
      this._timeoutMs = 0;
      this._timeoutTimer = null;
      this._lastActive = 0;
      this._connectOptions = { noDelay: options.noDelay, keepAlive: options.keepAlive };
      this._host = null;

      if (options.signal) addAbortSignal(options.signal, this);
      if (netId !== undefined) this._loadAddresses();
    }

    get pending() { return this._id === null || this.connecting; }
    // Packages test this to see whether the socket is open.
    get _handle() { return this._id === null ? null : this; }
    get bytesRead() { return this._bytesRead; }
    get bytesWritten() {
      let pending = this._pendingWriteBytes || 0;
      for (const entry of this.writableBuffer) {
        pending += typeof entry.chunk === 'string' ? Buffer.byteLength(entry.chunk, entry.encoding) : entry.chunk.length;
      }
      return this._bytesDispatched + pending;
    }
    get localAddress() { return this._sockname ? this._sockname.localAddress : undefined; }
    get localPort() { return this._sockname ? this._sockname.localPort : undefined; }
    get localFamily() { return this._sockname ? this._sockname.localFamily : undefined; }
    get remoteAddress() { return this._peername ? this._peername.remoteAddress : undefined; }
    get remotePort() { return this._peername ? this._peername.remotePort : undefined; }
    get remoteFamily() { return this._peername ? this._peername.remoteFamily : undefined; }
    get timeout() { return this._timeoutMs; }
    get readyState() {
      if (this.connecting) return 'opening';
      if (this.readable && this.writable) return 'open';
      if (this.readable && !this.writable) return 'readOnly';
      if (!this.readable && this.writable) return 'writeOnly';
      return 'closed';
    }

    _loadAddresses() {
      const encoded = this._id === null ? null : internal.getAddressInfo(this._id);
      if (encoded) {
        const info = JSON.parse(encoded);
        this._sockname = info;
        this._peername = info;
      }
    }

    /**
     * Connect to a remote address.
     */
    connect(...args) {
      const [options, cb] = normalizeArgs(args);

      // A socket can connect again after it has closed.
      if (this.write !== Socket.prototype.write) this.write = Socket.prototype.write;
      if (this.destroyed || this._writableState.ending) this._resetForReconnect();

      const { path } = options;
      if (path === undefined || path === null) {
        const { port, host } = options;
        if (host !== undefined && host !== null && typeof host !== 'string') {
          throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.host" property must be of type string');
        }
        if (port === undefined || port === null) {
          throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "options" or "port" or "path" argument must be specified');
        }
        validatePort(port);
      } else if (typeof path !== 'string') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.path" property must be of type string');
      }

      if (cb) this.once('connect', cb);
      if (typeof options.timeout === 'number') this.setTimeout(options.timeout);

      this.connecting = true;
      this._id = internal.createSocket();
      if (!this._refed) internal.unref(this._id);
      this._connectOptions = {
        noDelay: options.noDelay !== undefined ? options.noDelay : this._connectOptions.noDelay,
        keepAlive: options.keepAlive !== undefined ? options.keepAlive : this._connectOptions.keepAlive,
        keepAliveInitialDelay: options.keepAliveInitialDelay
      };

      if (path !== undefined && path !== null) {
        internal.connectUnix(this._id, path, (encoded) => this._onConnectResult(JSON.parse(encoded)));
        return this;
      }

      const host = options.host || 'localhost';
      const port = +options.port;
      this._host = host;
      const family = +options.family === 4 || +options.family === 6 ? +options.family : 0;
      const nativeOptions = JSON.stringify({
        family,
        localAddress: options.localAddress || '',
        localPort: options.localPort || 0
      });

      if (typeof options.lookup === 'function' && !isIP(host)) {
        this._connectWithLookup(options, host, port, family);
        return this;
      }
      if (options.lookup !== undefined && typeof options.lookup !== 'function') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options.lookup" property must be of type function');
      }

      internal.connect(this._id, host, port, nativeOptions, (encoded) => {
        const result = JSON.parse(encoded);
        if (!this.connecting) return;
        const failedLookup = result.error && result.error.syscall === 'getaddrinfo';
        if (result.isName || failedLookup) {
          if (failedLookup) {
            this.emit('lookup', nodeError(result.error), undefined, undefined, host);
          } else {
            this.emit('lookup', null, result.ip, isIP(result.ip), host);
          }
          if (!this.connecting) return;
        }
        this._onConnectResult(result);
      });
      return this;
    }

    // A script's lookup function chooses the address, so a policy only ever sees that
    // address: the name it answered for is not checked against anything.
    _connectWithLookup(options, host, port, family) {
      const dnsOptions = { family, hints: options.hints || 0, all: true };
      const nativeOptions = JSON.stringify({
        family: 0,
        localAddress: options.localAddress || '',
        localPort: options.localPort || 0
      });
      const fail = (err) => {
        this.connecting = false;
        this.destroy(err);
      };
      options.lookup(host, dnsOptions, (err, address, addressType) => {
        if (!this.connecting) return;
        if (err) {
          this.emit('lookup', err, address, addressType, host);
          fail(err);
          return;
        }
        const addresses = Array.isArray(address) ? address : [{ address, family: addressType }];
        for (const entry of addresses) {
          if (!isIP(entry.address)) {
            fail(codedError(TypeError, 'ERR_INVALID_IP_ADDRESS', `Invalid IP address: ${entry.address}`));
            return;
          }
        }
        const first = addresses[0];
        if (!first) {
          fail(Object.assign(new Error(`getaddrinfo ENOTFOUND ${host}`), { code: 'ENOTFOUND', syscall: 'getaddrinfo', hostname: host }));
          return;
        }
        this.emit('lookup', null, first.address, first.family, host);
        if (!this.connecting) return;
        const attempt = (index) => {
          internal.connect(this._id, addresses[index].address, port, nativeOptions, (encoded) => {
            if (!this.connecting) return;
            const result = JSON.parse(encoded);
            if (result.error && index + 1 < addresses.length) {
              attempt(index + 1);
              return;
            }
            this._onConnectResult(result);
          });
        };
        attempt(0);
      });
    }

    _onConnectResult(result) {
      if (this._id === null || this.destroyed) return;
      this.connecting = false;
      if (result.error) {
        this.destroy(nodeError(result.error));
        return;
      }
      this._loadAddresses();
      const { noDelay, keepAlive, keepAliveInitialDelay } = this._connectOptions;
      if (noDelay) this.setNoDelay(true);
      if (keepAlive) this.setKeepAlive(true, keepAliveInitialDelay);
      this._touch();
      this._onTransportConnected();
    }

    // node:tls overrides this to start a handshake before any data moves.
    _onTransportConnected() {
      this.emit('connect');
      this.emit('ready');
      this._flushIO();
      if (!this.isPaused() || this.readableFlowing === null) this.read(0);
    }

    _resetForReconnect() {
      const options = { allowHalfOpen: this.allowHalfOpen };
      const fresh = new Duplex({
        ...options,
        emitClose: false,
        autoDestroy: true,
        decodeStrings: false,
        highWaterMark: this.readableHighWaterMark
      });
      fresh._readableState.flowing = this._readableState.flowing;
      fresh._readableState.readableListening = this._readableState.readableListening;
      this._readableState = fresh._readableState;
      this._writableState = fresh._writableState;
      this._hadError = false;
      this._id = null;
      this._peername = null;
      this._sockname = null;
      this._ioQueue = [];
      this._readPending = false;
    }

    // Runs fn once data can move. That is after connecting, and for TLS after the handshake.
    _whenIOReady(fn) {
      if (!this.connecting && !this._ioBlocked) {
        fn();
        return;
      }
      this._ioQueue.push(fn);
    }

    _flushIO() {
      const queue = this._ioQueue;
      this._ioQueue = [];
      for (const fn of queue) fn();
    }

    _read() {
      this._whenIOReady(() => this._readNative());
    }

    _readNative() {
      if (this._id === null || this._readPending || this.destroyed) return;
      this._readPending = true;
      const id = this._id;
      internal.read(id, (encodedError, data) => {
        this._readPending = false;
        if (this._id !== id || this.destroyed) return;
        if (encodedError) {
          this.destroy(parseError(encodedError));
          return;
        }
        if (data === null) {
          this._onEnd();
          this.push(null);
          return;
        }
        const buf = Buffer.from(data, 'base64');
        this._bytesRead += buf.length;
        this._touch();
        this.push(buf);
      });
    }

    // After the peer has finished, a socket that closes with it cannot be written to.
    _onEnd() {
      if (!this.allowHalfOpen) {
        this.write = writeAfterFIN;
      }
    }

    _write(chunk, encoding, cb) {
      this._whenIOReady(() => this._writeNative(chunk, encoding, cb));
    }

    _writeNative(chunk, encoding, cb) {
      if (this._id === null) {
        cb(codedError(Error, 'ERR_SOCKET_CLOSED', 'Socket is closed'));
        return;
      }
      const buf = typeof chunk === 'string' ? Buffer.from(chunk, encoding) : chunk;
      this._pendingWriteBytes = buf.length;
      this._touch();
      // As in Node, a write already handed to the transport reports no error if the
      // socket is destroyed before it completes.
      this._writeInFlight = cb;
      internal.write(this._id, buf.toString('base64'), (encodedError) => {
        this._pendingWriteBytes = 0;
        if (this._writeInFlight !== cb) return;
        this._writeInFlight = null;
        if (encodedError) {
          cb(parseError(encodedError));
          return;
        }
        this._bytesDispatched += buf.length;
        this._touch();
        cb();
      });
    }

    _final(cb) {
      this._whenIOReady(() => {
        if (this._id === null) {
          cb();
          return;
        }
        internal.shutdown(this._id, () => cb());
      });
    }

    _destroy(exception, cb) {
      this.connecting = false;
      this._clearTimeoutTimer();
      this._hadError = !!exception;
      this._closeTransport();

      const inFlight = this._writeInFlight;
      this._writeInFlight = null;
      if (inFlight) inFlight();

      const queue = this._ioQueue;
      this._ioQueue = [];
      for (const fn of queue) fn();

      cb(exception);
      process.nextTick(() => {
        this.emit('close', this._hadError);
        if (this._server) {
          this._server._connections--;
          this._server._emitCloseIfDrained();
          this._server = null;
        }
      });
    }

    _closeTransport() {
      if (this._id !== null) {
        internal.close(this._id);
        this._id = null;
      }
    }

    destroySoon() {
      if (this.writable) this.end();
      if (this.writableFinished) this.destroy();
      else this.once('finish', this.destroy);
    }

    resetAndDestroy() {
      if (this._id !== null) {
        if (this.connecting) {
          this.once('connect', () => this.resetAndDestroy());
          return this;
        }
        internal.setLinger(this._id, 0);
      }
      this.destroy();
      return this;
    }

    /**
     * Fires 'timeout' after ms without activity.
     */
    setTimeout(ms, callback) {
      ms = Number(ms);
      if (!(ms >= 0)) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE', `The value of "msecs" is out of range. Received ${ms}`);
      }
      this._timeoutMs = ms;
      this._clearTimeoutTimer();
      if (ms === 0) {
        if (callback) this.removeListener('timeout', callback);
      } else {
        this._lastActive = Date.now();
        this._armTimeout(ms);
        if (callback) this.once('timeout', callback);
      }
      return this;
    }

    _armTimeout(delay) {
      this._timeoutTimer = setTimeout(() => {
        this._timeoutTimer = null;
        if (this._timeoutMs === 0 || this.destroyed) return;
        const idle = Date.now() - this._lastActive;
        if (idle < this._timeoutMs) {
          this._armTimeout(this._timeoutMs - idle);
          return;
        }
        this._lastActive = Date.now();
        this._armTimeout(this._timeoutMs);
        this.emit('timeout');
      }, delay);
      this._timeoutTimer.unref();
    }

    _clearTimeoutTimer() {
      if (this._timeoutTimer) {
        clearTimeout(this._timeoutTimer);
        this._timeoutTimer = null;
      }
    }

    _touch() {
      this._lastActive = Date.now();
    }

    setKeepAlive(enable, initialDelay) {
      if (this._id !== null && !this.connecting) internal.setKeepAlive(this._id, !!enable);
      else this._connectOptions.keepAlive = !!enable;
      return this;
    }

    setNoDelay(noDelay) {
      const enable = noDelay === undefined ? true : !!noDelay;
      if (this._id !== null && !this.connecting) internal.setNoDelay(this._id, enable);
      else this._connectOptions.noDelay = enable;
      return this;
    }

    address() {
      if (!this._sockname || !this._sockname.localAddress) return {};
      return {
        address: this._sockname.localAddress,
        family: this._sockname.localFamily,
        port: this._sockname.localPort
      };
    }

    ref() {
      this._refed = true;
      if (this._id !== null) internal.ref(this._id);
      return this;
    }

    unref() {
      this._refed = false;
      if (this._id !== null) internal.unref(this._id);
      return this;
    }
  }

  function writeAfterFIN(chunk, encoding, cb) {
    if (!this.writableEnded) {
      return Socket.prototype.write.call(this, chunk, encoding, cb);
    }
    if (typeof encoding === 'function') {
      cb = encoding;
      encoding = null;
    }
    const err = codedError(Error, 'EPIPE', 'This socket has been ended by the other party');
    if (typeof cb === 'function') process.nextTick(cb, err);
    this.destroy(err);
    return false;
  }

  /**
   * Server class - TCP or Unix domain socket server.
   */
  class Server extends EventEmitter {
    constructor(options, connectionListener) {
      super();

      if (typeof options === 'function') {
        connectionListener = options;
        options = {};
      }

      options = options || {};
      this._id = null;
      this._listening = false;
      this._connections = 0;
      this.maxConnections = options.maxConnections;
      this.allowHalfOpen = !!options.allowHalfOpen;
      this.pauseOnConnect = !!options.pauseOnConnect;
      this.noDelay = !!options.noDelay;
      this.keepAlive = !!options.keepAlive;
      this.keepAliveInitialDelay = options.keepAliveInitialDelay;
      this.highWaterMark = options.highWaterMark;
      this._refed = true;

      if (connectionListener) this.on('connection', connectionListener);
    }

    get listening() { return this._listening; }
    get connections() { return this._connections; }

    /**
     * Start listening for connections.
     */
    listen(...args) {
      const [first] = args;
      let options = {};
      let callback = args.length && typeof args[args.length - 1] === 'function' ? args[args.length - 1] : null;

      if (typeof first === 'object' && first !== null) {
        options = first;
      } else if (isPipeName(first)) {
        options = { path: first };
      } else if (first !== undefined && first !== null && typeof first !== 'function') {
        options = { port: first };
        if (typeof args[1] === 'string') options.host = args[1];
      }

      if (this._listening) {
        throw codedError(Error, 'ERR_SERVER_ALREADY_LISTEN', 'Listen method has been called more than once without closing.');
      }
      if (options.handle || options.fd !== undefined) {
        throw codedError(Error, 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM', 'Listening on an existing handle is not supported');
      }
      const port = options.port === undefined || options.port === null ? 0 : validatePort(options.port);

      if (callback) this.once('listening', callback);

      this._id = internal.createServer();
      if (!this._refed) internal.unrefServer(this._id);

      let encodedError;
      if (options.path !== undefined && options.path !== null) {
        encodedError = internal.listenUnix(this._id, String(options.path));
        if (encodedError) {
          this._failListen(listenError(JSON.parse(encodedError), options.path), callback);
          return this;
        }
      } else if (options.host) {
        encodedError = internal.listen(this._id, options.host, port);
        if (encodedError) {
          this._failListen(listenError(JSON.parse(encodedError), options.host, port), callback);
          return this;
        }
      } else {
        // With no host, Node prefers the IPv6 wildcard, which also accepts IPv4.
        encodedError = internal.listen(this._id, '::', port);
        if (encodedError) {
          const desc = JSON.parse(encodedError);
          if (desc.code === 'EAFNOSUPPORT' || desc.code === 'EADDRNOTAVAIL' || desc.code === 'EINVAL') {
            encodedError = internal.listen(this._id, '0.0.0.0', port);
          }
        }
        if (encodedError) {
          this._failListen(listenError(JSON.parse(encodedError), '::', port), callback);
          return this;
        }
      }

      this._listening = true;
      if (options.signal) {
        const onAbort = () => this.close();
        if (options.signal.aborted) process.nextTick(onAbort);
        else options.signal.addEventListener('abort', onAbort, { once: true });
      }
      this._acceptConnections();
      process.nextTick(() => {
        if (this._listening) this.emit('listening');
      });
      return this;
    }

    _failListen(err, callback) {
      if (callback) this.removeListener('listening', callback);
      internal.closeServer(this._id);
      this._id = null;
      process.nextTick(() => this.emit('error', err));
    }

    _acceptConnections() {
      if (!this._listening) return;
      const id = this._id;
      internal.accept(id, (encodedError, clientId, encodedInfo) => {
        if (encodedError) {
          if (this._listening) {
            this.emit('error', parseError(encodedError));
            this._acceptConnections();
          }
          return;
        }

        if (!this._listening || this._id !== id ||
            (this.maxConnections !== undefined && this._connections >= this.maxConnections)) {
          internal.close(clientId);
          if (this._listening) {
            const info = JSON.parse(encodedInfo);
            this.emit('drop', {
              localAddress: info.localAddress,
              localPort: info.localPort,
              localFamily: info.localFamily,
              remoteAddress: info.remoteAddress,
              remotePort: info.remotePort,
              remoteFamily: info.remoteFamily
            });
            this._acceptConnections();
          }
          return;
        }

        this._connections++;
        const socket = this._newSocket(clientId);
        socket._server = this;
        if (this.noDelay) socket.setNoDelay(true);
        if (this.keepAlive) socket.setKeepAlive(true, this.keepAliveInitialDelay);
        this._onConnection(socket);
        this._acceptConnections();
      });
    }

    _newSocket(clientId) {
      const socket = new Socket({
        _netId: clientId,
        allowHalfOpen: this.allowHalfOpen,
        readableHighWaterMark: this.highWaterMark,
        writableHighWaterMark: this.highWaterMark
      });
      if (this.pauseOnConnect) socket._readableState.flowing = false;
      return socket;
    }

    // node:tls overrides this to start a handshake before reporting the connection.
    _onConnection(socket) {
      this.emit('connection', socket);
      if (!this.pauseOnConnect) socket.read(0);
    }

    /**
     * Stops accepting connections. 'close' fires once existing ones have ended.
     */
    close(callback) {
      if (typeof callback === 'function') {
        if (this._id === null) {
          this.once('close', () => {
            callback(codedError(Error, 'ERR_SERVER_NOT_RUNNING', 'Server is not running.'));
          });
        } else {
          this.once('close', callback);
        }
      }

      if (this._id !== null) {
        internal.closeServer(this._id);
        this._id = null;
      }
      this._listening = false;
      this._emitCloseIfDrained();
      return this;
    }

    _emitCloseIfDrained() {
      if (this._id !== null || this._connections > 0) return;
      process.nextTick(() => {
        if (this._id !== null || this._connections > 0) return;
        this.emit('close');
      });
    }

    /**
     * Get the server's address.
     */
    address() {
      if (this._id === null) return null;
      const encoded = internal.getServerAddress(this._id);
      return encoded ? JSON.parse(encoded) : null;
    }

    /**
     * Get connected socket count.
     */
    getConnections(callback) {
      process.nextTick(callback, null, this._connections);
      return this;
    }

    ref() {
      this._refed = true;
      if (this._id !== null) internal.refServer(this._id);
      return this;
    }

    unref() {
      this._refed = false;
      if (this._id !== null) internal.unrefServer(this._id);
      return this;
    }
  }

  function createConnection(...args) {
    const normalized = normalizeArgs(args);
    const [options] = normalized;
    const socket = new Socket(options);
    if (options.timeout) socket.setTimeout(options.timeout);
    return socket.connect(normalized);
  }

  function createServer(options, connectionListener) {
    return new Server(options, connectionListener);
  }

  const kIPv4Part = '(?:[0-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5])';
  const kIPv4 = `(?:${kIPv4Part}\\.){3}${kIPv4Part}`;
  const kIPv6Seg = '(?:[0-9a-fA-F]{1,4})';
  const IPv4Re = new RegExp(`^${kIPv4}$`);
  const IPv6Re = new RegExp('^(?:' +
    `(?:${kIPv6Seg}:){7}(?:${kIPv6Seg}|:)|` +
    `(?:${kIPv6Seg}:){6}(?:${kIPv4}|:${kIPv6Seg}|:)|` +
    `(?:${kIPv6Seg}:){5}(?::${kIPv4}|(?::${kIPv6Seg}){1,2}|:)|` +
    `(?:${kIPv6Seg}:){4}(?:(?::${kIPv6Seg}){0,1}:${kIPv4}|(?::${kIPv6Seg}){1,3}|:)|` +
    `(?:${kIPv6Seg}:){3}(?:(?::${kIPv6Seg}){0,2}:${kIPv4}|(?::${kIPv6Seg}){1,4}|:)|` +
    `(?:${kIPv6Seg}:){2}(?:(?::${kIPv6Seg}){0,3}:${kIPv4}|(?::${kIPv6Seg}){1,5}|:)|` +
    `(?:${kIPv6Seg}:){1}(?:(?::${kIPv6Seg}){0,4}:${kIPv4}|(?::${kIPv6Seg}){1,6}|:)|` +
    `(?::(?:(?::${kIPv6Seg}){0,5}:${kIPv4}|(?::${kIPv6Seg}){1,7}|:))` +
    ')(?:%[0-9a-zA-Z-.:]{1,})?$');

  function isIPv4(input) {
    return IPv4Re.test(String(input));
  }

  function isIPv6(input) {
    return IPv6Re.test(String(input));
  }

  function isIP(input) {
    if (isIPv4(input)) return 4;
    if (isIPv6(input)) return 6;
    return 0;
  }

  function ipv4ToBigInt(ip) {
    return ip.split('.').reduce((acc, part) => (acc << 8n) + BigInt(+part), 0n);
  }

  // Returns the address as a number, with IPv4 in its IPv4-mapped IPv6 form so the two
  // families compare as they do in Node.
  function addressToBigInt(ip, family) {
    if (family === 'ipv4') return (0xffffn << 32n) + ipv4ToBigInt(ip);
    let text = ip.split('%')[0];
    const v4 = /(\d+\.\d+\.\d+\.\d+)$/.exec(text);
    let tail = 0n;
    let tailBits = 0n;
    if (v4) {
      tail = ipv4ToBigInt(v4[1]);
      tailBits = 32n;
      text = text.slice(0, -v4[1].length);
      if (text.endsWith(':') && !text.endsWith('::')) text = text.slice(0, -1);
    }
    const groups = text.split('::');
    const head = groups[0] ? groups[0].split(':') : [];
    const rest = groups.length > 1 && groups[1] ? groups[1].split(':') : [];
    const total = (128n - tailBits) / 16n;
    const zeros = groups.length > 1 ? Number(total) - head.length - rest.length : 0;
    const all = [...head, ...Array(zeros).fill('0'), ...rest];
    let out = 0n;
    for (const part of all) out = (out << 16n) + BigInt('0x' + part);
    return (out << tailBits) + tail;
  }

  function normalizeFamily(family) {
    const f = String(family === undefined ? 'ipv4' : family).toLowerCase();
    if (f !== 'ipv4' && f !== 'ipv6') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The argument 'family' must be one of: 'ipv4', 'ipv6'. Received ${String(family)}`);
    }
    return f;
  }

  function requireAddress(address, family) {
    const valid = family === 'ipv4' ? isIPv4(address) : isIPv6(address);
    if (!valid) {
      throw codedError(TypeError, 'ERR_INVALID_ADDRESS', `Invalid ${family} address: ${String(address)}`);
    }
  }

  class SocketAddress {
    constructor(options) {
      options = options || {};
      this.family = normalizeFamily(options.family);
      this.address = options.address === undefined ? (this.family === 'ipv4' ? '127.0.0.1' : '::') : options.address;
      requireAddress(this.address, this.family);
      this.port = options.port === undefined ? 0 : validatePort(options.port);
      this.flowlabel = options.flowlabel === undefined ? 0 : options.flowlabel;
    }

    static isSocketAddress(value) {
      return value instanceof SocketAddress;
    }
  }

  function familyName(family) {
    return family === 'ipv4' ? 'IPv4' : 'IPv6';
  }

  class BlockList {
    constructor() {
      this._rules = [];
    }

    _parse(address, family) {
      if (address instanceof SocketAddress) {
        return { address: address.address, family: address.family };
      }
      const f = normalizeFamily(family);
      requireAddress(address, f);
      return { address, family: f };
    }

    addAddress(address, family) {
      const a = this._parse(address, family);
      const value = addressToBigInt(a.address, a.family);
      this._rules.push({ start: value, end: value, text: `Address: ${familyName(a.family)} ${a.address}` });
    }

    addRange(start, end, family) {
      const a = this._parse(start, family);
      const b = this._parse(end, family);
      const s = addressToBigInt(a.address, a.family);
      const e = addressToBigInt(b.address, b.family);
      if (s > e) {
        throw codedError(RangeError, 'ERR_INVALID_ARG_VALUE', 'The argument \'start\' must be less than or equal to the end');
      }
      this._rules.push({ start: s, end: e, text: `Range: ${familyName(a.family)} ${a.address}-${b.address}` });
    }

    addSubnet(network, prefix, family) {
      const a = this._parse(network, family);
      const max = a.family === 'ipv4' ? 32 : 128;
      if (!Number.isInteger(prefix) || prefix < 0 || prefix > max) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE', `The value of "prefix" is out of range. It must be >= 0 && <= ${max}. Received ${prefix}`);
      }
      const hostBits = BigInt(max - prefix);
      const base = addressToBigInt(a.address, a.family);
      const start = (base >> hostBits) << hostBits;
      this._rules.push({ start, end: start + (1n << hostBits) - 1n, text: `Subnet: ${familyName(a.family)} ${a.address}/${prefix}` });
    }

    check(address, family) {
      const a = this._parse(address, family);
      const value = addressToBigInt(a.address, a.family);
      return this._rules.some((rule) => value >= rule.start && value <= rule.end);
    }

    get rules() {
      return this._rules.map((rule) => rule.text).reverse();
    }
  }

  let defaultAutoSelectFamily = true;
  let defaultAutoSelectFamilyAttemptTimeout = 250;

  // Export module
  const net = {
    Socket,
    Stream: Socket,
    Server,
    BlockList,
    SocketAddress,
    createConnection,
    createServer,
    connect: createConnection,
    isIP,
    isIPv4,
    isIPv6,
    getDefaultAutoSelectFamily: () => defaultAutoSelectFamily,
    setDefaultAutoSelectFamily: (value) => { defaultAutoSelectFamily = !!value; },
    getDefaultAutoSelectFamilyAttemptTimeout: () => defaultAutoSelectFamilyAttemptTimeout,
    setDefaultAutoSelectFamilyAttemptTimeout: (value) => { defaultAutoSelectFamilyAttemptTimeout = value; },
    _normalizeArgs: normalizeArgs
  };

  global.__net_module = net;

})(globalThis);
