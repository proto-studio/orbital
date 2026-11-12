// Runs unchanged in Node and in Orbital. fs_apis.node.json is this script's output under
// Node v20.18.1 plus the files it left, read back by the Go test. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSAPIs_MatchesNode'
const fs = require('fs');
const path = require('path');

const dir = globalThis.FS_DIR || process.argv[2];
const file = (name) => path.join(dir, name);
const results = {};
const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : s);
const errorOf = (e) => (e ? { code: e.code, syscall: e.syscall, message: local(e.message) } : null);
const call = (fn, ...args) => new Promise((resolve) => fn(...args, (err, ...values) => resolve({ err: errorOf(err), values })));

function streamDone(stream, events) {
  return new Promise((resolve) => {
    for (const name of ['open', 'ready', 'finish', 'close', 'error']) {
      stream.on(name, (arg) => {
        events.push(name === 'error' ? 'error ' + arg.code : name);
        if (name === 'close') resolve();
      });
    }
  });
}

async function testWriteStream() {
  const events = [];
  const ws = fs.createWriteStream(file('stream.bin'));
  const done = streamDone(ws, events);
  results.ws_pending = ws.pending;
  results.ws_write_returns = ws.write(Buffer.from([0x41, 0, 0xff]));
  ws.write('text\0', 'utf8');
  ws.end('end');
  await done;
  results.ws_events = events;
  results.ws_bytesWritten = ws.bytesWritten;
  results.ws_path = local(ws.path);
  results.ws_instance = ws instanceof fs.WriteStream;

  fs.writeFileSync(file('start.txt'), 'abcdefgh');
  const at = fs.createWriteStream(file('start.txt'), { flags: 'r+', start: 2 });
  const atEvents = [];
  const atDone = streamDone(at, atEvents);
  at.write('XY');
  at.end('Z');
  await atDone;
  results.ws_start = { events: atEvents, written: at.bytesWritten };

  const append = fs.createWriteStream(file('start.txt'), { flags: 'a' });
  const appendDone = streamDone(append, []);
  append.end('!');
  await appendDone;

  const bad = fs.createWriteStream(file('missing/x'));
  const badEvents = [];
  await new Promise((resolve) => {
    bad.on('error', (e) => { badEvents.push('error ' + e.code); });
    bad.on('close', () => { badEvents.push('close'); resolve(); });
  });
  results.ws_missing_dir = badEvents;

  const piped = fs.createWriteStream(file('piped.bin'));
  await new Promise((resolve) => {
    piped.on('close', resolve);
    fs.createReadStream(file('stream.bin')).pipe(piped);
  });
}

async function testCallbacks() {
  results.appendFile_new = await call(fs.appendFile, file('append.txt'), 'one');
  results.appendFile_options = await call(fs.appendFile, file('append.txt'), Buffer.from('two'), { flag: 'a' });
  results.appendFile_missing = await call(fs.appendFile, file('missing/x'), 'x');

  const opened = await call(fs.open, file('fd.bin'), 'w+');
  const fd = opened.values[0];
  results.open = { err: opened.err, isNumber: typeof fd === 'number' };
  const written = await call(fs.write, fd, Buffer.from([1, 2, 3, 4]), 1, 2, 0);
  results.write_buffer = { err: written.err, bytes: written.values[0], buffer: Buffer.isBuffer(written.values[1]) };
  const wroteString = await call(fs.write, fd, 'hey', 2);
  results.write_string = { err: wroteString.err, bytes: wroteString.values[0], string: wroteString.values[1] };
  const target = Buffer.alloc(6);
  const readBack = await call(fs.read, fd, target, 0, 6, 0);
  results.read = { err: readBack.err, bytes: readBack.values[0], same: readBack.values[1] === target, data: target.toString('hex') };
  results.close = await call(fs.close, fd);
  results.open_missing = await call(fs.open, file('nothing'), 'r');

  results.mkdir = await call(fs.mkdir, file('made'));
  results.rename = await call(fs.rename, file('append.txt'), file('made/renamed.txt'));
  const listed = await call(fs.readdir, file('made'));
  results.readdir = listed.values[0];
  results.unlink = await call(fs.unlink, file('made/renamed.txt'));

  let sync = true;
  const order = new Promise((resolve) => fs.close(9999, () => resolve(sync)));
  sync = false;
  results.callback_is_async = !(await order);
}

function testExports() {
  results.promises_same = fs.promises === require('fs/promises');
  results.constants = { F_OK: fs.constants.F_OK, R_OK: fs.constants.R_OK, W_OK: fs.constants.W_OK, X_OK: fs.constants.X_OK };
  results.types = ['createWriteStream', 'appendFile', 'write', 'read', 'open', 'close', 'mkdir', 'readdir']
    .map((name) => name + ':' + typeof fs[name]);
}

async function main() {
  await testWriteStream();
  await testCallbacks();
  testExports();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
