// tls - TLS/SSL module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const net = global.__net_module;
  const internal = global.__tls_internal;

  // TLS constants
  const DEFAULT_MIN_VERSION = 'TLSv1.2';
  const DEFAULT_MAX_VERSION = 'TLSv1.3';

  /**
   * TLSSocket class - Represents a TLS-wrapped socket.
   */
  class TLSSocket extends EventEmitter {
    constructor(socket, options) {
      super();
      
      options = options || {};
      
      this._socket = socket || new net.Socket();
      this._id = null;
      this._encrypted = false;
      this._authorized = false;
      this._authorizationError = null;
      this._isServer = options.isServer || false;
      this._requestCert = options.requestCert || false;
      this._rejectUnauthorized = options.rejectUnauthorized !== false;
      this._servername = options.servername;
      this._ALPNProtocols = options.ALPNProtocols;
      this._secureContext = options.secureContext;
      this._session = options.session;
      
      // Mirror socket properties
      this._connecting = false;
      this._connected = false;
      this._destroyed = false;
      this._writable = true;
      this._readable = true;
      
      // Forward events from underlying socket
      this._setupSocketEvents();
    }

    get encrypted() { return this._encrypted; }
    get authorized() { return this._authorized; }
    get authorizationError() { return this._authorizationError; }
    get connecting() { return this._socket.connecting; }
    get destroyed() { return this._socket.destroyed; }
    get localAddress() { return this._socket.localAddress; }
    get localPort() { return this._socket.localPort; }
    get remoteAddress() { return this._socket.remoteAddress; }
    get remotePort() { return this._socket.remotePort; }
    get remoteFamily() { return this._socket.remoteFamily; }

    _setupSocketEvents() {
      // Forward key events
      this._socket.on('error', (err) => this.emit('error', err));
      this._socket.on('close', (hadError) => this.emit('close', hadError));
      this._socket.on('end', () => this.emit('end'));
      this._socket.on('timeout', () => this.emit('timeout'));
    }

    /**
     * Connect and perform TLS handshake.
     */
    connect(options, callback) {
      if (typeof options === 'number') {
        const port = options;
        const host = typeof arguments[1] === 'string' ? arguments[1] : 'localhost';
        callback = typeof arguments[1] === 'function' ? arguments[1] : arguments[2];
        options = { port, host };
      }

      if (callback) {
        this.once('secureConnect', callback);
      }

      const host = options.host || options.hostname || 'localhost';
      const port = options.port;
      const servername = options.servername || options.host || host;

      this._servername = servername;

      // Create TLS socket
      this._id = internal.createTLSSocket(this._isServer);

      // First connect TCP
      internal.connect(this._id, host, port, servername, (err) => {
        if (err) {
          this.emit('error', new Error(err));
          this.emit('close', true);
          return;
        }

        this._encrypted = true;
        this._connected = true;
        this._authorized = true; // Simplified - would check cert

        this.emit('secureConnect');

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
          this._readable = false;
          this.emit('end');
          return;
        }

        const buf = Buffer.from(data, 'binary');
        this.emit('data', buf);

        this._startReading();
      });
    }

    /**
     * Write data.
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

        this.emit('drain');
        if (callback) callback();
      });

      return true;
    }

    /**
     * End the socket.
     */
    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
      }

      if (data) {
        this.write(data, encoding);
      }

      this._writable = false;

      if (callback) {
        this.once('finish', callback);
      }

      this.destroy();
      return this;
    }

    /**
     * Destroy the socket.
     */
    destroy(error) {
      if (this._destroyed) return this;

      this._destroyed = true;
      this._connected = false;

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
     * Set timeout.
     */
    setTimeout(timeout, callback) {
      if (callback) {
        this.once('timeout', callback);
      }
      return this;
    }

    setKeepAlive(enable, initialDelay) {
      return this;
    }

    setNoDelay(noDelay) {
      return this;
    }

    /**
     * Get certificate info.
     */
    getPeerCertificate(detailed) {
      if (!this._id) return {};
      return internal.getPeerCertificate(this._id, detailed) || {};
    }

    /**
     * Get cipher info.
     */
    getCipher() {
      if (!this._id) return null;
      return internal.getCipher(this._id);
    }

    /**
     * Get protocol version.
     */
    getProtocol() {
      if (!this._id) return null;
      return internal.getProtocol(this._id) || 'TLSv1.3';
    }

    /**
     * Get session.
     */
    getSession() {
      return this._session;
    }

    /**
     * Get TLS ticket keys.
     */
    getTLSTicket() {
      return null;
    }

    /**
     * Get ALPN protocol.
     */
    getALPNProtocol() {
      return false;
    }

    /**
     * Renegotiate (not typically supported).
     */
    renegotiate(options, callback) {
      if (callback) {
        process.nextTick(() => callback(new Error('Renegotiation not supported')));
      }
      return false;
    }

    /**
     * Disable renegotiation.
     */
    disableRenegotiation() {
      // No-op
    }

    address() {
      return this._socket.address();
    }

    ref() {
      return this;
    }

    unref() {
      return this;
    }
  }

  /**
   * TLS Server class.
   */
  class Server extends EventEmitter {
    constructor(options, connectionListener) {
      super();
      
      if (typeof options === 'function') {
        connectionListener = options;
        options = {};
      }
      
      options = options || {};
      
      this._options = options;
      this._server = null;
      this._listening = false;
      this._connections = 0;
      
      if (connectionListener) {
        this.on('secureConnection', connectionListener);
      }
    }

    get listening() { return this._listening; }

    /**
     * Start listening.
     */
    listen(port, host, callback) {
      if (typeof host === 'function') {
        callback = host;
        host = undefined;
      }

      if (callback) {
        this.once('listening', callback);
      }

      // Create underlying TCP server
      this._server = net.createServer((socket) => {
        // Wrap with TLS
        const tlsSocket = new TLSSocket(socket, {
          isServer: true,
          ...this._options
        });
        
        this._connections++;
        
        tlsSocket.on('close', () => {
          this._connections--;
        });

        // Emit secure connection
        process.nextTick(() => {
          tlsSocket._encrypted = true;
          tlsSocket._authorized = true;
          this.emit('secureConnection', tlsSocket);
        });
      });

      this._server.on('error', (err) => this.emit('error', err));
      this._server.on('listening', () => {
        this._listening = true;
        this.emit('listening');
      });

      this._server.listen(port, host);

      return this;
    }

    /**
     * Close the server.
     */
    close(callback) {
      if (callback) {
        this.once('close', callback);
      }

      this._listening = false;

      if (this._server) {
        this._server.close();
        this._server = null;
      }

      this.emit('close');
      return this;
    }

    address() {
      return this._server ? this._server.address() : null;
    }

    getConnections(callback) {
      process.nextTick(() => callback(null, this._connections));
    }

    ref() {
      if (this._server) this._server.ref();
      return this;
    }

    unref() {
      if (this._server) this._server.unref();
      return this;
    }

    getTicketKeys() {
      return Buffer.alloc(48);
    }

    setTicketKeys(keys) {
      // No-op
    }

    setSecureContext(context) {
      // No-op
    }

    addContext(hostname, context) {
      // No-op
    }
  }

  /**
   * Create a TLS connection.
   */
  function connect(options, callback) {
    if (typeof options === 'number') {
      const port = options;
      const host = typeof arguments[1] === 'string' ? arguments[1] : undefined;
      callback = typeof arguments[arguments.length - 1] === 'function' ? arguments[arguments.length - 1] : undefined;
      options = { port, host };
    }

    const socket = new TLSSocket(null, options);
    return socket.connect(options, callback);
  }

  /**
   * Create a TLS server.
   */
  function createServer(options, connectionListener) {
    return new Server(options, connectionListener);
  }

  /**
   * Create a secure context.
   */
  function createSecureContext(options) {
    return {
      context: options || {},
      // These would be filled in by the Go side
    };
  }

  /**
   * Get supported ciphers.
   */
  function getCiphers() {
    return [
      'TLS_AES_256_GCM_SHA384',
      'TLS_CHACHA20_POLY1305_SHA256',
      'TLS_AES_128_GCM_SHA256',
      'ECDHE-RSA-AES256-GCM-SHA384',
      'ECDHE-RSA-AES128-GCM-SHA256'
    ];
  }

  /**
   * Check if name matches certificate.
   */
  function checkServerIdentity(hostname, cert) {
    // Simplified check
    if (cert.subject && cert.subject.CN === hostname) {
      return undefined; // No error
    }
    if (cert.subjectaltname) {
      const altNames = cert.subjectaltname.split(', ');
      for (const name of altNames) {
        if (name.replace('DNS:', '') === hostname) {
          return undefined;
        }
      }
    }
    return new Error('Hostname/IP does not match certificate');
  }

  // TLS constants
  const rootCertificates = [];
  const DEFAULT_ECDH_CURVE = 'auto';

  // Export module
  const tls = {
    TLSSocket,
    Server,
    connect,
    createConnection: connect,
    createServer,
    createSecureContext,
    getCiphers,
    checkServerIdentity,
    rootCertificates,
    DEFAULT_ECDH_CURVE,
    DEFAULT_MIN_VERSION,
    DEFAULT_MAX_VERSION
  };

  global.__tls_module = tls;

})(globalThis);
