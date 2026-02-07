// paths.js - Test that paths are correctly sandboxed
// 
// Run with sandbox:    orbital --root examples/stack paths.js
// Run without sandbox: orbital examples/stack/paths.js

const path = require('path');
const helper = require('./helper.js');

console.log('=== Path Sandboxing Test ===\n');

console.log('__filename:', __filename);
console.log('__dirname:', __dirname);
console.log('module.filename:', module.filename);
console.log('process.cwd():', process.cwd());

console.log('\n=== Verification ===\n');

// When run with --root, paths should NOT contain system paths
const hasSandboxedPaths = __dirname === '/' || __dirname === '.';
const hasSystemPaths = __dirname.includes('/Users/') || __dirname.includes('/home/');
const cwdSandboxed = process.cwd() === '/';

if (hasSandboxedPaths) {
    console.log('✓ __dirname is sandboxed');
    console.log(cwdSandboxed ? '✓ process.cwd() is sandboxed' : '✗ process.cwd() leaked system path');
} else if (hasSystemPaths) {
    console.log('✓ Running without sandbox (full system paths visible)');
} else {
    console.log('? Unknown path format:', __dirname);
}

console.log('\n=== Require Test ===\n');
console.log('✓ Successfully required ./helper.js');
console.log('  helper.helperLevel1 is:', typeof helper.helperLevel1);
