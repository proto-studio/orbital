// createCipheriv / createDecipheriv and the Cipheriv and Decipheriv
// transform streams, over the native cipher contexts in cipher.go. Argument
// checks and errors follow Node 20.
(function () {
  'use strict';

  const crypto = __crypto_module;
  // Without the stream module (a bare runtime), the objects still cipher but
  // aren't streams.
  const Transform = (globalThis.__stream_module && globalThis.__stream_module.Transform) ||
    function Transform() {};
  const kHandle = Symbol('kCipherHandle');
  const kDecoder = Symbol('kDecoder');
  const kAuthTagLength = Symbol('kAuthTagLength');
  const kDone = Symbol('kDone');
  const kDecoderEncoding = Symbol('kDecoderEncoding');
  const kTag = Symbol('kTag');

  // A context is freed at final, or when its object is collected if it never
  // gets there.
  const registry = typeof FinalizationRegistry === 'function'
    ? new FinalizationRegistry((id) => crypto._cipherFree(id))
    : null;

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
    return codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "' + name + '" argument must be ' + expected + '. ' + describeReceived(value));
  }

  function inspectValue(value) {
    return typeof value === 'string' ? "'" + value + "'" : String(value);
  }

  // Rethrows a native "CODE|message" error the way Node throws it.
  function rethrow(e) {
    const text = String(e && e.message !== undefined ? e.message : e);
    const bar = text.indexOf('|');
    if (bar < 0) throw e;
    const code = text.slice(0, bar);
    const message = text.slice(bar + 1);
    let Type = Error;
    if (code === 'ERR_CRYPTO_INVALID_KEYLEN') Type = RangeError;
    else if (code === 'ERR_CRYPTO_INVALID_IV' || code === 'ERR_CRYPTO_INVALID_AUTH_TAG') Type = TypeError;
    throw codedError(Type, code, message);
  }

  function native(fn, ...args) {
    try {
      return fn(...args);
    } catch (e) {
      rethrow(e);
    }
  }

  function viewBytes(view) {
    return Buffer.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
  }

  function isAnyArrayBuffer(value) {
    return value instanceof ArrayBuffer ||
      (typeof SharedArrayBuffer !== 'undefined' && value instanceof SharedArrayBuffer);
  }

  function base64urlBytes(s) {
    return Buffer.from(String(s).replace(/-/g, '+').replace(/_/g, '/'), 'base64');
  }

  function secretKeyBytes(key, encoding) {
    if (typeof key === 'string') return Buffer.from(key, Buffer.isEncoding(encoding) ? encoding : 'utf8');
    if (ArrayBuffer.isView(key)) return viewBytes(key);
    if (isAnyArrayBuffer(key)) return Buffer.from(new Uint8Array(key));
    if (crypto.KeyObject && key instanceof crypto.KeyObject) {
      if (key.type !== 'secret') {
        throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE',
          'Invalid key object type ' + key.type + ', expected secret.');
      }
      return key.export();
    }
    if (key && typeof key === 'object' && key[Symbol.toStringTag] === 'CryptoKey' && key._jwk) {
      if (key.type !== 'secret') {
        throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE',
          'Invalid key object type ' + key.type + ', expected secret.');
      }
      return base64urlBytes(key._jwk.k);
    }
    throw invalidArgType('key', 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey', key);
  }

  function bufferSource(value, name, encoding) {
    if (typeof value === 'string') return Buffer.from(value, Buffer.isEncoding(encoding) ? encoding : 'utf8');
    if (ArrayBuffer.isView(value)) return viewBytes(value);
    if (isAnyArrayBuffer(value)) return Buffer.from(new Uint8Array(value));
    throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', value);
  }

  function authTagLengthOption(options) {
    if (!options || options.authTagLength === undefined) return -1;
    const n = options.authTagLength;
    if (typeof n !== 'number' || !Number.isInteger(n) || n < 0 || n > 4294967295) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
        "The property 'options.authTagLength' is invalid. Received " + inspectValue(n));
    }
    return n;
  }

  function init(self, cipher, key, iv, options, decrypt) {
    if (typeof cipher !== 'string') throw invalidArgType('cipher', 'of type string', cipher);
    const keyBytes = secretKeyBytes(key, options && options.encoding);
    const ivBytes = iv === null ? null : bufferSource(iv, 'iv');
    const tagLength = authTagLengthOption(options);
    const id = native(crypto._cipherInit, cipher, keyBytes.toString('base64'),
      ivBytes === null ? null : ivBytes.toString('base64'), decrypt, tagLength);
    self[kHandle] = id;
    self[kAuthTagLength] = tagLength;
    self[kDecoder] = null;
    self[kDone] = false;
    self[kTag] = null;
    if (registry) registry.register(self, id, self);
  }

  function dataBytes(data, inputEncoding) {
    if (typeof data === 'string') {
      if (inputEncoding === 'hex' && data.length % 2 !== 0) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
          "The argument 'encoding' is invalid for data of length " + data.length + ". Received 'hex'");
      }
      return Buffer.from(data, Buffer.isEncoding(inputEncoding) ? inputEncoding : 'utf8');
    }
    if (ArrayBuffer.isView(data)) return viewBytes(data);
    throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
  }

  // Base64 output is encoded in whole three-byte groups, with any remainder
  // kept for the next call, so the pieces join into one valid encoding.
  class Base64Decoder {
    constructor(encoding) {
      this.encoding = encoding;
      this.rest = Buffer.alloc(0);
    }

    write(buf) {
      const all = Buffer.concat([this.rest, buf]);
      const n = all.length - (all.length % 3);
      this.rest = all.subarray(n);
      return all.subarray(0, n).toString(this.encoding);
    }

    end() {
      const out = this.rest.toString(this.encoding);
      this.rest = Buffer.alloc(0);
      return out;
    }
  }

  function canonicalEncoding(encoding) {
    const e = String(encoding).toLowerCase();
    if (e === 'utf-8') return 'utf8';
    if (e === 'ucs2' || e === 'ucs-2' || e === 'utf-16le') return 'utf16le';
    if (e === 'binary') return 'latin1';
    return e;
  }

  function output(self, buf, outputEncoding) {
    if (outputEncoding && outputEncoding !== 'buffer') {
      // Compare encodings by their canonical names, so an alias such as
      // 'utf-8' keeps the decoder and any partial character it holds.
      const canonical = canonicalEncoding(outputEncoding);
      if (!self[kDecoder] || self[kDecoderEncoding] !== canonical) {
        self[kDecoderEncoding] = canonical;
        if (outputEncoding === 'base64' || outputEncoding === 'base64url') {
          self[kDecoder] = new Base64Decoder(outputEncoding);
        } else {
          const { StringDecoder } = globalThis.__string_decoder_module;
          self[kDecoder] = new StringDecoder(outputEncoding);
        }
      }
      return self[kDecoder].write(buf);
    }
    return buf;
  }

  const methods = {
    _transform(chunk, encoding, callback) {
      try {
        this.push(this.update(chunk, encoding));
      } catch (e) {
        callback(e);
        return;
      }
      callback();
    },

    _flush(callback) {
      try {
        this.push(this.final());
      } catch (e) {
        callback(e);
        return;
      }
      callback();
    },

    update(data, inputEncoding, outputEncoding) {
      if (this[kDone]) throw codedError(Error, '', 'Trying to add data in unsupported state');
      const bytes = dataBytes(data, inputEncoding);
      const out = Buffer.from(native(crypto._cipherUpdate, this[kHandle], bytes.toString('base64')), 'base64');
      return output(this, out, outputEncoding);
    },

    final(outputEncoding) {
      if (this[kDone]) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Invalid state');
      this[kDone] = true;
      let out;
      try {
        out = Buffer.from(native(crypto._cipherFinal, this[kHandle]), 'base64');
        if (this instanceof Cipheriv) {
          try {
            this[kTag] = Buffer.from(crypto._cipherGetAuthTag(this[kHandle]), 'base64');
          } catch {
            // not an authenticated cipher
          }
        }
      } finally {
        // The context is done with; getAuthTag uses the tag kept above.
        crypto._cipherFree(this[kHandle]);
        if (registry) registry.unregister(this);
      }
      if (outputEncoding && outputEncoding !== 'buffer') {
        const s = output(this, out, outputEncoding);
        const rest = this[kDecoder].end();
        return s + rest;
      }
      return out;
    },

    setAutoPadding(ap) {
      if (this[kDone]) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation setAutoPadding');
      native(crypto._cipherSetAutoPadding, this[kHandle], !!ap);
      return this;
    },

    setAAD(aadbuf, options) {
      const bytes = bufferSource(aadbuf, 'buffer', options && options.encoding);
      if (options !== undefined && options !== null && options.plaintextLength !== undefined) {
        const n = options.plaintextLength;
        if (typeof n !== 'number' || !Number.isInteger(n) || n < 0 || n > 2147483647) {
          throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
            "The property 'options.plaintextLength' is invalid. Received " + inspectValue(n));
        }
      }
      if (this[kDone]) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation setAAD');
      native(crypto._cipherSetAAD, this[kHandle], bytes.toString('base64'));
      return this;
    },
  };

  // Callable with or without new, as in Node. Transform may be a class, so the
  // object is built with Reflect.construct.
  function Cipheriv(cipher, key, iv, options) {
    if (!new.target) return new Cipheriv(cipher, key, iv, options);
    const self = Reflect.construct(Transform, [options], new.target);
    init(self, cipher, key, iv, options, false);
    return self;
  }

  function Decipheriv(cipher, key, iv, options) {
    if (!new.target) return new Decipheriv(cipher, key, iv, options);
    const self = Reflect.construct(Transform, [options], new.target);
    init(self, cipher, key, iv, options, true);
    return self;
  }

  for (const C of [Cipheriv, Decipheriv]) {
    Object.setPrototypeOf(C.prototype, Transform.prototype);
    Object.setPrototypeOf(C, Transform);
    for (const [name, fn] of Object.entries(methods)) {
      Object.defineProperty(C.prototype, name, { value: fn, writable: true, configurable: true, enumerable: true });
    }
  }

  Object.defineProperty(Cipheriv.prototype, 'getAuthTag', {
    value: function getAuthTag() {
      if (!this[kDone] || !this[kTag]) {
        throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation getAuthTag');
      }
      return Buffer.from(this[kTag]);
    },
    writable: true, configurable: true, enumerable: true,
  });

  Object.defineProperty(Decipheriv.prototype, 'setAuthTag', {
    value: function setAuthTag(tagbuf, encoding) {
      const tag = bufferSource(tagbuf, 'buffer', encoding);
      if (this[kDone]) throw codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation setAuthTag');
      const want = this[kAuthTagLength];
      if (want !== -1 && tag.length !== want) {
        throw codedError(TypeError, 'ERR_CRYPTO_INVALID_AUTH_TAG', 'Invalid authentication tag length: ' + tag.length);
      }
      native(crypto._cipherSetAuthTag, this[kHandle], tag.toString('base64'));
      return this;
    },
    writable: true, configurable: true, enumerable: true,
  });

  crypto.Cipheriv = Cipheriv;
  crypto.Decipheriv = Decipheriv;
  crypto.createCipheriv = function createCipheriv(cipher, key, iv, options) {
    return new Cipheriv(cipher, key, iv, options);
  };
  crypto.createDecipheriv = function createDecipheriv(cipher, key, iv, options) {
    return new Decipheriv(cipher, key, iv, options);
  };

  // getCipherInfo(nameOrNid[, { keyLength, ivLength }])
  crypto.getCipherInfo = function getCipherInfo(nameOrNid, options) {
    if (typeof nameOrNid !== 'string' && typeof nameOrNid !== 'number') {
      throw invalidArgType('nameOrNid', 'one of type string or number', nameOrNid);
    }
    const text = crypto._cipherInfo(nameOrNid);
    if (!text) return undefined;
    const spec = JSON.parse(text);
    if (options !== undefined) {
      if (options === null || typeof options !== 'object') throw invalidArgType('options', 'of type object', options);
      const { keyLength, ivLength } = options;
      if (keyLength !== undefined && keyLength !== spec.keyLength) return undefined;
      if (ivLength !== undefined) {
        // GCM takes any IV length, and reports the one asked about.
        if (spec.mode === 'gcm') {
          if (!(ivLength > 0)) return undefined;
          spec.ivLength = ivLength;
        } else if (ivLength !== (spec.ivLength || 0)) {
          return undefined;
        }
      }
    }
    const info = { mode: spec.mode, name: spec.name, nid: spec.nid, blockSize: spec.blockSize };
    if (spec.ivLength) info.ivLength = spec.ivLength;
    info.keyLength = spec.keyLength;
    return info;
  };
})();
