// net - TCP/IPC networking module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const internal = global.__net_internal;

  /**
   * Socket class - Represents a TCP/IPC socket.
   */
  class Socket extends EventEmitter {
    constructor(options) {
      super();
      options = options || {};
      
      this._id = null;
      this._connecting = false;
      this._connected = false;
      this._destroyed = false;
      this._writable = true;
      this._readable = true;
      this._allowHalfOpen = options.allowHalfOpen || false;
      this._readableHighWaterMark = options.readableHighWaterMark || 16384;
      this._writableHighWaterMark = options.writableHighWaterMark || 16384;
      this._timeout = 0;
      this._keepAlive = false;
      this._noDelay = false;
      this._localAddress = null;
      this._localPort = null;
      this._remoteAddress = null;
      this._remotePort = null;
      this._remoteFamily = null;
      this._bytesRead = 0;
      this._bytesWritten = 0;
      this._pendingData = [];
      this._readBuffer = [];
    }

    get connecting() { return this._connecting; }
    get destroyed() { return this._destroyed; }
    get localAddress() { return this._localAddress; }
    get localPort() { return this._localPort; }
    get remoteAddress() { return this._remoteAddress; }
    get remotePort() { return this._remotePort; }
    get remoteFamily() { return this._remoteFamily; }
    get bytesRead() { return this._bytesRead; }
    get bytesWritten() { return this._bytesWritten; }
    get readyState() {
      if (this._connecting) return 'opening';
      if (this._connected) return 'open';
      if (!this._readable && !this._writable) return 'closed';
      if (!this._readable) return 'writeOnly';
      if (!this._writable) return 'readOnly';
      return 'closed';
    }

    /**
     * Connect to a remote address.
     */
    connect(options, callback) {
      if (typeof options === 'number') {
        // connect(port, host, callback)
        const port = options;
        const host = typeof arguments[1] === 'string' ? arguments[1] : 'localhost';
        callback = typeof arguments[1] === 'function' ? arguments[1] : arguments[2];
        options = { port, host };
      } else if (typeof options === 'string') {
        // connect(path, callback) - IPC
        options = { path: options };
        callback = arguments[1];
      }

      if (callback) {
        this.once('connect', callback);
      }

      this._connecting = true;

      const host = options.host || 'localhost';
      const port = options.port;

      // Create socket and connect
      this._id = internal.createSocket();
      
      internal.connect(this._id, host, port, (err) => {
        this._connecting = false;
        
        if (err) {
          this._destroyed = true;
          this.emit('error', new Error(err));
          this.emit('close', true);
          return;
        }

        this._connected = true;
        
        // Get local/remote address info
        const addrInfo = internal.getAddressInfo(this._id);
        if (addrInfo) {
          this._localAddress = addrInfo.localAddress;
          this._localPort = addrInfo.localPort;
          this._remoteAddress = addrInfo.remoteAddress;
          this._remotePort = addrInfo.remotePort;
          this._remoteFamily = addrInfo.remoteFamily || 'IPv4';
        }

        this.emit('connect');
        this.emit('ready');

        // Start reading
        this._startReading();
      });

      return this;
    }

    _startReading() {
      if (this._destroyed || !this._connected) return;

      internal.read(this._id, (err, data) => {
        if (err) {
          if (!this._destroyed) {
            this.emit('error', new Error(err));
            this.destroy();
          }
          return;
        }

        if (data === null) {
          // EOF
          this._readable = false;
          this.emit('end');
          if (!this._allowHalfOpen) {
            this.end();
          }
          return;
        }

        this._bytesRead += data.length;
        
        // Convert to Buffer if it's a string
        const buf = typeof data === 'string' ? Buffer.from(data, 'binary') : Buffer.from(data);
        this.emit('data', buf);

        // Continue reading
        this._startReading();
      });
    }

    /**
     * Write data to the socket.
     */
    write(data, encoding, callback) {
      if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (this._destroyed) {
        if (callback) callback(new Error('Socket is closed'));
        return false;
      }

      if (!this._connected) {
        // Queue data until connected
        this._pendingData.push({ data, encoding, callback });
        return false;
      }

      // Convert data to Buffer
      let buf;
      if (Buffer.isBuffer(data)) {
        buf = data;
      } else if (typeof data === 'string') {
        buf = Buffer.from(data, encoding || 'utf8');
      } else {
        buf = Buffer.from(data);
      }

      internal.write(this._id, buf.toString('binary'), (err) => {
        if (err) {
          this.emit('error', new Error(err));
          if (callback) callback(new Error(err));
          return;
        }

        this._bytesWritten += buf.length;
        this.emit('drain');
        if (callback) callback();
      });

      return true;
    }

    /**
     * End the socket (half-close or full close).
     */
    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
      } else if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (data) {
        this.write(data, encoding);
      }

      this._writable = false;

      if (callback) {
        this.once('finish', callback);
      }

      if (!this._readable || !this._allowHalfOpen) {
        this.destroy();
      } else {
        this.emit('finish');
      }

      return this;
    }

    /**
     * Destroy the socket.
     */
    destroy(error) {
      if (this._destroyed) return this;

      this._destroyed = true;
      this._connected = false;
      this._readable = false;
      this._writable = false;

      if (this._id !== null) {
        internal.close(this._id);
        this._id = null;
      }

      if (error) {
        this.emit('error', error);
      }

      this.emit('close', !!error);
      return this;
    }

    /**
     * Set timeout for the socket.
     */
    setTimeout(timeout, callback) {
      this._timeout = timeout;
      if (callback) {
        this.once('timeout', callback);
      }
      if (this._id !== null) {
        internal.setTimeout(this._id, timeout);
      }
      return this;
    }

    /**
     * Enable/disable keep-alive.
     */
    setKeepAlive(enable, initialDelay) {
      this._keepAlive = enable;
      if (this._id !== null) {
        internal.setKeepAlive(this._id, enable, initialDelay || 0);
      }
      return this;
    }

    /**
     * Enable/disable Nagle's algorithm.
     */
    setNoDelay(noDelay) {
      this._noDelay = noDelay !== false;
      if (this._id !== null) {
        internal.setNoDelay(this._id, this._noDelay);
      }
      return this;
    }

    /**
     * Returns address info.
     */
    address() {
      return {
        address: this._localAddress,
        port: this._localPort,
        family: 'IPv4'
      };
    }

    /**
     * Reference/unreference for event loop.
     */
    ref() {
      if (this._id !== null) {
        internal.ref(this._id);
      }
      return this;
    }

    unref() {
      if (this._id !== null) {
        internal.unref(this._id);
      }
      return this;
    }

    /**
     * Pause reading.
     */
    pause() {
      // For now, a no-op - would need to pause the read loop
      return this;
    }

    /**
     * Resume reading.
     */
    resume() {
      return this;
    }

    /**
     * Pipe to another stream.
     */
    pipe(destination, options) {
      this.on('data', (chunk) => {
        destination.write(chunk);
      });
      this.on('end', () => {
        if (!options || options.end !== false) {
          destination.end();
        }
      });
      return destination;
    }
  }

  /**
   * Server class - TCP server.
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
      this._maxConnections = options.maxConnections || Infinity;
      this._allowHalfOpen = options.allowHalfOpen || false;
      this._pauseOnConnect = options.pauseOnConnect || false;

      if (connectionListener) {
        this.on('connection', connectionListener);
      }
    }

    get listening() { return this._listening; }
    get maxConnections() { return this._maxConnections; }
    set maxConnections(val) { this._maxConnections = val; }

    /**
     * Start listening for connections.
     */
    listen(options, callback) {
      if (typeof options === 'number') {
        // listen(port, host, backlog, callback)
        const port = options;
        const host = typeof arguments[1] === 'string' ? arguments[1] : '0.0.0.0';
        callback = typeof arguments[arguments.length - 1] === 'function' ? arguments[arguments.length - 1] : undefined;
        options = { port, host };
      } else if (typeof options === 'string') {
        // listen(path, callback) - IPC
        options = { path: options };
        callback = arguments[1];
      }

      if (callback) {
        this.once('listening', callback);
      }

      const host = options.host || '0.0.0.0';
      const port = options.port || 0;
      const backlog = options.backlog || 511;

      this._id = internal.createServer();

      internal.listen(this._id, host, port, backlog, (err) => {
        if (err) {
          this.emit('error', new Error(err));
          return;
        }

        this._listening = true;
        this.emit('listening');

        // Start accepting connections
        this._acceptConnections();
      });

      return this;
    }

    _acceptConnections() {
      if (!this._listening) return;

      internal.accept(this._id, (err, clientId, addrInfo) => {
        if (err) {
          if (this._listening) {
            this.emit('error', new Error(err));
          }
          return;
        }

        if (!this._listening) {
          internal.closeSocket(clientId);
          return;
        }

        if (this._connections >= this._maxConnections) {
          internal.closeSocket(clientId);
          this._acceptConnections();
          return;
        }

        this._connections++;

        // Create a Socket for the connection
        const socket = new Socket({ allowHalfOpen: this._allowHalfOpen });
        socket._id = clientId;
        socket._connected = true;
        socket._localAddress = addrInfo.localAddress;
        socket._localPort = addrInfo.localPort;
        socket._remoteAddress = addrInfo.remoteAddress;
        socket._remotePort = addrInfo.remotePort;
        socket._remoteFamily = addrInfo.remoteFamily || 'IPv4';

        socket.on('close', () => {
          this._connections--;
        });

        this.emit('connection', socket);

        if (!this._pauseOnConnect) {
          socket._startReading();
        }

        // Continue accepting
        this._acceptConnections();
      });
    }

    /**
     * Close the server.
     */
    close(callback) {
      if (callback) {
        this.once('close', callback);
      }

      this._listening = false;

      if (this._id !== null) {
        internal.closeServer(this._id);
        this._id = null;
      }

      this.emit('close');
      return this;
    }

    /**
     * Get the server's address.
     */
    address() {
      if (!this._listening) return null;
      return internal.getServerAddress(this._id);
    }

    /**
     * Get connected socket count.
     */
    getConnections(callback) {
      process.nextTick(() => callback(null, this._connections));
    }

    ref() {
      if (this._id !== null) {
        internal.refServer(this._id);
      }
      return this;
    }

    unref() {
      if (this._id !== null) {
        internal.unrefServer(this._id);
      }
      return this;
    }
  }

  /**
   * Create a new socket connection.
   */
  function createConnection(options, callback) {
    const socket = new Socket(options);
    return socket.connect(options, callback);
  }

  /**
   * Create a new server.
   */
  function createServer(options, connectionListener) {
    return new Server(options, connectionListener);
  }

  /**
   * Connect (alias for createConnection).
   */
  function connect(options, callback) {
    return createConnection(options, callback);
  }

  /**
   * Check if input is an IP address.
   */
  function isIP(input) {
    if (isIPv4(input)) return 4;
    if (isIPv6(input)) return 6;
    return 0;
  }

  function isIPv4(input) {
    const parts = (input || '').split('.');
    if (parts.length !== 4) return false;
    for (const part of parts) {
      const num = parseInt(part, 10);
      if (isNaN(num) || num < 0 || num > 255 || String(num) !== part) {
        return false;
      }
    }
    return true;
  }

  function isIPv6(input) {
    // Simplified check
    if (!input) return false;
    const parts = input.split(':');
    if (parts.length < 3 || parts.length > 8) return false;
    for (const part of parts) {
      if (part === '') continue; // Allow :: shorthand
      if (!/^[0-9a-fA-F]{1,4}$/.test(part)) return false;
    }
    return true;
  }

  // Export module
  const net = {
    Socket,
    Server,
    createConnection,
    createServer,
    connect,
    isIP,
    isIPv4,
    isIPv6
  };

  global.__net_module = net;

})(globalThis);
