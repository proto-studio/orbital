const log = globalThis.note || console.log;
// An unreferenced immediate doesn't keep the loop alive, and doesn't run once it is idle.
setImmediate(() => log('unref immediate')).unref();
log('main');
