// createDiffieHellman, getDiffieHellman, createECDH, crypto.diffieHellman and
// symmetric generateKey/generateKeySync, over the helpers in dh.go. Each object keeps its keys here; arguments,
// encodings and errors follow Node 20.
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

  const isBufferSource = (v) => ArrayBuffer.isView(v) || v instanceof ArrayBuffer;

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

  const modulusTooSmall = () => codedError(Error, 'ERR_OSSL_DH_MODULUS_TOO_SMALL',
    'error:0280007E:Diffie-Hellman routines::modulus too small');

  // ---- DiffieHellman ----------------------------------------------------------
  const DH_SOURCE = 'one of type number or string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView';

  function DiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding) {
    if (!new.target) return new DiffieHellman(sizeOrKey, keyEncoding, generator, genEncoding);
    if (typeof sizeOrKey !== 'number' && typeof sizeOrKey !== 'string' && !isBufferSource(sizeOrKey)) {
      throw invalidArgType('sizeOrKey', DH_SOURCE, sizeOrKey);
    }
    if (typeof sizeOrKey === 'number') validateInt32(sizeOrKey, 'sizeOrKey');
    if (keyEncoding && (typeof keyEncoding !== 'string' || (!Buffer.isEncoding(keyEncoding) && keyEncoding !== 'buffer'))) {
      genEncoding = generator;
      generator = keyEncoding;
      keyEncoding = false;
    }
    let g;
    if (!generator) {
      g = Buffer.from([2]);
    } else if (typeof generator === 'number') {
      validateInt32(generator, 'generator');
      let hex = generator.toString(16);
      if (hex.length % 2) hex = '0' + hex;
      g = Buffer.from(hex, 'hex');
    } else if (typeof generator === 'string') {
      g = toBuf(generator, genEncoding);
    } else if (isBufferSource(generator)) {
      g = toBuf(generator);
    } else {
      throw invalidArgType('generator', DH_SOURCE, generator);
    }
    // OpenSSL refuses a generator below 2 outright.
    if (!g.some((b, i) => b > (i === g.length - 1 ? 1 : 0))) {
      throw codedError(Error, 'ERR_OSSL_DH_BAD_GENERATOR', 'error:02800065:Diffie-Hellman routines::bad generator');
    }
    let prime;
    let verifyError;
    if (typeof sizeOrKey === 'number') {
      if (sizeOrKey < 512) throw modulusTooSmall();
      const gNum = parseInt(g.toString('hex') || '0', 16);
      prime = fromB64(native(crypto._dhGeneratePrime, sizeOrKey, gNum));
      verifyError = 0;
    } else {
      prime = toBuf(sizeOrKey, keyEncoding);
      verifyError = native(crypto._dhCheck, b64(prime), b64(g));
    }
    this[kState] = { prime, generator: g, verifyError, pub: null, priv: null };
  }

  function DiffieHellmanGroup(name) {
    if (!new.target) return new DiffieHellmanGroup(name);
    if (typeof name !== 'string') throw invalidArgType('name', 'of type string', name);
    const prime = crypto._dhGroup(name);
    if (!prime) throw codedError(Error, 'ERR_CRYPTO_UNKNOWN_DH_GROUP', 'Unknown DH group');
    this[kState] = { prime: fromB64(prime), generator: Buffer.from([2]), verifyError: 0, pub: null, priv: null, group: true };
  }

  const dhMethods = {
    generateKeys(encoding) {
      const s = this[kState];
      const bits = s.group ? null : s.prime.length * 8;
      const out = JSON.parse(native(crypto._dhKeys, b64(s.prime), b64(s.generator), s.priv ? b64(s.priv) : '',
        privateBits(s, bits)));
      s.priv = fromB64(out.priv);
      s.pub = fromB64(out.pub);
      return encode(Buffer.from(s.pub), encoding);
    },
    computeSecret(key, inEnc, outEnc) {
      const s = this[kState];
      const other = toBuf(key, inEnc);
      if (!s.priv) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'No private key - did you forget to generate one?');
      return encode(fromB64(native(crypto._dhSecret, b64(s.prime), b64(s.priv), b64(other))), outEnc);
    },
    getPrime(encoding) { return encode(Buffer.from(this[kState].prime), encoding); },
    getGenerator(encoding) { return encode(Buffer.from(this[kState].generator), encoding); },
    getPublicKey(encoding) {
      const s = this[kState];
      if (!s.pub) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'No public key - did you forget to generate one?');
      return encode(Buffer.from(s.pub), encoding);
    },
    getPrivateKey(encoding) {
      const s = this[kState];
      if (!s.priv) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'No private key - did you forget to generate one?');
      return encode(Buffer.from(s.priv), encoding);
    },
  };

  // Named groups use a private exponent sized to the group's strength, as
  // OpenSSL does (-1 asks dh.go for that); other primes use one as long as
  // the prime.
  function privateBits(s, bits) {
    return bits === null ? -1 : bits;
  }

  for (const C of [DiffieHellman, DiffieHellmanGroup]) {
    for (const [name, fn] of Object.entries(dhMethods)) {
      Object.defineProperty(C.prototype, name, { value: fn, writable: true, configurable: true, enumerable: true });
    }
    Object.defineProperty(C.prototype, 'verifyError', {
      get() { return this[kState].verifyError; }, configurable: true, enumerable: false,
    });
  }
  Object.defineProperty(DiffieHellman.prototype, 'setPublicKey', {
    value: function setPublicKey(key, encoding) {
      this[kState].pub = toBuf(key, encoding);
      return this;
    },
    writable: true, configurable: true, enumerable: true,
  });
  Object.defineProperty(DiffieHellman.prototype, 'setPrivateKey', {
    value: function setPrivateKey(key, encoding) {
      this[kState].priv = toBuf(key, encoding);
      return this;
    },
    writable: true, configurable: true, enumerable: true,
  });

  crypto.DiffieHellman = DiffieHellman;
  crypto.DiffieHellmanGroup = DiffieHellmanGroup;
  crypto.createDiffieHellman = function createDiffieHellman(prime, primeEncoding, generator, generatorEncoding) {
    return new DiffieHellman(prime, primeEncoding, generator, generatorEncoding);
  };
  crypto.getDiffieHellman = crypto.createDiffieHellmanGroup = function (name) {
    return new DiffieHellmanGroup(name);
  };

  // ---- crypto.diffieHellman with dh KeyObjects -------------------------------
  // Orbital has no dh KeyObjects, so diffieHellman can't take them.
  function unavailable(name) {
    return function () {
      throw codedError(TypeError, 'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM',
        'The feature ' + name + ' is unavailable on the current platform');
    };
  }

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
      // OpenSSL exports the scalar without leading zero bytes.
      let i = 0;
      while (i < s.priv.length - 1 && s.priv[i] === 0) i++;
      return encode(Buffer.from(s.priv.subarray(i)), encoding);
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
