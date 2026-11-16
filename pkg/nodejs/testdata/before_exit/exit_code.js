const log = globalThis.note || console.log;
process.exitCode = 3;
process.on('beforeExit', (code) => log('beforeExit', code, process.exitCode));
