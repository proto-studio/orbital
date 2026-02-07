// GNode API Demo - Showcases built-in Node.js APIs

console.log("=== GNode API Demo ===\n");

// ─────────────────────────────────────────────────────────────
// Process API
// ─────────────────────────────────────────────────────────────
console.log("--- Process ---");
console.log("Platform:", process.platform);
console.log("Architecture:", process.arch);
console.log("PID:", process.pid);
console.log("Node version:", process.version);
console.log("CWD:", process.cwd());

// ─────────────────────────────────────────────────────────────
// Path Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- Path Module ---");
const path = require('path');

console.log("join('foo', 'bar', 'baz'):", path.join('foo', 'bar', 'baz'));
console.log("resolve('./foo'):", path.resolve('./foo'));
console.log("basename('/foo/bar/file.txt'):", path.basename('/foo/bar/file.txt'));
console.log("dirname('/foo/bar/file.txt'):", path.dirname('/foo/bar/file.txt'));
console.log("extname('index.html'):", path.extname('index.html'));
console.log("isAbsolute('/foo'):", path.isAbsolute('/foo'));
console.log("parse('/home/user/file.txt'):", JSON.stringify(path.parse('/home/user/file.txt')));

// ─────────────────────────────────────────────────────────────
// Buffer Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- Buffer Module ---");
const buf1 = Buffer.from('Hello');
const buf2 = Buffer.from([72, 101, 108, 108, 111]);
console.log("Buffer.from('Hello'):", buf1);
console.log("Buffer.from([72,101,108,108,111]):", buf2.toString());
console.log("buf1.equals(buf2):", buf1.equals(buf2));
console.log("Buffer.concat:", Buffer.concat([buf1, Buffer.from(' World')]).toString());

// ─────────────────────────────────────────────────────────────
// URL Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- URL Module ---");
const url = new URL('https://user:pass@example.com:8080/path?query=value#hash');
console.log("hostname:", url.hostname);
console.log("port:", url.port);
console.log("pathname:", url.pathname);
console.log("searchParams.get('query'):", url.searchParams.get('query'));
console.log("hash:", url.hash);

// ─────────────────────────────────────────────────────────────
// Crypto Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- Crypto Module ---");
const crypto = require('crypto');

const hash = crypto.createHash('sha256').update('hello world').digest('hex');
console.log("SHA256('hello world'):", hash);

const uuid = crypto.randomUUID();
console.log("randomUUID():", uuid);

const randomBytes = crypto.randomBytes(8);
console.log("randomBytes(8):", randomBytes.toString('hex'));

// ─────────────────────────────────────────────────────────────
// OS Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- OS Module ---");
const os = require('os');

console.log("hostname:", os.hostname());
console.log("platform:", os.platform());
console.log("arch:", os.arch());
console.log("cpus:", os.cpus().length, "cores");
console.log("totalmem:", Math.round(os.totalmem() / 1024 / 1024 / 1024), "GB");
console.log("homedir:", os.homedir());
console.log("tmpdir:", os.tmpdir());

// ─────────────────────────────────────────────────────────────
// Util Module
// ─────────────────────────────────────────────────────────────
console.log("\n--- Util Module ---");
const util = require('util');

console.log("format:", util.format('Hello %s, you have %d messages', 'User', 5));
console.log("inspect:", util.inspect({ nested: { object: [1, 2, 3] } }));
console.log("types.isArray([]):", util.types.isArray([]));
console.log("types.isDate(new Date()):", util.types.isDate(new Date()));

// ─────────────────────────────────────────────────────────────
// EventEmitter
// ─────────────────────────────────────────────────────────────
console.log("\n--- EventEmitter ---");
const emitter = new EventEmitter();

emitter.on('data', (msg) => console.log("Received:", msg));
emitter.once('done', () => console.log("Done event (fires once)"));

emitter.emit('data', 'First message');
emitter.emit('data', 'Second message');
emitter.emit('done');
emitter.emit('done'); // Won't fire again

// ─────────────────────────────────────────────────────────────
// Timers
// ─────────────────────────────────────────────────────────────
console.log("\n--- Timers ---");
console.log("Scheduling timers...");

setTimeout(() => console.log("setTimeout: 50ms elapsed"), 50);
setImmediate(() => console.log("setImmediate: runs after I/O"));

let count = 0;
const interval = setInterval(() => {
    count++;
    console.log("setInterval: tick", count);
    if (count >= 3) {
        clearInterval(interval);
        console.log("Interval cleared");
    }
}, 30);

console.log("\n=== Demo Complete ===");
