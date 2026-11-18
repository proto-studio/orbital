// Runs unchanged in Node and in Orbital. In Node, the directory it is given can't be accessed
// (its mode is 000); in Orbital, the filesystem refuses everything. Both should fail the same
// way. fs_closed.node.json is this script's output under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFS_ClosedFilesystemMatchesNode'
const fs = require('fs');
const fsp = require('fs/promises');
const path = require('path');
const { Worker } = require('worker_threads');

const dir = globalThis.FS_DIR || process.argv[2];
const file = (name) => path.join(dir, name);
const results = {};

// Paths in messages name the temporary directory, which differs between runs. Module errors
// also name the script that loaded them, which differs between Node and Orbital, so only
// their first line, up to the importing file, is kept.
const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : s);
function describe(e) {
  if (!e) return null;
  let message = local(e.message);
  if (typeof e.code === 'string' && e.code.endsWith('MODULE_NOT_FOUND')) {
    message = message.split('\n')[0].split(' imported from ')[0];
  }
  return {
    name: e.name, code: e.code, errno: e.errno, syscall: e.syscall,
    path: local(e.path), dest: local(e.dest), message, isError: e instanceof Error
  };
}
function attempt(name, fn) {
  try {
    const value = fn();
    results[name] = { value: value === undefined ? null : value };
  } catch (e) {
    results[name] = { error: describe(e) };
  }
}
async function attemptAsync(name, fn) {
  try {
    const value = await fn();
    results[name] = { value: value === undefined ? null : value };
  } catch (e) {
    results[name] = { error: describe(e) };
  }
}
const callback = (fn) => new Promise((resolve, reject) => fn((err, value) => (err ? reject(err) : resolve(value))));
const streamError = (stream) => new Promise((resolve, reject) => {
  stream.on('error', reject);
  stream.on('open', () => { stream.destroy(); resolve('opened'); });
});

function testSync() {
  const f = file('sub/x.js');
  const d = file('sub');
  attempt('readFileSync', () => fs.readFileSync(f, 'utf8'));
  attempt('writeFileSync', () => fs.writeFileSync(file('sub/new.txt'), 'x'));
  attempt('appendFileSync', () => fs.appendFileSync(f, 'x'));
  attempt('openSync', () => fs.openSync(f, 'r'));
  attempt('openSyncWrite', () => fs.openSync(file('sub/new.txt'), 'w'));
  attempt('statSync', () => fs.statSync(f));
  attempt('lstatSync', () => fs.lstatSync(f));
  attempt('readdirSync', () => fs.readdirSync(d));
  attempt('opendirSync', () => fs.opendirSync(d));
  attempt('mkdirSync', () => fs.mkdirSync(file('sub/d')));
  attempt('mkdirSyncRecursive', () => fs.mkdirSync(file('sub/d/e'), { recursive: true }));
  attempt('unlinkSync', () => fs.unlinkSync(f));
  attempt('rmdirSync', () => fs.rmdirSync(d));
  attempt('rmSync', () => fs.rmSync(f));
  attempt('renameSync', () => fs.renameSync(f, file('sub/y.js')));
  attempt('copyFileSync', () => fs.copyFileSync(f, file('sub/y.js')));
  attempt('accessSync', () => fs.accessSync(f));
  attempt('chmodSync', () => fs.chmodSync(f, 0o644));
  attempt('utimesSync', () => fs.utimesSync(f, 1, 1));
  attempt('truncateSync', () => fs.truncateSync(f));
  attempt('symlinkSync', () => fs.symlinkSync('target', file('sub/link')));
  attempt('readlinkSync', () => fs.readlinkSync(f));
  attempt('linkSync', () => fs.linkSync(f, file('sub/hard')));
  attempt('statfsSync', () => fs.statfsSync(f));
  attempt('existsSync', () => fs.existsSync(f));
}

async function testCallbacks() {
  const f = file('sub/x.js');
  await attemptAsync('readFile', () => callback((cb) => fs.readFile(f, cb)));
  await attemptAsync('writeFile', () => callback((cb) => fs.writeFile(file('sub/new.txt'), 'x', cb)));
  await attemptAsync('open', () => callback((cb) => fs.open(f, 'r', cb)));
  await attemptAsync('stat', () => callback((cb) => fs.stat(f, cb)));
  await attemptAsync('readdir', () => callback((cb) => fs.readdir(file('sub'), cb)));
  await attemptAsync('mkdir', () => callback((cb) => fs.mkdir(file('sub/d'), cb)));
  await attemptAsync('unlink', () => callback((cb) => fs.unlink(f, cb)));
  await attemptAsync('rename', () => callback((cb) => fs.rename(f, file('sub/y.js'), cb)));
  await attemptAsync('access', () => callback((cb) => fs.access(f, cb)));
  await attemptAsync('exists', () => new Promise((resolve) => fs.exists(f, resolve)));
}

async function testPromises() {
  const f = file('sub/x.js');
  await attemptAsync('promises.readFile', () => fsp.readFile(f, 'utf8'));
  await attemptAsync('promises.writeFile', () => fsp.writeFile(file('sub/new.txt'), 'x'));
  await attemptAsync('promises.open', () => fsp.open(f, 'r'));
  await attemptAsync('promises.stat', () => fsp.stat(f));
  await attemptAsync('promises.readdir', () => fsp.readdir(file('sub')));
  await attemptAsync('promises.opendir', () => fsp.opendir(file('sub')));
  await attemptAsync('promises.mkdir', () => fsp.mkdir(file('sub/d')));
  await attemptAsync('promises.rm', () => fsp.rm(f));
  await attemptAsync('promises.access', () => fsp.access(f));
  await attemptAsync('promises.copyFile', () => fsp.copyFile(f, file('sub/y.js')));
}

async function testStreams() {
  await attemptAsync('createReadStream', () => streamError(fs.createReadStream(file('sub/x.js'))));
  await attemptAsync('createWriteStream', () => streamError(fs.createWriteStream(file('sub/new.txt'))));
}

async function testModules() {
  attempt('require', () => require(file('sub/x.js')));
  await attemptAsync('import', () => import(file('sub/x.mjs')));
  // A worker starts before it loads its entry file, so a refused file fails it afterwards.
  await attemptAsync('worker', () => new Promise((resolve, reject) => {
    const w = new Worker(file('sub/x.js'));
    w.on('error', reject);
    w.on('exit', (code) => resolve({ exit: code }));
  }));
}

async function main() {
  testSync();
  await testCallbacks();
  await testPromises();
  await testStreams();
  await testModules();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
