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

  function generateKeyPairSync(type, options) {
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
    Promise.resolve().then(function () {
      let r;
      try {
        r = crypto._generateKeyPair(type, JSON.stringify(options || {}));
      } catch (e) {
        callback(e);
        return;
      }
      callback(null, wrap(r.pub), wrap(r.priv));
    });
  }

  // Node special-cases util.promisify(generateKeyPair) to resolve with an object
  // { publicKey, privateKey } rather than just the first callback value.
  generateKeyPair[Symbol.for('nodejs.util.promisify.custom')] = function (type, options) {
    return Promise.resolve().then(function () {
      return generateKeyPairSync(type, options);
    });
  };

  crypto.KeyObject = KeyObject;
  crypto.createSecretKey = createSecretKey;
  crypto.createPrivateKey = createPrivateKey;
  crypto.createPublicKey = createPublicKey;
  crypto.generateKeyPair = generateKeyPair;
  crypto.generateKeyPairSync = generateKeyPairSync;

  globalThis.__crypto_module = crypto;
})();
