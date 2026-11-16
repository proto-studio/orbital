const log = globalThis.note || console.log;
process.on('uncaughtException', (e) => {
  log('caught', e.message);
  Promise.resolve().then(() => log('reaction'));
  process.exit(0);
});
setTimeout(() => {
  Promise.resolve().then(() => log('timer reaction'));
  throw new Error('boom');
}, 1);
