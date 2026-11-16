// crypto.X509Certificate over the native _x509Parse and _x509Check
// (x509.go). Fields are printed the way Node prints them from OpenSSL.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const kInfo = Symbol('kX509Info');
  const kCache = Symbol('kX509Cache');

  // Flags for _x509Check, as x509.go numbers them.
  const ALWAYS_CHECK_SUBJECT = 1;
  const NEVER_CHECK_SUBJECT = 2;
  const NO_WILDCARDS = 4;
  const NO_PARTIAL_WILDCARDS = 8;
  const MULTI_LABEL_WILDCARDS = 16;
  const SINGLE_LABEL_SUBDOMAINS = 32;

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // Node's description of a rejected value, as in its ERR_INVALID_ARG_TYPE.
  function describeReceived(value) {
    if (value === null || value === undefined) return 'Received ' + value;
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
      if (value.constructor && value.constructor.name) {
        return 'Received an instance of ' + value.constructor.name;
      }
      return 'Received ' + String(value);
    }
    let shown;
    if (typeof value === 'string') {
      shown = "'" + (value.length > 28 ? value.slice(0, 25) + '...' : value) + "'";
    } else if (typeof value === 'bigint') {
      shown = String(value) + 'n';
    } else {
      shown = Object.is(value, -0) ? '-0' : String(value);
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
  }

  function invalidArgType(name, expected, value) {
    const kind = name.includes('.') ? 'property' : 'argument';
    return codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "' + name + '" ' + kind + ' must be ' + expected + '. ' + describeReceived(value));
  }

  function inspect(value) {
    const util = globalThis.__util_module;
    if (util && typeof util.inspect === 'function') return util.inspect(value);
    return typeof value === 'string' ? "'" + value + "'" : String(value);
  }

  function invalidArgValue(name, value) {
    const kind = name.includes('.') ? 'property' : 'argument';
    return codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
      'The ' + kind + " '" + name + "' is invalid. Received " + inspect(value));
  }

  function validateString(value, name) {
    if (typeof value !== 'string') throw invalidArgType(name, 'of type string', value);
  }

  function validateBoolean(value, name) {
    if (typeof value !== 'boolean') throw invalidArgType(name, 'of type boolean', value);
  }

  function getFlags(options = {}) {
    if (options === null || typeof options !== 'object' || Array.isArray(options)) {
      throw invalidArgType('options', 'of type object', options);
    }
    const {
      subject = 'default', wildcards = true, partialWildcards = true,
      multiLabelWildcards = false, singleLabelSubdomains = false,
    } = { ...options };
    let flags = 0;
    validateString(subject, 'options.subject');
    validateBoolean(wildcards, 'options.wildcards');
    validateBoolean(partialWildcards, 'options.partialWildcards');
    validateBoolean(multiLabelWildcards, 'options.multiLabelWildcards');
    validateBoolean(singleLabelSubdomains, 'options.singleLabelSubdomains');
    switch (subject) {
      case 'default': break;
      case 'always': flags |= ALWAYS_CHECK_SUBJECT; break;
      case 'never': flags |= NEVER_CHECK_SUBJECT; break;
      default: throw invalidArgValue('options.subject', subject);
    }
    if (!wildcards) flags |= NO_WILDCARDS;
    if (!partialWildcards) flags |= NO_PARTIAL_WILDCARDS;
    if (multiLabelWildcards) flags |= MULTI_LABEL_WILDCARDS;
    if (singleLabelSubdomains) flags |= SINGLE_LABEL_SUBDOMAINS;
    return flags;
  }

  // Names cross to Go as C strings, which end at a NUL; Node refuses them.
  function checkNoNul(name) {
    if (name.includes('\0')) throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', 'Invalid name');
  }

  function checkKeyObject(key, type) {
    if (!(key instanceof crypto.KeyObject)) throw invalidArgType('pkey', 'an instance of KeyObject', key);
    if (key.type !== type) throw invalidArgValue('pkey', key);
  }

  function nameObject(attrs) {
    const out = Object.create(null);
    for (const { k, v } of attrs) {
      if (k in out) {
        if (Array.isArray(out[k])) out[k].push(v);
        else out[k] = [out[k], v];
      } else {
        out[k] = v;
      }
    }
    return out;
  }

  // Node's translatePeerCertificate parsing of the infoAccess string.
  function infoAccessObject(text) {
    const out = Object.create(null);
    text.replace(/([^\n:]*):([^\n]*)(?:\n|$)/g, (all, key, val) => {
      if (val.charCodeAt(0) === 0x22) val = JSON.parse(val);
      if (key in out) out[key].push(val);
      else out[key] = [val];
      return all;
    });
    return out;
  }

  function cached(cert, name, make) {
    const cache = cert[kCache];
    if (!cache.has(name)) cache.set(name, make());
    return cache.get(name);
  }

  class X509Certificate {
    constructor(buffer) {
      if (typeof buffer === 'string') buffer = Buffer.from(buffer);
      if (!ArrayBuffer.isView(buffer)) {
        throw invalidArgType('buffer', 'of type string or an instance of Buffer, TypedArray, or DataView', buffer);
      }
      const bytes = Buffer.from(new Uint8Array(buffer.buffer, buffer.byteOffset, buffer.byteLength));
      const info = JSON.parse(crypto._x509Parse(bytes.toString('base64')));
      if (info.errorCode) throw codedError(Error, info.errorCode, info.errorMessage);
      this[kInfo] = info;
      this[kCache] = new Map();
    }

    get subject() { return this[kInfo].subject === '' ? undefined : this[kInfo].subject; }
    get subjectAltName() { return this[kInfo].subjectAltName === null ? undefined : this[kInfo].subjectAltName; }
    get issuer() { return this[kInfo].issuer === '' ? undefined : this[kInfo].issuer; }
    get issuerCertificate() { return undefined; }
    get infoAccess() { return this[kInfo].infoAccess === null ? undefined : this[kInfo].infoAccess; }
    get validFrom() { return this[kInfo].validFrom; }
    get validTo() { return this[kInfo].validTo; }
    get fingerprint() { return this[kInfo].fingerprint; }
    get fingerprint256() { return this[kInfo].fingerprint256; }
    get fingerprint512() { return this[kInfo].fingerprint512; }
    get keyUsage() { return cached(this, 'keyUsage', () => (this[kInfo].keyUsage === null ? undefined : this[kInfo].keyUsage.slice())); }
    get serialNumber() { return this[kInfo].serialNumber; }
    // Node computes these once per certificate and returns the same object after.
    get raw() { return cached(this, 'raw', () => Buffer.from(this[kInfo].raw, 'base64')); }
    get publicKey() { return cached(this, 'publicKey', () => crypto.createPublicKey(spkiPEM(this[kInfo].spki))); }
    get ca() { return this[kInfo].ca; }

    toString() { return this[kInfo].pem; }
    toJSON() { return this.toString(); }

    checkHost(name, options) {
      validateString(name, 'name');
      checkNoNul(name);
      return crypto._x509Check('host', this[kInfo].raw, name, getFlags(options)) || undefined;
    }

    checkEmail(email, options) {
      validateString(email, 'email');
      checkNoNul(email);
      return crypto._x509Check('email', this[kInfo].raw, email, getFlags(options)) || undefined;
    }

    checkIP(ip) {
      validateString(ip, 'ip');
      let out;
      try {
        out = crypto._x509Check('ip', this[kInfo].raw, ip, 0);
      } catch (e) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', 'Invalid IP');
      }
      return out ? ip : undefined;
    }

    checkIssued(otherCert) {
      if (!(otherCert instanceof X509Certificate)) {
        throw invalidArgType('otherCert', 'an instance of X509Certificate', otherCert);
      }
      return crypto._x509Check('issued', this[kInfo].raw, otherCert[kInfo].raw, 0) === 'true';
    }

    checkPrivateKey(pkey) {
      checkKeyObject(pkey, 'private');
      // Sent as PKCS#8 and compared in Go, so no public KeyObject is made.
      const pkcs8 = pkey.export({ format: 'der', type: 'pkcs8' }).toString('base64');
      return crypto._x509Check('privateKey', this[kInfo].raw, pkcs8, 0) === 'true';
    }

    verify(pkey) {
      checkKeyObject(pkey, 'public');
      const spki = pkey.export({ format: 'der', type: 'spki' }).toString('base64');
      return crypto._x509Check('verify', this[kInfo].raw, spki, 0) === 'true';
    }

    toLegacyObject() {
      const info = this[kInfo];
      const out = {};
      out.subject = nameObject(info.subjectAttrs);
      out.issuer = nameObject(info.issuerAttrs);
      if (info.subjectAltName !== null) out.subjectaltname = info.subjectAltName;
      if (info.infoAccess !== null) out.infoAccess = infoAccessObject(info.infoAccess);
      out.ca = info.ca;
      if (info.modulus) {
        out.modulus = info.modulus;
        out.bits = info.bits;
        out.exponent = info.exponent;
        out.pubkey = Buffer.from(info.pubkey, 'base64');
      } else if (info.asn1Curve) {
        out.bits = info.bits;
        out.pubkey = Buffer.from(info.pubkey, 'base64');
        out.asn1Curve = info.asn1Curve;
        out.nistCurve = info.nistCurve;
      }
      out.valid_from = info.validFrom;
      out.valid_to = info.validTo;
      out.fingerprint = info.fingerprint;
      out.fingerprint256 = info.fingerprint256;
      out.fingerprint512 = info.fingerprint512;
      if (info.keyUsage !== null) out.ext_key_usage = info.keyUsage.slice();
      out.serialNumber = info.serialNumber;
      out.raw = this.raw;
      return out;
    }
  }

  function spkiPEM(b64) {
    const lines = b64.match(/.{1,64}/g) || [];
    return '-----BEGIN PUBLIC KEY-----\n' + lines.join('\n') + '\n-----END PUBLIC KEY-----\n';
  }

  crypto.X509Certificate = X509Certificate;
})();
