// Runs unchanged in Node and in Orbital. fs_more.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSMore_MatchesNode'
// It covers truncate, fsync, chmod, chown, utimes, access, exists, statfs, readv, writev,
// opendir, cp, openAsBlob, watch, watchFile and the Stats, Dirent and Dir classes.
const fs = require('fs');
const fsp = require('fs/promises');
const path = require('path');

const dir = globalThis.FS_DIR || process.argv[2];
const file = (name) => path.join(dir, name);
const results = {};

const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : s);
function describe(e) {
  if (!e) return null;
  return {
    name: e.name, code: e.code, errno: e.errno, syscall: e.syscall,
    path: local(e.path), message: local(e.message), info: e.info ? JSON.parse(local(JSON.stringify(e.info))) : undefined
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
const callback = (fn) => new Promise((resolve, reject) => fn((err, ...values) => (err ? reject(err) : resolve(values))));
const read = (name) => fs.readFileSync(file(name), 'utf8');
const mode = (name) => (fs.statSync(file(name)).mode & 0o7777).toString(8);
const tick = (ms) => new Promise((resolve) => setTimeout(resolve, ms || 20));

function testStatsClasses() {
  fs.writeFileSync(file('stats'), 'abc');
  fs.mkdirSync(file('stats_dir'));
  attempt('stats_file', () => {
    const s = fs.statSync(file('stats'));
    return {
      instance: s instanceof fs.Stats, ctor: s.constructor.name, keys: Object.keys(s),
      type: (s.mode & 0o170000).toString(8), isFile: s.isFile(), isDirectory: s.isDirectory(),
      isSymbolicLink: s.isSymbolicLink(), size: s.size, mtime: s.mtime instanceof Date,
      mtimeMatches: s.mtime.getTime() === Math.round(s.mtimeMs)
    };
  });
  attempt('stats_dir', () => {
    const s = fs.statSync(file('stats_dir'));
    return { type: (s.mode & 0o170000).toString(8), isFile: s.isFile(), isDirectory: s.isDirectory() };
  });
  attempt('stats_constructed', () => {
    const s = new fs.Stats(1, 0o100644, 1, 0, 0, 0, 4096, 2, 10, 1, 1000, 2000, 3000, 4000);
    return { isFile: s.isFile(), mtime: s.mtime.getTime(), keys: Object.keys(s) };
  });
  attempt('dirent', () => fs.readdirSync(dir, { withFileTypes: true })
    .filter((d) => d.name.startsWith('stats'))
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((d) => ({
      instance: d instanceof fs.Dirent, keys: Object.keys(d), name: d.name, parentPath: local(d.parentPath),
      path: local(d.path), isFile: d.isFile(), isDirectory: d.isDirectory()
    })));
}

async function testTruncate() {
  fs.writeFileSync(file('trunc'), 'abcdef');
  attempt('truncate_shorter', () => { fs.truncateSync(file('trunc'), 3); return read('trunc'); });
  attempt('truncate_longer', () => { fs.truncateSync(file('trunc'), 5); return Array.from(fs.readFileSync(file('trunc'))); });
  attempt('truncate_default', () => { fs.truncateSync(file('trunc')); return read('trunc'); });
  attempt('truncate_negative', () => { fs.writeFileSync(file('trunc'), 'xy'); fs.truncateSync(file('trunc'), -3); return read('trunc'); });
  attempt('truncate_missing', () => fs.truncateSync(file('missing/x'), 1));
  attempt('truncate_bad_len', () => fs.truncateSync(file('trunc'), 'x'));
  attempt('truncate_fraction', () => fs.truncateSync(file('trunc'), 1.5));
  fs.writeFileSync(file('trunc'), 'abcdef');
  await attemptAsync('truncate_callback', async () => { await callback((cb) => fs.truncate(file('trunc'), 4, cb)); return read('trunc'); });
  await attemptAsync('truncate_promise', async () => { await fsp.truncate(file('trunc'), 2); return read('trunc'); });
  await attemptAsync('truncate_callback_missing', () => callback((cb) => fs.truncate(file('missing/x'), cb)));
  attempt('truncate_callback_bad_len', () => fs.truncate(file('trunc'), 'x', () => {}));
  const fd = fs.openSync(file('trunc'), 'r+');
  attempt('ftruncate', () => { fs.ftruncateSync(fd, 1); return read('trunc'); });
  attempt('truncate_fd', () => { fs.truncateSync(fd, 0); return read('trunc'); });
  attempt('fsync', () => fs.fsyncSync(fd));
  attempt('fdatasync', () => fs.fdatasyncSync(fd));
  await attemptAsync('fsync_callback', () => callback((cb) => fs.fsync(fd, cb)));
  fs.closeSync(fd);
  attempt('ftruncate_bad_fd', () => fs.ftruncateSync(fd, 1));
  attempt('fsync_bad_fd', () => fs.fsyncSync(fd));
  attempt('fdatasync_bad_fd', () => fs.fdatasyncSync(fd));
  attempt('fsync_negative_fd', () => fs.fsyncSync(-1));
  attempt('fsync_string_fd', () => fs.fsyncSync('1'));
  await attemptAsync('fsync_callback_bad_fd', () => callback((cb) => fs.fsync(fd, cb)));
}

async function testModes() {
  fs.writeFileSync(file('modes'), 'm');
  attempt('chmod', () => { fs.chmodSync(file('modes'), 0o600); return mode('modes'); });
  attempt('chmod_string', () => { fs.chmodSync(file('modes'), '640'); return mode('modes'); });
  attempt('chmod_missing', () => fs.chmodSync(file('missing'), 0o600));
  attempt('chmod_bad', () => fs.chmodSync(file('modes'), 'abc'));
  attempt('chmod_undefined', () => fs.chmodSync(file('modes')));
  attempt('chmod_range', () => fs.chmodSync(file('modes'), 2 ** 33));
  await attemptAsync('chmod_callback', async () => { await callback((cb) => fs.chmod(file('modes'), 0o644, cb)); return mode('modes'); });
  await attemptAsync('chmod_promise', async () => { await fsp.chmod(file('modes'), 0o604); return mode('modes'); });
  await attemptAsync('chmod_callback_missing', () => callback((cb) => fs.chmod(file('missing'), 0o644, cb)));
  const fd = fs.openSync(file('modes'), 'r');
  attempt('fchmod', () => { fs.fchmodSync(fd, 0o600); return mode('modes'); });
  const own = fs.statSync(file('modes'));
  attempt('chown_same', () => fs.chownSync(file('modes'), own.uid, own.gid));
  attempt('fchown_same', () => fs.fchownSync(fd, own.uid, own.gid));
  attempt('chown_keep', () => fs.chownSync(file('modes'), -1, -1));
  attempt('chown_missing', () => fs.chownSync(file('missing'), own.uid, own.gid));
  attempt('chown_bad_uid', () => fs.chownSync(file('modes'), '1', 1));
  attempt('chown_range', () => fs.chownSync(file('modes'), -2, 0));
  await attemptAsync('chown_promise', () => fsp.chown(file('modes'), own.uid, own.gid));
  fs.closeSync(fd);
  attempt('fchmod_bad_fd', () => fs.fchmodSync(fd, 0o600));
  attempt('fchown_bad_fd', () => fs.fchownSync(fd, 0, 0));
}

async function testTimes() {
  fs.writeFileSync(file('times'), 't');
  const times = () => { const s = fs.statSync(file('times')); return [s.atimeMs, s.mtimeMs, s.mtime.getTime()]; };
  attempt('utimes_numbers', () => { fs.utimesSync(file('times'), 1000, 2000.5); return times(); });
  attempt('utimes_dates', () => { fs.utimesSync(file('times'), new Date(3000000), new Date(4000000)); return times(); });
  attempt('utimes_strings', () => { fs.utimesSync(file('times'), '5000', '6000.25'); return times(); });
  attempt('utimes_nan', () => fs.utimesSync(file('times'), NaN, 0));
  attempt('utimes_object', () => fs.utimesSync(file('times'), {}, 0));
  attempt('utimes_missing', () => fs.utimesSync(file('missing'), 1, 2));
  await attemptAsync('utimes_callback', async () => { await callback((cb) => fs.utimes(file('times'), 7000, 8000, cb)); return times(); });
  await attemptAsync('utimes_promise', async () => { await fsp.utimes(file('times'), 9000, 10000); return times(); });
  const fd = fs.openSync(file('times'), 'r+');
  attempt('futimes', () => { fs.futimesSync(fd, 11000, 12000); return times(); });
  await attemptAsync('filehandle_utimes', async () => {
    const handle = await fsp.open(file('times'), 'r+');
    await handle.utimes(13000, 14000);
    const set = times();
    await handle.chmod(0o640);
    await handle.truncate(0);
    await handle.sync();
    await handle.datasync();
    await handle.close();
    return [set, mode('times'), read('times')];
  });
  fs.closeSync(fd);
  attempt('futimes_bad_fd', () => fs.futimesSync(fd, 1, 2));
}

async function testAccess() {
  fs.writeFileSync(file('access'), 'a');
  fs.chmodSync(file('access'), 0o600);
  attempt('access_exists', () => fs.accessSync(file('access')));
  attempt('access_rw', () => fs.accessSync(file('access'), fs.constants.R_OK | fs.constants.W_OK));
  attempt('access_x', () => fs.accessSync(file('access'), fs.constants.X_OK));
  attempt('access_missing', () => fs.accessSync(file('missing')));
  attempt('access_range', () => fs.accessSync(file('access'), 8));
  attempt('access_type', () => fs.accessSync(file('access'), 'x'));
  await attemptAsync('access_callback_x', () => callback((cb) => fs.access(file('access'), fs.constants.X_OK, cb)));
  await attemptAsync('access_callback_ok', () => callback((cb) => fs.access(file('access'), cb)));
  await attemptAsync('access_promise_x', () => fsp.access(file('access'), fs.constants.X_OK));
  await attemptAsync('access_promise_missing', () => fsp.access(file('missing')));
  await attemptAsync('exists', async () => [
    await new Promise((resolve) => fs.exists(file('access'), (...args) => resolve(args))),
    await new Promise((resolve) => fs.exists(file('missing'), (...args) => resolve(args)))
  ]);
}

async function testStatfs() {
  attempt('statfs', () => {
    const s = fs.statfsSync(dir);
    return { ctor: s.constructor.name, keys: Object.keys(s), types: Object.values(s).map((v) => typeof v), positive: s.bsize > 0 && s.blocks > 0 };
  });
  attempt('statfs_bigint', () => Object.values(fs.statfsSync(dir, { bigint: true })).map((v) => typeof v));
  attempt('statfs_missing', () => fs.statfsSync(file('missing')));
  await attemptAsync('statfs_callback', async () => { const [s] = await callback((cb) => fs.statfs(dir, cb)); return s.constructor.name; });
  await attemptAsync('statfs_promise', async () => (await fsp.statfs(dir)).constructor.name);
}

async function testVectors() {
  fs.writeFileSync(file('vec'), '');
  const fd = fs.openSync(file('vec'), 'r+');
  attempt('writev', () => [fs.writevSync(fd, [Buffer.from('abc'), Buffer.from('def')]), read('vec')]);
  attempt('writev_position', () => [fs.writevSync(fd, [Buffer.from('X'), Buffer.from('Y')], 1), read('vec')]);
  attempt('readv', () => {
    const a = Buffer.alloc(2);
    const b = Buffer.alloc(10);
    const n = fs.readvSync(fd, [a, b], 0);
    return [n, a.toString(), b.toString('latin1')];
  });
  attempt('readv_current_offset', () => {
    fs.writeSync(fd, 'abcdefghij', 0);
    const rd = fs.openSync(file('vec'), 'r');
    fs.readSync(rd, Buffer.alloc(4));
    const a = Buffer.alloc(2);
    const b = Buffer.alloc(2);
    const n = fs.readvSync(rd, [a, b], -1);
    fs.closeSync(rd);
    return [n, a.toString(), b.toString()];
  });
  attempt('readv_bad', () => fs.readvSync(fd, 'x'));
  await attemptAsync('writev_callback', async () => {
    const [n, buffers] = await callback((cb) => fs.writev(fd, [Buffer.from('12')], 0, cb));
    return [n, buffers.length, read('vec')];
  });
  await attemptAsync('readv_callback', async () => {
    const [n, buffers] = await callback((cb) => fs.readv(fd, [Buffer.alloc(3)], 0, cb));
    return [n, buffers[0].toString()];
  });
  await attemptAsync('filehandle_vectors', async () => {
    const handle = await fsp.open(file('vec'), 'r+');
    const w = await handle.writev([Buffer.from('Q')], 0);
    const r = await handle.readv([Buffer.alloc(2)], 0);
    await handle.close();
    return [w.bytesWritten, r.bytesRead, r.buffers[0].toString()];
  });
  fs.closeSync(fd);
  attempt('readv_bad_fd', () => fs.readvSync(fd, [Buffer.alloc(1)]));
  attempt('writev_bad_fd', () => fs.writevSync(fd, [Buffer.alloc(1)]));
}

async function testOpendir() {
  fs.mkdirSync(file('od'));
  fs.writeFileSync(file('od/a'), 'a');
  fs.mkdirSync(file('od/b'));
  attempt('opendir_sync', () => {
    const d = fs.opendirSync(file('od'));
    const names = [];
    let e;
    while ((e = d.readSync()) !== null) names.push([e.name, e.isDirectory(), e instanceof fs.Dirent]);
    d.closeSync();
    return { names: names.sort(), path: local(d.path), dir: d instanceof fs.Dir };
  });
  attempt('opendir_recursive', () => {
    fs.mkdirSync(file('od/b/c'));
    fs.writeFileSync(file('od/b/c/deep'), 'd');
    const d = fs.opendirSync(file('od'), { recursive: true });
    const names = [];
    let e;
    while ((e = d.readSync()) !== null) names.push(e.name + ' ' + local(e.parentPath));
    d.closeSync();
    return names.sort();
  });
  await attemptAsync('opendir_recursive_promise', async () => {
    const names = [];
    for await (const e of await fsp.opendir(file('od'), { recursive: true })) names.push(e.name);
    return names.sort();
  });
  attempt('opendir_missing', () => fs.opendirSync(file('missing')));
  attempt('opendir_file', () => fs.opendirSync(file('od/a')));
  attempt('opendir_closed', () => { const d = fs.opendirSync(file('od')); d.closeSync(); return d.readSync(); });
  attempt('opendir_close_twice', () => { const d = fs.opendirSync(file('od')); d.closeSync(); d.closeSync(); });
  await attemptAsync('opendir_iterate', async () => {
    const names = [];
    for await (const e of await fsp.opendir(file('od'))) names.push(e.name);
    return names.sort();
  });
  await attemptAsync('opendir_callback', async () => {
    const [d] = await callback((cb) => fs.opendir(file('od'), cb));
    const first = await d.read();
    const [second] = await callback((cb) => d.read(cb));
    const third = await d.read();
    await d.close();
    return [first.name, second.name, third].sort();
  });
  await attemptAsync('opendir_callback_missing', () => callback((cb) => fs.opendir(file('missing'), cb)));
}

async function testCp() {
  fs.mkdirSync(file('cp_src/sub'), { recursive: true });
  fs.writeFileSync(file('cp_src/a'), 'a');
  fs.writeFileSync(file('cp_src/sub/b'), 'b');
  fs.chmodSync(file('cp_src/a'), 0o640);
  fs.writeFileSync(file('cp_file'), 'f');
  const tree = (name) => {
    const names = [];
    const walk = (rel) => {
      for (const e of fs.readdirSync(file(path.join(name, rel)), { withFileTypes: true })) {
        names.push(path.join(rel, e.name));
        if (e.isDirectory()) walk(path.join(rel, e.name));
      }
    };
    walk('');
    return names.sort();
  };
  attempt('cp_dir_without_recursive', () => fs.cpSync(file('cp_src'), file('cp_dst')));
  attempt('cp_same', () => fs.cpSync(file('cp_file'), file('cp_file')));
  attempt('cp_into_self', () => fs.cpSync(file('cp_src'), file('cp_src/sub/x'), { recursive: true }));
  attempt('cp_dir_to_file', () => fs.cpSync(file('cp_src'), file('cp_file'), { recursive: true }));
  attempt('cp_file_to_dir', () => fs.cpSync(file('cp_file'), file('cp_src')));
  attempt('cp_missing', () => fs.cpSync(file('missing'), file('x')));
  attempt('cp_bad_option', () => fs.cpSync(file('cp_file'), file('x'), { recursive: 'yes' }));
  attempt('cp_bad_filter', () => fs.cpSync(file('cp_file'), file('x'), { filter: 1 }));
  attempt('cp_dir', () => { fs.cpSync(file('cp_src'), file('cp_dst'), { recursive: true }); return [tree('cp_dst'), read('cp_dst/sub/b'), mode('cp_dst/a')]; });
  attempt('cp_exists_error', () => fs.cpSync(file('cp_file'), file('cp_dst/a'), { force: false, errorOnExist: true }));
  attempt('cp_exists_quiet', () => { fs.cpSync(file('cp_file'), file('cp_dst/a'), { force: false }); return read('cp_dst/a'); });
  attempt('cp_force', () => { fs.cpSync(file('cp_file'), file('cp_dst/a')); return read('cp_dst/a'); });
  attempt('cp_filter', () => { fs.cpSync(file('cp_src'), file('cp_filtered'), { recursive: true, filter: (src) => !src.endsWith('b') }); return tree('cp_filtered'); });
  attempt('cp_filter_promise', () => fs.cpSync(file('cp_src'), file('cp_x'), { recursive: true, filter: () => Promise.resolve(true) }));
  attempt('cp_read_only_dir', () => {
    fs.mkdirSync(file('cp_ro/sub'), { recursive: true });
    fs.writeFileSync(file('cp_ro/sub/f'), 'ro');
    fs.chmodSync(file('cp_ro/sub'), 0o555);
    fs.chmodSync(file('cp_ro'), 0o555);
    try {
      fs.cpSync(file('cp_ro'), file('cp_ro_copy'), { recursive: true });
      return [read('cp_ro_copy/sub/f'), mode('cp_ro_copy'), mode('cp_ro_copy/sub')];
    } finally {
      for (const name of ['cp_ro', 'cp_ro/sub', 'cp_ro_copy', 'cp_ro_copy/sub']) {
        try { fs.chmodSync(file(name), 0o755); } catch (e) {}
      }
    }
  });
  attempt('cp_timestamps', () => {
    fs.utimesSync(file('cp_file'), 100, 200);
    fs.cpSync(file('cp_file'), file('cp_times'), { preserveTimestamps: true });
    return fs.statSync(file('cp_times')).mtimeMs;
  });
  await attemptAsync('cp_callback', async () => { await callback((cb) => fs.cp(file('cp_src'), file('cp_cb'), { recursive: true }, cb)); return tree('cp_cb'); });
  await attemptAsync('cp_callback_dir', () => callback((cb) => fs.cp(file('cp_src'), file('cp_cb2'), cb)));
  attempt('cp_missing_parent', () => {
    fs.cpSync(file('cp_file'), file('cp_parent/a/b/file'));
    fs.cpSync(file('cp_src'), file('cp_parent/c/d'), { recursive: true });
    return [read('cp_parent/a/b/file'), tree('cp_parent/c/d')];
  });
  await attemptAsync('cp_filter_skips_directory', async () => {
    const asked = [];
    await fsp.cp(file('cp_src'), file('cp_skip'), { recursive: true, filter: async (src) => {
      asked.push(path.basename(src));
      return !src.endsWith('sub');
    } });
    return [asked.sort(), tree('cp_skip')];
  });
  await attemptAsync('cp_promise_filter', async () => {
    await fsp.cp(file('cp_src'), file('cp_async'), { recursive: true, filter: async (src) => !src.endsWith('sub') });
    return tree('cp_async');
  });
}

async function testBlob() {
  fs.writeFileSync(file('blob'), 'blob text');
  await attemptAsync('blob', async () => {
    const b = await fs.openAsBlob(file('blob'), { type: 'text/plain' });
    return [b instanceof Blob, b.size, b.type, await b.text()];
  });
  attempt('blob_missing', () => fs.openAsBlob(file('missing')));
}

async function testWatch() {
  fs.mkdirSync(file('w'));
  fs.writeFileSync(file('w/f'), 'x');
  attempt('watch_missing', () => fs.watch(file('missing')));
  await attemptAsync('watch_directory', async () => {
    const events = new Set();
    const w = fs.watch(file('w'), (type, name) => events.add(type + ' ' + name));
    await tick(150);
    fs.writeFileSync(file('w/new'), 'n');
    for (let i = 0; i < 100 && !events.has('rename new'); i++) await tick(20);
    let closed = false;
    w.on('close', () => { closed = true; });
    w.close();
    await tick();
    return [events.has('rename new'), closed];
  });
  await attemptAsync('watch_file', async () => {
    const events = [];
    const w = fs.watch(file('w/f'));
    w.on('change', (type, name) => events.push(name));
    await tick(150);
    fs.writeFileSync(file('w/f'), 'changed');
    for (let i = 0; i < 100 && !events.length; i++) await tick(20);
    w.close();
    return events[0];
  });
  await attemptAsync('watchFile', async () => {
    fs.writeFileSync(file('w/polled'), 'a');
    const seen = [];
    const listener = (curr, prev) => seen.push([curr instanceof fs.Stats, curr.size, prev.size]);
    const watcher = fs.watchFile(file('w/polled'), { interval: 20 }, listener);
    await tick(100);
    fs.writeFileSync(file('w/polled'), 'abc');
    for (let i = 0; i < 100 && !seen.length; i++) await tick(20);
    fs.unwatchFile(file('w/polled'), listener);
    return [seen[0], typeof watcher.ref, typeof watcher.unref];
  });
  await attemptAsync('unwatchFile_other_spelling', async () => {
    fs.writeFileSync(file('w/spelled'), 'a');
    let calls = 0;
    fs.watchFile(dir + '/w/./spelled', { interval: 20 }, () => { calls++; });
    await tick(60);
    fs.unwatchFile(dir + '/w/../w/spelled');
    fs.writeFileSync(file('w/spelled'), 'abcdef');
    await tick(150);
    return calls;
  });
  attempt('watchFile_no_listener', () => fs.watchFile(file('w/polled')));
}

async function main() {
  testStatsClasses();
  await testTruncate();
  await testModes();
  await testTimes();
  await testAccess();
  await testStatfs();
  await testVectors();
  await testOpendir();
  await testCp();
  await testBlob();
  await testWatch();
  results.types = ['accessSync', 'exists', 'truncate', 'ftruncateSync', 'fsync', 'fdatasyncSync', 'chmod', 'fchmodSync',
    'chown', 'fchownSync', 'utimes', 'futimesSync', 'opendir', 'opendirSync', 'cp', 'cpSync', 'statfs', 'statfsSync',
    'readv', 'writevSync', 'openAsBlob', 'watch', 'watchFile', 'unwatchFile', 'Dir', 'Stats', 'Dirent']
    .map((name) => name + ' ' + typeof fs[name]);
  results.promiseTypes = ['cp', 'opendir', 'statfs', 'truncate', 'chmod', 'chown', 'utimes'].map((name) => name + ' ' + typeof fsp[name]);
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
