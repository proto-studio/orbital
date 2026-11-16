const log = globalThis.note || console.log;
// Work scheduled by an 'unhandledRejection' handler for a rejection from a listener runs,
// and 'beforeExit' is emitted again afterwards.
let n = 0;
process.on('unhandledRejection', (reason) => {
  log('unhandledRejection', reason);
  setTimeout(() => log('timer'), 1);
});
process.on('beforeExit', () => {
  log('beforeExit', n);
  if (n++ === 0) Promise.reject('from beforeExit');
});
