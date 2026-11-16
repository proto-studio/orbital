const log = globalThis.note || console.log;
process.on('beforeExit', (code) => log('beforeExit', code));
setTimeout(() => log('timer'), 1);
log('main');
