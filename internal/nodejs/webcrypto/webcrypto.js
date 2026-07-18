// crypto/webcrypto - Web Crypto API (SubtleCrypto)
//
// The cryptographic operations are implemented natively in Go and exposed as
// __webcrypto_native (see subtle.go); this file is the WHATWG-shaped JS surface
// that libraries consume. Keys are carried as JWK objects, binary payloads cross
// the boundary as standard base64 strings.
(function(global) {
  'use strict';

  const cryptoModule = global.__crypto_module;
  const native = global.__webcrypto_native;

  // ---- byte / base64 helpers -------------------------------------------------
  function u8(data) {
    if (data == null) return new Uint8Array(0);
    if (data instanceof ArrayBuffer) return new Uint8Array(data);
    if (ArrayBuffer.isView(data)) {
      return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
    }
    throw new TypeError('Expected a BufferSource');
  }

  function bytesToB64(bytes) {
    let s = '';
    for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s);
  }

  function b64ToBytes(b64) {
    const s = atob(b64);
    const out = new Uint8Array(s.length);
    for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
    return out;
  }

  function b64urlToBytes(s) {
    s = String(s).replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    return b64ToBytes(s);
  }

  function bytesToB64url(bytes) {
    return bytesToB64(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  function normalizeAlgorithm(algorithm) {
    return typeof algorithm === 'string' ? { name: algorithm } : Object.assign({}, algorithm);
  }

  function hashName(h) {
    if (!h) return undefined;
    return typeof h === 'string' ? h : h.name;
  }

  // Only the crypto-material members of a JWK cross into Go.
  const JWK_FIELDS = ['kty', 'k', 'n', 'e', 'd', 'p', 'q', 'dp', 'dq', 'qi', 'crv', 'x', 'y'];
  function pickJWK(jwk) {
    const out = {};
    for (const f of JWK_FIELDS) if (jwk[f] !== undefined) out[f] = jwk[f];
    return out;
  }

  class CryptoKey {
    constructor(type, extractable, algorithm, usages, jwk) {
      this._type = type;
      this._extractable = extractable;
      this._algorithm = algorithm;
      this._usages = usages || [];
      this._jwk = jwk;
    }
    get type() { return this._type; }
    get extractable() { return this._extractable; }
    get algorithm() { return this._algorithm; }
    get usages() { return this._usages.slice(); }
    get [Symbol.toStringTag]() { return 'CryptoKey'; }
  }

  // Build the WebCrypto `algorithm` dictionary a CryptoKey exposes, from the
  // requested algorithm plus whatever the JWK tells us (modulusLength, curve,
  // key length). Libraries read these back (e.g. modulusLength >= 2048 checks).
  function buildKeyAlgorithm(algo, jwk) {
    const name = algo.name;
    const out = { name };
    const h = hashName(algo.hash);
    if (h) out.hash = { name: h };
    switch (name) {
      case 'HMAC': {
        const raw = jwk && jwk.k ? b64urlToBytes(jwk.k) : null;
        out.length = algo.length || (raw ? raw.length * 8 : 0);
        break;
      }
      case 'AES-GCM':
      case 'AES-CBC':
      case 'AES-KW':
      case 'AES-CTR': {
        const raw = jwk && jwk.k ? b64urlToBytes(jwk.k) : null;
        out.length = algo.length || (raw ? raw.length * 8 : 0);
        break;
      }
      case 'RSASSA-PKCS1-v1_5':
      case 'RSA-PSS':
      case 'RSA-OAEP': {
        out.modulusLength = algo.modulusLength || (jwk && jwk.n ? b64urlToBytes(jwk.n).length * 8 : 0);
        out.publicExponent = algo.publicExponent || new Uint8Array([1, 0, 1]);
        break;
      }
      case 'ECDSA':
      case 'ECDH': {
        out.namedCurve = algo.namedCurve || (jwk ? jwk.crv : undefined);
        break;
      }
      default:
        break;
    }
    return out;
  }

  function keyTypeFromJWK(jwk) {
    if (jwk.kty === 'oct') return 'secret';
    return jwk.d ? 'private' : 'public';
  }

  // Algorithms Orbital's SubtleCrypto can import. Anything else (e.g. the
  // post-quantum ML-DSA-* family) must reject with NotSupportedError, matching
  // Node/WebCrypto — libraries rely on this to detect unavailable algorithms.
  const SUPPORTED_IMPORT_ALGS = new Set([
    'HMAC', 'AES-GCM', 'AES-CBC', 'AES-KW', 'AES-CTR',
    'RSASSA-PKCS1-v1_5', 'RSA-PSS', 'RSA-OAEP',
    'ECDSA', 'ECDH', 'Ed25519', 'EdDSA', 'X25519',
    'PBKDF2', 'HKDF',
  ]);

  function assertSupportedAlg(name) {
    if (!SUPPORTED_IMPORT_ALGS.has(name)) {
      throw new DOMException('Unrecognized or unsupported algorithm name: ' + name, 'NotSupportedError');
    }
  }

  // Validate that a JWK carries the key material its kty requires. WebCrypto
  // throws DataError for a structurally invalid JWK (e.g. an RSA key with no
  // modulus); without this an unusable key would import silently.
  function assertValidJWK(jwk) {
    const need = (f) => {
      if (jwk[f] === undefined || jwk[f] === '') {
        throw new DOMException('Invalid JWK: "' + f + '" member is required', 'DataError');
      }
    };
    switch (jwk.kty) {
      case 'oct': need('k'); break;
      case 'RSA': need('n'); need('e'); break;
      case 'EC': need('crv'); need('x'); need('y'); break;
      case 'OKP': need('crv'); need('x'); break;
      default:
        throw new DOMException('Unsupported JWK "kty": ' + jwk.kty, 'NotSupportedError');
    }
  }

  class SubtleCrypto {
    async digest(algorithm, data) {
      const name = typeof algorithm === 'string' ? algorithm : algorithm.name;
      // Native (base64 boundary) so binary inputs with NUL bytes hash correctly.
      const outB64 = native.digest(name, bytesToB64(u8(data)));
      return b64ToBytes(outB64).buffer;
    }

    async generateKey(algorithm, extractable, keyUsages) {
      const algo = normalizeAlgorithm(algorithm);
      const params = {
        name: algo.name,
        length: algo.length || 0,
        modulusLength: algo.modulusLength || 0,
        hash: hashName(algo.hash) || '',
        namedCurve: algo.namedCurve || '',
      };
      const resultJSON = native.generateKey(JSON.stringify(params));
      const result = JSON.parse(resultJSON);

      if (result.secret) {
        const keyAlgo = buildKeyAlgorithm(algo, result.secret);
        return new CryptoKey('secret', extractable, keyAlgo, keyUsages, pickJWK(result.secret));
      }

      const pubUsages = [];
      const privUsages = [];
      for (const u of keyUsages) {
        if (['verify', 'encrypt', 'wrapKey'].includes(u)) pubUsages.push(u);
        else privUsages.push(u);
      }
      const publicKey = new CryptoKey('public', true, buildKeyAlgorithm(algo, result.publicKey), pubUsages, pickJWK(result.publicKey));
      const privateKey = new CryptoKey('private', extractable, buildKeyAlgorithm(algo, result.privateKey), privUsages, pickJWK(result.privateKey));
      return { publicKey, privateKey };
    }

    async importKey(format, keyData, algorithm, extractable, keyUsages) {
      const algo = normalizeAlgorithm(algorithm);
      assertSupportedAlg(algo.name);
      let jwk;
      if (format === 'raw') {
        jwk = { kty: 'oct', k: bytesToB64url(u8(keyData)) };
      } else if (format === 'jwk') {
        jwk = pickJWK(keyData);
      } else if (format === 'spki' || format === 'pkcs8') {
        // Convert the DER (SPKI public / PKCS#8 private) to a JWK via the native
        // KeyObject bridge, then proceed as for a jwk import.
        const wantPrivate = format === 'pkcs8';
        const id = cryptoModule._createKeyFromDER(bytesToB64(u8(keyData)), wantPrivate);
        jwk = pickJWK(JSON.parse(cryptoModule._keyExportJWK(id)));
      } else {
        throw new DOMException('Unsupported key format: ' + format, 'NotSupportedError');
      }
      assertValidJWK(jwk);
      const keyAlgo = buildKeyAlgorithm(algo, jwk);
      const type = keyTypeFromJWK(jwk);
      return new CryptoKey(type, extractable, keyAlgo, keyUsages, jwk);
    }

    async exportKey(format, key) {
      if (!key.extractable) {
        throw new DOMException('Key is not extractable', 'InvalidAccessError');
      }
      if (format === 'raw') {
        if (key._jwk.kty !== 'oct') {
          throw new DOMException('raw export only supported for symmetric keys', 'NotSupportedError');
        }
        return b64urlToBytes(key._jwk.k).buffer;
      }
      if (format === 'jwk') {
        return Object.assign({}, key._jwk, { ext: key.extractable, key_ops: key.usages });
      }
      if (format === 'spki' || format === 'pkcs8') {
        // Round-trip the stored JWK through the native KeyObject bridge to get
        // the DER encoding (SPKI for public keys, PKCS#8 for private keys).
        const wantPrivate = format === 'pkcs8';
        const jwkStr = JSON.stringify(key._jwk);
        const id = wantPrivate
          ? cryptoModule._createPrivateKeyJWK(jwkStr)
          : cryptoModule._createPublicKeyJWK(jwkStr);
        const derB64 = cryptoModule._keyExportDER(id, wantPrivate ? 'pkcs8' : 'spki');
        return b64ToBytes(derB64).buffer;
      }
      throw new DOMException('Unsupported key format: ' + format, 'NotSupportedError');
    }

    async sign(algorithm, key, data) {
      const algo = normalizeAlgorithm(algorithm);
      const params = {
        name: algo.name,
        hash: hashName(algo.hash) || (key.algorithm.hash && key.algorithm.hash.name) || '',
        saltLength: algo.saltLength || 0,
      };
      const sigB64 = native.sign(JSON.stringify(params), JSON.stringify(key._jwk), bytesToB64(u8(data)));
      return b64ToBytes(sigB64).buffer;
    }

    async verify(algorithm, key, signature, data) {
      const algo = normalizeAlgorithm(algorithm);
      const params = {
        name: algo.name,
        hash: hashName(algo.hash) || (key.algorithm.hash && key.algorithm.hash.name) || '',
        saltLength: algo.saltLength || 0,
      };
      const res = native.verify(
        JSON.stringify(params), JSON.stringify(key._jwk),
        bytesToB64(u8(signature)), bytesToB64(u8(data))
      );
      return res === 'true';
    }

    async encrypt(algorithm, key, data) {
      return this._crypt(algorithm, key, data, true);
    }

    async decrypt(algorithm, key, data) {
      return this._crypt(algorithm, key, data, false);
    }

    async _crypt(algorithm, key, data, encrypting) {
      const algo = normalizeAlgorithm(algorithm);
      const params = { name: algo.name };
      if (algo.iv !== undefined) params.ivB64 = bytesToB64(u8(algo.iv));
      if (algo.additionalData !== undefined) params.aadB64 = bytesToB64(u8(algo.additionalData));
      if (algo.tagLength !== undefined) params.tagLength = algo.tagLength;
      if (algo.name === 'RSA-OAEP') {
        params.hash = (key.algorithm.hash && key.algorithm.hash.name) || hashName(algo.hash) || 'SHA-1';
      }
      const fn = encrypting ? native.encrypt : native.decrypt;
      const outB64 = fn(JSON.stringify(params), JSON.stringify(key._jwk), bytesToB64(u8(data)));
      return b64ToBytes(outB64).buffer;
    }

    async deriveBits(algorithm, baseKey, length) {
      const algo = normalizeAlgorithm(algorithm);
      if (algo.name === 'PBKDF2') {
        const params = {
          passwordB64: bytesToB64(b64urlToBytes(baseKey._jwk.k)),
          saltB64: bytesToB64(u8(algo.salt)),
          iterations: algo.iterations,
          hash: hashName(algo.hash),
          length: length || 0,
        };
        return b64ToBytes(native.pbkdf2(JSON.stringify(params))).buffer;
      }
      if (algo.name !== 'ECDH' && algo.name !== 'X25519') {
        throw new DOMException('deriveBits: unsupported algorithm ' + algo.name, 'NotSupportedError');
      }
      if (!algo.public) {
        throw new DOMException('deriveBits requires a public key', 'OperationError');
      }
      const outB64 = native.deriveBits(
        JSON.stringify(baseKey._jwk), JSON.stringify(algo.public._jwk), length || 0
      );
      return b64ToBytes(outB64).buffer;
    }

    async deriveKey(algorithm, baseKey, derivedKeyAlgorithm, extractable, keyUsages) {
      const bits = await this.deriveBits(algorithm, baseKey, derivedKeyAlgorithm.length || 256);
      return this.importKey('raw', bits, derivedKeyAlgorithm, extractable, keyUsages);
    }

    async wrapKey(format, key, wrappingKey, wrapAlgorithm) {
      const algo = normalizeAlgorithm(wrapAlgorithm);
      const exported = await this.exportKey(format, key);
      const keyBytes = u8(exported);
      if (algo.name === 'AES-KW') {
        const kek = b64urlToBytes(wrappingKey._jwk.k);
        const wrappedB64 = native.aesKwWrap(bytesToB64(kek), bytesToB64(keyBytes));
        return b64ToBytes(wrappedB64).buffer;
      }
      // RSA-OAEP / AES-GCM key wrapping is just encryption of the exported bytes.
      const wrapped = await this.encrypt(wrapAlgorithm, wrappingKey, keyBytes);
      return wrapped;
    }

    async unwrapKey(format, wrappedKey, unwrappingKey, unwrapAlgorithm, unwrappedKeyAlgorithm, extractable, keyUsages) {
      const algo = normalizeAlgorithm(unwrapAlgorithm);
      let rawBytes;
      if (algo.name === 'AES-KW') {
        const kek = b64urlToBytes(unwrappingKey._jwk.k);
        const rawB64 = native.aesKwUnwrap(bytesToB64(kek), bytesToB64(u8(wrappedKey)));
        rawBytes = b64ToBytes(rawB64);
      } else {
        const dec = await this.decrypt(unwrapAlgorithm, unwrappingKey, u8(wrappedKey));
        rawBytes = u8(dec);
      }
      return this.importKey(format, rawBytes, unwrappedKeyAlgorithm, extractable, keyUsages);
    }

    get [Symbol.toStringTag]() { return 'SubtleCrypto'; }
  }

  const webcrypto = {
    subtle: new SubtleCrypto(),

    getRandomValues: function(typedArray) {
      if (!(typedArray instanceof Int8Array ||
            typedArray instanceof Uint8Array ||
            typedArray instanceof Uint8ClampedArray ||
            typedArray instanceof Int16Array ||
            typedArray instanceof Uint16Array ||
            typedArray instanceof Int32Array ||
            typedArray instanceof Uint32Array ||
            typedArray instanceof BigInt64Array ||
            typedArray instanceof BigUint64Array)) {
        throw new TypeError('Expected TypedArray');
      }
      if (typedArray.byteLength > 65536) {
        throw new DOMException('Quota exceeded', 'QuotaExceededError');
      }
      const buffer = cryptoModule.randomBytes(typedArray.byteLength);
      const view = new Uint8Array(typedArray.buffer, typedArray.byteOffset, typedArray.byteLength);
      for (let i = 0; i < buffer.length && i < view.length; i++) view[i] = buffer[i];
      return typedArray;
    },

    randomUUID: function() {
      const bytes = new Uint8Array(16);
      this.getRandomValues(bytes);
      bytes[6] = (bytes[6] & 0x0f) | 0x40;
      bytes[8] = (bytes[8] & 0x3f) | 0x80;
      const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
      return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    },

    CryptoKey,
    SubtleCrypto
  };

  global.__webcrypto_module = webcrypto;

  if (typeof global.crypto === 'undefined') {
    global.crypto = webcrypto;
  } else {
    global.crypto.subtle = webcrypto.subtle;
    global.crypto.getRandomValues = webcrypto.getRandomValues;
    global.crypto.randomUUID = webcrypto.randomUUID;
  }

})(globalThis);
