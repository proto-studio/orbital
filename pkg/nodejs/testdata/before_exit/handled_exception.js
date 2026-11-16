const log = globalThis.note || console.log;
process.on('uncaughtException', (e) => log('caught', e.message));
process.on('beforeExit', () => log('beforeExit'));
setTimeout(() => { throw new Error('in timer'); }, 1);
