// test.js - Sandbox isolation test
// This should be able to run infinite times without error
// because each request gets its own isolated filesystem.

const fs = require('fs');
const path = require('path');

const testFile = '/test.txt';

console.log('=== Sandbox Isolation Test ===');
console.log('Working directory:', process.cwd());
console.log('Test file:', testFile);
console.log('');

// Step 1: Check if file exists (should NOT exist in fresh sandbox)
console.log('Step 1: Checking if test.txt exists...');
if (fs.existsSync(testFile)) {
    console.error('ERROR: test.txt already exists!');
    console.error('This means sandbox isolation FAILED.');
    console.error('Each request should have its own empty filesystem.');
    throw new Error('SANDBOX ISOLATION FAILURE: File already exists');
}
console.log('  ✓ File does not exist (expected)');

// Step 2: Create and write to the file
console.log('');
console.log('Step 2: Creating and writing to test.txt...');
fs.writeFileSync(testFile, 'Hello from sandbox!\nTimestamp: ' + new Date().toISOString());
console.log('  ✓ File written successfully');

// Step 3: Read back and verify
console.log('');
console.log('Step 3: Reading file back...');
const content = fs.readFileSync(testFile, 'utf8');
console.log('  ✓ File content:');
console.log('    ' + content.replace(/\n/g, '\n    '));

// Step 4: List directory to confirm
console.log('');
console.log('Step 4: Listing root directory...');
const files = fs.readdirSync('/');
console.log('  ✓ Files in /:', files.join(', ') || '(empty)');

console.log('');
console.log('=== Test Passed ===');
console.log('If you see this message multiple times with the same curl command,');
console.log('sandbox isolation is working correctly!');
