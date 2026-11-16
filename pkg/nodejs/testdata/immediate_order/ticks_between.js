const log = globalThis.note || console.log;
// Between two immediates, process.nextTick callbacks and promise reactions run.
setImmediate(() => {
  log('immediate a');
  process.nextTick(() => log('tick a'));
  Promise.resolve().then(() => log('promise a'));
});
setImmediate(() => log('immediate b'));
