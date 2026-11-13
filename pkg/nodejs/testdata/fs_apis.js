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

async function testFailures() {
  const codeOf = (fn, ...args) => new Promise((resolve) => {
    try {
      fn(...args, (e) => resolve(e ? [e.code, e.syscall, local(e.path), local(e.dest), local(e.message)] : null));
    } catch (e) {
      resolve(['threw', e.code]);
    }
  });
  fs.writeFileSync(file('victim'), 'keep');
  results.fail_unlink_nul = await codeOf(fs.unlink, file('victim\0suffix'));
  results.fail_victim_kept = fs.existsSync(file('victim'));
  results.fail_rename_nul = await codeOf(fs.rename, file('victim'), file('a\0b'));
  results.fail_copy_missing = await codeOf(fs.copyFile, file('nothing'), file('dest'));
  fs.mkdirSync(file('dir'));
  results.fail_mkdir_existing = await codeOf(fs.mkdir, file('dir'));
  results.fail_rmdir_missing = await codeOf(fs.rmdir, file('nodir'));
  results.fail_readdir_missing = await codeOf(fs.readdir, file('nodir'));
  results.fail_rename_missing = await codeOf(fs.rename, file('nothing'), file('x'));
  results.fail_unlink_missing = await codeOf(fs.unlink, file('nothing'));
  try { fs.mkdirSync(file('dir')); } catch (e) { results.fail_mkdirSync = [e.code, e.syscall, local(e.message)]; }

  // Descriptor calls fail with Node's codes, and COPYFILE_EXCL keeps an existing destination.
  const bad = 9999;
  results.fail_close_bad = await codeOf(fs.close, bad);
  results.fail_fstat_bad = await codeOf(fs.fstat, bad);
  results.fail_read_bad = await codeOf(fs.read, bad, Buffer.alloc(1), 0, 1, null);
  results.fail_write_bad = await codeOf(fs.write, bad, Buffer.from('x'));
  try { fs.closeSync(bad); } catch (e) { results.fail_closeSync_bad = [e.code, e.syscall, e.message, e.path === undefined]; }
  fs.writeFileSync(file('excl_src'), 'new');
  fs.writeFileSync(file('excl_dest'), 'old');
  results.fail_copy_excl = await codeOf(fs.copyFile, file('excl_src'), file('excl_dest'), fs.constants.COPYFILE_EXCL);
  results.fail_copy_excl_kept = fs.readFileSync(file('excl_dest'), 'utf8');
  results.copy_excl_new = await codeOf(fs.copyFile, file('excl_src'), file('excl_new'), fs.constants.COPYFILE_EXCL);

  // An explicit close closes the descriptor even with autoClose off.
  const ws = fs.createWriteStream(file('autoclose'), { autoClose: false });
  ws.write('x');
  await new Promise((resolve) => ws.close(resolve));
  try {
    fs.closeSync(ws.fd);
    results.fail_fd_still_open = true;
  } catch (e) {
    results.fail_fd_still_open = false;
  }

  // With autoClose off, finishing leaves the stream and its descriptor open, and a later
  // destroy closes them. A failed open emits only the error.
  const isOpen = (fd) => { try { fs.fstatSync(fd); return true; } catch (e) { return false; } };
  const tick = () => new Promise((resolve) => setTimeout(resolve, 20));
  const kept = fs.createWriteStream(file('autoclose_kept'), { autoClose: false });
  const keptEvents = [];
  for (const name of ['finish', 'close']) kept.on(name, () => keptEvents.push(name));
  kept.end('kept');
  await tick();
  const keptFd = kept.fd;
  results.autoclose_finished = { events: keptEvents.slice(), destroyed: kept.destroyed, closed: kept.closed, fdOpen: isOpen(keptFd) };
  kept.destroy();
  await tick();
  results.autoclose_destroyed = { events: keptEvents, destroyed: kept.destroyed, fd: kept.fd, fdOpen: isOpen(keptFd) };
  const badOpen = fs.createWriteStream(file('missing/y'), { autoClose: false });
  const badOpenEvents = [];
  badOpen.on('error', (e) => badOpenEvents.push('error ' + e.code));
  badOpen.on('close', () => badOpenEvents.push('close'));
  await tick();
  results.autoclose_open_error = { events: badOpenEvents, destroyed: badOpen.destroyed };
}

