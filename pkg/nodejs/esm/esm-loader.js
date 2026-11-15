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
  var reject = Promise.reject.bind(Promise);

  globalThis.__esmModuleError = function (message, code, url, isTypeError) {
    var err = isTypeError ? new NativeTypeError(message) : new NativeError(message);
    if (code) {
      var name = isTypeError ? 'TypeError' : 'Error';
      var stack = err.stack;
      if (typeof stack === 'string' && stack.indexOf(name + ': ') === 0) {
        err.stack = name + ' [' + code + ']: ' + stack.slice(name.length + 2);
      }
      err.code = code;
    }
    if (url) err.url = url;
    return err;
  };

  // A promise rejected with a module graph's link error, so it is reported as an
  // uncaught exception, the way Node reports one.
  globalThis.__esmRejected = function (err) {
    return reject(err);
  };

  globalThis.__esmFinishDynamicImport = function (evalPromise, ns) {
    return Promise.resolve(evalPromise).then(function () {
      return ns;
    });
  };
})();
