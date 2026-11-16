const log = globalThis.note || console.log;
// Rejections the main script left are reported before the first timer runs.
process.on('unhandledRejection', (e) => log('unhandledRejection', e.message));
Promise.reject(new Error('rej'));
setTimeout(() => log('timer'), 0);
{ const end = Date.now() + 5; while (Date.now() < end); }
