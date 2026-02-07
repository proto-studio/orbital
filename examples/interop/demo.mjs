// Complete interop demonstration
// Run with: ./orbital examples/interop/demo.mjs

console.log('=== Module Interop Demo ===\n');

// ESM can import CommonJS modules
import cjsModule from './cjs-module.js';

console.log('1. ESM importing CommonJS (default import):');
console.log('   cjsModule.greeting:', cjsModule.greeting);
console.log('   cjsModule.add(10, 5):', cjsModule.add(10, 5));
console.log('   cjsModule.multiply(3, 7):', cjsModule.multiply(3, 7));

// ESM can also import other ESM
import esmModule, { greeting, subtract } from './esm-module.mjs';

console.log('\n2. ESM importing ESM:');
console.log('   Default import:', esmModule);
console.log('   Named import greeting:', greeting);
console.log('   Named import subtract(10, 3):', subtract(10, 3));

// Using require from ESM via Module.createRequire (Node.js pattern)
console.log('\n3. Using Module.createRequire() pattern:');
if (typeof Module !== 'undefined' && Module.createRequire) {
    const require = Module.createRequire(import.meta.url || '/');
    const path = require('path');
    console.log('   path.join("a", "b", "c"):', path.join('a', 'b', 'c'));
} else {
    console.log('   Module.createRequire not available');
}

console.log('\n=== Interop Demo Complete ===');
