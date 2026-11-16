const log = globalThis.note || console.log;
// clearImmediate stops an immediate later in the same batch.
let b;
setImmediate(() => { log('a'); clearImmediate(b); });
b = setImmediate(() => log('b'));
setImmediate(() => log('c'));
