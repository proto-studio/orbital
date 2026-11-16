const log = globalThis.note || console.log;
(async () => {
  await null;
  log('after await');
  Promise.resolve().then(() => log('reaction'));
  process.exit(0);
})();
Promise.resolve().then(() => log('queued before'));
