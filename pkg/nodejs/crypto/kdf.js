// crypto.pbkdf2, scrypt and hkdf, with their Sync forms: argument checks and
// errors as in Node 20 over the native _pbkdf2, _scrypt and _hkdf (kdf.go).
(function () {
  'use strict';

  const crypto = __crypto_module;
  const kMaxLength = 4294967296;
  // The longest key the natives can return (lengths.go); Node allows more.
  const kMaxNativeBytes = crypto._maxNativeBytes;

  function checkNativeKeyLen(keylen) {
    if (keylen > kMaxNativeBytes) throw outOfRange('keylen', '>= 0 && <= ' + kMaxNativeBytes, keylen);
  }
  const scryptDefaults = { N: 16384, r: 8, p: 1, maxmem: 32 << 20 };

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
      shown = value.length > 28 ? value.slice(0, 25) + '...' : value;
      shown = "'" + shown + "'";
    } else if (typeof value === 'bigint') {
      shown = String(value) + 'n';
    } else if (typeof value === 'symbol') {
      shown = String(value);
    } else {
      shown = Object.is(value, -0) ? '-0' : String(value);
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
  }

  // Node writes integers beyond 2^32 with _ separators in range errors.
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

  function invalidArgType(name, expected, value) {
    return codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "' + name + '" argument must be ' + expected + '. ' + describeReceived(value));
  }

  function outOfRange(name, range, value) {
    return codedError(RangeError, 'ERR_OUT_OF_RANGE',
      'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + formatRangeValue(value));
  }

  function validateString(value, name) {
    if (typeof value !== 'string') throw invalidArgType(name, 'of type string', value);
  }

  function validateFunction(value, name) {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
  }

  function validateInteger(value, name, min, max) {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > max) throw outOfRange(name, '>= ' + min + ' && <= ' + max, value);
  }

  function validateInt32(value, name, min) {
    validateInteger(value, name, min, 2147483647);
  }

  function validateUint32(value, name) {
    validateInteger(value, name, 0, 4294967295);
  }

  // The embedder's Config.CryptoMemoryLimit bounds a derived key's length,
  // after what the inputs (inputB64Length characters of base64) take; without
  // one, any length Node takes is allowed.
  function checkKeyLenLimit(keylen, name, inputB64Length) {
    const max = crypto._kdfMaxKeyLen(inputB64Length);
    if (max >= 0 && keylen > max) {
      throw outOfRange(name, '<= ' + max, keylen);
    }
  }

  function viewBytes(view) {
    return Buffer.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
  }

  // The bytes of a string (UTF-8) or BufferSource, as base64, or null.
  function sourceBytes(value) {
    if (typeof value === 'string') return Buffer.from(value, 'utf8');
    if (ArrayBuffer.isView(value)) return viewBytes(value);
    if (value instanceof ArrayBuffer ||
        (typeof SharedArrayBuffer !== 'undefined' && value instanceof SharedArrayBuffer)) {
      return Buffer.from(new Uint8Array(value));
    }
    return null;
  }

  function bufferSource(value, name) {
    const bytes = sourceBytes(value);
    if (bytes === null) {
      throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', value);
    }
    return bytes.toString('base64');
  }

  function digestSize(digest) {
    const size = crypto._digestSize(digest);
    if (!size) throw codedError(TypeError, 'ERR_CRYPTO_INVALID_DIGEST', 'Invalid digest: ' + digest);
    return size;
  }

  // Wraps a callback for the native base64 result. The callback runs in the
  // AsyncLocalStorage context that was current when the work was started.
  function deliver(callback, toResult) {
    const hooks = globalThis.__async_hooks_module;
    if (hooks) callback = hooks.AsyncLocalStorage.bind(callback);
    return function (message, data) {
      if (message !== undefined) {
        callback(new Error(message));
        return;
      }
      callback(null, toResult(data));
    };
  }

  const toBuffer = (b64) => Buffer.from(b64, 'base64');
  const toArrayBuffer = (b64) => {
    const buf = Buffer.from(b64, 'base64');
    return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.length);
  };

  // ---- pbkdf2 ---------------------------------------------------------------
  function pbkdf2Check(password, salt, iterations, keylen, digest) {
    validateString(digest, 'digest');
    password = bufferSource(password, 'password');
    salt = bufferSource(salt, 'salt');
    validateInt32(iterations, 'iterations', 1);
    validateInt32(keylen, 'keylen', 0);
    checkNativeKeyLen(keylen);
    digestSize(digest);
    checkKeyLenLimit(keylen, 'keylen', password.length + salt.length);
    return [password, salt, iterations, keylen, digest];
  }

  crypto.pbkdf2Sync = function pbkdf2Sync(password, salt, iterations, keylen, digest) {
    return toBuffer(crypto._pbkdf2(...pbkdf2Check(password, salt, iterations, keylen, digest)));
  };

  crypto.pbkdf2 = function pbkdf2(password, salt, iterations, keylen, digest, callback) {
    if (typeof digest === 'function') {
      callback = digest;
      digest = undefined;
    }
    const args = pbkdf2Check(password, salt, iterations, keylen, digest);
    validateFunction(callback, 'callback');
    crypto._pbkdf2(...args, deliver(callback, toBuffer));
  };

  // ---- scrypt ---------------------------------------------------------------
  function scryptCheck(password, salt, keylen, options) {
    password = bufferSource(password, 'password');
    salt = bufferSource(salt, 'salt');
    validateInt32(keylen, 'keylen', 0);
    checkNativeKeyLen(keylen);

    let { N, r, p, maxmem } = scryptDefaults;
    if (options && options !== scryptDefaults) {
      const hasN = options.N !== undefined;
      if (hasN) {
        N = options.N;
        validateUint32(N, 'N');
      }
      if (options.cost !== undefined) {
        if (hasN) throw codedError(Error, 'ERR_CRYPTO_SCRYPT_INVALID_PARAMETER', 'Invalid scrypt parameter');
        N = options.cost;
        validateUint32(N, 'cost');
      }
      const hasR = options.r !== undefined;
      if (hasR) {
        r = options.r;
        validateUint32(r, 'r');
      }
      if (options.blockSize !== undefined) {
        if (hasR) throw codedError(Error, 'ERR_CRYPTO_SCRYPT_INVALID_PARAMETER', 'Invalid scrypt parameter');
        r = options.blockSize;
        validateUint32(r, 'blockSize');
      }
      const hasP = options.p !== undefined;
      if (hasP) {
        p = options.p;
        validateUint32(p, 'p');
      }
      if (options.parallelization !== undefined) {
        if (hasP) throw codedError(Error, 'ERR_CRYPTO_SCRYPT_INVALID_PARAMETER', 'Invalid scrypt parameter');
        p = options.parallelization;
        validateUint32(p, 'parallelization');
      }
      if (options.maxmem !== undefined) {
        maxmem = options.maxmem;
        validateInteger(maxmem, 'maxmem', 0, Number.MAX_SAFE_INTEGER);
      }
      if (N === 0) N = scryptDefaults.N;
      if (r === 0) r = scryptDefaults.r;
      if (p === 0) p = scryptDefaults.p;
      if (maxmem === 0) maxmem = scryptDefaults.maxmem;
    }
    const failed = crypto._scryptCheck(N, r, p, maxmem, keylen, password.length + salt.length);
    if (failed) {
      const reason = failed.slice(1);
      throw codedError(RangeError, 'ERR_CRYPTO_INVALID_SCRYPT_PARAMS',
        'Invalid scrypt params' + (reason ? ': ' + reason : ''));
    }
    return [password, salt, N, r, p, maxmem, keylen];
  }

  crypto.scryptSync = function scryptSync(password, salt, keylen, options) {
    return toBuffer(crypto._scrypt(...scryptCheck(password, salt, keylen, options || scryptDefaults)));
  };

  crypto.scrypt = function scrypt(password, salt, keylen, options, callback) {
    if (typeof options === 'function') {
      callback = options;
      options = scryptDefaults;
    }
    const args = scryptCheck(password, salt, keylen, options);
    validateFunction(callback, 'callback');
    crypto._scrypt(...args, deliver(callback, toBuffer));
  };

  // ---- hkdf -----------------------------------------------------------------
  function hkdfKey(key) {
    if (crypto.KeyObject && key instanceof crypto.KeyObject && key.type === 'secret') {
      return key.export().toString('base64');
    }
    const bytes = sourceBytes(key);
    if (bytes === null) {
      throw invalidArgType('ikm', 'of type string or an instance of SecretKeyObject, ArrayBuffer, TypedArray, DataView, or Buffer', key);
    }
    return bytes.toString('base64');
  }

  function hkdfByteSource(value, name) {
    const bytes = sourceBytes(value);
    if (bytes === null) {
      throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, TypedArray, DataView, or Buffer', value);
    }
    return bytes;
  }

  function hkdfCheck(digest, key, salt, info, length) {
    validateString(digest, 'digest');
    key = hkdfKey(key);
    salt = hkdfByteSource(salt, 'salt');
    info = hkdfByteSource(info, 'info');
    validateInteger(length, 'length', 0, kMaxLength);
    if (info.length > 1024) {
      throw outOfRange('info', 'must not contain more than 1024 bytes', info.length);
    }
    const size = digestSize(digest);
    if (length > 255 * size) {
      throw codedError(RangeError, 'ERR_CRYPTO_INVALID_KEYLEN', 'Invalid key length');
    }
    salt = salt.toString('base64');
    info = info.toString('base64');
    checkKeyLenLimit(length, 'length', key.length + salt.length + info.length);
    return [digest, key, salt, info, length];
  }

  crypto.hkdfSync = function hkdfSync(digest, key, salt, info, length) {
    return toArrayBuffer(crypto._hkdf(...hkdfCheck(digest, key, salt, info, length)));
  };

  crypto.hkdf = function hkdf(digest, key, salt, info, length, callback) {
    const args = hkdfCheck(digest, key, salt, info, length);
    validateFunction(callback, 'callback');
    crypto._hkdf(...args, deliver(callback, toArrayBuffer));
  };
})();
