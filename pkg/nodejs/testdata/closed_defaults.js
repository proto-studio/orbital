// Runs unchanged in Node and in Orbital. In Node, the directory it is given can't be accessed
// (its mode is 000), and the OS refuses the network and running /bin/sh and /usr/bin/id
// (sandbox-exec on macOS). In Orbital, a zero runtime.Config refuses them. Both should fail
// the same way. closed_defaults.node.json is this script's output under Node v20.18.1.
// Regenerate it on macOS with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestClosedDefaultsMatchNode'
const fs = require('fs');
const net = require('net');
const dgram = require('dgram');
const dns = require('dns');
const http = require('http');
const childProcess = require('child_process');

const dir = globalThis.CLOSED_DIR || process.argv[2];
const results = {};

function describe(e) {
  if (!e) return null;
  const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : s);
  return {
    name: e.name, code: e.code, errno: e.errno, syscall: e.syscall, address: e.address,
    port: e.port, hostname: e.hostname, path: local(e.path), message: local(e.message)
  };
}
function attempt(name, fn) {
  try {
    fn();
    results[name] = 'ok';
  } catch (e) {
    results[name] = describe(e);
  }
}
const pending = [];
function settle(name, start) {
  pending.push(new Promise((resolve) => {
    start((err) => {
      results[name] = describe(err);
      resolve();
    });
  }));
}

// Files
attempt('readFileSync', () => fs.readFileSync(dir + '/a.txt'));
attempt('writeFileSync', () => fs.writeFileSync(dir + '/b.txt', 'x'));
settle('promises.readdir', (cb) => fs.promises.readdir(dir).then(() => cb(null), cb));

// Processes
attempt('execSync', () => childProcess.execSync('id', { stdio: 'pipe' }));
attempt('execFileSync', () => childProcess.execFileSync('/usr/bin/id', { stdio: 'pipe' }));
results.spawnSync = describe(childProcess.spawnSync('/usr/bin/id').error);
attempt('spawn', () => childProcess.spawn('/usr/bin/id'));
attempt('exec', () => childProcess.exec('id', () => {}));

// Sockets
settle('net.connect', (cb) => net.connect(8124, '127.0.0.1').on('error', cb).on('connect', () => cb(null)));
settle('net.connect name', (cb) => net.connect(8124, 'example.com').on('error', cb).on('connect', () => cb(null)));
settle('net.listen', (cb) => net.createServer().on('error', cb).listen(8123, '127.0.0.1', () => cb(null)));
settle('dgram.send', (cb) => {
  const socket = dgram.createSocket('udp4');
  socket.on('error', (err) => { socket.close(); cb(err); });
  socket.send('x', 8124, '127.0.0.1', (err) => { socket.close(); cb(err); });
});

// HTTP
settle('http.get', (cb) => http.get('http://127.0.0.1:8124/').on('error', cb).on('response', () => cb(null)));
settle('fetch', (cb) => fetch('http://127.0.0.1:8124/').then(() => cb(null), (e) => cb(e.cause)));
settle('fetch name', (cb) => fetch('http://example.com/').then(() => cb(null), (e) => cb(e.cause)));

// DNS
settle('dns.lookup', (cb) => dns.lookup('example.com', (err) => cb(err)));
settle('dns.lookup localhost', (cb) => dns.lookup('localhost', (err) => cb(err)));
settle('dns.resolve4', (cb) => dns.resolve4('example.com', (err) => cb(err)));
settle('dns.resolveMx', (cb) => dns.resolveMx('example.com', (err) => cb(err)));
settle('dns.reverse', (cb) => dns.reverse('192.0.2.1', (err) => cb(err)));

Promise.all(pending).then(() => {
  if (typeof done === 'function') done(results);
  else process.stdout.write(JSON.stringify(results));
});
