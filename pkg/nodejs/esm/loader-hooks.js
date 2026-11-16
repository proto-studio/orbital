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

  // Captured now, so a hook's initialize() replacing the globals can't change
  // the errors the loader throws.
  const NativeError = Error;
  const NativeTypeError = TypeError;
  const NativeURIError = URIError;

  const hooks = [];

  // Called (from Go) once per module.register(), after the hooks module has been
  // imported into this realm.
  // url is the specifier the hooks module was registered with.
  globalThis.__addHook = function (mod, url) {
    if (mod && (typeof mod.resolve === 'function' || typeof mod.load === 'function')) {
      hooks.push({ resolve: mod.resolve, load: mod.load, url: url });
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
  // With a Symbol.toStringTag present that tag decides toString, so the
  // object must also clone as an Error (V8's serializer knows the brand).
  const toStringTag = Symbol.toStringTag;
  function isErrorBrand(v) {
    if (v === null || typeof v !== 'object') return false;
    try {
      if (reflectApply(objectToString, v, []) !== '[object Error]') return false;
      if (!(toStringTag in v)) return true;
      if (!cloneValue) return false;
      const c = cloneValue(v);
      return c !== null && typeof c === 'object' && reflectApply(isPrototypeOf, ErrorPrototype, [c]);
    } catch (err) {
      return false;
    }
  }
  const symbolDescription = Object.getOwnPropertyDescriptor(Symbol.prototype, 'description').get;
  // A standard field, read on its own so one throwing accessor doesn't lose
  // the rest of the error.
  function field(e, key) {
    try {
      return NativeString(e[key]);
    } catch (err) {
      return '';
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
      if (v !== null && typeof v === 'object') {
        try {
          const json = jsonStringify(v);
          if (typeof json === 'string') return json;
        } catch (err) { /* not JSON */ }
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
  // seen holds the errors whose description is in progress: a cause that leads
  // back to one of them makes the chain cyclic, and like Node's transfer the
  // cause is then left out (all the way up).
  function describeError(e, depth, seen) {
    seen[seen.length] = e;
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
    const name = field(e, 'name');
    const message = field(e, 'message');
    let stack = field(e, 'stack');
    if (reflectApply(internalHas, internalErrors, [e]) && e.code) {
      // Node's own errors cross its hooks thread with the stack they
      // formatted there, "Error [CODE]: ..." first.
      const at = reflectApply(stringIndexOf, stack, ['\n    at ']);
      stack = name + ' [' + NativeString(e.code) + ']: ' + message + (at >= 0 ? reflectApply(stringSlice, stack, [at]) : '');
    }
    const bag = { props: props, hasCause: false };
    let cyclic = false;
    try {
      if (causeRead || reflectApply(hasOwn, e, ['cause'])) {
        const cause = causeRead ? causeValue : e.cause;
        let inProgress = false;
        for (let i = 0; i < seen.length; i++) {
          if (seen[i] === cause) inProgress = true;
        }
        if (inProgress) {
          cyclic = true;
        } else if (isErrorBrand(cause) && depth < 16) {
          const d = describeError(cause, depth + 1, seen);
          if (d.cyclic) {
            cyclic = true;
          } else {
            ownData(bag, 'causeError', { payload: d.payload, bag: d.bag });
            if (causeRead) ownData(bag, 'causeEnumerable', true);
            ownData(bag, 'hasCause', true);
          }
        } else {
          let c;
          try {
            c = cloneValue ? cloneValue(cause) : cause;
          } catch (err) {
            c = inspected(cause);
          }
          ownData(bag, 'cause', c);
          if (causeRead) ownData(bag, 'causeEnumerable', true);
          ownData(bag, 'hasCause', true);
        }
      }
    } catch (err) { /* not transferable */ }
    seen.length = seen.length - 1;
    const payload = jsonStringify({ ctor: ctor, name: name, message: message, stack: stack, props: jsonProps });
    return { payload: payload, bag: bag, cyclic: cyclic };
  }

  // The bag for an error __describeRejection described (see describeError).
  globalThis.__rejectionProps = function (e) {
    const snap = takeSnapshot(e);
    if (snap !== undefined && snap.bag !== undefined) return snap.bag;
    return describeError(e, 0, []).bag;
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
      if (typeof e === 'symbol') {
        // A symbol crosses as itself, with its description.
        const description = reflectApply(symbolDescription, e, []);
        return jsonStringify({ symbol: true, description: description === undefined ? null : description });
      }
      if (!isErrorBrand(e)) {
        // Not an error: Node transfers the value itself, structured-cloned,
        // which Go does with V8's serializer, from this one clone; a value
        // that can't be cloned (a function, a proxy) as its inspected string.
        if (e !== null && (typeof e === 'object' || typeof e === 'function')) {
          let value;
          try {
            if (!cloneValue) throw new NativeError('no clone');
            value = cloneValue(e);
          } catch (err) {
            value = inspected(e);
          }
          reflectApply(snapSet, snapshots, [e, { clone: { value: value } }]);
        }
        return jsonStringify({ clone: true });
      }
      const d = describeError(e, 0, []);
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
    const attrs = context && context.importAttributes;
    const r = globalThis.__goLoad(url, (context && context.format) || '',
      encodeAttrs(attrs), checkAttributes ? '1' : '');
    if (r.code) {
      // An import attribute the module's format doesn't allow (Node's
      // validateAttributes in its default load).
      const err = new NativeTypeError(r.message);
      err.code = r.code;
      throw err;
    }
    return { source: r.source, format: r.format, shortCircuit: true };
  }

  // Attributes cross to Go as JSON [key, value] pairs. A value that isn't a
  // string goes as { text, received, json?, undefined? }: its string form (what
  // Node prints for an unknown key), how Node describes it in a type error, and
  // what to rebuild it from for the load hooks' context.
  // The pairs travel as { array, pairs }, so an array a hook returned comes back
  // as an array.
  function encodeAttrs(attrs) {
    if (attrs == null || typeof attrs !== 'object') return '{"array":false,"pairs":[]}';
    return JSON.stringify({ array: Array.isArray(attrs), pairs: Object.keys(attrs).map((k) => {
      const v = attrs[k];
      if (typeof v === 'string') return [k, v];
      const other = { text: formatted(v), received: received(v) };
      if (v === undefined) other.undefined = true;
      else if (typeof v === 'bigint') other.bigint = String(v);
      else if (v === null || typeof v === 'number' || typeof v === 'boolean') other.json = v;
      else if (typeof v === 'object') {
        try { other.json = JSON.parse(JSON.stringify(v)); } catch (e) { /* not representable */ }
      }
      return [k, other];
    }) });
  }

  // The resolved attributes object itself stays in this realm until its load,
  // so the load hooks get a structured clone of it, as Node gives them (a Date
  // stays a Date, a Map a Map), rather than what survives the JSON trip to Go.
  const stashed = new Map();
  let stashSeq = 0;
  const clone = typeof structuredClone === 'function' ? structuredClone : null;

  function stashAttrs(attrs) {
    const id = ++stashSeq;
    stashed.set(id, attrs);
    const encoded = JSON.parse(encodeAttrs(attrs));
    encoded.id = id;
    return JSON.stringify(encoded);
  }

  // Go calls this when it refuses a resolution before its load, so the stashed
  // object isn't kept.
  globalThis.__dropAttrs = function (text) {
    try {
      const encoded = JSON.parse(text || '{}');
      if (encoded.id !== undefined) stashed.delete(encoded.id);
    } catch (e) { /* nothing stashed */ }
  };

  function unstashAttrs(text) {
    const encoded = JSON.parse(text || '{"array":false,"pairs":[]}');
    if (encoded.id !== undefined && stashed.has(encoded.id)) {
      const attrs = stashed.get(encoded.id);
      stashed.delete(encoded.id);
      if (clone && attrs !== null && typeof attrs === 'object') {
        try {
          const copy = clone(attrs);
          // A clone can't keep an own "__proto__" key; rebuild then instead.
          if (Object.keys(copy).length === Object.keys(attrs).length) return copy;
        } catch (e) { /* not cloneable: rebuild below */ }
      }
    }
    return decodeAttrs(text);
  }

  function decodeAttrs(text) {
    const encoded = JSON.parse(text || '{"array":false,"pairs":[]}');
    const attrs = encoded.array ? [] : {};
    for (const [k, v] of encoded.pairs) {
      let value = v;
      if (typeof v !== 'string') {
        value = v.undefined ? undefined : 'bigint' in v ? BigInt(v.bigint) : 'json' in v ? v.json : v.text;
      }
      // defineProperty, so a "__proto__" key stays an own attribute.
      Object.defineProperty(attrs, k, { value, writable: true, enumerable: true, configurable: true });
    }
    return attrs;
  }

  function safeString(v) {
    try { return String(v); } catch (e) { return ''; }
  }

  // A value as Node's error messages print it (util.format's %s).
  function formatted(v) {
    if (typeof v === 'bigint') return v + 'n';
    try {
      const util = typeof require === 'function' ? require('util') : null;
      if (util && typeof util.format === 'function') return util.format('%s', v);
    } catch (e) { /* fall back */ }
    return safeString(v);
  }

  // How Node's ERR_INVALID_ARG_TYPE describes the value it received.
  function received(v) {
    if (v == null) return String(v);
    if (typeof v === 'function') return v.name ? 'function ' + v.name : 'function';
    if (typeof v === 'object') {
      const ctor = v.constructor && v.constructor.name;
      // Node calls a null-prototype object an instance of Object as well.
      return 'an instance of ' + (ctor || 'Object');
    }
    let shown = typeof v === 'bigint' ? v + 'n' : safeString(v);
    if (shown.length > 25) shown = shown.slice(0, 25) + '...';
    return 'type ' + typeof v + ' (' + shown + ')';
  }

  // Whether the load in progress checks import attributes (not for an entry point).
  let checkAttributes = false;

  // The hook the chain called last, named the way Node names it in errors
  // about a hook's result: the specifier given to register(), then the kind.
  let lastHook = '';

  function buildChain(kind, terminal) {
    let next = function (a, b) {
      lastHook = "node:internal/modules/esm/" + kind + " '" + kind + "'";
      return terminal(a, b);
    };
    for (let i = hooks.length - 1; i >= 0; i--) {
      const hook = hooks[i][kind];
      if (typeof hook !== 'function') continue;
      const downstream = next;
      const id = hooks[i].url + " '" + kind + "'";
      next = function (a, b) {
        lastHook = id;
        return hook(a, b, downstream);
      };
    }
    return next;
  }

  // How Node describes an unexpected value in an error (determineSpecificType).
  function describe(value) {
    if (value == null) return '' + value;
    if (typeof value === 'function' && value.name) return 'function ' + value.name;
    let shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return 'type ' + typeof value + ' (' + shown + ')';
  }

  globalThis.__runResolve = async function (specifier, parentURL, attrsJSON) {
    const chain = buildChain('resolve', defaultResolve);
    const importAttributes = JSON.parse(attrsJSON || '{}');
    const ctx = {
      parentURL: parentURL || undefined,
      conditions: ['node', 'import', 'module', 'default'],
      importAttributes,
    };
    const res = await chain(specifier, ctx);
    // A resolve hook may replace the attributes the module is loaded with, with
    // an object (Node's check of a resolve hook's result).
    if (res.importAttributes != null && typeof res.importAttributes !== 'object') {
      const err = new NativeTypeError('Expected an object to be returned for the "importAttributes" from the "' +
        lastHook + '" hook but got ' + describe(res.importAttributes) + '.');
      err.code = 'ERR_INVALID_RETURN_PROPERTY_VALUE';
      throw err;
    }
    const resolvedAttributes = res.importAttributes != null ? res.importAttributes : importAttributes;
    return {
      url: res.url,
      format: res.format == null ? '' : res.format,
      importAttributes: stashAttrs(resolvedAttributes),
    };
  };

  globalThis.__runLoad = async function (url, format, attrsJSON, check) {
    const chain = buildChain('load', defaultLoad);
    const ctx = {
      format: format || undefined,
      conditions: ['node', 'import', 'module', 'default'],
      importAttributes: unstashAttrs(attrsJSON),
    };
    checkAttributes = !!check;
    const res = await chain(url, ctx);
    let source = res.source;
    // Hooks may return a string, a TypedArray/Buffer, or an ArrayBuffer.
    if (source != null && typeof source !== 'string') {
      source = Buffer.from(source).toString('utf8');
    }
    return { source: source == null ? '' : source, format: res.format || 'module' };
  };
})();
