const log = globalThis.note || console.log;
let n = 0;
process.on('beforeExit', () => {
  log('beforeExit', n++);
  setTimeout(() => log('timer'), 1);
  process.exit(5);
});
process.on('exit', (code) => log('exit', code));
