// Helper module in lib/utils/
// This should resolve 'logger' from lib/node_modules/logger
const logger = require('logger');

function processData(data) {
    logger.debug('Processing data: ' + data);
    return data.toUpperCase();
}

function formatOutput(text) {
    logger.trace('Formatting: ' + text);
    return '>>> ' + text + ' <<<';
}

module.exports = {
    processData,
    formatOutput,
    loggerUsed: logger.name
};
