// Node's uncaught exception and unhandled rejection handling, following
// lib/internal/process/execution.js and lib/internal/process/promises.js.
//
// Evaluates to a factory. The runtime calls it once with its native functions and keeps
// the returned object, so nothing here is reachable from a script's globals. Everything a
// script could replace is captured up front.
(function (native) {
  'use strict';

  const apply = Reflect.apply;
  const hasOwn = Object.prototype.hasOwnProperty;
  const shift = Array.prototype.shift;
  const NewWeakMap = WeakMap;
  const then = Promise.prototype.then;

  const kGenericUserError = 1;
  const kExceptionInFatalExceptionHandler = 7;

  let captureFn = null;
  let dead = false;

  function getProcess() {
    const p = globalThis.process;
    return p && typeof p.emit === 'function' ? p : null;
  }

  function inspect(value) {
    try {
      if (typeof globalThis.require === 'function') {
        return globalThis.require('util').inspect(value);
      }
    } catch (_) {
      // Fall through: the text is all that is left to report.
    }
    if (value && typeof value.stack === 'string') return value.stack;
    return String(value);
  }

  // format is the text Node prints for a fatal exception, without its source-line header.
  function format(err) {
    try {
      return typeof err === 'string' ? err : inspect(err);
    } catch (_) {
      return '[exception could not be formatted]';
    }
  }

  function exitCodeOf(p, fallback) {
    if (!p || p.exitCode === undefined || p.exitCode === null) return fallback;
    const code = Number(p.exitCode);
    return Number.isNaN(code) ? fallback : code;
  }

  function die(err, code, origin, handlerThrew) {
    dead = true;
    native.fatal(format(err), code, origin, !!handlerThrew);
  }

  // trigger is process._fatalException plus its native caller: run the handlers, and if
  // none takes the exception, stop the runtime.
  function trigger(err, fromPromise) {
    if (dead) return;
    const p = getProcess();
    const origin = fromPromise ? 'unhandledRejection' : 'uncaughtException';
    try {
      if (p) p.emit('uncaughtExceptionMonitor', err, origin);
      if (captureFn !== null) {
        captureFn(err);
        return;
      }
      if (p && p.emit('uncaughtException', err, origin)) return;
    } catch (fromHandler) {
      // Node reports the handler's exception, exits 7, and does not emit 'exit'.
      die(fromHandler, kExceptionInFatalExceptionHandler, origin, true);
      return;
    }
    try {
      if (p && !p._exiting) {
        p._exiting = true;
        p.exitCode = kGenericUserError;
        p.emit('exit', kGenericUserError);
      }
    } catch (_) {
      // Nothing can be done about an 'exit' listener that throws at this point.
    }
    die(err, exitCodeOf(p, kGenericUserError), origin);
  }

  // callback enters JavaScript from the event loop. Go only sees an exception's text, so
  // catching it here is what gives the handlers the thrown value itself.
  function callback(fn, recv, ...args) {
    try {
      return apply(fn, recv, args);
    } catch (err) {
      trigger(err, false);
      return undefined;
    }
  }

  // rebuildThrown recovers a main-script exception from its text. It only runs when the
  // V8 glue cannot hand over the thrown value: see Runtime.reportScriptError.
  function rebuildThrown(text) {
    const newline = text.indexOf('\n');
    const message = newline === -1 ? text : text.slice(0, newline);
    const match = /^([A-Za-z]*Error): ([\s\S]*)$/.exec(message);
    const named = /^[A-Za-z]*Error$/.test(message) ? [message, message, ''] : match;
    if (!named) return message;
    const Ctor = typeof globalThis[named[1]] === 'function' ? globalThis[named[1]] : Error;
    const err = new Ctor(named[2]);
    let stack = newline === -1 ? '' : text.slice(newline + 1);
    if (stack.startsWith(message + '\n')) stack = stack.slice(message.length + 1);
    err.stack = message + (stack ? '\n' + stack : '');
    return err;
  }

  function isErrorLike(value) {
    return typeof value === 'object' && value !== null && apply(hasOwn, value, ['stack']);
  }

  function unhandledRejectionError(reason) {
    const err = new Error(
      'This error originated either by throwing inside of an async function without a ' +
      'catch block, or by rejecting a promise which was not handled with .catch(). The ' +
      'promise rejected with the reason "' + (typeof reason === 'string' ? reason : inspect(reason)) + '".');
    err.name = 'UnhandledPromiseRejection';
    err.code = 'ERR_UNHANDLED_REJECTION';
    return err;
  }

  const kPromiseRejectWithNoHandler = 0;
  const kPromiseHandlerAddedAfterReject = 1;

  // V8 reports a rejection as it happens, before the code that rejected had any chance to
  // attach a handler, so a rejection only counts once the microtask queue has drained.
  const tracked = new NewWeakMap();
  const pendingRejections = [];
  const handledLate = [];

  function promiseReject(type, promise, reason) {
    if (type === kPromiseRejectWithNoHandler) {
      const info = { reason, reported: false, handled: false };
      tracked.set(promise, info);
      pendingRejections[pendingRejections.length] = { promise, info };
      native.rejectionsPending();
    } else if (type === kPromiseHandlerAddedAfterReject) {
      const info = tracked.get(promise);
      if (!info) return;
      tracked.delete(promise);
      info.handled = true;
      if (info.reported) {
        handledLate[handledLate.length] = promise;
        native.rejectionsPending();
      }
    }
  }

  function processRejections() {
    const p = getProcess();
    while (handledLate.length > 0 && !dead) {
      const promise = apply(shift, handledLate, []);
      try {
        if (p) p.emit('rejectionHandled', promise);
      } catch (err) {
        trigger(err, false);
      }
    }
    // Only what was pending on entry: a rejection a listener makes must wait for the queues
    // to drain, like any other, before it counts as unhandled.
    let batch = pendingRejections.length;
    while (batch-- > 0 && !dead) {
      const { promise, info } = apply(shift, pendingRejections, []);
      if (info.handled) continue;
      info.reported = true;
      try {
        if (!p || !p.emit('unhandledRejection', info.reason, promise)) {
          trigger(isErrorLike(info.reason) ? info.reason : unhandledRejectionError(info.reason), true);
        }
      } catch (err) {
        trigger(err, false);
      }
    }
  }

  function setUncaughtExceptionCaptureCallback(fn) {
    if (fn === null) {
      captureFn = null;
      return;
    }
    if (typeof fn !== 'function') {
      const err = new TypeError('The "fn" argument must be of type function or null.');
      err.code = 'ERR_INVALID_ARG_TYPE';
      throw err;
    }
    if (captureFn !== null) {
      const err = new Error('`process.setupUncaughtExceptionCapture()` was called while a capture callback was already active');
      err.code = 'ERR_UNCAUGHT_EXCEPTION_CAPTURE_ALREADY_SET';
      throw err;
    }
    captureFn = fn;
  }

  function bindProcess(process) {
    process.setUncaughtExceptionCaptureCallback = setUncaughtExceptionCaptureCallback;
    process.hasUncaughtExceptionCaptureCallback = function hasUncaughtExceptionCaptureCallback() {
      return captureFn !== null;
    };
  }

  function markHandled(promise) {
    apply(then, promise, [undefined, function () {}]);
  }

  // watchModule is what Node's ESM loader does with a module's evaluation: a rejection goes
  // straight to the fatal path, so 'unhandledRejection' never sees it.
  function watchModule(promise) {
    apply(then, promise, [undefined, function (err) { trigger(err, true); }]);
  }

  return { callback, trigger, rebuildThrown, promiseReject, processRejections, bindProcess, markHandled, watchModule };
})
