// dns - DNS resolution module
(function(global) {
  'use strict';

  const internal = global.__dns_internal;

  // Error codes
  const NODATA = 'ENODATA';
  const FORMERR = 'EFORMERR';
  const SERVFAIL = 'ESERVFAIL';
  const NOTFOUND = 'ENOTFOUND';
  const NOTIMP = 'ENOTIMP';
  const REFUSED = 'EREFUSED';
  const BADQUERY = 'EBADQUERY';
  const BADNAME = 'EBADNAME';
  const BADFAMILY = 'EBADFAMILY';
  const BADRESP = 'EBADRESP';
  const CONNREFUSED = 'ECONNREFUSED';
  const TIMEOUT = 'ETIMEOUT';
  const EOF = 'EOF';
  const FILE = 'EFILE';
  const NOMEM = 'ENOMEM';
  const DESTRUCTION = 'EDESTRUCTION';
  const BADSTR = 'EBADSTR';
  const BADFLAGS = 'EBADFLAGS';
  const NONAME = 'ENONAME';
  const BADHINTS = 'EBADHINTS';
  const NOTINITIALIZED = 'ENOTINITIALIZED';
  const LOADIPHLPAPI = 'ELOADIPHLPAPI';
  const ADDRGETNETWORKPARAMS = 'EADDRGETNETWORKPARAMS';
  const CANCELLED = 'ECANCELLED';

  // Helper to create DNS error
  function createDnsError(syscall, hostname, code) {
    const err = new Error(`${syscall} ${code || NOTFOUND} ${hostname}`);
    err.code = code || NOTFOUND;
    err.syscall = syscall;
    err.hostname = hostname;
    return err;
  }

  // Helper to promisify sync function
  function promisify(syncFn, syscall) {
    return function(hostname, ...args) {
      return new Promise((resolve, reject) => {
        setImmediate(() => {
          try {
            const result = syncFn(hostname, ...args);
            if (result === null || result === undefined) {
              reject(createDnsError(syscall, hostname));
            } else {
              resolve(result);
            }
          } catch (e) {
            reject(createDnsError(syscall, hostname, e.code || NOTFOUND));
          }
        });
      });
    };
  }

  // Callback-style wrapper
  function callbackify(syncFn, syscall) {
    return function(hostname, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }
      
      if (typeof callback !== 'function') {
        // Return promise if no callback
        return promisify(syncFn, syscall)(hostname, options);
      }
      
      setImmediate(() => {
        try {
          const result = syncFn(hostname, options);
          if (result === null || result === undefined) {
            callback(createDnsError(syscall, hostname));
          } else {
            callback(null, result);
          }
        } catch (e) {
          callback(createDnsError(syscall, hostname, e.code || NOTFOUND));
        }
      });
    };
  }

  const dns = {
    // Error codes
    NODATA,
    FORMERR,
    SERVFAIL,
    NOTFOUND,
    NOTIMP,
    REFUSED,
    BADQUERY,
    BADNAME,
    BADFAMILY,
    BADRESP,
    CONNREFUSED,
    TIMEOUT,
    EOF,
    FILE,
    NOMEM,
    DESTRUCTION,
    BADSTR,
    BADFLAGS,
    NONAME,
    BADHINTS,
    NOTINITIALIZED,
    LOADIPHLPAPI,
    ADDRGETNETWORKPARAMS,
    CANCELLED,

    /**
     * Lookup a hostname and return the first found A (IPv4) or AAAA (IPv6) record.
     * @param {string} hostname - The hostname to look up
     * @param {object|function} options - Options or callback
     * @param {function} callback - Callback function
     */
    lookup: function(hostname, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = {};
      }

      if (typeof callback !== 'function') {
        return new Promise((resolve, reject) => {
          setImmediate(() => {
            const result = internal.lookup(hostname);
            if (result === null || result === undefined) {
              reject(createDnsError('getaddrinfo', hostname));
            } else {
              const family = result.includes(':') ? 6 : 4;
              resolve({ address: result, family });
            }
          });
        });
      }

      setImmediate(() => {
        const result = internal.lookup(hostname);
        if (result === null || result === undefined) {
          callback(createDnsError('getaddrinfo', hostname));
        } else {
          const family = result.includes(':') ? 6 : 4;
          callback(null, result, family);
        }
      });
    },

    /**
     * Resolves a hostname into an array of resource records.
     * @param {string} hostname - The hostname to resolve
     * @param {string|object|function} rrtype - Record type or options or callback
     * @param {function} callback - Callback function
     */
    resolve: function(hostname, rrtype, callback) {
      if (typeof rrtype === 'function') {
        callback = rrtype;
        rrtype = 'A';
      } else if (typeof rrtype === 'object') {
        callback = callback || rrtype.callback;
        rrtype = rrtype.rrtype || 'A';
      }

      const syscall = 'query' + (rrtype || 'A').toUpperCase();

      if (typeof callback !== 'function') {
        return new Promise((resolve, reject) => {
          setImmediate(() => {
            const result = internal.resolve(hostname, rrtype || 'A');
            if (result === null || result === undefined) {
              reject(createDnsError(syscall, hostname));
            } else {
              resolve(result);
            }
          });
        });
      }

      setImmediate(() => {
        const result = internal.resolve(hostname, rrtype || 'A');
        if (result === null || result === undefined) {
          callback(createDnsError(syscall, hostname));
        } else {
          callback(null, result);
        }
      });
    },

    /**
     * Resolve IPv4 addresses.
     */
    resolve4: callbackify((hostname) => internal.resolve4(hostname), 'queryA'),

    /**
     * Resolve IPv6 addresses.
     */
    resolve6: callbackify((hostname) => internal.resolve6(hostname), 'queryAAAA'),

    /**
     * Resolve MX records.
     */
    resolveMx: callbackify((hostname) => internal.resolveMx(hostname), 'queryMX'),

    /**
     * Resolve TXT records.
     */
    resolveTxt: callbackify((hostname) => internal.resolveTxt(hostname), 'queryTXT'),

    /**
     * Resolve NS records.
     */
    resolveNs: callbackify((hostname) => internal.resolveNs(hostname), 'queryNS'),

    /**
     * Resolve CNAME records.
     */
    resolveCname: callbackify((hostname) => internal.resolveCname(hostname), 'queryCNAME'),

    /**
     * Perform reverse DNS lookup.
     */
    reverse: callbackify((ip) => internal.reverse(ip), 'getHostByAddr'),

    /**
     * Returns an array of IP addresses used for resolution.
     * (In sandbox mode, this returns empty or fake servers)
     */
    getServers: function() {
      return ['8.8.8.8', '8.8.4.4'];
    },

    /**
     * Sets the IP addresses of DNS servers.
     * (No-op in sandbox mode)
     */
    setServers: function(servers) {
      // No-op - servers are handled by the resolver interface
    },

    /**
     * Set default result order for dns.lookup().
     */
    setDefaultResultOrder: function(order) {
      // No-op
    },

    /**
     * Get default result order.
     */
    getDefaultResultOrder: function() {
      return 'verbatim';
    }
  };

  // Promises API
  dns.promises = {
    lookup: promisify((hostname) => internal.lookup(hostname), 'getaddrinfo'),
    resolve: function(hostname, rrtype) {
      return promisify((h) => internal.resolve(h, rrtype || 'A'), 'query' + (rrtype || 'A').toUpperCase())(hostname);
    },
    resolve4: promisify((hostname) => internal.resolve4(hostname), 'queryA'),
    resolve6: promisify((hostname) => internal.resolve6(hostname), 'queryAAAA'),
    resolveMx: promisify((hostname) => internal.resolveMx(hostname), 'queryMX'),
    resolveTxt: promisify((hostname) => internal.resolveTxt(hostname), 'queryTXT'),
    resolveNs: promisify((hostname) => internal.resolveNs(hostname), 'queryNS'),
    resolveCname: promisify((hostname) => internal.resolveCname(hostname), 'queryCNAME'),
    reverse: promisify((ip) => internal.reverse(ip), 'getHostByAddr'),
    getServers: dns.getServers,
    setServers: dns.setServers,
    setDefaultResultOrder: dns.setDefaultResultOrder,
    getDefaultResultOrder: dns.getDefaultResultOrder,

    // Resolver class
    Resolver: class Resolver {
      constructor() {
        this._servers = ['8.8.8.8', '8.8.4.4'];
      }
      getServers() { return this._servers; }
      setServers(servers) { this._servers = servers; }
      resolve(hostname, rrtype) { return dns.promises.resolve(hostname, rrtype); }
      resolve4(hostname) { return dns.promises.resolve4(hostname); }
      resolve6(hostname) { return dns.promises.resolve6(hostname); }
      resolveMx(hostname) { return dns.promises.resolveMx(hostname); }
      resolveTxt(hostname) { return dns.promises.resolveTxt(hostname); }
      resolveNs(hostname) { return dns.promises.resolveNs(hostname); }
      resolveCname(hostname) { return dns.promises.resolveCname(hostname); }
      reverse(ip) { return dns.promises.reverse(ip); }
      cancel() {}
    }
  };

  // Resolver class
  dns.Resolver = class Resolver {
    constructor() {
      this._servers = ['8.8.8.8', '8.8.4.4'];
    }
    getServers() { return this._servers; }
    setServers(servers) { this._servers = servers; }
    resolve(hostname, rrtype, callback) { return dns.resolve(hostname, rrtype, callback); }
    resolve4(hostname, callback) { return dns.resolve4(hostname, callback); }
    resolve6(hostname, callback) { return dns.resolve6(hostname, callback); }
    resolveMx(hostname, callback) { return dns.resolveMx(hostname, callback); }
    resolveTxt(hostname, callback) { return dns.resolveTxt(hostname, callback); }
    resolveNs(hostname, callback) { return dns.resolveNs(hostname, callback); }
    resolveCname(hostname, callback) { return dns.resolveCname(hostname, callback); }
    reverse(ip, callback) { return dns.reverse(ip, callback); }
    cancel() {}
  };

  // Export
  global.__dns_module = dns;
  global.__dns_promises_module = dns.promises;

})(globalThis);
