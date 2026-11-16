const log = globalThis.note || console.log;
const spin = (ms) => { const end = Date.now() + ms; while (Date.now() < end); };
// Immediates queued by an I/O callback run before any timer, and one queued by another
// immediate waits for the next turn, after timers that are due by then.
require('fs').stat(typeof __filename === 'string' ? __filename : '.', () => {
  setTimeout(() => log('timeout'), 0);
  setImmediate(() => {
    log('immediate 1');
    spin(3);
    setImmediate(() => log('immediate 3, next turn'));
  });
  setImmediate(() => log('immediate 2'));
});
