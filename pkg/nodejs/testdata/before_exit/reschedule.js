const log = globalThis.note || console.log;
let rounds = 0;
process.on('beforeExit', (code) => {
  log('beforeExit', code, rounds);
  if (rounds++ < 2) setTimeout(() => log('timer', rounds), 1);
  else if (rounds === 3) setImmediate(() => { log('immediate'); rounds++; });
});
