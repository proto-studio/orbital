const log = globalThis.note || console.log;
setTimeout(() => {
  Promise.resolve().then(() => log('reaction'));
  log('timer');
  process.exit(3);
}, 1);
setTimeout(() => log('later timer'), 20);
