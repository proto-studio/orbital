const log = globalThis.note || console.log;
Promise.resolve().then(() => { log('first'); process.exit(0); });
Promise.resolve().then(() => log('second'));
