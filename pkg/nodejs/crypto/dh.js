// createECDH, crypto.diffieHellman and symmetric generateKey/generateKeySync,
// over the helpers in dh.go. Each object keeps its keys here; arguments,
// encodings and errors follow Node 20. Finite-field Diffie-Hellman throws.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const kState = Symbol('kDHState');
  const POINT_FORMATS = ['compressed', 'uncompressed', 'hybrid'];

  function codedError(Type, code, message) {
    const err = new Type(message);
    if (code) err.code = code;
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

  function shown(value) {
    return typeof value === 'string' ? "'" + value + "'" : String(value);
  }

  function native(fn, ...args) {
    try {
      return fn(...args);
    } catch (e) {
      const text = String(e && e.message !== undefined ? e.message : e);
      const bar = text.indexOf('|');
      if (bar < 0) throw e;
      const code = text.slice(0, bar);
      let Type = Error;
      if (code === 'ERR_CRYPTO_INVALID_KEYLEN' || code === 'ERR_CRYPTO_INVALID_KEYTYPE') Type = RangeError;
      if (code === 'ERR_CRYPTO_INVALID_CURVE') Type = TypeError;
      throw codedError(Type, code, text.slice(bar + 1));
    }
  }

  function toBuf(value, encoding) {
    if (typeof value === 'string') {
      return Buffer.from(value, encoding && encoding !== 'buffer' && Buffer.isEncoding(encoding) ? encoding : 'utf8');
    }
    if (ArrayBuffer.isView(value)) return Buffer.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
    if (value instanceof ArrayBuffer) return Buffer.from(new Uint8Array(value));
    return value;
  }

  function encode(buf, encoding) {
    return encoding && encoding !== 'buffer' ? buf.toString(encoding) : buf;
  }

  function b64(buf) {
    return buf.toString('base64');
  }

  function fromB64(s) {
    return Buffer.from(s, 'base64');
  }

  function validateInt32(value, name, min = -2147483648, max = 2147483647) {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be an integer. Received ' + value);
    }
    if (value < min || value > max) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be >= ' + min + ' && <= ' + max + '. Received ' + value);
    }
  }

  // ---- finite-field Diffie-Hellman ---------------------------------------------
  // Not offered: its private-key exponentiation would need big-integer
  // arithmetic that isn't constant time (#242). Node's code for a feature the
  // runtime doesn't have is ERR_FEATURE_UNAVAILABLE_ON_PLATFORM.
  function unavailable(name) {
    return function () {
      throw codedError(TypeError, 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM',
        'The feature ' + name + ' is unavailable on the current platform: ' +
        'finite-field Diffie-Hellman is not supported (https://github.com/proto-studio/orbital/issues/242)');
    };
  }
  crypto.DiffieHellman = unavailable('DiffieHellman');
  crypto.DiffieHellmanGroup = unavailable('DiffieHellmanGroup');
  crypto.createDiffieHellman = unavailable('createDiffieHellman');
  crypto.getDiffieHellman = unavailable('getDiffieHellman');
  crypto.createDiffieHellmanGroup = unavailable('createDiffieHellmanGroup');

  // ---- ECDH ---------------------------------------------------------------------
  function checkFormat(format) {
    if (format === undefined) return 'uncompressed';
    if (!POINT_FORMATS.includes(format)) {
      throw codedError(TypeError, 'ERR_CRYPTO_ECDH_INVALID_FORMAT', 'Invalid ECDH format: ' + format);
    }
    return format;
  }

  function ECDH(curve) {
    if (!new.target) return new ECDH(curve);
    if (typeof curve !== 'string') throw invalidArgType('curve', 'of type string', curve);
    if (!['prime256v1', 'secp384r1', 'secp521r1'].includes(curve)) {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_CURVE', 'Invalid EC curve name');
    }
    this[kState] = { curve, priv: null, pub: null, ownPub: null };
  }

  const ecdhMethods = {
    generateKeys(encoding, format) {
      const s = this[kState];
      const out = JSON.parse(native(crypto._ecdh, 'generate', s.curve, '', '', ''));
      s.priv = fromB64(out.priv);
      s.pub = s.ownPub = fromB64(out.pub);
      return this.getPublicKey(encoding, format);
    },
    computeSecret(key, inEnc, outEnc) {
      const s = this[kState];
      const other = toBuf(key, inEnc);
      // As OpenSSL's EC_KEY_check_key: a public key is needed, and with a
      // private key it must be that key's own.
      if (!s.pub || (s.priv && !s.pub.equals(s.ownPub))) {
        throw codedError(RangeError, 'ERR_CRYPTO_INVALID_KEYPAIR', 'Invalid key pair');
      }
      if (!s.priv) {
        try {
          crypto._ecdh('convert', s.curve, b64(other), '', 'uncompressed');
        } catch (e) {
          throw codedError(Error, 'ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY', 'Public key is not valid for specified curve');
        }
        throw codedError(Error, 'ERR_CRYPTO_OPERATION_FAILED', 'Failed to compute ECDH key');
      }
      return encode(fromB64(native(crypto._ecdh, 'secret', s.curve, b64(s.priv), b64(other), '')), outEnc);
    },
    setPrivateKey(key, encoding) {
      const s = this[kState];
      const out = JSON.parse(native(crypto._ecdh, 'fromPrivate', s.curve, b64(toBuf(key, encoding)), '', ''));
      s.priv = fromB64(out.priv);
      s.pub = s.ownPub = fromB64(out.pub);
      return this;
    },
    setPublicKey(key, encoding) {
      const s = this[kState];
      s.pub = fromB64(native(crypto._ecdh, 'convert', s.curve, b64(toBuf(key, encoding)), '', 'uncompressed'));
      return this;
    },
    getPrivateKey(encoding) {
      const s = this[kState];
      if (!s.priv) throw codedError(Error, 'ERR_CRYPTO_OPERATION_FAILED', 'Failed to get ECDH private key');
      return encode(Buffer.from(s.priv), encoding);
    },
    getPublicKey(encoding, format) {
      const s = this[kState];
      const f = checkFormat(format);
      if (!s.pub) throw codedError(Error, 'ERR_CRYPTO_OPERATION_FAILED', 'Failed to get ECDH public key');
      return encode(fromB64(native(crypto._ecdh, 'convert', s.curve, b64(s.pub), '', f)), encoding);
    },
  };
  for (const [name, fn] of Object.entries(ecdhMethods)) {
    Object.defineProperty(ECDH.prototype, name, { value: fn, writable: true, configurable: true, enumerable: true });
  }
  ECDH.convertKey = function convertKey(key, curve, inEnc, outEnc, format) {
    if (typeof curve !== 'string') throw invalidArgType('curve', 'of type string', curve);
    const f = checkFormat(format);
    const buf = toBuf(key, inEnc);
    return encode(fromB64(native(crypto._ecdh, 'convert', curve, b64(buf), '', f)), outEnc);
  };
  crypto.ECDH = ECDH;
  crypto.createECDH = function createECDH(curve) {
    return new ECDH(curve);
  };

  // ---- crypto.diffieHellman ------------------------------------------------------
  crypto.diffieHellman = function diffieHellman(options) {
    if (options === null || typeof options !== 'object') throw invalidArgType('options', 'of type object', options);
    const { privateKey, publicKey } = options;
    const KeyObject = crypto.KeyObject;
    if (!(privateKey instanceof KeyObject)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', "The property 'options.privateKey' is invalid. Received " + shown(privateKey));
    }
    if (!(publicKey instanceof KeyObject)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE', "The property 'options.publicKey' is invalid. Received " + shown(publicKey));
    }
    if (privateKey.type !== 'private') {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE', 'Invalid key object type ' + privateKey.type + ', expected private.');
    }
    if (publicKey.type !== 'public' && publicKey.type !== 'private') {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE', 'Invalid key object type ' + publicKey.type + ', expected private or public.');
    }
    const a = privateKey.asymmetricKeyType, b = publicKey.asymmetricKeyType;
    if (a !== b || !['ec', 'x25519', 'x448', 'dh'].includes(a)) {
      throw codedError(Error, 'ERR_CRYPTO_INCOMPATIBLE_KEY', 'Incompatible key types for Diffie-Hellman: ' + a + ' and ' + b);
    }
    if (a === 'dh') unavailable('diffieHellman with dh keys')();
    return fromB64(native(crypto._keyAgreement, crypto._keyHandle(privateKey), crypto._keyHandle(publicKey)));
  };

  // ---- generateKey / generateKeySync --------------------------------------------
  function secretKeyBytes(type, options) {
    if (typeof type !== 'string') throw invalidArgType('type', 'of type string', type);
    if (options === null || typeof options !== 'object') throw invalidArgType('options', 'of type object', options);
    const { length } = options;
    if (type === 'hmac') {
      if (typeof length !== 'number') throw invalidArgType('options.length', 'of type number', length);
      validateInt32(length, 'options.length', 8, 2147483647);
      return Math.floor(length / 8);
    }
    if (type === 'aes') {
      if (length !== 128 && length !== 192 && length !== 256) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
          "The property 'options.length' must be one of: 128, 192, 256. Received " + shown(length));
      }
      return length / 8;
    }
    throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
      "The argument 'type' must be a supported key type. Received " + shown(type));
  }

  crypto.generateKeySync = function generateKeySync(type, options) {
    return crypto.createSecretKey(crypto.randomBytes(secretKeyBytes(type, options)));
  };

  crypto.generateKey = function generateKey(type, options, callback) {
    const size = secretKeyBytes(type, options);
    if (typeof callback !== 'function') throw invalidArgType('callback', 'of type function', callback);
    // The callback runs in the AsyncLocalStorage context of the call.
    const hooks = globalThis.__async_hooks_module;
    if (hooks) callback = hooks.AsyncLocalStorage.bind(callback);
    crypto.randomBytes(size, (err, buf) => {
      if (err) callback(err);
      else callback(null, crypto.createSecretKey(buf));
    });
  };
})();
