const log = globalThis.note || console.log;
// A nextTick or promise reaction runs after the listener, but doesn't keep the loop alive.
process.on('beforeExit', () => {
  log('beforeExit');
  process.nextTick(() => log('tick'));
  Promise.resolve().then(() => log('promise'));
});
