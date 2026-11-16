// domain - deprecated error handling, following Node's lib/domain.js.
//
// A domain follows the work started inside it: timers, immediates, process.nextTick
// callbacks and event emitters created while it is active run with it active again, and an exception none of them catches goes to the domain's 'error'
// listeners. Node does this through async_hooks; here the schedulers are wrapped when the
// runtime starts (doing nothing while no domain is active), and EventEmitter and the
// uncaught exception path are hooked the first time a domain is created.
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module.EventEmitter;
  const apply = Reflect.apply;

  // The domains entered and not yet exited, innermost last.
  let stack = [];

  const exports = {
    active: null,
    create,
    createDomain: create,
    Domain: null,
    _stack: stack,
  };

  function getProcess() {
    const p = global.process;
    return p && typeof p === 'object' ? p : null;
  }

  function setActive(domain) {
    exports.active = domain;
    const p = getProcess();
    if (p) p.domain = domain;
  }

  let captureSet = null;

  function codedError(Ctor, code, message) {
    const err = new Ctor(message);
    err.code = code;
    return err;
  }

  // The uncaught exception capture callback is set while some domain on the stack listens
  // for 'error', and points at the active domain's handler.
  function updateExceptionCapture() {
    if (captureSet === null) return;
    captureSet(null);
    if (!stack.every((domain) => domain.listenerCount('error') === 0)) {
      captureSet((er) => getProcess().domain._errorHandler(er));
    }
  }

  // Bind fn to the domain active when it was scheduled, so it runs with that domain
  // entered. An exception leaves the domain entered, for the domain's handler to find; the
  // stack is cleared once the exception has been handled, either way.
  function bindToActive(fn) {
    const domain = exports.active;
    if (!domain || typeof fn !== 'function') return fn;
    return function domainBound(...args) {
      domain.enter();
      const ret = apply(fn, this, args);
      domain.exit();
      return ret;
    };
  }

  function wrapScheduler(owner, name, callbackIndex) {
    const original = owner[name];
    if (typeof original !== 'function') return;
    const wrapped = function(...args) {
      if (exports.active && typeof args[callbackIndex] === 'function') {
        args[callbackIndex] = bindToActive(args[callbackIndex]);
      }
      return apply(original, this, args);
    };
    // Keep what the original carries, such as util.promisify.custom.
    for (const key of Reflect.ownKeys(original)) {
      if (key === 'length' || key === 'name' || key === 'prototype') continue;
      const desc = Object.getOwnPropertyDescriptor(original, key);
      if (desc) Object.defineProperty(wrapped, key, desc);
    }
    Object.defineProperty(wrapped, 'name', { value: original.name, configurable: true });
    owner[name] = wrapped;
  }

  // Promise reactions registered while a domain is active run inside it. If one throws, or
  // returns a promise that rejects, the rejection that follows is attributed to the domain
  // and reported to its 'error' listeners rather than as an unhandledRejection. catch() and finally() go through then(). Node follows promises with
  // a V8 promise hook, which also covers await; here await is not followed. then() is only
  // replaced once a domain is created, so programs without domains keep the native one.
  const promiseThen = Promise.prototype.then;
  // The domain a rejection belongs to, by reason, for rejections that happen after the
  // reaction returned: the promise a throwing reaction rejects, or the one adopting a
  // rejected promise the reaction returned. The rejection tracker asks for it.
  const rejectionDomains = new WeakMap();
  let primitiveRejection = null;

  function noteRejection(domain, reason) {
    if ((typeof reason === 'object' && reason !== null) || typeof reason === 'function') {
      rejectionDomains.set(reason, domain);
    } else {
      primitiveRejection = { domain, reason };
    }
  }

  exports._takeRejectionDomain = function(reason) {
    if ((typeof reason === 'object' && reason !== null) || typeof reason === 'function') {
      const domain = rejectionDomains.get(reason);
      if (domain) rejectionDomains.delete(reason);
      return domain || null;
    }
    if (primitiveRejection !== null && Object.is(primitiveRejection.reason, reason)) {
      const domain = primitiveRejection.domain;
      primitiveRejection = null;
      return domain;
    }
    return null;
  };

  function bindReaction(domain, fn) {
    if (typeof fn !== 'function') return fn;
    return function domainReaction(value) {
      domain.enter();
      try {
        const ret = apply(fn, this, [value]);
        if (ret instanceof Promise) {
          apply(promiseThen, ret, [undefined, (reason) => noteRejection(domain, reason)]);
        }
        return ret;
      } catch (err) {
        noteRejection(domain, err);
        throw err;
      } finally {
        // The domain ends with the reaction, so later jobs run outside it.
        domain.exit();
      }
    };
  }
  function installPromises() {
    Object.defineProperty(Promise.prototype, 'then', {
      value: {
        then(onFulfilled, onRejected) {
          const domain = exports.active;
          if (domain) {
            onFulfilled = bindReaction(domain, onFulfilled);
            onRejected = bindReaction(domain, onRejected);
          }
          return apply(promiseThen, this, [onFulfilled, onRejected]);
        },
      }.then,
      writable: true,
      enumerable: false,
      configurable: true,
    });
  }

  let installed = false;

  function install() {
    if (installed) return;
    installed = true;
    const p = getProcess();

    // Node refuses domains alongside a capture callback the program set itself, and once
    // domains are in use the program can no longer set one.
    if (p && typeof p.setUncaughtExceptionCaptureCallback === 'function') {
      if (typeof p.hasUncaughtExceptionCaptureCallback === 'function' &&
          p.hasUncaughtExceptionCaptureCallback()) {
        installed = false;
        throw codedError(Error, 'ERR_DOMAIN_CALLBACK_NOT_AVAILABLE',
          'A callback was registered through process.setUncaughtExceptionCaptureCallback(), ' +
          'which is mutually exclusive with using the `domain` module');
      }
      const original = p.setUncaughtExceptionCaptureCallback;
      captureSet = (fn) => apply(original, p, [fn]);
      p.setUncaughtExceptionCaptureCallback = function setUncaughtExceptionCaptureCallback() {
        throw codedError(Error, 'ERR_DOMAIN_CANNOT_SET_UNCAUGHT_EXCEPTION_CAPTURE',
          'The `domain` module is in use, which is mutually exclusive with calling ' +
          'process.setUncaughtExceptionCaptureCallback()');
      };
    }
    if (p) p.domain = null;

    // An exception no domain took ends the current tick, so process 'uncaughtException'
    // listeners run with no domain entered, as in Node.
    if (p && typeof p.emit === 'function') {
      const processEmit = p.emit;
      p.emit = function emit(type, ...args) {
        if (type === 'uncaughtException' && stack.length > 0) domainUncaughtExceptionClear();
        return apply(processEmit, this, [type, ...args]);
      };
    }


    installPromises();

    // Emitters created while a domain is active belong to it.
    EventEmitter.usingDomains = true;
    const eventInit = EventEmitter.init;
    EventEmitter.init = function init(...args) {
      this.domain = null;
      if (exports.active && !(this instanceof Domain)) this.domain = exports.active;
      return apply(eventInit, this, args);
    };

    const eventEmit = EventEmitter.prototype.emit;
    EventEmitter.prototype.emit = function emit(...args) {
      const domain = this.domain;
      const type = args[0];
      const shouldEmitError = type === 'error' && this.listenerCount(type) > 0;
      if (shouldEmitError || domain === null || domain === undefined) {
        return apply(eventEmit, this, args);
      }
      if (type === 'error') {
        const er = args.length > 1 && args[1] ? args[1] :
          codedError(Error, 'ERR_UNHANDLED_ERROR', 'Unhandled error.');
        if (typeof er === 'object') {
          er.domainEmitter = this;
          Object.defineProperty(er, 'domain', { configurable: true, enumerable: false, value: domain, writable: true });
          er.domainThrown = false;
        }
        // The domain's handler does not run in its own context, so take the active
        // domain and its duplicates off the stack while it runs.
        const origStack = stack.slice();
        const origActive = exports.active;
        let idx = stack.length - 1;
        while (idx > -1 && exports.active === stack[idx]) --idx;
        stack.length = idx < 0 ? 0 : idx + 1;
        setActive(stack.length > 0 ? stack[stack.length - 1] : null);
        updateExceptionCapture();
        domain.emit('error', er);
        stack.length = 0;
        for (const d of origStack) stack.push(d);
        setActive(origActive);
        updateExceptionCapture();
        return false;
      }
      domain.enter();
      const ret = apply(eventEmit, this, args);
      domain.exit();
      return ret;
    };
  }

  // Uncaught exceptions end the current tick, so no domain is left on the stack.
  function domainUncaughtExceptionClear() {
    stack.length = 0;
    setActive(null);
    updateExceptionCapture();
  }

  const queueUpdate = typeof global.queueMicrotask === 'function'
    ? global.queueMicrotask
    : (fn) => Promise.resolve().then(fn);

  class Domain extends EventEmitter {
    constructor() {
      install();
      super();
      this.members = [];
      // Adding or removing an 'error' listener changes whether exceptions are captured;
      // 'newListener' comes before the listener is added, so look again afterwards.
      this.on('removeListener', updateExceptionCapture);
      this.on('newListener', () => queueUpdate(updateExceptionCapture));
    }

    add(ee) {
      if (ee.domain === this) return;
      if (ee.domain) ee.domain.remove(ee);
      if (this.domain && (ee instanceof Domain)) {
        for (let d = this.domain; d; d = d.domain) {
          if (ee === d) return;
        }
      }
      Object.defineProperty(ee, 'domain', { configurable: true, enumerable: false, value: this, writable: true });
      this.members.push(ee);
    }

    // This EventEmitter does not report each listener removeAllListeners() drops, so the
    // capture callback is looked at again here.
    removeAllListeners(...args) {
      const ret = super.removeAllListeners(...args);
      updateExceptionCapture();
      return ret;
    }

    remove(ee) {
      ee.domain = null;
      const index = this.members.indexOf(ee);
      if (index !== -1) this.members.splice(index, 1);
    }

    run(fn, ...args) {
      // As in Node, an exception leaves the domain entered and reaches the caller, or
      // the uncaught exception handler and from there this domain.
      this.enter();
      const ret = apply(fn, this, args);
      this.exit();
      return ret;
    }

    bind(cb) {
      if (typeof cb !== 'function') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "cb" argument must be of type function.');
      }
      const self = this;
      function runBound(...args) {
        self.enter();
        const ret = apply(cb, this, args);
        self.exit();
        return ret;
      }
      Object.defineProperty(runBound, 'domain', { configurable: true, enumerable: false, value: this, writable: true });
      return runBound;
    }

    intercept(cb) {
      if (typeof cb !== 'function') {
        throw codedError(TypeError, 'ERR_INVALID_ARG_TYPE', 'The "cb" argument must be of type function.');
      }
      const self = this;
      function runIntercepted(...args) {
        if (args[0] && args[0] instanceof Error) {
          const er = args[0];
          er.domainBound = cb;
          er.domainThrown = false;
          Object.defineProperty(er, 'domain', { configurable: true, enumerable: false, value: self, writable: true });
          self.emit('error', er);
          return undefined;
        }
        self.enter();
        const ret = apply(cb, this, args.slice(1));
        self.exit();
        return ret;
      }
      Object.defineProperty(runIntercepted, 'domain', { configurable: true, enumerable: false, value: this, writable: true });
      return runIntercepted;
    }

    enter() {
      setActive(this);
      stack.push(this);
      updateExceptionCapture();
    }

    exit() {
      const index = stack.lastIndexOf(this);
      if (index === -1) return;
      stack.splice(index);
      setActive(stack.length === 0 ? undefined : stack[stack.length - 1]);
      updateExceptionCapture();
    }

    _errorHandler(er) {
      let caught = false;
      if ((typeof er === 'object' && er !== null) || typeof er === 'function') {
        Object.defineProperty(er, 'domain', { configurable: true, enumerable: false, value: this, writable: true });
        er.domainThrown = true;
      }
      // The handler does not run inside this domain, so an exception it throws cannot
      // come back to it.
      while (exports.active === this) this.exit();
      if (stack.length === 0) {
        if (this.listenerCount('error') > 0) {
          if (captureSet !== null) captureSet(null);
          try {
            caught = this.emit('error', er);
          } finally {
            updateExceptionCapture();
          }
        }
      } else {
        try {
          caught = this.emit('error', er);
        } catch (er2) {
          // The handler threw: let an outer domain have it, or else fail with it.
          updateExceptionCapture();
          if (stack.length) {
            setActive(stack[stack.length - 1]);
            caught = exports.active._errorHandler(er2);
          } else {
            throw er2;
          }
        }
      }
      domainUncaughtExceptionClear();
      return caught;
    }
  }

  exports.Domain = Domain;

  // The schedulers are wrapped now, before any program code runs, so a reference a program
  // keeps (const { setTimeout } = require('timers')) still follows domains created later,
  // as Node's async hooks do. The wrappers only bind while a domain is active.
  wrapScheduler(global, 'setTimeout', 0);
  wrapScheduler(global, 'setInterval', 0);
  wrapScheduler(global, 'setImmediate', 0);
  if (getProcess()) wrapScheduler(getProcess(), 'nextTick', 0);


  function create() {
    return new Domain();
  }

  global.__domain_module = exports;
})(globalThis);
