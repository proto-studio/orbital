// worker_threads - JS surface over the native bridges in worker_threads.go.
//
// Parent side: the Worker class drives __wt_spawn/__wt_post/__wt_terminate and
// receives events through the dispatcher registered with __wt_set_dispatcher.
// Worker side: __wt_init_worker (called from Go after the worker runtime is up)
// installs parentPort/workerData and the inbound delivery hook.
//
// Messages are structured-cloned via JSON at the boundary (see the Go side).
(function(global) {
  'use strict';

  const EventEmitter = global.__events_module;

  class MessagePort extends EventEmitter {
    constructor() {
      super();
      this._onmessage = null;
    }
    postMessage() {}
    start() {}
    close() {}
    ref() {
      return this;
    }
    unref() {
      return this;
    }
  }

  class MessageChannel {
    constructor() {
      this.port1 = new MessagePort();
      this.port2 = new MessagePort();
      // Local, same-thread channel: deliver each port's messages to the other.
      wireLocalPair(this.port1, this.port2);
    }
  }

  const soon =
    typeof global.queueMicrotask === 'function'
      ? global.queueMicrotask
      : function(fn) { Promise.resolve().then(fn); };

  function wireLocalPair(a, b) {
    a.postMessage = function(value) {
      soon(() => b.emit('message', structuredCloneJSON(value)));
    };
    b.postMessage = function(value) {
      soon(() => a.emit('message', structuredCloneJSON(value)));
    };
  }

  function structuredCloneJSON(value) {
    if (value === undefined) return undefined;
    try {
      return JSON.parse(JSON.stringify(value));
    } catch (e) {
      return value;
    }
  }

  // Node 21+: marks a value uncloneable for structuredClone. We have nothing to
  // enforce, so this is a no-op returning the value.
  function markAsUncloneable(value) {
    return value;
  }

  // ---- parent side: Worker class ------------------------------------------

  const workersById = new Map();
  let Worker;

  if (typeof global.__wt_spawn === 'function') {
    Worker = class Worker extends EventEmitter {
      constructor(filename, options) {
        super();
        options = options || {};
        const isEval = !!options.eval;
        let target = filename;
        if (!isEval) {
          try {
            target = require('path').resolve(String(filename));
          } catch (e) {
            target = String(filename);
          }
        }
        const dataJson = JSON.stringify(
          options.workerData === undefined ? null : options.workerData
        );
        const id = global.__wt_spawn(String(target), dataJson, isEval);
        this._id = id;
        this.threadId = id;
        workersById.set(id, this);
      }

      postMessage(value) {
        global.__wt_post(
          this._id,
          JSON.stringify(value === undefined ? null : value)
        );
      }

      terminate() {
        global.__wt_terminate(this._id);
        return Promise.resolve(0);
      }

      ref() {
        return this;
      }

      unref() {
        return this;
      }
    };

    global.__wt_set_dispatcher(function(id, kind, data, code) {
      const w = workersById.get(id);
      if (!w) return;
      switch (kind) {
        case 'online':
          w.emit('online');
          break;
        case 'message': {
          let v;
          try {
            v = JSON.parse(data);
          } catch (e) {
            v = data;
          }
          w.emit('message', v);
          break;
        }
        case 'error':
          w.emit('error', new Error(data));
          break;
        case 'exit':
          workersById.delete(id);
          w.emit('exit', code);
          break;
      }
    });
  } else {
    Worker = class Worker extends EventEmitter {
      constructor() {
        super();
        throw new Error('worker_threads.Worker is not available in this runtime');
      }
    };
  }

  // ---- worker side: parentPort / workerData -------------------------------
  // Called from Go (worker runtime) once the native worker bridges are set up.
  global.__wt_init_worker = function(dataJson) {
    const mod = global.__worker_threads_module;
    if (!mod) return;
    mod.isMainThread = false;
    mod.threadId = 1;
    try {
      mod.workerData = JSON.parse(dataJson);
    } catch (e) {
      mod.workerData = null;
    }

    const port = new MessagePort();
    let refCount = 0;
    function updateRef() {
      const n = port.listenerCount ? port.listenerCount('message') : 0;
      if (n > 0 && refCount === 0) {
        refCount = 1;
        if (typeof global.__wt_worker_ref === 'function') global.__wt_worker_ref(true);
      } else if (n === 0 && refCount === 1) {
        refCount = 0;
        if (typeof global.__wt_worker_ref === 'function') global.__wt_worker_ref(false);
      }
    }

    // Wrap listener mutators so the worker stays alive exactly while it has a
    // 'message' handler (matching Node's ref semantics for parentPort).
    ['on', 'once', 'addListener', 'prependListener', 'prependOnceListener',
     'removeListener', 'off', 'removeAllListeners'].forEach(function(m) {
      const orig = port[m];
      if (typeof orig !== 'function') return;
      port[m] = function() {
        const r = orig.apply(this, arguments);
        updateRef();
        return r;
      };
    });

    port.postMessage = function(value) {
      if (typeof global.__wt_worker_send === 'function') {
        global.__wt_worker_send(JSON.stringify(value === undefined ? null : value));
      }
    };
    port.close = function() {
      if (refCount === 1 && typeof global.__wt_worker_ref === 'function') {
        refCount = 0;
        global.__wt_worker_ref(false);
      }
    };

    mod.parentPort = port;

    // Inbound delivery hook invoked from Go on the worker event loop.
    global.__wt_deliver = function(str) {
      let v;
      try {
        v = JSON.parse(str);
      } catch (e) {
        v = str;
      }
      port.emit('message', v);
    };
  };

  const worker_threads = {
    isMainThread: true,
    threadId: 0,
    parentPort: null,
    workerData: null,
    resourceLimits: {},
    MessagePort,
    MessageChannel,
    Worker,
    markAsUncloneable,
    BroadcastChannel:
      typeof global.BroadcastChannel === 'function'
        ? global.BroadcastChannel
        : undefined,
    setEnvironmentData() {},
    getEnvironmentData() {
      return undefined;
    }
  };

  global.__worker_threads_module = worker_threads;
})(globalThis);
