// https - HTTPS module (wrapper around http with TLS)
(function(global) {
  'use strict';

  // Get http module
  const http = global.__http_module;

  /**
   * HTTPS request function that forces HTTPS protocol.
   */
  function request(options, callback) {
    // Normalize options
    if (typeof options === 'string') {
      options = { url: options };
    } else {
      options = Object.assign({}, options);
    }

    // Force HTTPS
    if (options.url) {
      if (!options.url.startsWith('https://')) {
        if (options.url.startsWith('http://')) {
          options.url = options.url.replace('http://', 'https://');
        } else {
          options.url = 'https://' + options.url;
        }
      }
    } else {
      // Options-object form (e.g. https.get({ hostname, path })): always pin the
      // scheme to https and default the port to 443, regardless of whether a
      // protocol was supplied. Otherwise http.request would build an
      // "http://host:443" URL and the request would fail.
      options.protocol = 'https:';
      if (!options.port) {
        options.port = 443;
      }
    }

    return http.request(options, callback);
  }

  /**
   * HTTPS GET request.
   */
  function get(options, callback) {
    const req = request(options, callback);
    req.end();
    return req;
  }

  /**
   * Creates an HTTPS server (stub - would need TLS support).
   */
  function createServer(options, requestListener) {
    throw new Error('https.createServer not supported - use http.createServer instead');
  }

  /**
   * Agent class for HTTPS.
   */
  class Agent {
    constructor(options) {
      this.options = options || {};
      this.maxSockets = this.options.maxSockets || Infinity;
      this.maxFreeSockets = this.options.maxFreeSockets || 256;
      this.maxTotalSockets = this.options.maxTotalSockets || Infinity;
      this.sockets = {};
      this.freeSockets = {};
      this.requests = {};
      this.scheduling = this.options.scheduling || 'lifo';
    }

    createConnection(options, callback) {
      // Return a mock socket
      const socket = {
        connect: () => {},
        end: () => {},
        destroy: () => {},
        on: () => socket,
        once: () => socket,
        emit: () => {},
        write: () => true,
        setEncoding: () => {},
        setTimeout: () => {}
      };
      if (callback) {
        setImmediate(() => callback(null, socket));
      }
      return socket;
    }

    getName(options) {
      return (options.host || 'localhost') + ':' + (options.port || 443);
    }

    destroy() {
      // Clean up
    }
  }

  // Default global agent
  const globalAgent = new Agent({
    keepAlive: true,
    keepAliveMsecs: 1000,
    maxSockets: Infinity,
    maxFreeSockets: 256
  });

  const https = {
    request,
    get,
    createServer,
    Agent,
    globalAgent,
    
    // Constants from http
    METHODS: http.METHODS,
    STATUS_CODES: http.STATUS_CODES
  };

  // Export
  global.__https_module = https;

})(globalThis);
