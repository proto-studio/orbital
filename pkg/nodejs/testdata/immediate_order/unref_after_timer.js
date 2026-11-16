const log = globalThis.note || console.log;
// An unreferenced immediate still runs in the turn that a due timer kept alive.
setImmediate(() => log('unref immediate')).unref();
setTimeout(() => log('timeout'), 1);
{ const end = Date.now() + 5; while (Date.now() < end); }
