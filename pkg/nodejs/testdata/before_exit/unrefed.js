const log = globalThis.note || console.log;
// An unref'd timer doesn't keep the loop alive, so it doesn't run after 'beforeExit'.
process.on('beforeExit', () => {
  log('beforeExit');
  setTimeout(() => log('unref timer'), 1).unref();
});
