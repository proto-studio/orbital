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

  // ---- thrown values ------------------------------------------------------
  // A worker's uncaught exception reaches the parent's 'error' event the way
  // Node's lib/internal/error_serdes.js sends it. An object whose prototype
  // chain has a built-in error constructor comes back as an object with that
  // prototype and every non-function property found along the chain, its cause
  // sent the same way; a symbol comes back through Symbol.for; an object with a
  // custom inspector comes back as its inspected text behind one; anything else
  // is cloned with V8's serializer, and what cannot be cloned arrives as its
  // util.inspect() text. The natives below are V8's ValueSerializer, with array
  // buffer views written as Node's serializer writes them: type and own bytes.

  const serializeNative = global.__wt_serialize;
  const deserializeNative = global.__wt_deserialize;
  delete global.__wt_serialize;
  delete global.__wt_deserialize;

  const apply = Reflect.apply;
  const objectToString = Object.prototype.toString;
  const getOwnDesc = Object.getOwnPropertyDescriptor;
  const getOwnNames = Object.getOwnPropertyNames;
  const getProto = Object.getPrototypeOf;
  const defineProperty = Object.defineProperty;
  const objectCreate = Object.create;
  const hasOwnProp = Object.prototype.hasOwnProperty;
  const symbolFor = Symbol.for;
  const stringSlice = String.prototype.slice;
  const errorCtors = {
    __proto__: null, Error, EvalError, RangeError, ReferenceError, SyntaxError, TypeError, URIError
  };
  const inspectCustom = Symbol.for('nodejs.util.inspect.custom');
  // Buffer.prototype, so a Buffer is sent and rebuilt as one; views otherwise keep their type.
  const bufferProto = global.Buffer ? global.Buffer.prototype : undefined;

  function serialize(value) {
    return serializeNative(value, bufferProto);
  }

  function deserialize(data) {
    return deserializeNative(data, bufferProto);
  }

  // The envelope's first character says what follows, as error_serdes' first byte does.
  const kSerializedError = 'E';
  const kSerializedObject = 'O';
  const kInspectedError = 'I';
  const kInspectedSymbol = 'S';
  const kCustomInspectedObject = 'C';

  // util is registered before this module, so its inspect is taken now, before any
  // program code could replace it.
  const utilInspect = global.__util_module && typeof global.__util_module.inspect === 'function'
    ? global.__util_module.inspect
    : null;

  function inspect(value) {
    if (utilInspect === null) return String(value);
    return apply(utilInspect, undefined, [value]);
  }

  function inspectOrString(value) {
    try {
      return inspect(value);
    } catch (e) {
      try {
        return String(value);
      } catch (e2) {
        return '[object could not be inspected]';
      }
    }
  }

  function tryGetAllProperties(object, target) {
    const all = { __proto__: null };
    if (object === null) return all;
    const inherited = tryGetAllProperties(getProto(object), target);
    for (const key of getOwnNames(inherited)) all[key] = inherited[key];
    for (const key of getOwnNames(object)) {
      let descriptor;
      try {
        descriptor = getOwnDesc(object, key);
      } catch (e) {
        continue;
      }
      const getter = descriptor.get;
      if (getter && key !== '__proto__') {
        try {
          descriptor.value = apply(getter, target, []);
        } catch (e) {
          // Continue regardless of error, as Node does.
        }
      }
      if (key === 'cause') {
        // A cause whose getter threw is left out, as Node leaves it out.
        if (!('value' in descriptor)) continue;
        descriptor.value = serializeThrown(descriptor.value);
        delete descriptor.get;
        delete descriptor.set;
        defineProperty(all, key, { value: descriptor, enumerable: true, writable: true, configurable: true });
      } else if ('value' in descriptor && typeof descriptor.value !== 'function' && typeof descriptor.value !== 'symbol') {
        delete descriptor.get;
        delete descriptor.set;
        defineProperty(all, key, { value: descriptor, enumerable: true, writable: true, configurable: true });
      }
    }
    return all;
  }

  function builtinErrorName(error) {
    for (let current = error; current !== null; current = getProto(current)) {
      const ctor = getOwnDesc(current, 'constructor');
      if (!ctor || !ctor.value) continue;
      const name = getOwnDesc(ctor.value, 'name');
      if (name && typeof name.value === 'string' && name.value in errorCtors) return name.value;
    }
    return null;
  }

  function serializeThrown(error) {
    if (typeof error === 'symbol') return kInspectedSymbol + inspectOrString(error);
    try {
      if (typeof error === 'object' && error !== null && apply(objectToString, error, []) === '[object Error]') {
        const ctor = builtinErrorName(error);
        if (ctor !== null) {
          return kSerializedError + serialize({ constructor: ctor, properties: tryGetAllProperties(error, error) });
        }
      }
    } catch (e) {
      // Try the next form.
    }
    try {
      if ((typeof error === 'object' && error !== null || typeof error === 'function') && inspectCustom in error) {
        return kCustomInspectedObject + inspect(error);
      }
    } catch (e) {
      // A custom inspector that throws: try cloning instead, as Node does.
    }
    try {
      return kSerializedObject + serialize(error);
    } catch (e) {
      // Fall back to the text.
    }
    return kInspectedError + inspectOrString(error);
  }

  function deserializeThrown(data) {
    const kind = data[0];
    const rest = apply(stringSlice, data, [1]);
    switch (kind) {
      case kSerializedError: {
        const { constructor, properties } = deserialize(rest);
        defineProperty(properties, Symbol.toStringTag, {
          value: { value: 'Error', configurable: true }, enumerable: true, writable: true, configurable: true,
        });
        if (apply(hasOwnProp, properties, ['cause']) && properties.cause) {
          properties.cause.value = deserializeThrown(properties.cause.value);
        }
        return objectCreate(errorCtors[constructor].prototype, properties);
      }
      case kSerializedObject:
        return deserialize(rest);
      case kInspectedSymbol:
        // inspect() gives "Symbol(description)".
        return symbolFor(apply(stringSlice, rest, ['Symbol('.length, -1]));
      case kCustomInspectedObject:
        return { __proto__: null, [inspectCustom]: () => rest };
    }
    return rest;
  }

  // ---- parent side: Worker class ------------------------------------------

  const workersById = new Map();
  let Worker;

  // resourceLimits, kept in Node's order, as its lib/internal/worker.js keeps them.
  const kMaxYoungGenerationSizeMb = 0;
  const kMaxOldGenerationSizeMb = 1;
  const kCodeRangeSizeMb = 2;
  const kStackSizeMb = 3;
  const kDefaultStackSizeMb = 4;

  // What the script asked for, as Node reads it: numbers only, as given except that the old
  // generation is at least 2 MiB, -1 for what it didn't give, and Node's default stack for
  // one that isn't positive. Values that aren't positive ask for nothing. They read back
  // like this until the worker starts and reports what it got. A stack larger than the
  // default isn't given here, as no limit is raised.
  function parseResourceLimits(obj) {
    const ret = [-1, -1, -1, kDefaultStackSizeMb];
    if (typeof obj !== 'object' || obj === null) return ret;
    if (typeof obj.maxOldGenerationSizeMb === 'number')
      ret[kMaxOldGenerationSizeMb] = Math.max(obj.maxOldGenerationSizeMb, 2);
    if (typeof obj.maxYoungGenerationSizeMb === 'number')
      ret[kMaxYoungGenerationSizeMb] = obj.maxYoungGenerationSizeMb;
    if (typeof obj.codeRangeSizeMb === 'number')
      ret[kCodeRangeSizeMb] = obj.codeRangeSizeMb;
    if (typeof obj.stackSizeMb === 'number' && obj.stackSizeMb > 0)
      ret[kStackSizeMb] = Math.min(obj.stackSizeMb, kDefaultStackSizeMb);
    return ret;
  }

  function makeResourceLimits(limits) {
    return {
      maxYoungGenerationSizeMb: limits[kMaxYoungGenerationSizeMb],
      maxOldGenerationSizeMb: limits[kMaxOldGenerationSizeMb],
      codeRangeSizeMb: limits[kCodeRangeSizeMb],
      stackSizeMb: limits[kStackSizeMb],
    };
  }

  // The limits a worker started with, as Go writes them: four numbers separated by commas.
  function parseLimits(text) {
    if (typeof text !== 'string') return null;
    const limits = text.split(',').map(Number);
    if (limits.length !== 4 || limits.some((n) => n !== n)) return null;
    return limits;
  }


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
        const limits = parseResourceLimits(options.resourceLimits);
        const id = global.__wt_spawn(String(target), dataJson, isEval,
          limits[kMaxYoungGenerationSizeMb], limits[kMaxOldGenerationSizeMb],
          limits[kCodeRangeSizeMb], limits[kStackSizeMb]);
        this._id = id;
        // null once the worker has exited.
        this._resourceLimits = limits;
        this.threadId = id;
        workersById.set(id, this);
      }

      get resourceLimits() {
        if (this._resourceLimits === null) return {};
        return makeResourceLimits(this._resourceLimits);
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
        case 'online': {
          // The limits the worker started with.
          const limits = parseLimits(data);
          if (limits !== null) w._resourceLimits = limits;
          w.emit('online');
          break;
        }
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
        case 'codedError': {
          // "CODE\nmessage": an Error with Node's code.
          const nl = data.indexOf('\n');
          const err = new Error(data.slice(nl + 1));
          err.code = data.slice(0, nl);
          if (err.code === 'MODULE_NOT_FOUND') err.requireStack = [];
          // Node reports these as the worker exits, when its limits are gone.
          if (err.code === 'ERR_WORKER_OUT_OF_MEMORY' || err.code === 'ERR_WORKER_INIT_FAILED')
            w._resourceLimits = null;
          w.emit('error', err);
          break;
        }
        case 'thrown': {
          let err;
          try {
            err = deserializeThrown(data);
          } catch (e) {
            err = new Error(data);
          }
          w.emit('error', err);
          break;
        }
        case 'exit':
          workersById.delete(id);
          w._resourceLimits = null;
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
  global.__wt_serialize_thrown = serializeThrown;

  global.__wt_init_worker = function(dataJson, limitsText) {
    const mod = global.__worker_threads_module;
    if (!mod) return;
    mod.isMainThread = false;
    const limits = parseLimits(limitsText);
    if (limits !== null) mod.resourceLimits = makeResourceLimits(limits);
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
