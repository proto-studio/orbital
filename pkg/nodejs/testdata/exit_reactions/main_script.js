const log = globalThis.note || console.log;
Promise.resolve().then(() => log('reaction'));
queueMicrotask(() => log('queueMicrotask'));
process.nextTick(() => log('tick'));
process.on('exit', (code) => {
  log('exit', code);
  Promise.resolve().then(() => log('reaction from exit listener'));
});
log('before exit');
process.exit(0);
