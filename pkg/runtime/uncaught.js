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

  const StringCtor = String;

  // offsetOf is how many characters of a CommonJS module's wrapper come before its source
  // on the first line of the script named file (see Runtime.SetFirstLineOffset), or 0.
  function offsetOf(file) {
    try {
      const offset = native.keptOffset(file);
      return typeof offset === 'number' ? offset : 0;
    } catch (_) {
      return 0;
    }
  }

  // readSource is the text the runtime compiled under a frame's name (see
  // Runtime.keepSource), or null. Nothing is read from files.
  function readSource(file) {
    try {
      const kept = native.keptSource(file);
      return typeof kept === 'string' ? kept : null;
    } catch (_) {
      return null;
    }
  }

  // The header is built only with methods captured here, before any script runs, so a
  // script that replaces String or RegExp methods can't change it.
  const charCodeAt = String.prototype.charCodeAt;
  const strSlice = String.prototype.slice;
  const strIndexOf = String.prototype.indexOf;
  const regexpExec = RegExp.prototype.exec;
  const NumberCtor = Number;
  const len = (str) => str.length;
  const code = (str, i) => apply(charCodeAt, str, [i]);
  const cut = (str, from, to) => apply(strSlice, str, to === undefined ? [from] : [from, to]);

  // lineOf is the 1-based line n of text, counting every JavaScript line terminator as V8
  // does, or undefined.
  function lineOf(text, n) {
    let line = 1;
    let start = 0;
    const end = len(text);
    for (let i = 0; i < end; i++) {
      const c = code(text, i);
      if (c !== 10 && c !== 13 && c !== 0x2028 && c !== 0x2029) continue;
      if (line === n) return cut(text, start, i);
      if (c === 13 && i + 1 < end && code(text, i + 1) === 10) i++;
      line++;
      start = i + 1;
    }
    return line === n ? cut(text, start) : undefined;
  }

  // untilNul is line up to a NUL, as Node prints it as a C string. A module's NULs are
  // kept as another character, nulMark, chosen not to appear in its source.
  function untilNul(line, nulMark) {
    const mark = typeof nulMark === 'string' && len(nulMark) === 1 ? code(nulMark, 0) : -1;
    for (let i = 0; i < len(line); i++) {
      const c = code(line, i);
      if (c === 0 || c === mark) return cut(line, 0, i);
    }
    return line;
  }

  function nulMarkOf(file) {
    try {
      return native.keptNul(file);
    } catch (_) {
      return '';
    }
  }

  const isIdentChar = (c) => (c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) ||
    c === 36 || c === 95 || (c > 127 && c !== 0xFEFF && c !== 0xA0 && c !== 0x2028 && c !== 0x2029);

  // throwBefore is where a throw keyword starts if only spaces separate it from column, or
  // -1. A linear scan.
  function throwBefore(line, column) {
    let i = column - 1;
    while (i >= 0) {
      const c = code(line, i);
      if (c === 32 || c === 9 || c === 11 || c === 12 || c === 0xA0) i--;
      else break;
    }
    if (i === column - 1 || i < 4) return -1;
    if (cut(line, i - 4, i + 1) !== 'throw') return -1;
    if (i - 5 >= 0 && isIdentChar(code(line, i - 5))) return -1;
    return i - 4;
  }

  // places lists the "file:line:column" candidates of one stack frame: a frame is
  // "name (place)" or just "place", and either part can hold " (", as in
  // "/tmp/app (copy)/main.js", so each split is a candidate, the first split first.
  function places(at) {
    const out = [];
    if (len(at) > 0 && code(at, len(at) - 1) === 41) {
      for (let i = apply(strIndexOf, at, [' (']); i !== -1; i = apply(strIndexOf, at, [' (', i + 1])) {
        out[out.length] = cut(at, i + 2, len(at) - 1);
      }
    }
    out[out.length] = at;
    return out;
  }

  // errorSource is the header Node prints above a fatal exception: the file and line, the
  // line itself, and a caret under where it was thrown. Node takes the place from V8's
  // message for the throw; here it comes from the error's first stack frame in a script,
  // which is where the error was made. For an exception thrown where it was made, as by
  // the usual `throw new Error(...)`, the caret moves back to the throw, where Node puts
  // it. A rejection has no throw to point at, and Node marks where the error was made.
  function errorSource(err, thrown) {
    if (typeof err !== 'object' || err === null) return '';
    let stack;
    try {
      stack = err.stack;
    } catch (_) {
      return '';
    }
    if (typeof stack !== 'string') return '';
    // Frames of built-in functions, such as "at decodeURIComponent (<anonymous>)", come
    // before the script's own and have no place.
    let file = null;
    let line = 0;
    let column = 0;
    let text = null;
    let placed = false;
    let start = apply(strIndexOf, stack, ['\n']);
    while (start !== -1 && !placed) {
      const next = apply(strIndexOf, stack, ['\n', start + 1]);
      let frame = cut(stack, start + 1, next === -1 ? undefined : next);
      start = next;
      let k = 0;
      while (k < len(frame) && (code(frame, k) === 32 || code(frame, k) === 9)) k++;
      if (cut(frame, k, k + 3) !== 'at ') continue;
      frame = cut(frame, k + 3);
      const candidates = places(frame);
      for (let i = 0; i < candidates.length && text === null; i++) {
        const place = apply(regexpExec, /^(.+):(\d+):(\d+)$/, [candidates[i]]);
        if (!place) continue;
        placed = true;
        text = readSource(place[1]);
        file = place[1];
        line = NumberCtor(place[2]);
        column = NumberCtor(place[3]) - 1;
      }
    }
    if (text === null) return '';
    let sourceLine = lineOf(text, line);
    if (sourceLine === undefined) return '';
    sourceLine = untilNul(sourceLine, nulMarkOf(file));
    const offset = line === 1 ? offsetOf(file) : 0;
    if (offset > 0) {
      column -= offset;
      // The CommonJS loader compiles a module without its BOM, which the file still has
      // and which Node counts on the line.
      if (code(text, 0) === 0xFEFF) column++;
      if (column < 0) return '';
    }
    if (thrown) {
      const at = throwBefore(sourceLine, column);
      if (at !== -1) column = at;
    }
    let caret = '';
    for (let i = 0; i < column && i < len(sourceLine); i++) {
      caret += code(sourceLine, i) === 9 ? '\t' : ' ';
    }
    return file + ':' + line + '\n' + sourceLine + '\n' + caret + '^\n';
  }

  function die(err, code, origin, handlerThrew) {
    dead = true;
    // The header is only a help: nothing here may stop the report.
    let source = '';
    try {
      source = StringCtor(errorSource(err, origin === 'uncaughtException' || !!handlerThrew));
    } catch (_) {
      source = '';
    }
    native.fatalSource(source);
    native.fatal(format(err), code, origin, !!handlerThrew, err);
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

  // ---- process.nextTick ----------------------------------------------------------------
  // Node's lib/internal/process/task_queues.js. When the outermost callback into JavaScript
  // returns, the tick queue runs, then the microtask (promise) queue, and again until both are
  // empty, so a tick runs ahead of promise reactions queued by the same code. V8 also runs its
  // microtasks by itself once the stack empties, which is why the ticks run before that.

  // A null prototype keeps setters or species a script adds to Array out of the queue.
  const tickQueue = [];
  Object.setPrototypeOf(tickQueue, null);
  const kCompactAfter = 1024;
  let tickHead = 0;
  // depth counts the callbacks into JavaScript on the stack; only the outermost one runs
  // the queues, as in Node's InternalCallbackScope.
  let depth = 0;
  let processingTicks = false;
  // A tick queued with no callback on the stack, as by a script an embedder runs, has
  // nothing to run it on return. The event loop does then.
  let loopScheduled = false;

  function nextTick(callback, ...args) {
    if (typeof callback !== 'function') {
      const err = new TypeError('The "callback" argument must be of type function. Received ' +
        describeReceived(callback));
      err.code = 'ERR_INVALID_ARG_TYPE';
      throw err;
    }
    const p = getProcess();
    if (dead || (p && p._exiting)) return;
    tickQueue[tickQueue.length] = { callback, args };
    if (!loopScheduled) {
      loopScheduled = true;
      native.scheduleTicks();
    }
  }

  function describeReceived(value) {
    if (value === null || value === undefined) return String(value);
    if (typeof value === 'object') {
      const ctor = value.constructor;
      return ctor && ctor.name ? 'an instance of ' + ctor.name : inspect(value);
    }
    let shown = inspect(value);
    if (shown.length > 28) shown = shown.slice(0, 25) + '...';
    return 'type ' + typeof value + ' (' + shown + ')';
  }

  // processTicks is processTicksAndRejections, less the rejections, which the runtime
  // reports once its queues have drained.
  function processTicks() {
    if (processingTicks) return;
    processingTicks = true;
    try {
      do {
        while (tickHead < tickQueue.length && !dead) {
          const tick = tickQueue[tickHead];
          tickQueue[tickHead++] = undefined;
          // A tick that queues the next one keeps the queue growing at its end while it
          // drains from the front, so drop the used half once it is large.
          if (tickHead >= kCompactAfter && tickHead * 2 >= tickQueue.length) {
            const live = tickQueue.length - tickHead;
            for (let i = 0; i < live; i++) tickQueue[i] = tickQueue[tickHead + i];
            tickQueue.length = live;
            tickHead = 0;
          }
          try {
            apply(tick.callback, undefined, tick.args);
          } catch (err) {
            trigger(err, false);
          }
        }
        tickQueue.length = 0;
        tickHead = 0;
        if (dead) return;
        native.runMicrotasks();
      } while (tickQueue.length > 0 && !dead);
    } finally {
      processingTicks = false;
    }
  }

  // runScheduledTicks is the event loop's turn for ticks no callback has run.
  function runScheduledTicks() {
    loopScheduled = false;
    if (depth === 0) processTicks();
  }

  // runMain runs the main script as the outermost callback.
  function runMain(body) {
    depth++;
    try {
      body();
    } finally {
      depth--;
    }
    if (depth === 0 && !dead) processTicks();
  }

  // queueMicrotask queues a V8 microtask, as Node's does, so it runs in order with promise
  // reactions. An exception goes to 'uncaughtException' rather than rejecting anything.
  // Awaiting a value that is not a promise queues exactly one microtask through V8's own
  // promise, so nothing a script does to Promise, its species or then can get in the way.
  async function runMicrotask(callback) {
    await undefined;
    try {
      apply(callback, undefined, []);
    } catch (err) {
      trigger(err, false);
    }
  }
  function queueMicrotask(callback) {
    if (typeof callback !== 'function') {
      const err = new TypeError('The "callback" argument must be of type function. Received ' +
        describeReceived(callback));
      err.code = 'ERR_INVALID_ARG_TYPE';
      throw err;
    }
    runMicrotask(callback);
  }

  // callback enters JavaScript from the event loop. Go only sees an exception's text, so
  // catching it here is what gives the handlers the thrown value itself. The outermost one
  // runs the tick and microtask queues before returning, as node::MakeCallback does.
  function callback(fn, recv, ...args) {
    let result;
    depth++;
    try {
      result = apply(fn, recv, args);
    } catch (err) {
      trigger(err, false);
      result = undefined;
    } finally {
      depth--;
    }
    if (depth === 0 && !dead) processTicks();
    return result;
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

  // activeDomain is the domain module's process.domain, if a domain is active: Node
  // reports an unhandled rejection to the domain that was active when the promise was
  // rejected.
  // A promise then() returned while a domain was active belongs to that domain even if it
  // rejects later; the domain module keeps track of those promises. Any other rejection
  // belongs to the domain active when it happens.
  function activeDomain(promise) {
    try {
      // The promise's own domain comes first: by the time V8 reports the rejection the
      // reaction has returned, and whatever domain is active is an outer one.
      let d = null;
      const dm = globalThis.__domain_module;
      if (dm && typeof dm._rejectionDomainFor === 'function') d = dm._rejectionDomainFor(promise);
      if (!d) {
        const p = getProcess();
        d = p ? p.domain : null;
      }
      return d && typeof d === 'object' && typeof d.emit === 'function' ? d : null;
    } catch (_) {
      return null;
    }
  }

  // How an unhandled rejection is treated: --unhandled-rejections.
  const mode = native.unhandledRejections || 'throw';
  let lastRejectionId = 0;

  function PromiseRejectionHandledWarning(uid) {
    const warning = new Error('Promise rejection was handled asynchronously (rejection id: ' + uid + ')');
    warning.name = 'PromiseRejectionHandledWarning';
    warning.id = uid;
    return warning;
  }

  // noSideEffectsToString is how Node names a rejection reason in a warning without
  // running any of the reason's own code.
  function noSideEffectsToString(value) {
    if (typeof value === 'symbol') return apply(Symbol.prototype.toString, value, []);
    if ((typeof value !== 'object' && typeof value !== 'function') || value === null) return String(value);
    try {
      if (Array.isArray(value)) return '[object Array]';
      // A proxy can make its prototype chain endless, so the walk is bounded.
      let o = value;
      for (let depth = 0; o !== null && depth < 64; depth++) {
        const desc = Object.getOwnPropertyDescriptor(o, 'constructor');
        if (desc && typeof desc.value === 'function') {
          const name = Object.getOwnPropertyDescriptor(desc.value, 'name');
          if (name && typeof name.value === 'string' && name.value !== '') return '#<' + name.value + '>';
          break;
        }
        o = Object.getPrototypeOf(o);
      }
    } catch (_) {
      // A proxy's trap threw: fall back to the plain name.
    }
    return '[object Object]';
  }

  function emitWarning(p, warning, type) {
    if (p && typeof p.emitWarning === 'function') p.emitWarning(warning, type);
  }

  // Node's emitUnhandledRejectionWarning: the reason, then what to do about it.
  // Node's isErrorStackTraceLimitWritable: a program can freeze Error or the property.
  function stackTraceLimitWritable() {
    const desc = Object.getOwnPropertyDescriptor(Error, 'stackTraceLimit');
    if (desc === undefined) return Object.isExtensible(Error);
    return apply(hasOwn, desc, ['writable']) ? desc.writable : desc.set !== undefined;
  }

  function warnUnhandled(p, uid, reason) {
    const limit = Error.stackTraceLimit;
    const writable = stackTraceLimitWritable();
    if (writable) Error.stackTraceLimit = 0;
    const warning = new Error(
      'Unhandled promise rejection. This error originated either by throwing inside of an ' +
      'async function without a catch block, or by rejecting a promise which was not handled ' +
      'with .catch(). To terminate the node process on unhandled promise rejection, use the ' +
      'CLI flag `--unhandled-rejections=strict` (see ' +
      'https://nodejs.org/api/cli.html#cli_unhandled_rejections_mode). (rejection id: ' + uid + ')');
    if (writable) Error.stackTraceLimit = limit;
    Object.defineProperty(warning, 'name', {
      value: 'UnhandledPromiseRejectionWarning', enumerable: false, writable: true, configurable: true,
    });
    try {
      if (isErrorLike(reason)) {
        warning.stack = reason.stack;
        emitWarning(p, reason.stack, 'UnhandledPromiseRejectionWarning');
      } else {
        emitWarning(p, noSideEffectsToString(reason), 'UnhandledPromiseRejectionWarning');
      }
    } catch (_) {
      try {
        emitWarning(p, noSideEffectsToString(reason), 'UnhandledPromiseRejectionWarning');
      } catch (_) {
        // Nothing more can be said about it.
      }
    }
    emitWarning(p, warning);
  }

  // reportUnhandled is the switch in Node's processPromiseRejections.
  function reportUnhandled(p, info, promise) {
    const reason = info.reason;
    // As in Node, the domain the promise belongs to takes the event as 'error'; with no
    // listener there, emit throws, and the reason becomes an uncaught exception.
    const emit = () => {
      if (info.domain) return info.domain.emit('error', reason);
      return p ? p.emit('unhandledRejection', reason, promise) : false;
    };
    const fatal = () => (isErrorLike(reason) ? reason : unhandledRejectionError(reason));
    switch (mode) {
      case 'strict':
        trigger(fatal(), true);
        if (!dead && !emit()) warnUnhandled(p, info.uid, reason);
        break;
      case 'none':
        emit();
        break;
      case 'warn':
        emit();
        warnUnhandled(p, info.uid, reason);
        break;
      case 'warn-with-error-code':
        if (!emit()) {
          warnUnhandled(p, info.uid, reason);
          if (p) p.exitCode = kGenericUserError;
        }
        break;
      default:
        if (!emit()) trigger(fatal(), true);
    }
  }

  function promiseReject(type, promise, reason) {
    if (type === kPromiseRejectWithNoHandler) {
      const info = { reason, reported: false, handled: false, domain: activeDomain(promise), uid: ++lastRejectionId };
      tracked.set(promise, info);
      pendingRejections[pendingRejections.length] = { promise, info };
      native.rejectionsPending();
    } else if (type === kPromiseHandlerAddedAfterReject) {
      const info = tracked.get(promise);
      if (!info) return;
      tracked.delete(promise);
      info.handled = true;
      if (info.reported) {
        // Made now, as Node does, so its stack shows where the handler was added.
        handledLate[handledLate.length] = { promise, warning: PromiseRejectionHandledWarning(info.uid) };
        native.rejectionsPending();
      }
    }
  }

  function processRejections() {
    const p = getProcess();
    while (handledLate.length > 0 && !dead) {
      const { promise, warning } = apply(shift, handledLate, []);
      try {
        if (!p || !p.emit('rejectionHandled', promise)) emitWarning(p, warning);
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
        reportUnhandled(p, info, promise);
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
    process.nextTick = nextTick;
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

  // beforeExit emits process 'beforeExit' when the event loop has run out of work, as Node
  // does, with process.exitCode (or 0) as its argument. It isn't emitted once the process
  // is exiting.
  function beforeExit() {
    const p = getProcess();
    if (dead || !p || p._exiting) return;
    // Node passes a number even when exitCode was set to a numeric string.
    p.emit('beforeExit', Number(p.exitCode) || 0);
  }

  return {
    callback, trigger, rebuildThrown, promiseReject, processRejections, bindProcess, markHandled,
    watchModule, runMain, runScheduledTicks, queueMicrotask, beforeExit,
  };
})
