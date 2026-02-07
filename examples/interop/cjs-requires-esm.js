// CommonJS trying to require ESM
// In Node.js, this throws an error: "require() of ES Module ... not supported"

console.log('=== CommonJS requiring ESM ===\n');

try {
    const esmModule = require('./esm-module.mjs');
    console.log('This should NOT succeed in Node.js-compatible behavior');
    console.log('Imported:', esmModule);
} catch (e) {
    console.log('Expected error:', e.message);
    console.log('\nIn Node.js, you must use dynamic import() instead:');
    console.log('  const mod = await import("./esm-module.mjs")');
}
