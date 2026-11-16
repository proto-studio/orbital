const log = globalThis.note || console.log;
process.on('beforeExit', () => log('beforeExit'));
process.on('exit', (code) => log('exit', code));
setTimeout(() => log('never'), 1);
process.exit(0);
