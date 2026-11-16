const log = globalThis.note || console.log;
// The listener gets a number, even when exitCode was set to a numeric string.
process.exitCode = '0';
process.on('beforeExit', (code) => log('beforeExit', typeof code, code, code === 0));
