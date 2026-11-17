// ESM loader (JS side).
//
// Module resolution + loading now lives in Go (see loader.go / bridge.go). The
// only JS helper still required is one used by the C++ dynamic-import host
// callback: it chains a module's evaluation promise (which fulfills after any
// top-level await) to its namespace object, so `await import(x)` yields the
// namespace only once the module has finished evaluating (and rejects if it
// threw).
(function () {
  'use strict';

  // import() hands its load-and-evaluate job here rather than running the module
  // during the call. Node loads the module asynchronously, so the code after import(),
  // the microtasks it queues and process.nextTick callbacks all run before the module
  // does. Captured now so user code replacing setImmediate cannot affect import().
  var schedule = globalThis.setImmediate;
  if (typeof schedule === 'function') {
    globalThis.__esmScheduleDynamicImport = function (job) {
      schedule(job);
    };
  }

  // The error a failed import throws, shaped like Node's: its code shows in the
  // stack's first line ("Error [ERR_MODULE_NOT_FOUND]: ...") and as `code`, and
  // a missing module's file: URL as `url`. Go builds it through this helper.
  // Captured now so code replacing the globals cannot change these errors.
  var NativeError = Error;
  var NativeTypeError = TypeError;
  var NativeURIError = URIError;
  var NativeRangeError = RangeError;
  var reject = Promise.reject.bind(Promise);
  var defineProperty = Object.defineProperty;
  var apply = Reflect.apply;
  var captureStackTrace = Error.captureStackTrace;

  // An own data property, as an assignment would make, without calling a setter
  // code may have put on Error.prototype.
  function own(obj, key, value, enumerable) {
    defineProperty(obj, key, { value: value, writable: true, enumerable: enumerable, configurable: true });
  }

  // Each coded error's code as created: Node formats with that, whatever the
  // program later does to the code property.
  var codes = new WeakMap();
  var codesGet = WeakMap.prototype.get;
  var codesSet = WeakMap.prototype.set;
  function codeOf(err) {
    var code = apply(codesGet, codes, [err]);
    return code === undefined ? err.code : code;
  }

  // Like Node's coded errors, these stringify as "Error [CODE]: message", from a
  // prototype between the error and Error.prototype (or TypeError.prototype).
  function codedToString() {
    return this.name + ' [' + codeOf(this) + ']: ' + this.message;
  }
  var CodedError = Object.create(NativeError.prototype);
  var CodedTypeError = Object.create(NativeTypeError.prototype);
  var CodedRangeError = Object.create(RangeError.prototype);
  own(CodedError, 'toString', codedToString, false);
  own(CodedTypeError, 'toString', codedToString, false);
  own(CodedRangeError, 'toString', codedToString, false);
  var setPrototypeOf = Object.setPrototypeOf;
  var getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  var NativeString = String;
  var stringIndexOf = String.prototype.indexOf;
  var stringSlice = String.prototype.slice;
  var hasOwn = Object.prototype.hasOwnProperty;

  // Replaces err's stack accessor with one that formats through the original
  // on first read and puts the code in the header ("Error [CODE]: ...").
  // holder carries the same frames, captured with the error, so the default
  // header can be built without the engine also reading name and message.
  function lazyCodedStack(err, desc, code, holder) {
    var done = false;
    var value;
    defineProperty(err, 'stack', {
      get: function () {
        if (!done) {
          done = true;
          if (typeof NativeError.prepareStackTrace === 'function') {
            // A program's own formatter runs now, and its result is kept as is.
            value = apply(desc.get, err, []);
          } else {
            // Like Node, the header is built from the name and message the
            // error has now (each read once), with the code it was created
            // with; the frames are the engine's.
            var raw = NativeString(holder.stack);
            var at = apply(stringIndexOf, raw, ['\n    at ']);
            value = NativeString(err.name) + ' [' + code + ']: ' + NativeString(err.message) + (at >= 0 ? apply(stringSlice, raw, [at]) : '');
          }
        }
        return value;
      },
      set: function (v) {
        done = true;
        value = v;
      },
      enumerable: desc.enumerable,
      configurable: true,
    });
  }

  // kind is the error's class: "Error", "TypeError", "URIError" or "RangeError".
  globalThis.__esmModuleError = function (message, code, url, kind, input, module) {
    var isTypeError = kind === 'TypeError';
    var err = isTypeError ? new NativeTypeError(message)
      : kind === 'URIError' ? new NativeURIError(message)
      : kind === 'RangeError' ? new NativeRangeError(message) : new NativeError(message);
    if (code) {
      // The "[CODE]" header is added when the stack is first read, from the
      // engine's own accessor, so formatting (and an Error.prepareStackTrace the
      // program installs, even later) happens at that read, as in Node.
      var desc = getOwnPropertyDescriptor(err, 'stack');
      if (desc && typeof desc.get === 'function') {
        var holder = {};
        captureStackTrace(holder);
        lazyCodedStack(err, desc, code, holder);
      }
      own(err, 'code', code, true);
      apply(codesSet, codes, [err, code]);
      setPrototypeOf(err, isTypeError ? CodedTypeError : kind === 'RangeError' ? CodedRangeError : CodedError);
    }
    if (url) own(err, 'url', url, true);
    if (input) own(err, 'input', input, true);
    if (module) own(err, 'module', module, true);
    return err;
  };

  // Rebuilds an error a loader hook threw, from the description the loader
  // realm gave (see __describeRejection in loader-hooks.js): its class, message,
  // stack and own enumerable properties, as Node transfers it from its hooks
  // thread. (A thrown value that isn't an error is structured-cloned in Go.)
  var jsonParse = JSON.parse;
  var objectKeys = Object.keys;
  var objectCreate = Object.create;
  var hookCtors = {
    Error: NativeError, TypeError: NativeTypeError, URIError: NativeURIError,
    RangeError: RangeError, SyntaxError: SyntaxError, ReferenceError: ReferenceError, EvalError: EvalError,
  };
  // bag, when given, is the error's own properties and cause as
  // structured-cloned, a cause that is an error described the same way.
  // Like Node's deserialized errors, the result has its class's prototype and
  // own name, message, stack and (if it had one) cause, then its properties.
  var NativeSymbol = Symbol;
  globalThis.__esmHookError = function hookError(payload, bag) {
    var d = jsonParse(payload);
    // A symbol a hook threw.
    if (d.symbol === true) return d.description === null ? NativeSymbol() : NativeSymbol(d.description);
    var props = d.props || {};
    if (bag !== undefined && bag !== null) props = bag.props || {};
    var Ctor = (typeof d.ctor === 'string' && apply(hasOwn, hookCtors, [d.ctor])) ? hookCtors[d.ctor] : NativeError;
    var err = objectCreate(Ctor.prototype);
    own(err, 'name', d.name, false);
    own(err, 'message', d.message, false);
    own(err, 'stack', d.stack, false);
    if (bag && bag.hasCause) {
      var causeError = bag.causeError;
      own(err, 'cause', causeError ? hookError(causeError.payload, causeError.bag) : bag.cause, bag.causeEnumerable === true);
    }
    var keys = objectKeys(props);
    for (var i = 0; i < keys.length; i++) own(err, keys[i], props[keys[i]], true);
    return err;
  };

  // A promise rejected with a module graph's link error, so it is reported as an
  // uncaught exception, the way Node reports one.
  globalThis.__esmRejected = function (err) {
    return reject(err);
  };

  // A file's JSON value, made once and shared by every module job for it (one
  // per import type), as Node shares it through the CommonJS cache. Fixed on
  // the global object, so code can't replace or shadow it, and only callable
  // with the secret the loader puts in the JSON modules it generates (set
  // once, by __esmJSONSetup, before any program code runs).
  var jsonValues = new Map();
  var jsonHas = Map.prototype.has;
  var jsonGet = Map.prototype.get;
  var jsonSet = Map.prototype.set;
  var jsonToken = null;
  globalThis.__esmJSONSetup = function (token) {
    delete globalThis.__esmJSONSetup;
    if (jsonToken === null && typeof token === 'string' && token !== '') jsonToken = token;
  };
  defineProperty(globalThis, '__esmJSONValue', {
    value: function (token, path, make) {
      if (jsonToken === null || token !== jsonToken) throw new NativeTypeError('__esmJSONValue is internal to the module loader');
      if (apply(jsonHas, jsonValues, [path])) return apply(jsonGet, jsonValues, [path]);
      var value = make();
      apply(jsonSet, jsonValues, [path, value]);
      return value;
    },
    writable: false,
    enumerable: false,
    configurable: false,
  });

  globalThis.__esmFinishDynamicImport = function (evalPromise, ns) {
    return Promise.resolve(evalPromise).then(function () {
      return ns;
    });
  };
})();