// writeFile and appendFile given a descriptor write to it at its offset, and create no file
// named after the number.
async function testDescriptorWrites() {
  fs.mkdirSync(file('fdwrites'));
  const target = file('fdwrites/target.txt');
  fs.writeFileSync(target, 'start');
  const fd = fs.openSync(target, 'r+');
  fs.writeFileSync(fd, 'ab');
  fs.appendFileSync(fd, 'c', { flag: 'w' });
  results.fd_writeFile = await call(fs.writeFile, fd, Buffer.from('d'));
  results.fd_appendFile = await call(fs.appendFile, fd, 'e', 'utf8');
  fs.closeSync(fd);
  results.fd_writeFile_bad = await call(fs.writeFile, 9999, 'x');
  results.fd_appendFile_bad = await call(fs.appendFile, 9999, 'x');
  try { fs.writeFileSync(9999, 'x'); } catch (e) { results.fd_writeFileSync_bad = errorOf(e); }

  const handle = await fs.promises.open(target, 'a');
  await fs.promises.appendFile(handle, '!');
  await fs.promises.writeFile(handle, '?');
  await handle.close();
  results.fd_promises_number = await fs.promises.writeFile(9999, 'x').then(() => null, (e) => [e.code, e.message]);
  results.fd_contents = fs.readFileSync(target, 'utf8');
  results.fd_listing = fs.readdirSync(file('fdwrites'));
}

// The numeric open flags in fs.constants open files as their names say. Their values differ
// between platforms, so only what they do is compared.
async function testOpenFlags() {
  const { O_RDONLY, O_WRONLY, O_RDWR, O_CREAT, O_EXCL, O_TRUNC, O_APPEND } = fs.constants;
  results.flag_types = [O_RDONLY, O_WRONLY, O_RDWR, O_CREAT, O_EXCL, O_TRUNC, O_APPEND].map((v) => typeof v);
  const target = file('flags.txt');
  fs.writeFileSync(target, 'base');
  let fd = fs.openSync(target, O_WRONLY | O_APPEND);
  fs.writeSync(fd, '+append');
  fs.closeSync(fd);
  results.flag_append = fs.readFileSync(target, 'utf8');
  fs.appendFileSync(target, '+again', { flag: O_WRONLY | O_APPEND });
  results.flag_append_option = fs.readFileSync(target, 'utf8');
  const stream = fs.createWriteStream(target, { flags: O_WRONLY | O_APPEND });
  await new Promise((resolve) => { stream.on('close', resolve); stream.end('+stream'); });
  results.flag_append_stream = fs.readFileSync(target, 'utf8');
  try {
    fs.openSync(target, O_WRONLY | O_CREAT | O_EXCL);
    results.flag_excl = 'opened';
  } catch (e) {
    results.flag_excl = e.code;
  }
  fd = fs.openSync(file('flags_new.txt'), O_RDWR | O_CREAT | O_TRUNC);
  fs.writeSync(fd, 'new');
  fs.closeSync(fd);
  fs.writeFileSync(target, 'cut', { flag: O_WRONLY | O_TRUNC });
  results.flag_trunc = fs.readFileSync(target, 'utf8');
  results.flag_promises_same = fs.promises.constants === fs.constants;
}

// rm with force ignores a missing path in every form, and reports anything else.
async function testRm() {
  const missing = file('rm_missing');
  const describe = (e) => (e ? [e.name, e.code, e.syscall, local(e.path), local(e.message)] : null);
  const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
  const promised = (promise) => promise.then(() => null, describe);
  results.rm_promises_force = await promised(fs.promises.rm(missing, { force: true }));
  results.rm_promises_recursive_force = await promised(fs.promises.rm(missing, { recursive: true, force: true }));
  results.rm_promises_missing = await promised(fs.promises.rm(missing));
  results.rmSync_force = attempt(() => fs.rmSync(missing, { force: true }));
  results.rmSync_missing = attempt(() => fs.rmSync(missing));
  results.rm_force = (await call(fs.rm, missing, { force: true })).err;
  results.rm_missing = (await call(fs.rm, missing)).err;

  fs.mkdirSync(file('rm_dir/inner'), { recursive: true });
  fs.writeFileSync(file('rm_dir/inner/f'), 'x');
  fs.writeFileSync(file('rm_file'), 'x');
  results.rmSync_dir = attempt(() => fs.rmSync(file('rm_dir'), { force: true }));
  results.rm_file = (await call(fs.rm, file('rm_file'))).err;
  results.rm_dir_recursive = await promised(fs.promises.rm(file('rm_dir'), { recursive: true }));
  results.rm_left = ['rm_dir', 'rm_file'].map((name) => fs.existsSync(file(name)));
}

function testExports() {
  results.promises_same = fs.promises === require('fs/promises');
  results.constants = { F_OK: fs.constants.F_OK, R_OK: fs.constants.R_OK, W_OK: fs.constants.W_OK, X_OK: fs.constants.X_OK };
  results.types = ['createWriteStream', 'appendFile', 'write', 'read', 'open', 'close', 'mkdir', 'readdir', 'rm', 'rmSync']
    .map((name) => name + ':' + typeof fs[name]);
}

async function main() {
  await testWriteStream();
  await testCallbacks();
  await testFailures();
  await testDescriptorWrites();
  await testOpenFlags();
  await testRm();
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
