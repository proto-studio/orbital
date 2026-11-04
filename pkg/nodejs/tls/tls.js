// tls - TLS/SSL module. A TLSSocket is a net.Socket whose native transport has been
// upgraded in place.
(function(global) {
  'use strict';

  const net = global.__net_module;
  const internal = global.__tls_internal;

  const DEFAULT_MIN_VERSION = 'TLSv1.2';
  const DEFAULT_MAX_VERSION = 'TLSv1.3';
  const DEFAULT_ECDH_CURVE = 'auto';
  const DEFAULT_CIPHERS = 'TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:' +
    'ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384';
  const kDefaultHandshakeTimeout = 120000;

  function hasOwn(object, key) {
    return Object.prototype.hasOwnProperty.call(object, key);
  }

  function makeError(message, code) {
    const err = new Error(message);
    if (code) err.code = code;
    return err;
  }

  function pemList(value) {
    if (value === undefined || value === null) return [];
    return (Array.isArray(value) ? value : [value]).map((item) =>
      typeof item === 'string' ? item : Buffer.from(item).toString('utf8'));
  }

  // Node's ALPNProtocols takes an array of strings or buffers, or one length-prefixed buffer.
  function alpnList(value) {
    if (value === undefined || value === null) return [];
    if (Array.isArray(value)) {
      return value.map((item) => (typeof item === 'string' ? item : Buffer.from(item).toString('latin1')));
    }
    const buf = Buffer.from(value);
    const out = [];
    for (let i = 0; i < buf.length;) {
      const len = buf[i];
      out.push(buf.slice(i + 1, i + 1 + len).toString('latin1'));
      i += 1 + len;
    }
    return out;
  }

  function linkCertificates(chain, detailed) {
    if (!chain || chain.length === 0) return {};
    for (const cert of chain) cert.raw = Buffer.from(cert.raw, 'base64');
    if (detailed) {
      for (let i = 0; i < chain.length - 1; i++) chain[i].issuerCertificate = chain[i + 1];
      const last = chain[chain.length - 1];
      last.issuerCertificate = last;
    }
    return chain[0];
  }

  /**
   * TLSSocket class - a net.Socket carrying TLS.
   */
  class TLSSocket extends net.Socket {
    constructor(socket, options) {
      options = { ...options };
      if (socket && !(socket instanceof net.Socket)) {
        throw makeError('tls: only a net.Socket can be wrapped', 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM');
      }
      super({ allowHalfOpen: socket ? socket.allowHalfOpen : options.allowHalfOpen, signal: options.signal });

      this.encrypted = true;
      this.authorized = false;
      this.authorizationError = null;
      this.alpnProtocol = null;
      this.servername = null;
      this.secureConnecting = true;
      this._tlsOptions = options;
      this._isServer = !!options.isServer;
      this._wrapped = null;
      // Nothing moves on this socket until the handshake finishes.
      this._ioBlocked = true;

      if (socket) this._adopt(socket);

      // A server-side socket built directly, as for STARTTLS, starts straight away.
      if (this._isServer && !options._managedByServer && this._id !== null) {
        const server = options.server;
        const contextId = options.secureContext ? contextIdFrom({ secureContext: options.secureContext })
          : server ? server._contextId : contextIdFrom({});
        this._startServerHandshake({
          contextId,
          ALPNProtocols: options.ALPNProtocols,
          requestCert: options.requestCert,
          rejectUnauthorized: options.rejectUnauthorized,
          SNICallback: options.SNICallback,
          contexts: server ? server._contexts : []
        }, (err) => {
          if (err) {
            this.destroy(err);
            return;
          }
          this.emit('secure');
          this._flushIO();
          this.read(0);
        });
      }
    }

    // Starts a server handshake. done(err) runs when it finishes.
    _startServerHandshake(options, done) {
      const requestCert = !!options.requestCert;
      const rejectUnauthorized = requestCert && options.rejectUnauthorized !== false;
      const sniCallback = typeof options.SNICallback === 'function' ? options.SNICallback : null;
      const contexts = options.contexts || [];

      const chooseContext = (servername, reply) => {
        if (!sniCallback) {
          for (let i = contexts.length - 1; i >= 0; i--) {
            if (contexts[i].pattern.test(servername)) {
              reply(contexts[i].id);
              return;
            }
          }
          reply(0);
          return;
        }
        sniCallback(servername, (err, context) => {
          if (err || !context) {
            reply(0);
            return;
          }
          reply(contextIdFrom({ secureContext: context }));
        });
      };

      internal.upgradeServer(this._id, JSON.stringify({
        context: options.contextId,
        ALPNProtocols: alpnList(options.ALPNProtocols),
        requestCert,
        rejectUnauthorized,
        hasSNI: sniCallback !== null || contexts.length > 0
      }), chooseContext, (encoded) => {
        if (this.destroyed) return;
        const result = JSON.parse(encoded);
        if (result.message) {
          this.authorizationError = result.authorizationError || null;
          done(makeError(result.message, result.code));
          return;
        }
        this.authorized = !!result.authorized;
        this.authorizationError = result.authorizationError ||
          (requestCert && !result.authorized ? 'UNABLE_TO_GET_ISSUER_CERT' : null);
        this.alpnProtocol = result.alpn || false;
        this.servername = result.servername || false;
        this._finishHandshake();
        done(null);
      });
    }

    // Takes over the native socket of an existing net.Socket.
    _adopt(socket) {
      this._wrapped = socket;
      const takeOver = () => {
        if (socket.destroyed) return;
        this._id = socket._id;
        socket._id = null;
        socket.connecting = false;
        this._server = socket._server;
        socket._server = null;
        this._loadAddresses();
        if (!socket._refed) this.unref();
        this._onAdopted();
      };
      socket.on('error', (err) => {
        if (!this.destroyed) this.destroy(err);
      });
      socket.on('close', () => {
        if (!this.destroyed) this.destroy();
      });
      if (socket.connecting) {
        this.connecting = true;
        socket.once('connect', () => {
          this.connecting = false;
          takeOver();
        });
      } else {
        takeOver();
      }
    }

    _onAdopted() {
      if (this._isServer) return;
      if (this._connectOptionsReady) this._startClientHandshake();
      else this._adoptedReady = true;
    }

    _onTransportConnected() {
      this.emit('connect');
      this.emit('ready');
      this._startClientHandshake();
    }

    _closeTransport() {
      super._closeTransport();
      if (this._wrapped && !this._wrapped.destroyed) this._wrapped.destroy();
    }

    _startClientHandshake() {
      const options = this._tlsOptions;
      const host = options.host || (this._wrapped && this._wrapped._host) || 'localhost';
      const servername = options.servername || host;
      const checkIdentity = typeof options.checkServerIdentity === 'function' ? options.checkServerIdentity : null;

      internal.upgradeClient(this._id, host, JSON.stringify(clientNativeOptions(options, checkIdentity !== null)), (encoded) => {
        if (this.destroyed) return;
        const result = JSON.parse(encoded);
        this.authorized = !!result.authorized;
        this.authorizationError = result.authorizationError || null;

        if (result.message) {
          this.destroy(makeError(result.message, result.code));
          return;
        }

        this.alpnProtocol = result.alpn || false;
        this.servername = options.servername || false;

        if (result.scriptChecksIdentity) {
          const err = checkIdentity(servername, this.getPeerCertificate(true));
          if (err) {
            this.authorized = false;
            this.authorizationError = err.code || err.message;
            if (options.rejectUnauthorized) {
              this.destroy(err);
              return;
            }
          }
        }

        this._finishHandshake();
        this.emit('secureConnect');
        this._flushIO();
        if (!this.isPaused() || this.readableFlowing === null) this.read(0);
      });
    }

    _finishHandshake() {
      this.secureConnecting = false;
      this._ioBlocked = false;
    }

    getPeerCertificate(detailed) {
      if (this._id === null) return {};
      const encoded = internal.getPeerCertificate(this._id);
      return encoded ? linkCertificates(JSON.parse(encoded), detailed) : {};
    }

    getCertificate() {
      if (this._id === null) return null;
      const encoded = internal.getCertificate(this._id);
      return encoded ? linkCertificates(JSON.parse(encoded), false) : {};
    }

    getCipher() {
      if (this._id === null) return undefined;
      const encoded = internal.getCipher(this._id);
      return encoded ? JSON.parse(encoded) : undefined;
    }

    getProtocol() {
      if (this._id === null) return null;
      return internal.getProtocol(this._id);
    }

    isSessionReused() {
      return this._id !== null && internal.isSessionReused(this._id);
    }

    exportKeyingMaterial(length, label, context) {
      if (this._id === null) {
        throw makeError('TLS socket connection must be securely established', 'ERR_TLS_INVALID_STATE');
      }
      const encodedContext = context === undefined ? null : Buffer.from(context).toString('base64');
      return Buffer.from(internal.exportKeyingMaterial(this._id, length, String(label), encodedContext), 'base64');
    }

    // Session resumption tickets are not exposed.
    getSession() { return undefined; }
    getTLSTicket() { return undefined; }
    setSession() {}
    getFinished() { return undefined; }
    getPeerFinished() { return undefined; }
    getEphemeralKeyInfo() { return null; }
    getSharedSigalgs() { return []; }
    setMaxSendFragment() { return true; }
    enableTrace() {}
    disableRenegotiation() {}

    renegotiate(options, callback) {
      if (callback) {
        process.nextTick(callback, makeError('Renegotiation is not supported', 'ERR_TLS_RENEGOTIATION_DISABLED'));
      }
      return false;
    }
  }

  // As in Node, the environment only supplies a default for an absent option.
  function resolveRejectUnauthorized(options) {
    return hasOwn(options, 'rejectUnauthorized')
      ? options.rejectUnauthorized !== false
      : process.env.NODE_TLS_REJECT_UNAUTHORIZED !== '0';
  }

  // A secure context supplies the credentials in place of the connect options.
  function clientNativeOptions(options, scriptChecksIdentity) {
    const credentials = options.secureContext ? (options.secureContext.context || {}) : options;
    return {
      rejectUnauthorized: options.rejectUnauthorized !== false,
      ca: credentials.ca ? pemList(credentials.ca) : null,
      cert: pemList(credentials.cert).join('\n'),
      key: pemList(credentials.key).map(keyText).join('\n'),
      servername: options.servername || '',
      scriptChecksIdentity,
      ALPNProtocols: alpnList(options.ALPNProtocols),
      minVersion: credentials.minVersion || options.minVersion || '',
      maxVersion: credentials.maxVersion || options.maxVersion || ''
    };
  }

  function keyText(key) {
    return key && typeof key === 'object' && key.pem ? pemList(key.pem)[0] : key;
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

    options = { ...options, rejectUnauthorized: resolveRejectUnauthorized(options) };
    if (options.pfx) {
      throw makeError('PFX credentials are not supported', 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM');
    }

    // Bad credentials throw here, before connecting, as they do in Node.
    const invalid = internal.validateClient(JSON.stringify(clientNativeOptions(options, false)));
    if (invalid) {
      const { message, code } = JSON.parse(invalid);
      throw makeError(message, code);
    }

    const socket = new TLSSocket(options.socket || null, options);
    if (callback) socket.once('secureConnect', callback);

    if (options.socket) {
      if (socket._adoptedReady) socket._startClientHandshake();
      else socket._connectOptionsReady = true;
    } else if (options.path) {
      net.Socket.prototype.connect.call(socket, { path: options.path });
    } else {
      net.Socket.prototype.connect.call(socket, {
        port: options.port,
        host: options.host,
        family: options.family,
        lookup: options.lookup,
        localAddress: options.localAddress,
        localPort: options.localPort,
        timeout: options.timeout,
        noDelay: options.noDelay,
        keepAlive: options.keepAlive
      });
    }
    return socket;
  }

  function contextIdFrom(options) {
    const memo = options.secureContext;
    if (memo && memo._id) return memo._id;
    const credentials = memo ? (memo.context || {}) : options;
    if (credentials.pfx) {
      throw makeError('PFX credentials are not supported', 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM');
    }
    const encoded = {
      cert: pemList(credentials.cert),
      key: pemList(credentials.key).map(keyText),
      ca: credentials.ca ? pemList(credentials.ca) : null,
      minVersion: credentials.minVersion || '',
      maxVersion: credentials.maxVersion || ''
    };
    const id = internal.createServerContext(JSON.stringify(encoded));
    if (typeof id === 'string') {
      const { message, code } = JSON.parse(id);
      throw makeError(message, code);
    }
    if (memo) Object.defineProperty(memo, '_id', { value: id, enumerable: false });
    return id;
  }

  function wildcardToRegExp(hostname) {
    const text = String(hostname).replace(/([.^$+?\-\\[\]{}])/g, '\\$1').replace(/\*/g, '[^.]*');
    return new RegExp(`^${text}$`, 'i');
  }

  /**
   * TLS Server class.
   */
  class Server extends net.Server {
    constructor(options, secureConnectionListener) {
      if (typeof options === 'function') {
        secureConnectionListener = options;
        options = {};
      }
      options = { ...options };
      super({
        allowHalfOpen: options.allowHalfOpen,
        pauseOnConnect: options.pauseOnConnect,
        noDelay: options.noDelay,
        keepAlive: options.keepAlive,
        keepAliveInitialDelay: options.keepAliveInitialDelay,
        highWaterMark: options.highWaterMark
      });
      this._tlsOptions = options;
      this._contexts = [];
      this._ticketKeys = Buffer.alloc(48);
      this.handshakeTimeout = options.handshakeTimeout === undefined ? kDefaultHandshakeTimeout : options.handshakeTimeout;
      this.setSecureContext(options);
      if (secureConnectionListener) this.on('secureConnection', secureConnectionListener);
    }

    setSecureContext(options) {
      this._contextId = contextIdFrom(options);
    }

    addContext(hostname, context) {
      const id = contextIdFrom(context && context.context ? { secureContext: context } : (context || {}));
      this._contexts.push({ pattern: wildcardToRegExp(hostname), id });
    }

    getTicketKeys() {
      return this._ticketKeys;
    }

    setTicketKeys(keys) {
      this._ticketKeys = Buffer.from(keys);
    }

    _onConnection(rawSocket) {
      this.emit('connection', rawSocket);
      const options = this._tlsOptions;
      const socket = new TLSSocket(rawSocket, { isServer: true, _managedByServer: true });

      let timer = null;
      const clearTimer = () => {
        if (timer) clearTimeout(timer);
        timer = null;
      };
      if (this.handshakeTimeout > 0) {
        timer = setTimeout(() => {
          timer = null;
          this.emit('tlsClientError', makeError('TLS handshake timeout', 'ERR_TLS_HANDSHAKE_TIMEOUT'), socket);
          socket.destroy();
        }, this.handshakeTimeout);
        timer.unref();
      }
      socket.once('close', clearTimer);

      socket._startServerHandshake({
        contextId: this._contextId,
        ALPNProtocols: options.ALPNProtocols,
        requestCert: options.requestCert,
        rejectUnauthorized: options.rejectUnauthorized,
        SNICallback: options.SNICallback,
        contexts: this._contexts
      }, (err) => {
        clearTimer();
        if (err) {
          this.emit('tlsClientError', err, socket);
          socket.destroy();
          return;
        }
        this.emit('secureConnection', socket);
        socket._flushIO();
        if (!this.pauseOnConnect) socket.read(0);
      });
    }
  }

  function createServer(options, secureConnectionListener) {
    return new Server(options, secureConnectionListener);
  }

  /**
   * Create a secure context. Credentials are checked now, as in Node.
   */
  function createSecureContext(options) {
    options = options || {};
    if (options.cert || options.key || options.pfx) {
      const certs = pemList(options.cert);
      const keys = pemList(options.key);
      if (certs.length && keys.length) contextIdFrom(options);
    }
    return { context: { ...options } };
  }

  function getCiphers() {
    return [
      'tls_aes_256_gcm_sha384',
      'tls_chacha20_poly1305_sha256',
      'tls_aes_128_gcm_sha256',
      'ecdhe-rsa-aes256-gcm-sha384',
      'ecdhe-rsa-aes128-gcm-sha256',
      'ecdhe-ecdsa-aes256-gcm-sha384',
      'ecdhe-ecdsa-aes128-gcm-sha256',
      'ecdhe-rsa-chacha20-poly1305',
      'ecdhe-ecdsa-chacha20-poly1305'
    ];
  }

  // Whether a hostname matches a certificate name. A wildcard covers part or all of the
  // leftmost label only, and needs at least three labels, as in Node.
  function hostMatches(hostname, pattern, allowWildcard) {
    const labels = (name) => name.replace(/\.$/, '').toLowerCase().split('.');
    const host = labels(hostname);
    const want = labels(pattern);
    if (host.length !== want.length || want.includes('')) return false;
    if (want.some((label) => /[^!-\u007f]/.test(label))) return false;
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

  const rootCertificates = [];

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
    CLIENT_RENEG_LIMIT: 3,
    CLIENT_RENEG_WINDOW: 600,
    DEFAULT_CIPHERS,
    DEFAULT_ECDH_CURVE,
    DEFAULT_MIN_VERSION,
    DEFAULT_MAX_VERSION
  };

  global.__tls_module = tls;

})(globalThis);
