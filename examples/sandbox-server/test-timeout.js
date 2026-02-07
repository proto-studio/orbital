// Test timeout functionality
// This script runs an infinite loop that should be stopped by the timeout

console.log('Starting infinite loop test...');
console.log('This should be killed by the timeout after a few seconds.');

let count = 0;
const startTime = Date.now();

// Report progress every second (if we're still running)
setInterval(() => {
    count++;
    const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
    console.log(`Still running after ${elapsed}s (iteration ${count})`);
}, 1000);

// This infinite loop should be killed by the timeout
// Note: V8 may not respond to kill signals during a tight CPU-bound loop
// So we use a timer-based approach instead
console.log('Waiting for timeout or kill signal...');
