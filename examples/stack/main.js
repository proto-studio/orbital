// main.js - Stack trace test
// Run with: orbital examples/stack/main.js

const helper = require('./helper.js');

function main() {
    console.log('main.js: Starting stack trace test\n');
    
    try {
        outerFunction(5);
    } catch (err) {
        console.log('\n--- Caught Exception ---');
        console.log('Message:', err.message);
        console.log('\nStack trace:');
        console.log(err.stack);
    }
}

function outerFunction(n) {
    console.log('main.js: outerFunction called with', n);
    return middleFunction(n + 1);
}

function middleFunction(n) {
    console.log('main.js: middleFunction called with', n);
    return innerFunction(n + 1);
}

function innerFunction(n) {
    console.log('main.js: innerFunction called with', n);
    // Call into the helper module
    return helper.helperLevel1(n + 1);
}

// Run it
main();
