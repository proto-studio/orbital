// crypto.randomBytes: Node's argument checks, with the callback form filled
// off the JS thread by the native _randomBytesAsync (see async.go).
(function () {
  'use strict';

  const crypto = __crypto_module;
  const nativeRandomBytes = crypto.randomBytes;
  const kMaxLength = 2147483647;

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

  function assertSize(size) {
    if (typeof size !== 'number') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "size" argument must be of type number. ' + describeReceived(size));
    }
    if (!(size >= 0 && size <= kMaxLength)) {
      throw codedError(RangeError, 'ERR_OUT_OF_RANGE',
        'The value of "size" is out of range. It must be >= 0 && <= ' + kMaxLength + '. Received ' + size);
    }
    return Math.floor(size);
  }

  crypto.randomBytes = function randomBytes(size, callback) {
    size = assertSize(size);
    if (callback === undefined) {
      return nativeRandomBytes(size);
    }
    if (typeof callback !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "callback" argument must be of type function. ' + describeReceived(callback));
    }
    crypto._randomBytesAsync(size, function (message, data) {
      if (message !== undefined) {
        callback(new Error(message));
        return;
      }
      callback(null, Buffer.from(data, 'base64'));
    });
  };
})();
