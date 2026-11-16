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
  const isPrototypeOf = Object.prototype.isPrototypeOf;
  const ErrorPrototype = Error.prototype;
  function internal(err) {
    reflectApply(internalAdd, internalErrors, [err]);
    return err;
  }

  // An error's own enumerable properties, for Go to structured-clone across
  // (V8's serializer keeps NaN, BigInts and cycles, which JSON doesn't). A value
  // that can't be cloned is left out.
  const cloneValue = typeof structuredClone === 'function' ? structuredClone : null;
  globalThis.__rejectionProps = function (e) {
    const props = {};
    const keys = objectKeys(e);
    for (let i = 0; i < keys.length; i++) {
      try {
        const v = e[keys[i]];
        if (cloneValue) cloneValue(v);
        props[keys[i]] = v;
      } catch (err) { /* not transferable */ }
    }
    return props;
  };

  // Describes a rejection for Go, which rebuilds it in the application realm
  // with its class, message, stack and own enumerable properties, as Node's
  // transfer of an error from its hooks thread keeps them (so even Node's own
  // errors arrive as plain errors there, their code a property).
  globalThis.__describeRejection = function (e) {
    try {
      // Not an error: Node transfers the value itself, structured-cloned, which
      // Go does with V8's serializer.
      if (e === null || (typeof e !== 'object' && typeof e !== 'function') ||
          !reflectApply(isPrototypeOf, ErrorPrototype, [e])) {
        return jsonStringify({ clone: true });
      }
      const props = {};
      const keys = objectKeys(e);
      for (let i = 0; i < keys.length; i++) {
        try {
          const v = e[keys[i]];
          jsonStringify(v);
          props[keys[i]] = v;
        } catch (err) { /* not transferable */ }
      }
      let ctor = '';
      try { ctor = e.constructor && e.constructor.name; } catch (err) { /* none */ }
      let stack = String(e.stack);
      if (reflectApply(internalHas, internalErrors, [e]) && e.code) {
        // Node's own errors cross its hooks thread with the stack they
        // formatted there, "Error [CODE]: ..." first.
        const at = stack.indexOf('\n    at ');
        stack = String(e.name) + ' [' + e.code + ']: ' + String(e.message) + (at >= 0 ? stack.slice(at) : '');
      }
      return jsonStringify({ ctor: ctor, name: String(e.name), message: String(e.message), stack: stack, props: props });
    } catch (err) {
      return '';
    }
  };

  // Default (terminal) resolve: bridge to the Go default loader. Returns a url
  // string, or throws a Node-shaped ERR_MODULE_NOT_FOUND so resolve hooks that
  // probe alternatives (e.g. .js -> .ts) can catch and retry.
  function defaultResolve(specifier, context) {
    const parentURL = (context && context.parentURL) || '';
    const r = globalThis.__goResolve(specifier, parentURL);
    if (!r.found) {
      const Ctor = r.kind === 'TypeError' ? TypeError : r.kind === 'URIError' ? URIError : Error;
      const err = new Ctor(r.message);
      if (r.code) err.code = r.code;
      // The missing module's url lets resolve hooks rewrite the extension and
      // retry (e.g. ts-blank-space maps a missing ".js" to its ".ts" source).
      if (r.url) err.url = r.url;
      if (r.input) err.input = r.input;
      if (r.module) err.module = r.module;
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
