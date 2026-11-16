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

  // An Error by brand, as Node's transfer tells one (Object.prototype.toString),
  // so an object that merely inherits from Error.prototype is not one, and an
  // Error is one whatever its properties hold.
  const objectToString = Object.prototype.toString;
  const objectGetPrototypeOf = Object.getPrototypeOf;
  const objectGetOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  const functionToString = Function.prototype.toString;
  function isErrorBrand(v) {
    if (v === null || typeof v !== 'object') return false;
    try {
      return reflectApply(objectToString, v, []) === '[object Error]';
    } catch (err) {
      return false;
    }
  }

  // The built-in class an error crosses as: the first one on its prototype
  // chain, so a subclass of TypeError arrives as a TypeError.
  const builtinErrors = ['Error', 'TypeError', 'RangeError', 'SyntaxError', 'ReferenceError', 'EvalError', 'URIError'];
  function builtinClass(e) {
    let p = e;
    for (let depth = 0; depth < 64; depth++) {
      try {
        p = objectGetPrototypeOf(p);
      } catch (err) {
        break;
      }
      if (p === null) break;
      try {
        const d = objectGetOwnPropertyDescriptor(p, 'constructor');
        if (d && typeof d.value === 'function') {
          const name = d.value.name;
          for (let i = 0; i < builtinErrors.length; i++) {
            if (builtinErrors[i] === name) return name;
          }
        }
      } catch (err) { /* keep looking */ }
    }
    return 'Error';
  }

  // How Node's transfer shows a value it can't clone (util.inspect's form).
  function inspected(v) {
    try {
      if (typeof v === 'function') {
        let src = '';
        try { src = reflectApply(functionToString, v, []); } catch (err) { /* native */ }
        const name = v.name;
        if (reflectApply(stringSlice, src, [0, 6]) === 'class ' || src === 'class') {
          return '[class ' + (name ? NativeString(name) : '(anonymous)') + ']';
        }
        return name ? '[Function: ' + NativeString(name) + ']' : '[Function (anonymous)]';
      }
      return NativeString(v);
    } catch (err) {
      return '';
    }
  }

  // Describes an error: the JSON payload Go reads (class, name, message, stack
  // and the JSON-safe properties) and the bag it serializes (the properties,
  // and the cause, itself described when it is an error). The properties are
  // read once and cloned as one graph, so values they share stay shared; when
  // that fails, each is cloned alone and one that can't be is left out.
  function describeError(e, depth) {
    const raw = {};
    const keys = objectKeys(e);
    // An enumerable cause is read here, once, and crosses as the cause below
    // (still enumerable), not among the properties.
    let causeRead = false;
    let causeValue;
    for (let i = 0; i < keys.length; i++) {
      try {
        const v = e[keys[i]];
        if (keys[i] === 'cause') {
          causeRead = true;
          causeValue = v;
          continue;
        }
        // A function or symbol can't cross, and would fail the clone below
        // after it had read the other values: left out first.
        if (typeof v === 'function' || typeof v === 'symbol') continue;
        ownData(raw, keys[i], v);
      } catch (err) { /* unreadable */ }
    }
    let props;
    try {
      props = cloneValue ? cloneValue(raw) : raw;
    } catch (err) {
      props = {};
      const rawKeys = objectKeys(raw);
      for (let i = 0; i < rawKeys.length; i++) {
        try {
          ownData(props, rawKeys[i], cloneValue ? cloneValue(raw[rawKeys[i]]) : raw[rawKeys[i]]);
        } catch (err2) { /* not transferable */ }
      }
    }
    const jsonProps = {};
    const propKeys = objectKeys(props);
    for (let i = 0; i < propKeys.length; i++) {
      try {
        jsonStringify(props[propKeys[i]]);
        ownData(jsonProps, propKeys[i], props[propKeys[i]]);
      } catch (err) { /* not JSON */ }
    }
    const ctor = builtinClass(e);
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
      if (causeRead || reflectApply(hasOwn, e, ['cause'])) {
        const cause = causeRead ? causeValue : e.cause;
        if (causeRead) ownData(bag, 'causeEnumerable', true);
        if (isErrorBrand(cause) && depth < 16) {
          const d = describeError(cause, depth + 1);
          ownData(bag, 'causeError', { payload: d.payload, bag: d.bag });
        } else {
          let c;
          try {
            c = cloneValue ? cloneValue(cause) : cause;
          } catch (err) {
            c = inspected(cause);
          }
          ownData(bag, 'cause', c);
        }
        ownData(bag, 'hasCause', true);
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
      if (!isErrorBrand(e)) {
        // Not an error: Node transfers the value itself, structured-cloned,
        // which Go does with V8's serializer, from this one clone.
        if (e !== null && (typeof e === 'object' || typeof e === 'function') && cloneValue) {
          try {
            reflectApply(snapSet, snapshots, [e, { clone: { value: cloneValue(e) } }]);
          } catch (err) { /* Go reports it can't be cloned */ }
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
