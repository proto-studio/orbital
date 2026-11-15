// KeyObject class + generateKeyPair/createSecretKey/createPrivateKey/
// createPublicKey wrappers over the native Go helpers (see keyobject.go).
//
// A KeyObject holds only an opaque integer handle into the Go key registry; all
// key bytes stay in Go. Raw bytes cross as base64, JWK/info as JSON.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const kHandle = Symbol('kKeyObjectHandle');

  class KeyObject {
    constructor(id) {
      this[kHandle] = id;
      const info = JSON.parse(crypto._keyInfo(id));
      this._type = info.type;
      this._asym = info.asymmetricKeyType;
      this._symSize = info.symmetricKeySize;
    }

    get type() {
      return this._type;
    }

    get asymmetricKeyType() {
      return this._asym;
    }

    get symmetricKeySize() {
      return this._symSize;
    }

    export(options) {
      options = options || {};
      const fmt = options.format;
      if (this._type === 'secret') {
        if (fmt === 'jwk') return JSON.parse(crypto._keyExportJWK(this[kHandle]));
        return Buffer.from(crypto._keyExportSecret(this[kHandle]), 'base64');
      }
      if (fmt === 'jwk') return JSON.parse(crypto._keyExportJWK(this[kHandle]));
      const type = options.type || (this._type === 'private' ? 'pkcs8' : 'spki');
      if (fmt === 'der') {
        return Buffer.from(crypto._keyExportDER(this[kHandle], type), 'base64');
      }
      return crypto._keyExportPEM(this[kHandle], type); // 'pem' (default)
    }

    equals(other) {
      if (!(other instanceof KeyObject)) return false;
      return crypto._keyEquals(this[kHandle], other[kHandle]);
    }
  }

  function wrap(id) {
    return new KeyObject(id);
  }

  function createSecretKey(key, encoding) {
    let buf;
    if (typeof key === 'string') buf = Buffer.from(key, encoding || 'utf8');
    else if (ArrayBuffer.isView(key)) buf = Buffer.from(new Uint8Array(key.buffer, key.byteOffset, key.byteLength));
    else buf = Buffer.from(key);
    return wrap(crypto._createSecretKey(buf.toString('base64')));
  }

  // Resolve any accepted key input to a Go handle. wantPrivate selects the
  // private vs public interpretation (and lets createPublicKey derive the public
  // half of a private KeyObject).
  function toHandle(input, wantPrivate) {
    if (input instanceof KeyObject) {
      if (!wantPrivate && input._type === 'private') {
        const jwk = crypto._keyExportJWK(input[kHandle]);
        return crypto._createPublicKeyJWK(jwk);
      }
      return input[kHandle];
    }
    if (typeof input === 'string') {
      return crypto._createKeyFromPEM(input, wantPrivate);
    }
    if (Buffer.isBuffer(input) || input instanceof Uint8Array) {
      return crypto._createKeyFromPEM(Buffer.from(input).toString('utf8'), wantPrivate);
    }
    if (input && typeof input === 'object') {
      if (input.format === 'jwk') {
        const s = JSON.stringify(input.key);
        return wantPrivate ? crypto._createPrivateKeyJWK(s) : crypto._createPublicKeyJWK(s);
      }
      const k = input.key;
      if (k instanceof KeyObject) return toHandle(k, wantPrivate);
      if (typeof k === 'string') return crypto._createKeyFromPEM(k, wantPrivate);
      if (Buffer.isBuffer(k) || k instanceof Uint8Array) {
        return crypto._createKeyFromPEM(Buffer.from(k).toString('utf8'), wantPrivate);
      }
    }
    throw new TypeError('Invalid key object');
  }

  function createPrivateKey(input) {
    return wrap(toHandle(input, true));
  }

  function createPublicKey(input) {
    return wrap(toHandle(input, false));
  }

  function codedError(Type, code, message) {
    const err = new Type(message);
    err.code = code;
    return err;
  }

  // Node's description of a rejected value, as in its ERR_INVALID_ARG_TYPE.
  function describeReceived(value) {
    if (value === null || value === undefined) return ' Received ' + value;
    if (typeof value === 'function') return ' Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
      if (value.constructor && value.constructor.name) {
        return ' Received an instance of ' + value.constructor.name;
      }
      return ' Received ' + String(value);
    }
    let shown;
    if (typeof value === 'string') {
      shown = value.length > 28 ? value.slice(0, 25) + '...' : value;
      shown = "'" + shown + "'";
    } else if (typeof value === 'bigint') {
      shown = String(value) + 'n';
    } else if (typeof value === 'symbol') {
      shown = String(value);
    } else {
      shown = Object.is(value, -0) ? '-0' : String(value);
    }
    return ' Received type ' + typeof value + ' (' + shown + ')';
  }

  // Node writes large out-of-range integers with _ separators.
  function formatRangeValue(value) {
    if (!Number.isInteger(value) || Math.abs(value) <= 2 ** 32) return String(value);
    let digits = String(Math.abs(value));
    let out = '';
    while (digits.length > 3) {
      out = '_' + digits.slice(-3) + out;
      digits = digits.slice(0, -3);
    }
    return (value < 0 ? '-' : '') + digits + out;
  }

  function validateUint32(value, name) {
    if (typeof value !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "' + name + '" property must be of type number.' + describeReceived(value));
    }
    if (!Number.isInteger(value)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be an integer. Received ' + value);
    }
    if (value < 0 || value > 4294967295) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "' + name + '" is out of range. It must be >= 0 && <= 4294967295. Received ' + formatRangeValue(value));
    }
  }

  // Checks the options Node validates before generating, so bad values
  // throw synchronously for both generateKeyPair and generateKeyPairSync.
  function validateKeyPairOptions(type, options) {
    if ((type === 'rsa' || type === 'rsa-pss') && options && options.publicExponent != null) {
      validateUint32(options.publicExponent, 'options.publicExponent');
    }
  }

  function generateKeyPairSync(type, options) {
    validateKeyPairOptions(type, options);
    const r = crypto._generateKeyPair(type, JSON.stringify(options || {}));
    return { publicKey: wrap(r.pub), privateKey: wrap(r.priv) };
  }

  function generateKeyPair(type, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = {};
    }
    if (typeof callback !== 'function') {
      throw new TypeError('The "callback" argument must be of type function');
    }
    validateKeyPairOptions(type, options);
    // Generation runs off the JS thread; see async.go.
    crypto._generateKeyPairAsync(type, JSON.stringify(options || {}), function (message, pub, priv) {
      if (message !== undefined) {
        callback(new Error(message));
        return;
      }
      callback(null, wrap(pub), wrap(priv));
    });
  }

  // Node special-cases util.promisify(generateKeyPair) to resolve with an object
  // { publicKey, privateKey } rather than just the first callback value.
  generateKeyPair[Symbol.for('nodejs.util.promisify.custom')] = function (type, options) {
    return new Promise(function (resolve, reject) {
      generateKeyPair(type, options, function (err, publicKey, privateKey) {
        if (err) reject(err);
        else resolve({ publicKey: publicKey, privateKey: privateKey });
      });
    });
  };

  // The registry handle behind a KeyObject, for sign.js.
  crypto._keyHandle = function (key) {
    return key[kHandle];
  };

  crypto.KeyObject = KeyObject;
  crypto.createSecretKey = createSecretKey;
  crypto.createPrivateKey = createPrivateKey;
  crypto.createPublicKey = createPublicKey;
  crypto.generateKeyPair = generateKeyPair;
  crypto.generateKeyPairSync = generateKeyPairSync;

  globalThis.__crypto_module = crypto;
})();
