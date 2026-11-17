// The remaining node:crypto helpers: randomFill, getRandomValues, webcrypto
// and subtle, checkPrime and generatePrime, getFips, secureHeapUsed,
// setEngine and the legacy Certificate (SPKAC) class. Argument checks and
// errors follow Node 20; the work is done by the natives in extras.go.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const kMaxPossibleLength = 2147483647;
  // The most bytes the native returns at once (lengths.go); larger fills
  // are made in pieces of this size.
  const maxNativeBytes = () => crypto._maxNativeBytes;
  // The largest prime size, in bits, the native can return.
  const kMaxPrimeBits = crypto._maxPrimeBits;

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
    if (typeof value === 'bigint') return String(value) + 'n';
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
    const kind = name.includes('.') ? 'property' : 'argument';
    return codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
      'The "' + name + '" ' + kind + ' must be ' + expected + '. ' + describeReceived(value));
  }

  function outOfRange(name, range, value) {
    return codedError(RangeError, 'ERR_OUT_OF_RANGE',
      'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + formatRangeValue(value));
  }

  function validateFunction(value, name) {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
  }

  function validateObject(value, name) {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      throw invalidArgType(name, 'of type object', value);
    }
  }

  function validateBoolean(value, name) {
    if (typeof value !== 'boolean') throw invalidArgType(name, 'of type boolean', value);
  }

  function validateNumber(value, name) {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
  }

  function validateInt32(value, name, min) {
    validateNumber(value, name);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > 2147483647) throw outOfRange(name, '>= ' + min + ' && <= 2147483647', value);
  }

  // Binds a callback to the AsyncLocalStorage context current when the work
  // starts, so it runs in that context as in Node.
  function bindContext(callback) {
    const hooks = globalThis.__async_hooks_module;
    return hooks ? hooks.AsyncLocalStorage.bind(callback) : callback;
  }

  function isAnyArrayBuffer(value) {
    return value instanceof ArrayBuffer ||
      (typeof SharedArrayBuffer !== 'undefined' && value instanceof SharedArrayBuffer);
  }

  function bytesOf(value) {
    if (isAnyArrayBuffer(value)) return new Uint8Array(value);
    return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  }

  // ---- randomFill -----------------------------------------------------------
  function checkBuf(buf) {
    if (!isAnyArrayBuffer(buf) && !ArrayBuffer.isView(buf)) {
      throw invalidArgType('buf', 'an instance of ArrayBuffer or ArrayBufferView', buf);
    }
  }

  function assertOffset(offset, elementSize, length) {
    validateNumber(offset, 'offset');
    offset *= elementSize;
    const maxLength = Math.min(length, kMaxPossibleLength);
    if (Number.isNaN(offset) || offset > maxLength || offset < 0) {
      throw outOfRange('offset', '>= 0 && <= ' + maxLength, offset);
    }
    return offset >>> 0;
  }

  function assertSize(size, elementSize, offset, length) {
    validateNumber(size, 'size');
    size *= elementSize;
    if (Number.isNaN(size) || size > kMaxPossibleLength || size < 0) {
      throw outOfRange('size', '>= 0 && <= ' + kMaxPossibleLength, size);
    }
    if (size + offset > length) {
      throw outOfRange('size + offset', '<= ' + length, size + offset);
    }
    return size >>> 0;
  }

  function fill(buf, offset, size, base64) {
    bytesOf(buf).set(Buffer.from(base64, 'base64'), offset);
  }

  crypto.randomFillSync = function randomFillSync(buf, offset = 0, size) {
    checkBuf(buf);
    const elementSize = buf.BYTES_PER_ELEMENT || 1;
    offset = assertOffset(offset, elementSize, buf.byteLength);
    if (size === undefined) {
      size = buf.byteLength - offset;
    } else {
      size = assertSize(size, elementSize, offset, buf.byteLength);
    }
    const piece = maxNativeBytes();
    for (let done = 0; done < size; done += piece) {
      const n = Math.min(piece, size - done);
      fill(buf, offset + done, n, crypto._randomBase64(n));
    }
    return buf;
  };

  crypto.randomFill = function randomFill(buf, offset, size, callback) {
    checkBuf(buf);
    const elementSize = buf.BYTES_PER_ELEMENT || 1;
    if (typeof offset === 'function') {
      callback = offset;
      offset = 0;
      size = buf.byteLength;
    } else if (typeof size === 'function') {
      callback = size;
      offset = assertOffset(offset, elementSize, buf.byteLength);
      size = buf.byteLength - offset;
    } else {
      validateFunction(callback, 'callback');
      offset = assertOffset(offset, elementSize, buf.byteLength);
      if (size === undefined) {
        size = buf.byteLength - offset;
      } else {
        size = assertSize(size, elementSize, offset, buf.byteLength);
      }
    }
    // Node calls back at once when there's nothing to fill.
    if (size === 0) {
      callback(null, buf);
      return;
    }
    // The bytes are made off the JS thread and copied in on it.
    callback = bindContext(callback);
    let done = 0;
    const piece = maxNativeBytes();
    const next = () => {
      const n = Math.min(piece, size - done);
      crypto._randomBase64(n, onPiece);
    };
    function onPiece(message, data) {
      if (message !== undefined) {
        callback(new Error(message));
        return;
      }
      const n = Math.min(piece, size - done);
      fill(buf, offset + done, n, data);
      done += n;
      if (done < size) {
        next();
        return;
      }
      callback(null, buf);
    }
    next();
  };

  // ---- webcrypto ------------------------------------------------------------
  // webcrypto.js installs its object as __webcrypto_module (and as
  // globalThis.crypto, which scripts may replace or delete).
  const webcrypto = () => globalThis.__webcrypto_module;
  crypto.getRandomValues = function getRandomValues(array) {
    return webcrypto().getRandomValues(array);
  };
  Object.defineProperty(crypto, 'webcrypto', {
    configurable: false, enumerable: true, get() { return webcrypto(); },
  });
  Object.defineProperty(crypto, 'subtle', {
    configurable: false, enumerable: true, get() { return webcrypto().subtle; },
  });

  // ---- primes ---------------------------------------------------------------
  const PRIME_SOURCE = 'of type bigint or an instance of ArrayBuffer, TypedArray, Buffer, or DataView';

  // A bigint or BufferSource as a hex string (big-endian, unsigned).
  function unsignedHex(value, name) {
    if (typeof value === 'bigint') {
      if (value < 0n) throw outOfRange(name, '>= 0', value);
      return value.toString(16);
    }
    if (!isAnyArrayBuffer(value) && !ArrayBuffer.isView(value)) {
      throw invalidArgType(name, PRIME_SOURCE, value);
    }
    const hex = Buffer.from(bytesOf(value)).toString('hex');
    return hex === '' ? '0' : hex;
  }

  function checkPrimeArgs(candidate, options, callback, sync) {
    const hex = unsignedHex(candidate, 'candidate');
    if (typeof options === 'function') {
      callback = options;
      options = {};
    }
    if (options === undefined) options = {};
    if (!sync) validateFunction(callback, 'callback');
    validateObject(options, 'options');
    const { checks = 0 } = options;
    validateInt32(checks, 'options.checks', 0);
    return [hex, checks, callback];
  }

  crypto.checkPrimeSync = function checkPrimeSync(candidate, options = {}) {
    const [hex, checks] = checkPrimeArgs(candidate, options, undefined, true);
    return crypto._checkPrime(hex, checks) === 'true';
  };

  crypto.checkPrime = function checkPrime(candidate, options = {}, callback) {
    const [hex, checks, unbound] = checkPrimeArgs(candidate, options, callback, false);
    const cb = bindContext(unbound);
    crypto._checkPrime(hex, checks, function (message, result) {
      if (message !== undefined) {
        cb(new Error(message));
        return;
      }
      cb(undefined, result === 'true');
    });
  };

  function primeJob(size, options) {
    validateObject(options, 'options');
    const { safe = false, bigint = false } = options;
    let { add, rem } = options;
    validateBoolean(safe, 'options.safe');
    validateBoolean(bigint, 'options.bigint');
    const addHex = add === undefined ? '' : unsignedHex(add, 'options.add');
    const remHex = rem === undefined ? '' : unsignedHex(rem, 'options.rem');
    if (addHex !== '') {
      const a = BigInt('0x' + addHex);
      if (a.toString(2).length > size && a !== 0n) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'invalid options.add');
      }
      if (remHex !== '' && !(a > BigInt('0x' + remHex))) {
        throw codedError(RangeError, 'ERR_OUT_OF_RANGE', 'invalid options.rem');
      }
    }
    return { args: [size, safe, addHex, remHex], bigint };
  }

  function primeResult(hex, bigint) {
    if (bigint) return BigInt('0x' + hex);
    const buf = Buffer.from(hex.length % 2 ? '0' + hex : hex, 'hex');
    return buf.buffer.slice(buf.byteOffset, buf.byteOffset + buf.length);
  }

  // Node takes any int32 size; the native can't return a prime longer than a
  // string holds.
  function checkPrimeSize(size) {
    if (size > kMaxPrimeBits) throw outOfRange('size', '>= 1 && <= ' + kMaxPrimeBits, size);
  }

  crypto.generatePrimeSync = function generatePrimeSync(size, options = {}) {
    validateInt32(size, 'size', 1);
    checkPrimeSize(size);
    const job = primeJob(size, options);
    return primeResult(crypto._generatePrime(...job.args), job.bigint);
  };

  crypto.generatePrime = function generatePrime(size, options = {}, callback) {
    validateInt32(size, 'size', 1);
    checkPrimeSize(size);
    if (typeof options === 'function') {
      callback = options;
      options = {};
    }
    validateFunction(callback, 'callback');
    const job = primeJob(size, options);
    callback = bindContext(callback);
    crypto._generatePrime(...job.args, function (message, hex) {
      if (message !== undefined) {
        callback(new Error(message));
        return;
      }
      callback(undefined, primeResult(hex, job.bigint));
    });
  };

  // ---- FIPS, secure heap and engines -----------------------------------------
  // Orbital uses Go's crypto, with no OpenSSL FIPS provider, secure heap or
  // engines.
  crypto.getFips = function getFips() {
    return 0;
  };

  crypto.secureHeapUsed = function secureHeapUsed() {
    return { total: 0, used: 0, utilization: 0, min: 0 };
  };

  crypto.setEngine = function setEngine(id, flags) {
    if (typeof id !== 'string') throw invalidArgType('id', 'of type string', id);
    if (flags !== undefined) validateNumber(flags, 'flags');
    throw codedError(Error, 'ERR_CRYPTO_ENGINE_UNKNOWN', 'Engine "' + id + '" was not found');
  };

  // ---- Certificate (SPKAC) ----------------------------------------------------
  function spkacBase64(spkac, encoding) {
    let bytes;
    if (typeof spkac === 'string') {
      bytes = Buffer.from(spkac, encoding || 'utf8');
    } else if (isAnyArrayBuffer(spkac) || ArrayBuffer.isView(spkac)) {
      bytes = Buffer.from(bytesOf(spkac));
    } else {
      throw invalidArgType('spkac', 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', spkac);
    }
    return bytes.length === 0 ? null : bytes.toString('base64');
  }

  function exportChallenge(spkac, encoding) {
    const input = spkacBase64(spkac, encoding);
    if (input === null) return '';
    const out = crypto._spkac('challenge', input);
    return out === '!' ? '' : Buffer.from(out, 'base64');
  }

  function exportPublicKey(spkac, encoding) {
    const input = spkacBase64(spkac, encoding);
    if (input === null) return '';
    const out = crypto._spkac('publicKey', input);
    return out === '' ? '' : Buffer.from(out, 'base64');
  }

  function verifySpkac(spkac, encoding) {
    const input = spkacBase64(spkac, encoding);
    if (input === null) return '';
    return crypto._spkac('verify', input) === 'true';
  }

  function Certificate() {
    if (!(this instanceof Certificate)) return new Certificate();
  }
  Certificate.prototype.verifySpkac = verifySpkac;
  Certificate.prototype.exportPublicKey = exportPublicKey;
  Certificate.prototype.exportChallenge = exportChallenge;
  Certificate.exportChallenge = exportChallenge;
  Certificate.exportPublicKey = exportPublicKey;
  Certificate.verifySpkac = verifySpkac;
  crypto.Certificate = Certificate;
})();
