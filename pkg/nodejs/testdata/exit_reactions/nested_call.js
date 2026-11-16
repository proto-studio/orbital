const log = globalThis.note || console.log;
// process.exit deep in a call stack, with reactions queued from several levels.
function inner() {
  Promise.resolve().then(() => log('inner reaction'));
  process.exit(0);
  log('after exit');
}
function outer() {
  Promise.resolve().then(() => log('outer reaction'));
  try {
    inner();
  } finally {
    log('finally');
  }
}
setImmediate(outer);
