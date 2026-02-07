// App module in lib/
// This should resolve 'logger' from lib/node_modules/logger (the closer one)
const logger = require('logger');
const helper = require('./utils/helper.js');

function run() {
    console.log('\n--- Running from lib/app.js ---');
    console.log('Logger being used:', logger.name);
    console.log('Logger level:', logger.level);
    
    logger.log('App started');
    logger.debug('Debug mode enabled');
    
    // Use helper (which also uses logger)
    const result = helper.processData('hello world');
    console.log('Processed result:', result);
    console.log('Helper used logger:', helper.loggerUsed);
}

module.exports = { run, loggerName: logger.name };
