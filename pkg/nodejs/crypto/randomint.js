// crypto.randomInt: argument handling and errors as in Node 20, with the
// number itself drawn uniformly by the native _randomInt (see randomint.go).
(function () {
  'use strict';

  const crypto = __crypto_module;
  // Node's limit on max - min: the largest value 6 random bytes can hold.
  const RAND_MAX = 0xFFFFFFFFFFFF;

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

  function rangeError(name, range, value) {
    return codedError(RangeError, 'ERR_OUT_OF_RANGE',
      'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + formatRangeValue(value));
  }

  function defer(fn, a, b) {
    if (typeof process !== 'undefined' && process && typeof process.nextTick === 'function') {
      process.nextTick(fn, a, b);
    } else {
      queueMicrotask(function () { fn(a, b); });
    }
  }

  crypto.randomInt = function randomInt(min, max, callback) {
    const minNotSpecified = typeof max === 'undefined' || typeof max === 'function';
    if (minNotSpecified) {
      callback = max;
      max = min;
      min = 0;
    }
    const isSync = typeof callback === 'undefined';
    if (!isSync && typeof callback !== 'function') {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "callback" argument must be of type function. ' + describeReceived(callback));
    }
    if (!Number.isSafeInteger(min)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "min" argument must be a safe integer. ' + describeReceived(min));
    }
    if (!Number.isSafeInteger(max)) {
      throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE',
        'The "max" argument must be a safe integer. ' + describeReceived(max));
    }
    if (max <= min) {
      throw rangeError('max', 'greater than the value of "min" (' + min + ')', max);
    }
    const range = max - min;
    if (!(range <= RAND_MAX)) {
      throw rangeError(minNotSpecified ? 'max' : 'max - min', '<= ' + RAND_MAX, range);
    }
    const n = min + crypto._randomInt(range);
    if (isSync) return n;
    defer(callback, undefined, n);
  };
})();
