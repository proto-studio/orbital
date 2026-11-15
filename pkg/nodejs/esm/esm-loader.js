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
  globalThis.__esmModuleError = function (message, code, url) {
    var err = new Error(message);
    if (code) {
      var stack = err.stack;
      if (typeof stack === 'string' && stack.indexOf('Error: ') === 0) {
        err.stack = 'Error [' + code + ']: ' + stack.slice(7);
      }
      err.code = code;
    }
    if (url) err.url = url;
    return err;
  };

  globalThis.__esmFinishDynamicImport = function (evalPromise, ns) {
    return Promise.resolve(evalPromise).then(function () {
      return ns;
    });
  };
})();
