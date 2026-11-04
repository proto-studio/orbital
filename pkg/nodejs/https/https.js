// https - HTTP over TLS
(function(global) {
  'use strict';

  const http = global.__http_module;
  const tls = global.__tls_module;

  const setupServer = http._setupServer;
  const serverMethods = http._serverMethods;
  const connectionListener = http._connectionListener;
  delete http._setupServer;
  delete http._serverMethods;
  delete http._connectionListener;

  /**
   * Server class - an HTTP server over TLS.
   */
  class Server extends tls.Server {
    constructor(options, requestListener) {
      if (typeof options === 'function') {
        requestListener = options;
        options = {};
      }
      options = { ...options };
      if (!options.ALPNProtocols) options.ALPNProtocols = ['http/1.1'];
      super({
        ...options,
        allowHalfOpen: true,
        noDelay: options.noDelay === undefined ? true : options.noDelay
      });
      setupServer(this, options);
      this.httpAllowHalfOpen = false;
      if (requestListener) this.on('request', requestListener);
      this.on('secureConnection', (socket) => connectionListener(this, socket));
      this.on('tlsClientError', (err, conn) => {
        if (!this.emit('clientError', err, conn)) conn.destroy(err);
      });
    }
  }
  Object.assign(Server.prototype, serverMethods);

  function createServer(options, requestListener) {
    return new Server(options, requestListener);
  }

  function createConnection(port, host, options) {
    if (port !== null && typeof port === 'object') {
      options = port;
    } else if (host !== null && typeof host === 'object') {
      options = { ...host };
    } else if (options === null || typeof options !== 'object') {
      options = {};
    } else {
      options = { ...options };
    }
    if (typeof port === 'number') options.port = port;
    if (typeof host === 'string') options.host = host;
    if (options.path) {
      // A request path must not be read as a socket path.
      options = { ...options, path: options.socketPath };
    }
    return tls.connect(options);
  }

  // Everything that decides whether a connection may serve a request. Requests with
  // different trust settings must never share a kept-alive connection.
  const kConnectionKeys = [
    'ca', 'cert', 'clientCertEngine', 'ciphers', 'key', 'pfx', 'rejectUnauthorized',
    'servername', 'minVersion', 'maxVersion', 'secureProtocol', 'crl', 'honorCipherOrder',
    'ecdhCurve', 'dhparam', 'secureOptions', 'sessionIdContext', 'sigalgs', 'privateKeyIdentifier',
    'privateKeyEngine'
  ];

  /**
   * Agent class for HTTPS.
   */
  class Agent extends http.Agent {
    constructor(options) {
      super(options);
      this.defaultPort = 443;
      this.protocol = 'https:';
      this.maxCachedSessions = this.options.maxCachedSessions === undefined ? 100 : this.options.maxCachedSessions;
    }

    createConnection(...args) {
      return createConnection(...args);
    }

    getName(options) {
      let name = http.Agent.prototype.getName.call(this, options);
      for (const key of kConnectionKeys) {
        name += ':';
        if (options[key] !== undefined && options[key] !== null) {
          name += Buffer.isBuffer(options[key]) ? options[key].toString('latin1') : String(options[key]);
        }
      }
      return name;
    }
  }

  const globalAgent = new Agent({ keepAlive: true, scheduling: 'lifo', timeout: 5000 });

  function request(...args) {
    let options = {};
    if (typeof args[0] === 'string') {
      const parsed = new URL(args.shift());
      options = urlToOptions(parsed);
    } else if (args[0] instanceof URL) {
      options = urlToOptions(args.shift());
    }
    if (args[0] && typeof args[0] !== 'function') {
      Object.assign(options, args.shift());
    }
    options._defaultAgent = module.globalAgent;
    args.unshift(options);
    return new http.ClientRequest(...args);
  }

  function urlToOptions(url) {
    const options = {
      protocol: url.protocol,
      hostname: typeof url.hostname === 'string' && url.hostname.startsWith('[') ? url.hostname.slice(1, -1) : url.hostname,
      path: `${url.pathname || ''}${url.search || ''}`,
      href: url.href
    };
    if (url.port !== '') options.port = Number(url.port);
    if (url.username || url.password) {
      options.auth = `${decodeURIComponent(url.username)}:${decodeURIComponent(url.password)}`;
    }
    return options;
  }

  function get(...args) {
    const req = request(...args);
    req.end();
    return req;
  }

  const module = {
    Agent,
    Server,
    createServer,
    get,
    globalAgent,
    request,
    METHODS: http.METHODS,
    STATUS_CODES: http.STATUS_CODES
  };

  global.__https_module = module;

})(globalThis);
