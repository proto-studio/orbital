const log = globalThis.note || console.log;
// While something else keeps the loop alive, an unreferenced immediate runs.
setImmediate(() => log('unref immediate')).unref();
setTimeout(() => log('timer'), 5);
