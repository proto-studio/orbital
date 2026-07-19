// async_hooks - real core implementation.
//
// AsyncLocalStorage, AsyncResource and createHook are implemented here and the
// runtime itself propagates context across the async boundaries it owns (see
// wrapScheduler below). This is core runtime behavior installed at bootstrap,
// not a user-space polyfill.
//
// Known gap: context does NOT yet cross a native `await` / `.then()`
// continuation, because those run in V8's internal microtask queue which the Go
// event loop never sees. Hooking them correctly needs V8's SetPromiseHook,
// exposed through the cgo glue (built on CI). We deliberately do not patch
// Promise.prototype.then to fake it. See docs/async-context.md.
(function(global) {
  'use strict';

  // The active context: a Map of AsyncLocalStorage instance -> stored value.
  // Frames are immutable snapshots; run()/enterWith() install a new Map so a
  // captured frame keeps referring to the value that was current when captured.
  let currentStore = new Map();
  let nextAsyncId = 1;
  let currentAsyncId = 1;
  const hooks = [];

  function captureFrame() {
    return currentStore;
  }

  function runInFrame(frame, fn, thisArg, args) {
    const prev = currentStore;
    currentStore = frame;
    try {
      return fn.apply(thisArg, args);
    } finally {
      currentStore = prev;
    }
  }

  function fire(name, a, b, c, d) {
    for (let i = 0; i < hooks.length; i++) {
      const cb = hooks[i][name];
      if (typeof cb === 'function') {
        try {
          cb(a, b, c, d);
        } catch (e) {
          // async_hooks callbacks must never throw into user code.
        }
      }
    }
  }

  class AsyncLocalStorage {
    getStore() {
      return currentStore.get(this);
    }

    run(store, callback, ...args) {
      const next = new Map(currentStore);
      next.set(this, store);
      return runInFrame(next, callback, undefined, args);
    }

    enterWith(store) {
      const next = new Map(currentStore);
      next.set(this, store);
      currentStore = next;
    }

    exit(callback, ...args) {
      const next = new Map(currentStore);
      next.delete(this);
      return runInFrame(next, callback, undefined, args);
    }

    disable() {
      currentStore.delete(this);
    }

    static bind(fn) {
      const frame = captureFrame();
      return function (...args) {
        return runInFrame(frame, fn, this, args);
      };
    }

    static snapshot() {
      const frame = captureFrame();
      return function (fn, ...args) {
        return runInFrame(frame, fn, undefined, args);
      };
    }
  }

  class AsyncResource {
    constructor(type, options) {
      this.type = type;
      this._asyncId = nextAsyncId++;
      this._triggerAsyncId =
        options && typeof options.triggerAsyncId === 'number'
          ? options.triggerAsyncId
          : currentAsyncId;
      // Capture the async context active at creation, so callbacks bound to
      // this resource later run with the right context.
      this._frame = captureFrame();
      this._destroyed = false;
      fire('init', this._asyncId, type, this._triggerAsyncId, this);
    }

    runInAsyncScope(fn, thisArg, ...args) {
      const prevId = currentAsyncId;
      currentAsyncId = this._asyncId;
      fire('before', this._asyncId);
      try {
        return runInFrame(this._frame, fn, thisArg, args);
      } finally {
        fire('after', this._asyncId);
        currentAsyncId = prevId;
      }
    }

    bind(fn, thisArg) {
      const res = this;
      const bound = function (...args) {
        return res.runInAsyncScope(
          fn,
          thisArg === undefined ? this : thisArg,
          ...args
        );
      };
      bound.asyncResource = this;
      return bound;
    }

    emitDestroy() {
      if (!this._destroyed) {
        this._destroyed = true;
        fire('destroy', this._asyncId);
      }
      return this;
    }

    asyncId() {
      return this._asyncId;
    }

    triggerAsyncId() {
      return this._triggerAsyncId;
    }

    static bind(fn, type, thisArg) {
      const res = new AsyncResource(type || 'bound-anonymous-fn');
      return res.bind(fn, thisArg);
    }
  }

  function createHook(callbacks) {
    callbacks = callbacks || {};
    return {
      init: callbacks.init,
      before: callbacks.before,
      after: callbacks.after,
      destroy: callbacks.destroy,
      promiseResolve: callbacks.promiseResolve,
      _enabled: false,
      enable() {
        if (!this._enabled) {
          this._enabled = true;
          hooks.push(this);
        }
        return this;
      },
      disable() {
        const i = hooks.indexOf(this);
        if (i >= 0) {
          hooks.splice(i, 1);
          this._enabled = false;
        }
        return this;
      }
    };
  }

  // Make an async-scheduling primitive context-aware: the callback runs with
  // the context that was active when it was scheduled.
  function wrapScheduler(obj, name) {
    const orig = obj[name];
    if (typeof orig !== 'function') return;
    const wrapper = function (callback, ...rest) {
      if (typeof callback !== 'function') {
        return orig.apply(this, arguments);
      }
      const frame = captureFrame();
      const wrapped = function (...cbArgs) {
        return runInFrame(frame, callback, this, cbArgs);
      };
      return orig.call(this, wrapped, ...rest);
    };
    for (const k of Object.keys(orig)) {
      try {
        wrapper[k] = orig[k];
      } catch (e) {
        // ignore non-copyable properties
      }
    }
    obj[name] = wrapper;
  }

  wrapScheduler(global, 'setTimeout');
  wrapScheduler(global, 'setInterval');
  wrapScheduler(global, 'setImmediate');
  wrapScheduler(global, 'queueMicrotask');
  if (global.process && typeof global.process.nextTick === 'function') {
    wrapScheduler(global.process, 'nextTick');
  }

  const async_hooks = {
    AsyncLocalStorage,
    AsyncResource,
    createHook,
    executionAsyncId() {
      return currentAsyncId;
    },
    triggerAsyncId() {
      return 0;
    },
    executionAsyncResource() {
      return Object.create(null);
    }
  };

  global.__async_hooks_module = async_hooks;
})(globalThis);
