// Runs unchanged in Node and in Orbital. fs_errors.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSErrors_MatchesNode'
const fs = require('fs');
const fsp = require('fs/promises');
const path = require('path');

const dir = globalThis.FS_DIR || process.argv[2];
const file = (name) => path.join(dir, name);
const results = {};

// Paths in messages name the temporary directory, which differs between runs.
const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : s);
function describe(e) {
  if (!e) return null;
  return {
    name: e.name, code: e.code, errno: e.errno, syscall: e.syscall,
    path: local(e.path), message: local(e.message), isError: e instanceof Error
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
const read = (name) => (fs.existsSync(file(name)) ? fs.readFileSync(file(name), 'utf8') : null);
const mode = (name) => (fs.statSync(file(name)).mode & 0o777).toString(8);

function testMissingDirectory() {
  attempt('writeFileSync_missing_dir', () => fs.writeFileSync(file('missing/f'), 'x'));
  attempt('appendFileSync_missing_dir', () => fs.appendFileSync(file('missing/f'), 'x'));
  attempt('readFileSync_missing', () => fs.readFileSync(file('nothing')));
  attempt('readFileSync_directory', () => fs.readFileSync(dir));
}

async function testAsyncErrors() {
  await attemptAsync('writeFile_missing_dir', () => callback((cb) => fs.writeFile(file('missing/f'), 'x', cb)));
  await attemptAsync('readFile_missing', () => callback((cb) => fs.readFile(file('nothing'), cb)));
  await attemptAsync('readFile_directory', () => callback((cb) => fs.readFile(dir, cb)));
  await attemptAsync('promises_writeFile_missing_dir', () => fsp.writeFile(file('missing/f'), 'x'));
}

async function testFlags() {
  fs.writeFileSync(file('append'), 'one');
  fs.writeFileSync(file('append'), 'two', { flag: 'a' });
  results.writeFileSync_flag_a = read('append');

  fs.writeFileSync(file('excl'), 'one');
  attempt('writeFileSync_flag_wx_existing', () => fs.writeFileSync(file('excl'), 'two', { flag: 'wx' }));
  results.after_wx = read('excl');
  attempt('writeFileSync_flag_wx_new', () => fs.writeFileSync(file('excl_new'), 'new', { flag: 'wx' }));
  attempt('appendFileSync_flag_ax_existing', () => fs.appendFileSync(file('excl'), 'two', { flag: 'ax' }));

  fs.writeFileSync(file('overwrite'), 'one');
  fs.appendFileSync(file('overwrite'), 'two', { flag: 'w' });
  results.appendFileSync_flag_w = read('overwrite');

  fs.writeFileSync(file('async_append'), 'one');
  await callback((cb) => fs.writeFile(file('async_append'), 'two', { flag: 'a' }, cb));
  results.writeFile_flag_a = read('async_append');

  fs.writeFileSync(file('promise_append'), 'one');
  await fsp.writeFile(file('promise_append'), 'two', { flag: 'a' });
  await fsp.appendFile(file('promise_append'), 'three');
  results.promises_flag_a = read('promise_append');

  attempt('writeFileSync_bad_flag', () => fs.writeFileSync(file('bad'), 'x', { flag: 'zz' }));
}

function testModes() {
  fs.writeFileSync(file('mode_600'), 'x', { mode: 0o600 });
  results.mode_600 = mode('mode_600');
  fs.writeFileSync(file('mode_string'), 'x', { mode: '640' });
  results.mode_string = mode('mode_string');
  fs.appendFileSync(file('mode_append'), 'x', { mode: 0o600 });
  results.mode_append = mode('mode_append');
}

// An invalid mode throws before the file is created or truncated.
async function testInvalidModes() {
  fs.writeFileSync(file('mode_keep'), 'kept');
  const bad = [['negative', -1], ['fraction', 1.5], ['too_big', 4294967296], ['nan', NaN],
    ['infinity', Infinity], ['octal_8', '8'], ['prefixed', '0o644'], ['hex', '0x1'], ['spaced', ' 644'],
    ['empty', ''], ['boolean', true], ['object', {}]];
  for (const [name, value] of bad) {
    attempt('mode_writeFileSync_' + name, () => fs.writeFileSync(file('mode_new_' + name), 'x', { mode: value }));
    results['mode_created_' + name] = fs.existsSync(file('mode_new_' + name));
  }
  attempt('mode_writeFileSync_existing', () => fs.writeFileSync(file('mode_keep'), 'x', { mode: -1 }));
  attempt('mode_appendFileSync', () => fs.appendFileSync(file('mode_keep'), 'x', { mode: -1 }));
  attempt('mode_writeFile', () => fs.writeFile(file('mode_keep'), 'x', { mode: -1 }, () => {}));
  await attemptAsync('mode_promises_writeFile', () => fsp.writeFile(file('mode_keep'), 'x', { mode: -1 }));
  await attemptAsync('mode_promises_appendFile', () => fsp.appendFile(file('mode_keep'), 'x', { mode: '9' }));
  results.mode_keep = read('mode_keep');
  fs.writeFileSync(file('mode_zero_string'), 'x', { mode: '0' });
  results.mode_zero_string = mode('mode_zero_string');
}

async function testNulPaths() {
  const bad = file('n\0m');
  attempt('nul_writeFileSync', () => fs.writeFileSync(bad, 'x'));
  results.nul_left_file = fs.existsSync(file('n'));
  attempt('nul_readFileSync', () => fs.readFileSync(bad));
  attempt('nul_appendFileSync', () => fs.appendFileSync(bad, 'x'));
  attempt('nul_statSync', () => fs.statSync(bad));
  attempt('nul_mkdirSync', () => fs.mkdirSync(bad));
  attempt('nul_unlinkSync', () => fs.unlinkSync(bad));
  attempt('nul_openSync', () => fs.openSync(bad, 'w'));
  attempt('nul_renameSync', () => fs.renameSync(file('append'), bad));
  attempt('nul_readFile', () => fs.readFile(bad, () => {}));
  attempt('nul_writeFile', () => fs.writeFile(bad, 'x', () => {}));
  results.nul_existsSync = fs.existsSync(bad);
  await attemptAsync('nul_promises_writeFile', () => fsp.writeFile(bad, 'x'));
  await attemptAsync('nul_promises_readFile', () => fsp.readFile(bad));
  // path keeps a NUL, so fs sees it and refuses the path.
  results.nul_path = {
    join: path.join('a', 'n\0m'), basename: path.basename('/x/n\0m.t'), extname: path.extname('n.t\0x'),
    parse: path.parse('/x/a\0b.c'), relative: path.relative('/a', '/a/n\0m'),
    basenameSuffix: [path.basename('a\0', '0'), path.basename('a\0', '\0'), path.basename('/x/a.txt', 'txt'),
      path.basename('/x/aaa', 'aaa'), path.basename('', 'x'), path.basename(''),
      path.basename('report.txt', 'report.txt'), path.basename('aaa/', 'aaa')],
    formatInherited: path.format(Object.create({ dir: '/tmp', base: 'report.txt' })),
    parseKeys: Object.keys(path.parse('/x/a\0b.c'))
  };
  // An s flag asks for synchronous writes, and the data still lands.
  fs.writeFileSync(file('sync_flag'), 'one', { flag: 'as' });
  fs.appendFileSync(file('sync_flag'), 'two', { flag: 'as+' });
  results.sync_flags = read('sync_flag');
}

async function main() {
  testMissingDirectory();
  await testAsyncErrors();
  await testFlags();
  testModes();
  await testInvalidModes();
  await testNulPaths();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
