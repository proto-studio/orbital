// tls - TLS/SSL module
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const net = global.__net_module;
  const internal = global.__tls_internal;

  // TLS constants
  const DEFAULT_MIN_VERSION = 'TLSv1.2';
  const DEFAULT_MAX_VERSION = 'TLSv1.3';

  // Node's default for a socket's write buffer.
  const kHighWaterMark = 16384;

  function makeError(message, code) {
    const err = new Error(message);
    err.code = code;
    return err;
  }

  function hasOwn(object, key) {
    return Object.prototype.hasOwnProperty.call(object, key);
  }

  function pemList(value) {
    if (value === undefined || value === null) return [];
    return (Array.isArray(value) ? value : [value]).map((item) =>
      typeof item === 'string' ? item : Buffer.from(item).toString('utf8'));
  }

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
      this._allowHalfOpen = !!options.allowHalfOpen;

      // Writes run one at a time, in order, once the handshake is done. end() takes
      // effect after the last one.
      this._writes = [];
      this._writeLength = 0;
      this._currentWrite = null;
      this._needDrain = false;
      this._ending = false;
      this._finished = false;
      this._readEnded = false;
      this._shuttingDown = false;
      this._endCallbacks = [];

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

      const host = options.host || 'localhost';
      const servername = options.servername || host;
      const checkIdentity = typeof options.checkServerIdentity === 'function' ? options.checkServerIdentity : null;

      this._servername = servername;

      // As in Node, the environment only supplies a default for an absent option.
      const rejectUnauthorized = hasOwn(options, 'rejectUnauthorized')
        ? options.rejectUnauthorized !== false
        : process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0';
      this._rejectUnauthorized = rejectUnauthorized;

      // A secure context supplies the credentials in place of the connect options.
      const credentials = options.secureContext ? (options.secureContext.context || {}) : options;

      // Bad credentials throw here, before connecting, as they do in Node.
      const id = internal.createTLSSocket(this._isServer, host, JSON.stringify({
        rejectUnauthorized,
        ca: credentials.ca ? pemList(credentials.ca) : null,
        cert: pemList(credentials.cert).join('\n'),
        key: pemList(credentials.key).join('\n'),
        servername: options.servername || '',
        scriptChecksIdentity: checkIdentity !== null
      }));
      if (typeof id === 'string') {
        const { message, code } = JSON.parse(id);
        const err = new Error(message);
        if (code) err.code = code;
        throw err;
      }
      this._id = id;

      internal.connect(this._id, options.port, (_, encoded) => {
        if (this._destroyed) return;

        const result = JSON.parse(encoded);
        this._authorized = result.authorized;
        this._authorizationError = result.authorizationError || null;

        if (result.message) {
          const err = new Error(result.message);
          if (result.code) err.code = result.code;
          this.destroy(err);
          return;
        }

        this._encrypted = true;
        this._connected = true;

        if (result.scriptChecksIdentity) {
          const err = checkIdentity(servername, this.getPeerCertificate(true));
          if (err) {
            this._authorized = false;
            this._authorizationError = err.code || err.message;
            if (rejectUnauthorized) {
              this.destroy(err);
              return;
            }
          }
        }

        this.emit('secureConnect');

        this._pumpWrites();
        this._startReading();
      });

      return this;
    }

    _startReading() {
      if (this._destroyed || !this._connected) return;

      internal.read(this._id, (err, data) => {
        if (this._destroyed) return;

        if (err) {
          this.emit('error', new Error(err));
          this.destroy();
          return;
        }

        if (data === null) {
          this._readable = false;
          this._readEnded = true;
          this.emit('end');
          if (!this._allowHalfOpen) this.end();
          this._destroyIfDone();
          return;
        }

        this.emit('data', Buffer.from(data, 'base64'));

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

      if (this._ending || this._destroyed) {
        const err = this._ending
          ? makeError('write after end', 'ERR_STREAM_WRITE_AFTER_END')
          : makeError('Cannot call write after a stream was destroyed', 'ERR_STREAM_DESTROYED');
        process.nextTick(() => {
          if (callback) callback(err);
          this.destroy(err);
        });
        return false;
      }

      // A socket that never connected has nothing to write to.
      if (this._id === null) {
        const err = new Error('Socket not found');
        this.emit('error', err);
        if (callback) callback(err);
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

      this._writes.push({ buf, callback });
      this._writeLength += buf.length;
      const belowLimit = this._writeLength < kHighWaterMark;
      if (!belowLimit) this._needDrain = true;

      this._pumpWrites();
      return belowLimit;
    }

    _pumpWrites() {
      if (this._currentWrite || !this._connected || this._destroyed) return;

      const entry = this._writes.shift();
      if (!entry) {
        if (this._ending) this._shutdown();
        return;
      }

      this._currentWrite = entry;
      internal.write(this._id, entry.buf.toString('base64'), (err) => {
        // destroy() has already answered this write.
        if (this._destroyed) return;

        this._currentWrite = null;
        this._writeLength -= entry.buf.length;

        if (err) {
          const error = new Error(err);
          if (entry.callback) entry.callback(error);
          this.destroy(error);
          return;
        }

        if (this._needDrain && !this._ending && this._writeLength === 0) {
          this._needDrain = false;
          this.emit('drain');
        }
        if (entry.callback) entry.callback();
        this._pumpWrites();
      });
    }

    _shutdown() {
      if (this._shuttingDown) return;
      this._shuttingDown = true;

      internal.shutdown(this._id, () => {
        if (this._destroyed) return;

        this._finished = true;
        for (const callback of this._endCallbacks.splice(0)) callback();
        this.emit('finish');
        this._destroyIfDone();
      });
    }

    _destroyIfDone() {
      if (this._readEnded && this._finished) this.destroy();
    }

    /**
     * Finish writing. The socket closes once the peer has finished too.
     */
    end(data, encoding, callback) {
      if (typeof data === 'function') {
        callback = data;
        data = undefined;
        encoding = undefined;
      } else if (typeof encoding === 'function') {
        callback = encoding;
        encoding = undefined;
      }

      if (data !== undefined && data !== null) {
        this.write(data, encoding);
      }

      if (callback) {
        if (this._finished) {
          process.nextTick(callback, makeError('Cannot call end after a stream was finished', 'ERR_STREAM_ALREADY_FINISHED'));
        } else if (this._destroyed) {
          process.nextTick(callback, makeError('Cannot call end after a stream was destroyed', 'ERR_STREAM_DESTROYED'));
        } else {
          this._endCallbacks.push(callback);
        }
      }

      if (!this._ending) {
        this._ending = true;
        this._writable = false;
        if (this._id === null) {
          this.destroy();
        } else {
          this._pumpWrites();
        }
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

      if (this._id !== null) {
        internal.close(this._id);
        this._id = null;
      }

      this._cancelPendingWrites();

      if (error) {
        this.emit('error', error);
      }

      this.emit('close', !!error);
      return this;
    }

    // As in Node, a write already handed to the socket reports no error, and one still
    // waiting never went out.
    _cancelPendingWrites() {
      const current = this._currentWrite;
      this._currentWrite = null;
      if (current && current.callback) current.callback(null);

      const err = makeError('Cannot call write after a stream was destroyed', 'ERR_STREAM_DESTROYED');
      for (const { callback } of this._writes.splice(0)) {
        if (callback) callback(err);
      }
      for (const callback of this._endCallbacks.splice(0)) callback(err);
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
      const encoded = internal.getPeerCertificate(this._id);
      if (!encoded) return {};
      const cert = JSON.parse(encoded);
      cert.raw = Buffer.from(cert.raw, 'base64');
      return cert;
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
  function connect(...args) {
    let options = {};
    let callback;
    for (const arg of args) {
      if (typeof arg === 'function') callback = arg;
      else if (typeof arg === 'number') options.port = arg;
      else if (typeof arg === 'string') {
        if (options.port === undefined) options.port = Number(arg);
        else options.host = arg;
      } else if (arg) options = { ...options, ...arg };
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

  // Whether a hostname matches a certificate name. A wildcard covers part or all of the
  // leftmost label only, and needs at least three labels, as in Node.
  function hostMatches(hostname, pattern, allowWildcard) {
    const labels = (name) => name.replace(/\.$/, '').toLowerCase().split('.');
    const host = labels(hostname);
    const want = labels(pattern);
    if (host.length !== want.length || want.includes('')) return false;
    if (want.some((label) => /[^\u0021-\u007f]/.test(label))) return false;
    for (let i = 1; i < host.length; i++) {
      if (host[i] !== want[i]) return false;
    }
    const parts = want[0].split('*');
    if (parts.length === 1 || want[0].includes('xn--')) return host[0] === want[0];
    if (!allowWildcard || parts.length > 2 || want.length <= 2) return false;
    return host[0].length >= parts[0].length + parts[1].length &&
      host[0].startsWith(parts[0]) && host[0].endsWith(parts[1]);
  }

  /**
   * Check if name matches certificate. Returns an Error if it does not.
   */
  function checkServerIdentity(hostname, cert) {
    hostname = String(hostname);
    const subject = cert.subject;
    const altNames = cert.subjectaltname;
    const names = altNames ? altNames.split(', ') : [];
    const dnsNames = names.filter((n) => n.startsWith('DNS:')).map((n) => n.slice(4));
    const ips = names.filter((n) => n.startsWith('IP Address:')).map((n) => n.slice(11));

    let valid = false;
    let reason = 'Cert does not contain a DNS name';
    if (net.isIP(hostname)) {
      valid = ips.includes(hostname);
      reason = `IP: ${hostname} is not in the cert's list: ${ips.join(', ')}`;
    } else if (dnsNames.length > 0) {
      valid = dnsNames.some((name) => hostMatches(hostname, name, true));
      reason = `Host: ${hostname}. is not in the cert's altnames: ${altNames}`;
    } else if (subject) {
      const cn = subject.CN;
      valid = Array.isArray(cn)
        ? cn.some((name) => hostMatches(hostname, name, false))
        : !!cn && hostMatches(hostname, cn, false);
      reason = `Host: ${hostname}. is not cert's CN: ${cn}`;
    }
    if (valid) return undefined;

    const err = new Error(`Hostname/IP does not match certificate's altnames: ${reason}`);
    err.code = 'ERR_TLS_CERT_ALTNAME_INVALID';
    err.reason = reason;
    err.host = hostname;
    err.cert = cert;
    return err;
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
