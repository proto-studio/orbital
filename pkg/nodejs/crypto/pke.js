// publicEncrypt, privateDecrypt, privateEncrypt and publicDecrypt over
// _rsaCrypt (pke.go), with Node 20's options, defaults and errors.
(function () {
  'use strict';

  const crypto = __crypto_module;
  const RSA_PKCS1_PADDING = 1;
  const RSA_PKCS1_OAEP_PADDING = 4;

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

  function viewBytes(view) {
    return Buffer.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
  }

  function bufferArg(value, name, encoding) {
    if (typeof value === 'string') return Buffer.from(value, Buffer.isEncoding(encoding) ? encoding : 'utf8');
    if (ArrayBuffer.isView(value)) return viewBytes(value);
    if (value instanceof ArrayBuffer) return Buffer.from(new Uint8Array(value));
    throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', value);
  }

  function keyFor(input, wantPrivate) {
    const KeyObject = crypto.KeyObject;
    // A KeyObject or WebCrypto CryptoKey, directly or as options.key.
    let key = input instanceof KeyObject ? input
      : (input && typeof input === 'object' && input.key instanceof KeyObject ? input.key : null);
    if (!key) key = crypto._fromCryptoKey(input) || (input && typeof input === 'object' ? crypto._fromCryptoKey(input.key) : null);
    if (!key) {
      try {
        key = wantPrivate ? crypto.createPrivateKey(input) : crypto.createPublicKey(input);
      } catch (e) {
        if (e && e.code) throw e;
        // The public operations take a private key too.
        let fallback;
        if (!wantPrivate) {
          try {
            fallback = crypto.createPrivateKey(input);
          } catch (e2) {
            // fall through to Node's error
          }
        }
        if (!fallback) throw codedError(Error, 'ERR_OSSL_UNSUPPORTED', 'error:1E08010C:DECODER routines::unsupported');
        key = fallback;
      }
    }
    if (wantPrivate && key.type !== 'private') {
      throw codedError(TypeError, 'ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE',
        'Invalid key object type ' + key.type + ', expected private.');
    }
    return key;
  }

  function operation(op, wantPrivate, defaultPadding) {
    return function (options, buffer) {
      const opts = options && typeof options === 'object' && !ArrayBuffer.isView(options) ? options : {};
      const key = keyFor(options, wantPrivate);
      const padding = opts.padding === undefined ? defaultPadding : opts.padding;
      let oaepHash = 'sha1';
      if (opts.oaepHash !== undefined) {
        if (typeof opts.oaepHash !== 'string') throw invalidArgType('key.oaepHash', 'of type string', opts.oaepHash);
        oaepHash = opts.oaepHash;
      }
      let label = Buffer.alloc(0);
      if (opts.oaepLabel !== undefined) label = bufferArg(opts.oaepLabel, 'key.oaepLabel', opts.encoding);
      const data = bufferArg(buffer, 'buffer', opts.encoding);
      if (op === 'privateDecrypt' && padding === RSA_PKCS1_PADDING) {
        throw codedError(TypeError, 'ERR_INVALID_ARG_VALUE',
          'RSA_PKCS1_PADDING is no longer supported for private decryption, this can be reverted with --security-revert=CVE-2023-46809');
      }
      let out;
      try {
        out = crypto._rsaCrypt(op, crypto._keyHandle(key), data.toString('base64'), padding, oaepHash, label.toString('base64'));
      } catch (e) {
        const text = String(e && e.message !== undefined ? e.message : e);
        const bar = text.indexOf('|');
        if (bar < 0) throw e;
        throw codedError(Error, text.slice(0, bar), text.slice(bar + 1));
      }
      return Buffer.from(out, 'base64');
    };
  }

  crypto.publicEncrypt = operation('publicEncrypt', false, RSA_PKCS1_OAEP_PADDING);
  crypto.privateDecrypt = operation('privateDecrypt', true, RSA_PKCS1_OAEP_PADDING);
  crypto.privateEncrypt = operation('privateEncrypt', true, RSA_PKCS1_PADDING);
  crypto.publicDecrypt = operation('publicDecrypt', false, RSA_PKCS1_PADDING);
})();
