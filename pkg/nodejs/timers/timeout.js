// Wraps the native timer functions in Node's Timeout and Immediate objects.
(function(global) {
  'use strict';

  const nativeSetTimeout = global.setTimeout;
  const nativeSetInterval = global.setInterval;
  const nativeSetImmediate = global.setImmediate;
  const nativeClear = global.clearTimeout;
  const setRef = global.__timer_set_ref;
  delete global.__timer_set_ref;

  function invalidCallback(callback) {
    if (typeof callback !== 'function') {
      const err = new TypeError('The "callback" argument must be of type function. Received ' + String(callback));
      err.code = 'ERR_INVALID_ARG_TYPE';
      throw err;
    }
  }

  class Timeout {
    constructor(callback, delay, args, repeat) {
      this._callback = callback;
      this._args = args;
      this._delay = delay;
      this._repeat = repeat;
      this._ref = true;
      this._destroyed = false;
      this._start();
    }

    _start() {
      const fire = () => {
        if (!this._repeat) this._destroyed = true;
        this._callback.apply(this, this._args);
      };
      this._id = (this._repeat ? nativeSetInterval : nativeSetTimeout)(fire, this._delay);
      if (!this._ref) setRef(this._id, false);
    }

    ref() {
      this._ref = true;
      if (!this._destroyed) setRef(this._id, true);
      return this;
    }

    unref() {
      this._ref = false;
      if (!this._destroyed) setRef(this._id, false);
      return this;
    }

    hasRef() {
      return this._ref;
    }

    refresh() {
      if (this._destroyed && this._repeat) return this;
      nativeClear(this._id);
      this._destroyed = false;
      this._start();
      return this;
    }

    close() {
      this._destroyed = true;
      nativeClear(this._id);
      return this;
    }

    [Symbol.toPrimitive]() {
      return this._id;
    }
  }

  class Immediate {
    constructor(callback, args) {
      this._ref = true;
      this._id = nativeSetImmediate(() => callback.apply(this, args));
    }

    ref() {
      this._ref = true;
      setRef(this._id, true);
      return this;
    }

    unref() {
      this._ref = false;
      setRef(this._id, false);
      return this;
    }

    hasRef() {
      return this._ref;
    }

    [Symbol.toPrimitive]() {
      return this._id;
    }
  }

  function clear(timer) {
    if (timer === undefined || timer === null) return;
    if (timer instanceof Timeout) {
      timer.close();
    } else if (timer instanceof Immediate) {
      nativeClear(timer._id);
    } else {
      nativeClear(Number(timer));
    }
  }

  global.setTimeout = function setTimeout(callback, delay, ...args) {
    invalidCallback(callback);
    const ms = Number(delay);
    return new Timeout(callback, ms >= 1 && ms <= 2147483647 ? Math.trunc(ms) : 1, args, false);
  };
  global.setInterval = function setInterval(callback, delay, ...args) {
    invalidCallback(callback);
    const ms = Number(delay);
    return new Timeout(callback, ms >= 1 && ms <= 2147483647 ? Math.trunc(ms) : 1, args, true);
  };
  global.setImmediate = function setImmediate(callback, ...args) {
    invalidCallback(callback);
    return new Immediate(callback, args);
  };
  global.clearTimeout = clear;
  global.clearInterval = clear;
  global.clearImmediate = clear;
})(globalThis);
