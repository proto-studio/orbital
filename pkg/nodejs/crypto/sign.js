// createSign / createVerify (Sign and Verify writable streams) and the
// one-shot crypto.sign / crypto.verify, over _sign and _verify (sign.go).
// Keys, options and errors follow Node 20.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const { Writable } = globalThis.__stream_module || { Writable: function Writable() {} };
  const kData = Symbol('kSignData');
  const kDigest = Symbol('kSignDigest');
  const kDone = Symbol('kSignDone');

  const RSA_PKCS1_PADDING = 1;
  const RSA_PKCS1_PSS_PADDING = 6;
  const RSA_PSS_SALTLEN_AUTO = -2;

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

  function invalidArgValue(name, value) {
    const shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    return codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
      "The property '" + name + "' is invalid. Received " + shown);
  }

  // Rethrows a native "CODE|message" error the way Node throws it.
  function native(fn, ...args) {
    try {
      return fn(...args);
    } catch (e) {
      const text = String(e && e.message !== undefined ? e.message : e);
      const bar = text.indexOf('|');
      if (bar < 0) throw e;
      throw codedError(Error, text.slice(0, bar), text.slice(bar + 1));
    }
  }

  function viewBytes(view) {
    return Buffer.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
  }

  function bytesOf(value, name, encoding) {
    if (typeof value === 'string') return Buffer.from(value, Buffer.isEncoding(encoding) ? encoding : 'utf8');
    if (ArrayBuffer.isView(value)) return viewBytes(value);
    if (value instanceof ArrayBuffer) return Buffer.from(new Uint8Array(value));
    throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', value);
  }

  function intOption(options, name) {
    const value = options[name];
    if (value === undefined) return undefined;
    if (typeof value !== 'number' || !Number.isInteger(value) || value < -2147483648 || value > 2147483647) {
      throw invalidArgValue('options.' + name, value);
    }
    return value;
  }

  // The key and the padding, salt length and signature encoding that go with it.
  function resolveKey(input, wantPrivate) {
    const KeyObject = crypto.KeyObject;
    let key;
    let options = {};
    if (input instanceof KeyObject) {
      key = input;
    } else if (input && typeof input === 'object' && !ArrayBuffer.isView(input) && !(input instanceof ArrayBuffer)) {
      options = input;
      if (input.key instanceof KeyObject) key = input.key;
    }
    if (!key) {
      if (typeof input !== 'string' && !ArrayBuffer.isView(input) && !(input && typeof input === 'object')) {
        throw invalidArgType('key', 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, KeyObject, or CryptoKey', input);
      }
      try {
        key = wantPrivate ? crypto.createPrivateKey(input) : crypto.createPublicKey(input);
      } catch (e) {
        if (e && e.code) throw e;
        throw codedError(Error, 'ERR_OSSL_UNSUPPORTED', 'error:1E08010C:DECODER routines::unsupported');
      }
    }
    if (wantPrivate && key.type !== 'private') {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE',
        'Invalid key object type ' + key.type + ', expected private.');
    }
    if (!wantPrivate && key.type === 'secret') {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE',
        'Invalid key object type secret, expected public.');
    }
    if (!wantPrivate && key.type === 'private') key = crypto.createPublicKey(key);
    const padding = intOption(options, 'padding');
    const saltLength = intOption(options, 'saltLength');
    const dsaEncoding = options.dsaEncoding === undefined ? 'der' : options.dsaEncoding;
    if (dsaEncoding !== 'der' && dsaEncoding !== 'ieee-p1363') {
      throw invalidArgValue('options.dsaEncoding', dsaEncoding);
    }
    return {
      handle: crypto._keyHandle(key),
      padding: padding === undefined ? RSA_PKCS1_PADDING : padding,
      saltLength: saltLength === undefined ? RSA_PSS_SALTLEN_AUTO : saltLength,
      p1363: dsaEncoding === 'ieee-p1363',
    };
  }

  function checkDigest(algorithm) {
    if (typeof algorithm !== 'string') throw invalidArgType('algorithm', 'of type string', algorithm);
    if (!crypto._digestSize(algorithm)) throw codedError(TypeError, 'ERR_CRYPTO_INVALID_DIGEST', 'Invalid digest');
  }

  function notInitialised() {
    return codedError(Error, 'ERR_CRYPTO_INVALID_STATE', 'Not initialised');
  }

  function update(self, data, encoding) {
    if (self[kDone]) throw notInitialised();
    if (typeof data === 'string') {
      self[kData].push(Buffer.from(data, Buffer.isEncoding(encoding) ? encoding : 'utf8'));
    } else if (ArrayBuffer.isView(data)) {
      self[kData].push(viewBytes(data));
    } else {
      throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
    }
    return self;
  }

  function construct(Target, newTarget, algorithm, options) {
    checkDigest(algorithm);
    const self = Reflect.construct(Writable, [options], newTarget);
    self[kDigest] = algorithm;
    self[kData] = [];
    self[kDone] = false;
    return self;
  }

  function Sign(algorithm, options) {
    if (!new.target) return new Sign(algorithm, options);
    return construct(Sign, new.target, algorithm, options);
  }

  function Verify(algorithm, options) {
    if (!new.target) return new Verify(algorithm, options);
    return construct(Verify, new.target, algorithm, options);
  }

  for (const C of [Sign, Verify]) {
    Object.setPrototypeOf(C.prototype, Writable.prototype);
    Object.setPrototypeOf(C, Writable);
    Object.defineProperty(C.prototype, '_write', {
      value: function _write(chunk, encoding, callback) {
        try {
          update(this, chunk, encoding);
        } catch (e) {
          callback(e);
          return;
        }
        callback();
      },
      writable: true, configurable: true, enumerable: true,
    });
    Object.defineProperty(C.prototype, 'update', {
      value: function (data, encoding) { return update(this, data, encoding); },
      writable: true, configurable: true, enumerable: true,
    });
  }

  Object.defineProperty(Sign.prototype, 'sign', {
    value: function sign(privateKey, outputEncoding) {
      if (this[kDone]) throw notInitialised();
      const k = resolveKey(privateKey, true);
      const data = Buffer.concat(this[kData]).toString('base64');
      const sig = Buffer.from(native(crypto._sign, k.handle, data, this[kDigest], true, k.padding, k.saltLength, k.p1363), 'base64');
      this[kDone] = true;
      this[kData] = [];
      return outputEncoding && outputEncoding !== 'buffer' ? sig.toString(outputEncoding) : sig;
    },
    writable: true, configurable: true, enumerable: true,
  });

  Object.defineProperty(Verify.prototype, 'verify', {
    value: function verify(object, signature, sigEncoding) {
      if (this[kDone]) throw notInitialised();
      const k = resolveKey(object, false);
      const sig = bytesOf(signature, 'signature', sigEncoding).toString('base64');
      const data = Buffer.concat(this[kData]).toString('base64');
      const ok = native(crypto._verify, k.handle, data, sig, this[kDigest], true, k.padding, k.saltLength, k.p1363);
      this[kDone] = true;
      this[kData] = [];
      return ok;
    },
    writable: true, configurable: true, enumerable: true,
  });

  crypto.Sign = Sign;
  crypto.Verify = Verify;
  crypto.createSign = function createSign(algorithm, options) {
    return new Sign(algorithm, options);
  };
  crypto.createVerify = function createVerify(algorithm, options) {
    return new Verify(algorithm, options);
  };

  function oneShotAlgorithm(algorithm) {
    if (algorithm === null || algorithm === undefined) return '';
    if (typeof algorithm !== 'string') throw invalidArgType('algorithm', 'of type string', algorithm);
    return algorithm;
  }

  function later(callback, fn) {
    if (callback === undefined) return fn();
    if (typeof callback !== 'function') throw invalidArgType('callback', 'of type function', callback);
    let result;
    let error = null;
    try {
      result = fn();
    } catch (e) {
      error = e;
    }
    process.nextTick(() => (error ? callback(error) : callback(null, result)));
    return undefined;
  }

  crypto.sign = function sign(algorithm, data, key, callback) {
    const digest = oneShotAlgorithm(algorithm);
    const bytes = bytesOf(data, 'data');
    const k = resolveKey(key, true);
    return later(callback, () => Buffer.from(
      native(crypto._sign, k.handle, bytes.toString('base64'), digest, false, k.padding, k.saltLength, k.p1363), 'base64'));
  };

  crypto.verify = function verify(algorithm, data, key, signature, callback) {
    const digest = oneShotAlgorithm(algorithm);
    const bytes = bytesOf(data, 'data');
    const k = resolveKey(key, false);
    const sig = bytesOf(signature, 'signature');
    return later(callback, () => native(crypto._verify, k.handle, bytes.toString('base64'), sig.toString('base64'),
      digest, false, k.padding, k.saltLength, k.p1363));
  };

  Object.assign(crypto.constants, {
    RSA_PKCS1_PADDING: 1, RSA_NO_PADDING: 3, RSA_PKCS1_OAEP_PADDING: 4, RSA_X931_PADDING: 5,
    RSA_PKCS1_PSS_PADDING: 6, RSA_PSS_SALTLEN_DIGEST: -1, RSA_PSS_SALTLEN_MAX_SIGN: -2, RSA_PSS_SALTLEN_AUTO: -2,
    POINT_CONVERSION_COMPRESSED: 2, POINT_CONVERSION_UNCOMPRESSED: 4, POINT_CONVERSION_HYBRID: 6,
  });
})();
