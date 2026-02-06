// GNode Demo - Node.js APIs in action

// Console API
console.log("=== GNode Demo ===");
console.log("Hello from", "GNode!");

// Timers
console.log("\n--- Timers ---");
console.log("Setting timeout for 100ms...");

setTimeout(() => {
    console.log("Timeout fired!");
}, 100);

// Process API
console.log("\n--- Process Info ---");
console.log("Platform:", process.platform);
console.log("Architecture:", process.arch);
console.log("PID:", process.pid);
console.log("Node version:", process.version);
console.log("CWD:", process.cwd());

// Environment variables
console.log("\n--- Environment ---");
console.log("HOME:", process.env.HOME || process.env.USERPROFILE);
console.log("PATH length:", (process.env.PATH || "").length, "chars");

// Path module
const path = require('path');
console.log("\n--- Path Operations ---");
console.log("path.join('foo', 'bar'):", path.join('foo', 'bar'));
console.log("path.basename('/foo/bar/baz.txt'):", path.basename('/foo/bar/baz.txt'));
console.log("path.dirname('/foo/bar/baz.txt'):", path.dirname('/foo/bar/baz.txt'));
console.log("path.extname('index.html'):", path.extname('index.html'));
console.log("path.isAbsolute('/foo'):", path.isAbsolute('/foo'));
console.log("path.isAbsolute('foo'):", path.isAbsolute('foo'));

// EventEmitter
console.log("\n--- EventEmitter ---");
const emitter = new EventEmitter();

emitter.on('greet', (name) => {
    console.log(`Hello, ${name}!`);
});

emitter.once('farewell', (name) => {
    console.log(`Goodbye, ${name}!`);
});

emitter.emit('greet', 'World');
emitter.emit('farewell', 'GNode');
emitter.emit('farewell', 'Again'); // Won't fire, it was a once listener

// Console timing
console.log("\n--- Console Timing ---");
console.time('fibonacci');
function fib(n) {
    if (n <= 1) return n;
    return fib(n - 1) + fib(n - 2);
}
const result = fib(30);
console.timeEnd('fibonacci');
console.log("fib(30) =", result);

// Process nextTick
console.log("\n--- Process nextTick ---");
console.log("Before nextTick");
process.nextTick(() => {
    console.log("Inside nextTick callback");
});
console.log("After nextTick (callback hasn't run yet)");

// Memory usage
console.log("\n--- Memory Usage ---");
const mem = process.memoryUsage();
console.log("Heap used:", Math.round(mem.heapUsed / 1024 / 1024), "MB");
console.log("Heap total:", Math.round(mem.heapTotal / 1024 / 1024), "MB");

console.log("\n=== Demo Complete ===");
