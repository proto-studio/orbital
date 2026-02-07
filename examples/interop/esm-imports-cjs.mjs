// ESM importing CommonJS module
// In Node.js, this works - the module.exports becomes the default export

import cjsModule from './cjs-module.js';

console.log('=== ESM importing CommonJS ===\n');
console.log('Imported module:', cjsModule);
console.log('greeting:', cjsModule.greeting);
console.log('add(2, 3):', cjsModule.add(2, 3));
console.log('multiply(4, 5):', cjsModule.multiply(4, 5));
