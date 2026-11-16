// timers/promises, following Node 20.18.1's lib/timers/promises.js. It also gives
// setTimeout and setImmediate their util.promisify.custom versions, as lib/timers.js does.
(function(global) {
  'use strict';

  const customPromisify = Symbol.for('nodejs.util.promisify.custom');
  const kScheduler = Symbol('kScheduler');

  // The timer functions as timers.js defines them, before anything wraps the globals.
  const timerSetTimeout = global.setTimeout;
  const timerSetInterval = global.setInterval;
  const timerSetImmediate = global.setImmediate;
  const timerClear = global.clearTimeout;

  function inspect(value) {
    try {
      if (typeof global.require === 'function') return global.require('util').inspect(value);
    } catch (_) {
      // Fall back to String below.
    }
    return String(value);
  }

  function describeReceived(value) {
    if (value == null) return ' Received ' + value;
    if (typeof value === 'function') return ' Received function ' + value.name;
    if (typeof value === 'object') {
      if (value.constructor && value.constructor.name) {
        return ' Received an instance of ' + value.constructor.name;
      }
      return ' Received ' + inspect(value);
    }
    let shown = typeof value === 'symbol' ? String(value) : inspect(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return ' Received type ' + typeof value + ' (' + shown + ')';
  }

  // Node's ERR_INVALID_ARG_TYPE: a dotted name is a property, anything else an argument.
  function invalidArgType(name, expected, value) {
    const kind = name.includes('.') ? 'property' : 'argument';
    const err = new TypeError('The "' + name + '" ' + kind + ' must be ' + expected + '.' +
      describeReceived(value));
    err.code = 'ERR_INVALID_ARG_TYPE';
    return err;
  }

  function validateNumber(value, name) {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
  }

  function validateObject(value, name) {
    if (value === null || Array.isArray(value) || typeof value !== 'object') {
      throw invalidArgType(name, 'of type object', value);
    }
  }

  function validateAbortSignal(value, name) {
    if (value !== undefined && (value === null || typeof value !== 'object' || !('aborted' in value))) {
      throw invalidArgType(name, 'an instance of AbortSignal', value);
    }
  }

  function validateBoolean(value, name) {
    if (typeof value !== 'boolean') throw invalidArgType(name, 'of type boolean', value);
  }

  class AbortError extends Error {
    constructor(message = 'The operation was aborted', options = undefined) {
      super(message, options);
      this.code = 'ABORT_ERR';
      this.name = 'AbortError';
    }
  }

  function validateOptions(options) {
    validateObject(options, 'options');
    if (typeof options.signal !== 'undefined') validateAbortSignal(options.signal, 'options.signal');
    if (typeof options.ref !== 'undefined') validateBoolean(options.ref, 'options.ref');
  }

  // Runs schedule(resolve) to start the timer, rejecting with an AbortError and clearing the
  // timer if the signal aborts first.
  function cancellable(signal, schedule) {
    if (signal && signal.aborted) {
      return Promise.reject(new AbortError(undefined, { cause: signal.reason }));
    }
    let oncancel;
    const ret = new Promise((resolve, reject) => {
      let settled = false;
      const timer = schedule((value) => {
        settled = true;
        resolve(value);
      });
      if (signal) {
        oncancel = () => {
          if (settled) return;
          timerClear(timer);
          reject(new AbortError(undefined, { cause: signal.reason }));
        };
        signal.addEventListener('abort', oncancel);
      }
    });
    return oncancel !== undefined ?
      ret.finally(() => signal.removeEventListener('abort', oncancel)) : ret;
  }

  function setTimeout(after, value, options = {}) {
    try {
      if (typeof after !== 'undefined') validateNumber(after, 'delay');
      validateOptions(options);
    } catch (err) {
      return Promise.reject(err);
    }
    const { signal, ref = true } = options;
    return cancellable(signal, (resolve) => {
      const timer = timerSetTimeout(resolve, after, value);
      if (!ref) timer.unref();
      return timer;
    });
  }

  function setImmediate(value, options = {}) {
    try {
      validateOptions(options);
    } catch (err) {
      return Promise.reject(err);
    }
    const { signal, ref = true } = options;
    return cancellable(signal, (resolve) => {
      const immediate = timerSetImmediate(resolve, value);
      if (!ref) immediate.unref();
      return immediate;
    });
  }

  async function* setInterval(after, value, options = {}) {
    if (typeof after !== 'undefined') validateNumber(after, 'delay');
    validateOptions(options);
    const { signal, ref = true } = options;
    if (signal && signal.aborted) {
      throw new AbortError(undefined, { cause: signal.reason });
    }

    let onCancel;
    let interval;
    try {
      let notYielded = 0;
      let callback;
      interval = timerSetInterval(() => {
        notYielded++;
        if (callback) {
          callback();
          callback = undefined;
        }
      }, after);
      if (!ref) interval.unref();
      if (signal) {
        onCancel = () => {
          timerClear(interval);
          if (callback) {
            callback(Promise.reject(new AbortError(undefined, { cause: signal.reason })));
            callback = undefined;
          }
        };
        signal.addEventListener('abort', onCancel, { once: true });
      }

      while (!(signal && signal.aborted)) {
        if (notYielded === 0) {
          await new Promise((resolve) => { callback = resolve; });
        }
        for (; notYielded > 0; notYielded--) {
          yield value;
        }
      }
      throw new AbortError(undefined, { cause: signal && signal.reason });
    } finally {
      timerClear(interval);
      if (signal) signal.removeEventListener('abort', onCancel);
    }
  }

  function invalidThis() {
    const err = new TypeError('Value of "this" must be of type Scheduler');
    err.code = 'ERR_INVALID_THIS';
    return err;
  }

  class Scheduler {
    constructor() {
      const err = new TypeError('Illegal constructor');
      err.code = 'ERR_ILLEGAL_CONSTRUCTOR';
      throw err;
    }

    yield() {
      if (!this[kScheduler]) throw invalidThis();
      return setImmediate();
    }

    wait(delay, options) {
      if (!this[kScheduler]) throw invalidThis();
      return setTimeout(delay, undefined, options);
    }
  }

  const timersPromises = {
    setTimeout,
    setImmediate,
    setInterval,
    scheduler: Reflect.construct(function() {
      this[kScheduler] = true;
    }, [], Scheduler),
  };

  // util.promisify(setTimeout) and util.promisify(setImmediate) give these, as in Node.
  for (const [fn, custom] of [[global.setTimeout, setTimeout], [global.setImmediate, setImmediate]]) {
    Object.defineProperty(fn, customPromisify, {
      enumerable: true,
      configurable: true,
      get() {
        return custom;
      },
    });
  }

  // Export
  global.__timers_promises_module = timersPromises;

})(globalThis);
