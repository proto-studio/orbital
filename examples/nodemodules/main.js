// Main entry point - demonstrates node_modules resolution
// Run with: ./orbital examples/nodemodules/main.js
//
// Directory structure:
// examples/nodemodules/
// ├── main.js                      <- YOU ARE HERE
// ├── node_modules/
// │   └── logger/
// │       └── index.js             <- "RootLogger" (used by main.js)
// └── lib/
//     ├── app.js                   <- Uses lib/node_modules/logger
//     ├── node_modules/
//     │   └── logger/
//     │       └── index.js         <- "LibLogger" (used by lib/*.js)
//     └── utils/
//         └── helper.js            <- Uses lib/node_modules/logger (walks up from utils/)
//
// This demonstrates that node_modules resolution starts from the
// importing file's directory and walks UP the tree.

console.log('=== Node Modules Resolution Demo ===\n');

// main.js requires 'logger' - should get ./node_modules/logger (RootLogger)
const logger = require('logger');

console.log('--- From main.js ---');
console.log('Logger name:', logger.name);
console.log('Logger level:', logger.level);
logger.log('Hello from main!');
logger.info('This is an info message');

// Now require lib/app.js which uses its own closer logger
const app = require('./lib/app.js');

console.log('\n--- Verifying resolution ---');
console.log('main.js uses:', logger.name);
console.log('lib/app.js uses:', app.loggerName);

// Run the app
app.run();

console.log('\n=== Resolution Summary ===');
console.log('✓ main.js -> node_modules/logger (RootLogger)');
console.log('✓ lib/app.js -> lib/node_modules/logger (LibLogger)');
console.log('✓ lib/utils/helper.js -> lib/node_modules/logger (LibLogger)');
console.log('  (helper.js walks UP from lib/utils/ to find lib/node_modules/)');

console.log('\n=== Demo Complete ===');
