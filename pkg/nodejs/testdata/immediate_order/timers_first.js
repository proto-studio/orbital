const log = globalThis.note || console.log;
const spin = (ms) => { const end = Date.now() + ms; while (Date.now() < end); };
// Every due timer runs before the immediates, including ones queued by those timers.
setTimeout(() => { log('timer a'); setImmediate(() => log('immediate from a')); }, 1);
setTimeout(() => log('timer b'), 1);
setImmediate(() => log('immediate from main'));
spin(5);
