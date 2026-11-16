// Loader-hook orchestrator — runs INSIDE the isolated loader realm (its own V8
// context, globals, and module cache; see hooks.go). This mirrors Node's
// module-customization-hooks thread: registered hooks live here, isolated from
// the application realm, and communicate with it only through serializable
// strings marshaled by Go.
//
// The application realm's Go resolver calls __runResolve / __runLoad here; the
// hook chain's innermost "next" (the default resolve/load) bridges back to Go
// via __goResolve / __goLoad (which run the pure-Go default loader against the
// application's module graph). Only strings cross the boundary.
(function () {
  'use strict';

  const hooks = [];

  // Called (from Go) once per module.register(), after the hooks module has been
  // imported into this realm.
  globalThis.__addHook = function (mod) {
    if (mod && (typeof mod.resolve === 'function' || typeof mod.load === 'function')) {
      hooks.push({ resolve: mod.resolve, load: mod.load });
    }
  };

  globalThis.__hasHooks = function () {
    return hooks.length;
  };

  // The loader's own errors (Node's internal ones), as against errors a hook
  // throws, which reach the importer as they are.
  const internalErrors = new WeakSet();
  const internalAdd = WeakSet.prototype.add;
  const internalHas = WeakSet.prototype.has;
  const reflectApply = Reflect.apply;
  const objectKeys = Object.keys;
  const jsonStringify = JSON.stringify;
  const jsonParse = JSON.parse;
  const isPrototypeOf = Object.prototype.isPrototypeOf;
  const ErrorPrototype = Error.prototype;
  function internal(err) {
    reflectApply(internalAdd, internalErrors, [err]);
    return err;
  }

  // A thrown value crosses as Node's single transfer does: every property is
  // read once, into a structured clone (V8's serializer keeps NaN, BigInts and
  // cycles, which JSON doesn't), and both the JSON description and what Go
  // serializes come from that snapshot. A value that can't be cloned is left out.
  // V8's own structured clone (see installV8Clone in hooks.go), captured
  // before any hook runs.
  const cloneValue = typeof globalThis.__v8Clone === 'function' ? globalThis.__v8Clone
    : typeof structuredClone === 'function' ? structuredClone : null;
  delete globalThis.__v8Clone;
  const hasOwn = Object.prototype.hasOwnProperty;
  const objectDefineProperty = Object.defineProperty;
  const NativeString = String;
  const stringIndexOf = String.prototype.indexOf;
  const stringSlice = String.prototype.slice;
  const snapshots = new WeakMap();
  const snapGet = WeakMap.prototype.get;
  const snapSet = WeakMap.prototype.set;
  const snapDelete = WeakMap.prototype.delete;
  function takeSnapshot(e) {
    const snap = reflectApply(snapGet, snapshots, [e]);
    if (snap !== undefined) reflectApply(snapDelete, snapshots, [e]);
    return snap;
  }

  // The value's structured clone, which is also the brand check: only a real
  // Error (not an object that merely inherits from Error.prototype) clones as
  // an Error.
  function cloneOf(v) {
    if (v === null || (typeof v !== 'object' && typeof v !== 'function')) return { ok: true, clone: v, isError: false };
    if (!cloneValue) return { ok: true, clone: v, isError: reflectApply(isPrototypeOf, ErrorPrototype, [v]) };
    try {
      const c = cloneValue(v);
      return { ok: true, clone: c, isError: c !== null && typeof c === 'object' && reflectApply(isPrototypeOf, ErrorPrototype, [c]) };
    } catch (err) {
      return { ok: false, clone: undefined, isError: false };
    }
  }

  // Describes an error: the JSON payload Go reads (class, name, message, stack
  // and the JSON-safe properties) and the bag it serializes (the properties,
  // and the cause, itself described when it is an error).
  function describeError(e, depth) {
    const jsonProps = {};
    const props = {};
    const keys = objectKeys(e);
    for (let i = 0; i < keys.length; i++) {
      let c;
      try {
        c = cloneOf(e[keys[i]]);
      } catch (err) {
        continue;
      }
      if (!c.ok) continue;
      ownData(props, keys[i], c.clone);
      try {
        jsonStringify(c.clone);
        ownData(jsonProps, keys[i], c.clone);
      } catch (err) { /* not JSON */ }
    }
    let ctor = '';
    try { ctor = e.constructor && e.constructor.name; } catch (err) { /* none */ }
    const name = NativeString(e.name);
    const message = NativeString(e.message);
    let stack = NativeString(e.stack);
    if (reflectApply(internalHas, internalErrors, [e]) && e.code) {
      // Node's own errors cross its hooks thread with the stack they
      // formatted there, "Error [CODE]: ..." first.
      const at = reflectApply(stringIndexOf, stack, ['\n    at ']);
      stack = name + ' [' + NativeString(e.code) + ']: ' + message + (at >= 0 ? reflectApply(stringSlice, stack, [at]) : '');
    }
    const bag = { props: props, hasCause: false };
    try {
      if (reflectApply(hasOwn, e, ['cause'])) {
        const cause = e.cause;
        const c = cloneOf(cause);
        if (c.isError && depth < 16) {
          const d = describeError(cause, depth + 1);
          ownData(bag, 'causeError', { payload: d.payload, bag: d.bag });
          ownData(bag, 'hasCause', true);
        } else if (c.ok) {
          ownData(bag, 'cause', c.clone);
          ownData(bag, 'hasCause', true);
        }
      }
    } catch (err) { /* not transferable */ }
    const payload = jsonStringify({ ctor: ctor, name: name, message: message, stack: stack, props: jsonProps });
    return { payload: payload, bag: bag };
  }

  // The bag for an error __describeRejection described (see describeError).
  globalThis.__rejectionProps = function (e) {
    const snap = takeSnapshot(e);
    if (snap !== undefined && snap.bag !== undefined) return snap.bag;
    return describeError(e, 0).bag;
  };

  // The structured clone of a thrown value that isn't an error, taken when it
  // was described, for Go to serialize without reading the value again.
  globalThis.__rejectionClone = function (e) {
    const snap = takeSnapshot(e);
    if (snap !== undefined && snap.clone !== undefined) return snap.clone.value;
    return e;
  };

  // Describes a rejection for Go, which rebuilds it in the application realm
  // with its class, message, stack, own enumerable properties and cause, as
  // Node's transfer of an error from its hooks thread keeps them (so even
  // Node's own errors arrive as plain errors there, their code a property).
  globalThis.__describeRejection = function (e) {
    try {
      const c = cloneOf(e);
      if (!c.isError) {
        // Not an error: Node transfers the value itself, structured-cloned,
        // which Go does with V8's serializer.
        if (c.ok && e !== null && (typeof e === 'object' || typeof e === 'function')) {
          reflectApply(snapSet, snapshots, [e, { clone: { value: c.clone } }]);
        }
        return jsonStringify({ clone: true });
      }
      const d = describeError(e, 0);
      reflectApply(snapSet, snapshots, [e, { bag: d.bag }]);
      return d.payload;
    } catch (err) {
      return '';
    }
  };

  // Default (terminal) resolve: bridge to the Go default loader. Returns a url
  // string, or throws a Node-shaped ERR_MODULE_NOT_FOUND so resolve hooks that
  // probe alternatives (e.g. .js -> .ts) can catch and retry.
  // Captured before any hook runs, so a hook replacing the globals or adding
  // setters to their prototypes can't change the loader's own errors.
  const NativeError = Error;
  const NativeTypeError = TypeError;
  const NativeURIError = URIError;
  function ownData(obj, key, value) {
    objectDefineProperty(obj, key, { value: value, writable: true, enumerable: true, configurable: true });
  }
  function defaultResolve(specifier, context) {
    const parentURL = (context && context.parentURL) || '';
    const r = jsonParse(globalThis.__goResolve(specifier, parentURL));
    if (!r.found) {
      const Ctor = r.kind === 'TypeError' ? NativeTypeError : r.kind === 'URIError' ? NativeURIError : NativeError;
      const err = new Ctor(r.message);
      if (r.code) ownData(err, 'code', r.code);
      // The missing module's url lets resolve hooks rewrite the extension and
      // retry (e.g. ts-blank-space maps a missing ".js" to its ".ts" source).
      if (r.url) ownData(err, 'url', r.url);
      if (r.input) ownData(err, 'input', r.input);
      if (r.module) ownData(err, 'module', r.module);
      throw internal(err);
    }
    return { url: r.url, format: (context && context.format) || null, shortCircuit: true };
  }

  // Default (terminal) load: bridge to the Go default loader. Returns
  // { source, format } with the raw (un-finalized) source; the application realm
  // performs CJS/JSON finalization after the hook chain.
  function defaultLoad(url, context) {
    const r = globalThis.__goLoad(url);
    return { source: r.source, format: r.format, shortCircuit: true };
  }

  function buildChain(kind, terminal) {
    let next = terminal;
    for (let i = hooks.length - 1; i >= 0; i--) {
      const hook = hooks[i][kind];
      if (typeof hook !== 'function') continue;
      const downstream = next;
      next = function (a, b) {
        return hook(a, b, downstream);
      };
    }
    return next;
  }

  globalThis.__runResolve = async function (specifier, parentURL) {
    const chain = buildChain('resolve', defaultResolve);
    const ctx = {
      parentURL: parentURL || undefined,
      conditions: ['node', 'import', 'module', 'default'],
      importAttributes: {},
    };
    const res = await chain(specifier, ctx);
    return { url: res.url, format: res.format == null ? '' : res.format };
  };

  globalThis.__runLoad = async function (url, format) {
    const chain = buildChain('load', defaultLoad);
    const ctx = {
      format: format || undefined,
      conditions: ['node', 'import', 'module', 'default'],
      importAttributes: {},
    };
    const res = await chain(url, ctx);
    let source = res.source;
    // Hooks may return a string, a TypedArray/Buffer, or an ArrayBuffer.
    if (source != null && typeof source !== 'string') {
      source = Buffer.from(source).toString('utf8');
    }
    return { source: source == null ? '' : source, format: res.format || 'module' };
  };
})();
