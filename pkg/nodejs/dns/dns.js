// dns - DNS resolution module
(function(global) {
  'use strict';

  const internal = global.__dns_internal;

  const codes = {
    NODATA: 'ENODATA',
    FORMERR: 'EFORMERR',
    SERVFAIL: 'ESERVFAIL',
    NOTFOUND: 'ENOTFOUND',
    NOTIMP: 'ENOTIMP',
    REFUSED: 'EREFUSED',
    BADQUERY: 'EBADQUERY',
    BADNAME: 'EBADNAME',
    BADFAMILY: 'EBADFAMILY',
    BADRESP: 'EBADRESP',
    CONNREFUSED: 'ECONNREFUSED',
    TIMEOUT: 'ETIMEOUT',
    EOF: 'EOF',
    FILE: 'EFILE',
    NOMEM: 'ENOMEM',
    DESTRUCTION: 'EDESTRUCTION',
    BADSTR: 'EBADSTR',
    BADFLAGS: 'EBADFLAGS',
    NONAME: 'ENONAME',
    BADHINTS: 'EBADHINTS',
    NOTINITIALIZED: 'ENOTINITIALIZED',
    LOADIPHLPAPI: 'ELOADIPHLPAPI',
    ADDRGETNETWORKPARAMS: 'EADDRGETNETWORKPARAMS',
    CANCELLED: 'ECANCELLED'
  };
  const kErrnoGetaddrinfo = { ENOTFOUND: -3008, EAI_AGAIN: -3001 };

  let defaultResultOrder = 'verbatim';

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  function requireCallback(callback) {
    if (typeof callback !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "callback" argument must be of type function.');
    }
  }

  function requireString(value, name) {
    if (typeof value !== 'string') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', `The "${name}" argument must be of type string. Received ${String(value)}`);
    }
  }

  function dnsError(code, syscall, hostname) {
    const err = new Error(`${syscall} ${code}${hostname ? ` ${hostname}` : ''}`);
    if (syscall === 'getaddrinfo' && kErrnoGetaddrinfo[code]) err.errno = kErrnoGetaddrinfo[code];
    err.code = code;
    err.syscall = syscall;
    if (hostname) err.hostname = hostname;
    return err;
  }

  function isIP(host) {
    return global.__net_module.isIP(host);
  }

  function normalizeLookupOptions(options) {
    let family = 0;
    let all = false;
    let order = defaultResultOrder;
    if (typeof options === 'number') {
      family = options;
    } else if (options !== undefined && options !== null) {
      if (typeof options !== 'object') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "options" argument must be of type object.');
      }
      if (options.family !== undefined && options.family !== null) {
        if (options.family === 'IPv4') family = 4;
        else if (options.family === 'IPv6') family = 6;
        else family = options.family;
      }
      all = options.all === true;
      if (options.verbatim === true) order = 'verbatim';
      else if (options.verbatim === false) order = 'ipv4first';
      if (options.order !== undefined) order = options.order;
    }
    if (family !== 0 && family !== 4 && family !== 6) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The property 'options.family' must be one of: 0, 4, 6. Received ${String(family)}`);
    }
    if (order !== 'verbatim' && order !== 'ipv4first' && order !== 'ipv6first') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The property 'options.order' must be one of: 'verbatim', 'ipv4first', 'ipv6first'.`);
    }
    return { family, all, order };
  }

  function orderAddresses(addresses, order) {
    if (order === 'verbatim') return addresses;
    const first = order === 'ipv4first' ? 4 : 6;
    return [...addresses.filter((a) => a.family === first), ...addresses.filter((a) => a.family !== first)];
  }

  function lookupInternal(hostname, opts, done) {
    if (!hostname) {
      process.nextTick(done, null, []);
      return;
    }
    const literal = isIP(hostname);
    if (literal) {
      process.nextTick(done, null, [{ address: hostname, family: literal }]);
      return;
    }
    internal.lookup(hostname, opts.family, (code, encoded) => {
      if (code) {
        done(dnsError(code, 'getaddrinfo', hostname));
        return;
      }
      done(null, orderAddresses(JSON.parse(encoded), opts.order));
    });
  }

  function lookup(hostname, options, callback) {
    if (hostname !== undefined && hostname !== null && hostname !== '' && typeof hostname !== 'string') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "hostname" argument must be of type string.');
    }
    if (typeof options === 'function') {
      callback = options;
      options = undefined;
    }
    requireCallback(callback);
    const opts = normalizeLookupOptions(options);
    lookupInternal(hostname, opts, (err, addresses) => {
      if (err) {
        callback(err);
      } else if (opts.all) {
        callback(null, addresses);
      } else if (addresses.length === 0) {
        callback(null, null, opts.family === 6 ? 6 : 4);
      } else {
        callback(null, addresses[0].address, addresses[0].family);
      }
    });
    return {};
  }

  function lookupPromise(hostname, options) {
    return new Promise((resolve, reject) => {
      const opts = normalizeLookupOptions(options);
      lookupInternal(hostname, opts, (err, addresses) => {
        if (err) reject(err);
        else if (opts.all) resolve(addresses);
        else if (addresses.length === 0) resolve({ address: null, family: opts.family === 6 ? 6 : 4 });
        else resolve(addresses[0]);
      });
    });
  }

  const kSyscalls = {
    A: 'queryA', AAAA: 'queryAaaa', ANY: 'queryAny', CAA: 'queryCaa', CNAME: 'queryCname',
    MX: 'queryMx', NAPTR: 'queryNaptr', NS: 'queryNs', PTR: 'queryPtr', SOA: 'querySoa',
    SRV: 'querySrv', TXT: 'queryTxt'
  };

  // Turns native records into the shape each dns function reports.
  function shape(rrtype, records, options) {
    const strip = (record) => {
      const { type, ...rest } = record;
      return rest;
    };
    switch (rrtype) {
      case 'A':
      case 'AAAA':
        if (options && options.ttl) return records.map(({ address, ttl }) => ({ address, ttl }));
        return records.map((r) => r.address);
      case 'CNAME':
      case 'NS':
      case 'PTR':
        return records.map((r) => r.value);
      case 'TXT':
        return records.map((r) => r.entries);
      case 'SOA':
        return strip(records[0]);
      case 'ANY':
        return records.map((r) => {
          if (r.type === 'TXT') return { entries: r.entries, type: 'TXT' };
          if (r.type === 'CNAME' || r.type === 'NS' || r.type === 'PTR') return { value: r.value, type: r.type };
          return r;
        });
      default:
        return records.map(strip);
    }
  }

  function queryInternal(rrtype, name, options, servers, done) {
    internal.query(rrtype, name, JSON.stringify(servers), (code, encoded) => {
      if (code) {
        done(dnsError(code, kSyscalls[rrtype], name));
        return;
      }
      done(null, shape(rrtype, JSON.parse(encoded), options));
    });
  }

  const kRecordMethods = {
    resolve4: 'A', resolve6: 'AAAA', resolveAny: 'ANY', resolveCaa: 'CAA', resolveCname: 'CNAME',
    resolveMx: 'MX', resolveNaptr: 'NAPTR', resolveNs: 'NS', resolvePtr: 'PTR', resolveSoa: 'SOA',
    resolveSrv: 'SRV', resolveTxt: 'TXT'
  };

  function validRrtype(rrtype) {
    if (typeof rrtype !== 'string') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "rrtype" argument must be of type string.');
    }
    if (!kSyscalls[rrtype]) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The argument 'rrtype' is invalid. Received '${rrtype}'`);
    }
  }

  // Formats a server as "host", "host:port" or "[v6]:port", dropping the default port.
  function parseServer(server) {
    requireString(server, 'servers[]');
    let host = server;
    let port = 53;
    let m;
    if ((m = /^\[([^\]]+)\](?::(\d+))?$/.exec(server))) {
      host = m[1];
      port = m[2] ? Number(m[2]) : 53;
    } else if ((m = /^([^:]+):(\d+)$/.exec(server))) {
      host = m[1];
      port = Number(m[2]);
    }
    if (!isIP(host)) {
      throw codedError(TypeError, 'ERR_INVALID_IP_ADDRESS', `Invalid IP address: ${server}`);
    }
    return { host, port };
  }

  function formatServer({ host, port }) {
    if (port === 53) return host;
    return isIP(host) === 6 ? `[${host}]:${port}` : `${host}:${port}`;
  }

  function nativeServer({ host, port }) {
    return isIP(host) === 6 ? `[${host}]:${port}` : `${host}:${port}`;
  }

  class ResolverBase {
    constructor() {
      this._servers = null;
    }

    getServers() {
      if (this._servers) return this._servers.map(formatServer);
      return JSON.parse(internal.systemServers());
    }

    setServers(servers) {
      if (!Array.isArray(servers)) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "servers" argument must be an instance of Array.');
      }
      this._servers = servers.map(parseServer);
    }

    _native() {
      return this._servers ? this._servers.map(nativeServer) : [];
    }

    cancel() {}
    setLocalAddress() {}
  }

  class Resolver extends ResolverBase {
    resolve(hostname, rrtype, callback) {
      if (typeof rrtype === 'function') {
        callback = rrtype;
        rrtype = 'A';
      }
      if (rrtype === undefined) rrtype = 'A';
      requireString(hostname, 'hostname');
      validRrtype(rrtype);
      requireCallback(callback);
      queryInternal(rrtype, hostname, null, this._native(), callback);
      return {};
    }

    reverse(ip, callback) {
      requireString(ip, 'ip');
      requireCallback(callback);
      if (!isIP(ip)) throw dnsError('EINVAL', 'getHostByAddr', ip);
      internal.reverse(ip, JSON.stringify(this._native()), (code, encoded) => {
        if (code) callback(dnsError(code, 'getHostByAddr', ip));
        else callback(null, JSON.parse(encoded));
      });
      return {};
    }
  }

  class PromiseResolver extends ResolverBase {
    resolve(hostname, rrtype) {
      if (rrtype === undefined) rrtype = 'A';
      requireString(hostname, 'hostname');
      validRrtype(rrtype);
      return new Promise((resolve, reject) => {
        queryInternal(rrtype, hostname, null, this._native(), (err, result) => {
          if (err) reject(err);
          else resolve(result);
        });
      });
    }

    reverse(ip) {
      requireString(ip, 'ip');
      if (!isIP(ip)) throw dnsError('EINVAL', 'getHostByAddr', ip);
      return new Promise((resolve, reject) => {
        internal.reverse(ip, JSON.stringify(this._native()), (code, encoded) => {
          if (code) reject(dnsError(code, 'getHostByAddr', ip));
          else resolve(JSON.parse(encoded));
        });
      });
    }
  }

  for (const [method, rrtype] of Object.entries(kRecordMethods)) {
    Resolver.prototype[method] = function(hostname, options, callback) {
      if (typeof options === 'function') {
        callback = options;
        options = undefined;
      }
      requireString(hostname, 'hostname');
      requireCallback(callback);
      queryInternal(rrtype, hostname, options, this._native(), callback);
      return {};
    };
    PromiseResolver.prototype[method] = function(hostname, options) {
      requireString(hostname, 'hostname');
      return new Promise((resolve, reject) => {
        queryInternal(rrtype, hostname, options, this._native(), (err, result) => {
          if (err) reject(err);
          else resolve(result);
        });
      });
    };
  }

  function lookupService(address, port, callback) {
    if (arguments.length !== 3) {
      throw codedError(TypeError, 'ERR_MISSING_ARGS', 'The "address", "port", and "callback" arguments must be specified');
    }
    if (!isIP(address)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The argument 'address' is invalid. Received '${address}'`);
    }
    const p = Number(port);
    if (!(p >= 0 && p <= 65535) || !Number.isInteger(p)) {
      throw codedError(RangeError, 'ERR_SOCKET_BAD_PORT', `Port should be >= 0 and < 65536. Received ${String(port)}.`);
    }
    requireCallback(callback);
    internal.lookupService(address, p, (code, encoded) => {
      if (code) {
        callback(dnsError(code, 'getnameinfo', address));
        return;
      }
      const { hostname, service } = JSON.parse(encoded);
      callback(null, hostname, service);
    });
    return {};
  }

  function lookupServicePromise(address, port) {
    return new Promise((resolve, reject) => {
      lookupService(address, port, (err, hostname, service) => {
        if (err) reject(err);
        else resolve({ hostname, service });
      });
    });
  }

  function setDefaultResultOrder(order) {
    if (order !== 'ipv4first' && order !== 'ipv6first' && order !== 'verbatim') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', `The argument 'order' must be one of: 'verbatim', 'ipv4first', 'ipv6first'. Received '${String(order)}'`);
    }
    defaultResultOrder = order;
  }

  const defaultResolver = new Resolver();
  const defaultPromiseResolver = new PromiseResolver();

  const dns = {
    ...codes,
    ADDRCONFIG: 1024,
    V4MAPPED: 2048,
    ALL: 256,
    lookup,
    lookupService,
    Resolver,
    getServers: () => defaultResolver.getServers(),
    setServers: (servers) => {
      defaultResolver.setServers(servers);
      defaultPromiseResolver._servers = defaultResolver._servers;
    },
    setDefaultResultOrder,
    getDefaultResultOrder: () => defaultResultOrder,
    resolve: (...args) => defaultResolver.resolve(...args),
    reverse: (...args) => defaultResolver.reverse(...args)
  };
  for (const method of Object.keys(kRecordMethods)) {
    dns[method] = (...args) => defaultResolver[method](...args);
  }

  dns.promises = {
    ...codes,
    lookup: lookupPromise,
    lookupService: lookupServicePromise,
    Resolver: PromiseResolver,
    getServers: dns.getServers,
    setServers: dns.setServers,
    setDefaultResultOrder,
    getDefaultResultOrder: () => defaultResultOrder,
    resolve: (...args) => defaultPromiseResolver.resolve(...args),
    reverse: (...args) => defaultPromiseResolver.reverse(...args)
  };
  for (const method of Object.keys(kRecordMethods)) {
    dns.promises[method] = (...args) => defaultPromiseResolver[method](...args);
  }

  global.__dns_module = dns;
  global.__dns_promises_module = dns.promises;

})(globalThis);
