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
  // prototype and every non-function property found along the chain; anything
  // else is cloned like a message, and what cannot be cloned arrives as its
  // util.inspect() text. Node clones with the V8 serializer; this encodes the
  // same types as JSON.

  const objectToString = Object.prototype.toString;
  const hasOwnProp = Object.prototype.hasOwnProperty;
  const getOwnDesc = Object.getOwnPropertyDescriptor;
  const getProto = Object.getPrototypeOf;
  const errorCtors = {
    Error, EvalError, RangeError, ReferenceError, SyntaxError, TypeError, URIError
  };
  const typedArrays = {
    Int8Array, Uint8Array, Uint8ClampedArray, Int16Array, Uint16Array,
    Int32Array, Uint32Array, Float32Array, Float64Array,
    BigInt64Array, BigUint64Array
  };
  const inspectCustom = Symbol.for('nodejs.util.inspect.custom');

  function inspectValue(value) {
    try {
      if (typeof global.require === 'function') return global.require('util').inspect(value);
    } catch (e) {
      // Fall back to the plain string below.
    }
    try {
      return String(value);
    } catch (e) {
      return '[object could not be inspected]';
    }
  }

  function uncloneable() {
    return new TypeError('could not be cloned');
  }

  // encodeValue mirrors what the V8 serializer accepts. It throws for values
  // the serializer would reject, such as functions and symbols.
  function encodeValue(value, memo) {
    switch (typeof value) {
      case 'undefined':
        return { t: 'u' };
      case 'number':
        if (Object.is(value, -0)) return { t: 'n', v: '-0' };
        if (!Number.isFinite(value)) return { t: 'n', v: String(value) };
        return { t: 'p', v: value };
      case 'string':
      case 'boolean':
        return { t: 'p', v: value };
      case 'bigint':
        return { t: 'b', v: String(value) };
      case 'function':
      case 'symbol':
        throw uncloneable();
    }
    if (value === null) return { t: 'p', v: null };
    if (memo.has(value)) return { t: 'ref', id: memo.get(value) };
    const id = memo.size;
    memo.set(value, id);
    const tag = objectToString.call(value);
    switch (tag) {
      case '[object Date]':
        return { t: 'd', id, v: value.getTime() };
      case '[object RegExp]':
        return { t: 'r', id, s: value.source, f: value.flags };
      case '[object Error]': {
        // Like V8: the name picks the prototype, and message, stack and cause
        // are the only properties kept.
        const out = { t: 'e', id, name: String(value.name) };
        const message = getOwnDesc(value, 'message');
        if (message && 'value' in message) out.message = String(message.value);
        const stack = value.stack;
        if (typeof stack === 'string') out.stack = stack;
        const cause = getOwnDesc(value, 'cause');
        if (cause && 'value' in cause) out.cause = encodeValue(cause.value, memo);
        return out;
      }
      case '[object Map]': {
        const entries = [];
        for (const [k, v] of value) entries.push([encodeValue(k, memo), encodeValue(v, memo)]);
        return { t: 'm', id, v: entries };
      }
      case '[object Set]': {
        const items = [];
        for (const v of value) items.push(encodeValue(v, memo));
        return { t: 's', id, v: items };
      }
      case '[object Array]': {
        const props = [];
        for (const k of Object.keys(value)) props.push([k, encodeValue(value[k], memo)]);
        return { t: 'a', id, n: value.length, v: props };
      }
    }
    if (ArrayBuffer.isView(value)) {
      if (value instanceof DataView) throw uncloneable();
      const name = typeof global.Buffer === 'function' && global.Buffer.isBuffer(value)
        ? 'Buffer'
        : value[Symbol.toStringTag];
      if (name !== 'Buffer' && !hasOwnProp.call(typedArrays, name)) throw uncloneable();
      return { t: 'ta', id, c: name, v: Array.from(value, String) };
    }
    const props = [];
    for (const k of Object.keys(value)) props.push([k, encodeValue(value[k], memo)]);
    return { t: 'o', id, v: props };
  }

  function decodeValue(d, refs) {
    switch (d.t) {
      case 'u':
        return undefined;
      case 'p':
        return d.v;
      case 'n':
        return d.v === '-0' ? -0 : Number(d.v);
      case 'b':
        return BigInt(d.v);
      case 'ref':
        return refs[d.id];
      case 'd':
        return (refs[d.id] = new Date(d.v));
      case 'r':
        return (refs[d.id] = new RegExp(d.s, d.f));
      case 'e': {
        const Ctor = hasOwnProp.call(errorCtors, d.name) ? errorCtors[d.name] : Error;
        const err = 'message' in d ? new Ctor(d.message) : new Ctor();
        refs[d.id] = err;
        if ('stack' in d) {
          Object.defineProperty(err, 'stack', { value: d.stack, writable: true, configurable: true, enumerable: false });
        }
        if ('cause' in d) {
          Object.defineProperty(err, 'cause', { value: decodeValue(d.cause, refs), writable: true, configurable: true, enumerable: false });
        }
        return err;
      }
      case 'm': {
        const map = (refs[d.id] = new Map());
        for (const [k, v] of d.v) map.set(decodeValue(k, refs), decodeValue(v, refs));
        return map;
      }
      case 's': {
        const set = (refs[d.id] = new Set());
        for (const v of d.v) set.add(decodeValue(v, refs));
        return set;
      }
      case 'a': {
        const arr = (refs[d.id] = new Array(d.n));
        for (const [k, v] of d.v) arr[k] = decodeValue(v, refs);
        return arr;
      }
      case 'ta': {
        const big = d.c === 'BigInt64Array' || d.c === 'BigUint64Array';
        const items = d.v.map(big ? BigInt : Number);
        const out = d.c === 'Buffer' && typeof global.Buffer === 'function'
          ? global.Buffer.from(items)
          : typedArrays[d.c].from(items);
        return (refs[d.id] = out);
      }
      case 'o': {
        const obj = (refs[d.id] = {});
        for (const [k, v] of d.v) obj[k] = decodeValue(v, refs);
        return obj;
      }
    }
    return undefined;
  }

  // allProperties is error_serdes' TryGetAllProperties: the descriptors of
  // every own string-keyed property along the chain, nearest last, with
  // getters read against the object and functions and symbols left out.
  function allProperties(object, target) {
    const all = new Map();
    if (object === null) return all;
    for (const [k, desc] of allProperties(getProto(object), target)) all.set(k, desc);
    for (const key of Object.getOwnPropertyNames(object)) {
      let desc;
      try {
        desc = getOwnDesc(object, key);
      } catch (e) {
        continue;
      }
      if (desc.get && key !== '__proto__') {
        try {
          desc.value = desc.get.call(target);
        } catch (e) {
          // Leave the property out, as Node does.
        }
      }
      if ('value' in desc && typeof desc.value !== 'function' && typeof desc.value !== 'symbol') {
        all.delete(key);
        all.set(key, { value: desc.value, enumerable: !!desc.enumerable, writable: desc.writable !== false, configurable: !!desc.configurable });
      }
    }
    return all;
  }

  function builtinErrorName(error) {
    for (let current = error; current !== null; current = getProto(current)) {
      const ctor = getOwnDesc(current, 'constructor');
      if (!ctor || !ctor.value) continue;
      const name = getOwnDesc(ctor.value, 'name');
      if (name && typeof name.value === 'string' && hasOwnProp.call(errorCtors, name.value)) return name.value;
    }
    return null;
  }

  function serializeThrown(error) {
    try {
      if (typeof error === 'object' && error !== null && objectToString.call(error) === '[object Error]') {
        const ctor = builtinErrorName(error);
        if (ctor !== null) {
          const memo = new Map();
          const props = [];
          for (const [k, desc] of allProperties(error, error)) {
            props.push([k, encodeValue(desc.value, memo), desc.enumerable, desc.writable, desc.configurable]);
          }
          return JSON.stringify({ k: 'error', c: ctor, p: props });
        }
      }
    } catch (e) {
      // Try the next form.
    }
    try {
      if (error !== null && error !== undefined && hasOwnProp.call(error, inspectCustom)) {
        return JSON.stringify({ k: 'custom', s: inspectValue(error) });
      }
    } catch (e) {
      // Try the next form.
    }
    try {
      return JSON.stringify({ k: 'value', v: encodeValue(error, new Map()) });
    } catch (e) {
      // Fall back to the text.
    }
    return JSON.stringify({ k: 'text', s: inspectValue(error) });
  }

  function deserializeThrown(json) {
    const d = JSON.parse(json);
    switch (d.k) {
      case 'error': {
        const refs = [];
        const descs = {};
        for (const [k, v, enumerable, writable, configurable] of d.p) {
          descs[k] = { value: decodeValue(v, refs), enumerable, writable, configurable };
        }
        descs[Symbol.toStringTag] = { value: 'Error', configurable: true };
        return Object.create(errorCtors[d.c].prototype, descs);
      }
      case 'custom': {
        const text = d.s;
        return { __proto__: null, [inspectCustom]: () => text };
      }
      case 'value':
        return decodeValue(d.v, []);
    }
    return d.s;
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
