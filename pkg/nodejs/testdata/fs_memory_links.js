// Runs unchanged in Node and in Orbital. fs_memory_links.node.json is this script's output
// under Node v20.18.1 on a real disk. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSMemoryLinks_MatchesNode'
// Orbital runs it on the local filesystem. It covers following symbolic links, dangling
// links, link cycles, calls on a link itself and hard links, in the synchronous, callback
// and promise forms. fs_mock.js makes the failing calls again against a mock filesystem,
// and must fail as this capture did.
const fs = require('fs');
const fsp = require('fs/promises');
const os = require('os');
const path = require('path');

const dir = fs.realpathSync(globalThis.FS_DIR || process.argv[2]);
const file = (name) => path.join(dir, name);
// A path whose ".." stays as written, for the OS to apply after the links before it.
const raw = (name) => dir + '/' + name;
const results = {};

const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : Array.isArray(s) ? s.map(local) : s);
function describe(e) {
  if (!e) return null;
  // errno differs between Linux and macOS for some codes, so it is checked against the code.
  const errno = typeof e.errno === 'number' && os.constants.errno[e.code] === -e.errno ? 'matches code' : e.errno;
  return { name: e.name, code: e.code, errno, syscall: e.syscall, path: local(e.path), dest: local(e.dest), message: local(e.message) };
}
function attempt(name, fn) {
  try {
    const value = fn();
    results[name] = { value: value === undefined ? null : local(value) };
  } catch (e) {
    results[name] = { error: describe(e) };
  }
}
async function attemptAsync(name, fn) {
  try {
    const value = await fn();
    results[name] = { value: value === undefined ? null : local(value) };
  } catch (e) {
    results[name] = { error: describe(e) };
  }
}
const callback = (fn) => new Promise((resolve, reject) => fn((err, ...values) => (err ? reject(err) : resolve(values))));
const kind = (s) => [s.isSymbolicLink(), s.isFile(), s.isDirectory(), (s.mode & 0o170000).toString(8)];

async function main() {
  // The issue's example.
  fs.mkdirSync(file('b/sub'), { recursive: true });
  fs.writeFileSync(file('b/f'), 'inner');
  attempt('example', () => {
    fs.symlinkSync('b/sub', file('alias'));
    return [fs.readlinkSync(file('alias')), fs.lstatSync(file('alias')).isSymbolicLink(),
      fs.readFileSync(raw('alias/../f'), 'utf8'), fs.realpathSync(file('alias')), fs.realpathSync.native(raw('alias/../f'))];
  });
  attempt('dotdot_write', () => { fs.writeFileSync(raw('alias/../w'), 'w'); return fs.readdirSync(file('b')).sort(); });
  attempt('dotdot_mkdir', () => { fs.mkdirSync(raw('alias/../made')); return fs.statSync(file('b/made')).isDirectory(); });

  // Targets relative to the link's own directory, absolute ones, and chains of links.
  fs.writeFileSync(file('f'), 'hi');
  attempt('relative_in_subdir', () => { fs.symlinkSync('../f', file('b/up')); return fs.readFileSync(file('b/up'), 'utf8'); });
  attempt('absolute', () => { fs.symlinkSync(file('f'), file('abs')); return [fs.readFileSync(file('abs'), 'utf8'), fs.readlinkSync(file('abs'))]; });
  attempt('chain', () => {
    fs.symlinkSync('abs', file('chain1'));
    fs.symlinkSync('chain1', file('chain2'));
    return [fs.readFileSync(file('chain2'), 'utf8'), fs.realpathSync(file('chain2')), fs.realpathSync.native(file('chain2'))];
  });
  attempt('lstat_link', () => { const s = fs.lstatSync(file('chain1')); return [...kind(s), s.size]; });
  attempt('stat_link', () => { const s = fs.statSync(file('chain1')); return [...kind(s), s.size]; });
  attempt('dir_link', () => {
    fs.symlinkSync('b', file('bl'));
    return [kind(fs.statSync(file('bl'))), kind(fs.lstatSync(file('bl'))), kind(fs.lstatSync(file('bl/'))), fs.readdirSync(file('bl')).sort(),
      fs.readFileSync(file('bl/up'), 'utf8')];
  });
  attempt('dirents', () => fs.readdirSync(dir, { withFileTypes: true })
    .map((d) => [d.name, d.isSymbolicLink(), d.isFile(), d.isDirectory()])
    .sort((a, b) => a[0].localeCompare(b[0])));
  attempt('handle_through_link', () => {
    const fd = fs.openSync(file('abs'), 'r+');
    fs.writeSync(fd, 'HI', 0);
    const st = fs.fstatSync(fd);
    fs.closeSync(fd);
    return [fs.readFileSync(file('f'), 'utf8'), st.isFile()];
  });
  attempt('nofollow', () => {
    const { O_WRONLY, O_RDONLY, O_TRUNC, O_NOFOLLOW } = fs.constants;
    const codes = [];
    for (const flags of [O_WRONLY | O_TRUNC | O_NOFOLLOW, O_RDONLY | O_NOFOLLOW]) {
      try { fs.closeSync(fs.openSync(file('abs'), flags)); codes.push('ok'); } catch (e) { codes.push(e.code + ' ' + e.syscall); }
    }
    fs.closeSync(fs.openSync(file('f'), O_RDONLY | O_NOFOLLOW));
    return [codes, fs.readFileSync(file('f'), 'utf8')];
  });
  attempt('copy_through_links', () => {
    fs.copyFileSync(file('chain2'), file('copied'));
    fs.symlinkSync('copytarget', file('copylink'));
    fs.copyFileSync(file('copied'), file('copylink'));
    return [fs.readFileSync(file('copied'), 'utf8'), fs.readFileSync(file('copytarget'), 'utf8'), fs.lstatSync(file('copylink')).isSymbolicLink()];
  });
  attempt('metadata_through_link', () => {
    fs.chmodSync(file('abs'), 0o600);
    fs.truncateSync(file('abs'), 1);
    fs.utimesSync(file('abs'), 1000, 2000);
    fs.accessSync(file('abs'), fs.constants.R_OK);
    const s = fs.statSync(file('f'));
    return [(s.mode & 0o777).toString(8), s.mtimeMs, s.size];
  });
  attempt('mkdir_recursive_through_link', () => { fs.mkdirSync(file('bl/deep/er'), { recursive: true }); return fs.statSync(file('b/deep/er')).isDirectory(); });

  // A link that leads nowhere.
  fs.symlinkSync('nowhere', file('dangling'));
  attempt('dangling_lstat', () => kind(fs.lstatSync(file('dangling'))));
  attempt('dangling_stat', () => fs.statSync(file('dangling')));
  attempt('dangling_read', () => fs.readFileSync(file('dangling')));
  attempt('dangling_exists', () => fs.existsSync(file('dangling')));
  attempt('dangling_readlink', () => fs.readlinkSync(file('dangling')));
  attempt('dangling_realpath', () => fs.realpathSync(file('dangling')));
  attempt('dangling_realpath_native', () => fs.realpathSync.native(file('dangling')));
  attempt('dangling_open_excl', () => fs.openSync(file('dangling'), 'wx'));
  attempt('dangling_mkdir', () => fs.mkdirSync(file('dangling')));
  attempt('dangling_mkdir_recursive', () => fs.mkdirSync(file('dangling'), { recursive: true }));
  attempt('dangling_mkdir_recursive_child', () => fs.mkdirSync(file('dangling/a/b'), { recursive: true }));
  attempt('dangling_mkdir_recursive_made', () => fs.existsSync(file('nowhere')));
  attempt('dangling_write', () => { fs.writeFileSync(file('dangling'), 'made'); return [fs.readFileSync(file('nowhere'), 'utf8'), fs.lstatSync(file('dangling')).isSymbolicLink()]; });

  // Link cycles.
  fs.symlinkSync('loop', file('loop'));
  fs.symlinkSync('cyc_b', file('cyc_a'));
  fs.symlinkSync('cyc_a', file('cyc_b'));
  attempt('loop_stat', () => fs.statSync(file('loop')));
  attempt('loop_lstat', () => kind(fs.lstatSync(file('loop'))));
  attempt('loop_readlink', () => fs.readlinkSync(file('loop')));
  attempt('loop_read', () => fs.readFileSync(file('cyc_a')));
  attempt('loop_write', () => fs.writeFileSync(file('cyc_a'), 'x'));
  attempt('loop_open', () => fs.openSync(file('cyc_b'), 'r'));
  attempt('loop_readdir', () => fs.readdirSync(file('cyc_a')));
  attempt('loop_exists', () => fs.existsSync(file('cyc_a')));
  attempt('loop_access', () => fs.accessSync(file('cyc_a')));
  attempt('loop_chmod', () => fs.chmodSync(file('cyc_a'), 0o644));
  attempt('loop_inner', () => fs.statSync(file('cyc_a/x')));
  attempt('loop_mkdir_inner', () => fs.mkdirSync(file('cyc_a/x')));
  attempt('loop_realpath', () => fs.realpathSync(file('cyc_a')));
  attempt('loop_realpath_native', () => fs.realpathSync.native(file('cyc_a')));
  attempt('loop_copy', () => fs.copyFileSync(file('f'), file('cyc_a')));

  // A file in the middle of a path.
  attempt('through_file', () => fs.statSync(file('abs/x')));
  // A "/" after a file. After a link to a file it depends on the OS version: older macOS
  // follows the link and ignores the "/".
  attempt('file_trailing_slash', () => fs.statSync(raw('f/')));
  attempt('file_trailing_slash_lstat', () => fs.lstatSync(raw('f/')));
  attempt('read_through_file', () => fs.readFileSync(file('f/x')));

  // Names longer than every OS allows.
  attempt('name_too_long_stat', () => fs.statSync(file('n'.repeat(256))));
  attempt('name_too_long_write', () => fs.writeFileSync(file('n'.repeat(256)), 'x'));
  attempt('name_too_long_symlink', () => fs.symlinkSync('f', file('n'.repeat(256))));

  // Errors from the link calls themselves.
  attempt('symlink_onto_file', () => fs.symlinkSync('x', file('f')));
  attempt('symlink_onto_dir', () => fs.symlinkSync('x', file('b')));
  attempt('symlink_onto_link', () => fs.symlinkSync('x', file('dangling')));
  attempt('symlink_onto_loop', () => fs.symlinkSync('x', file('loop')));
  attempt('symlink_missing_parent', () => fs.symlinkSync('x', file('nodir/l')));
  attempt('symlink_parent_file', () => fs.symlinkSync('x', file('f/l')));
  attempt('symlink_slash', () => fs.symlinkSync('x', file('newl') + '/'));
  attempt('symlink_slash_made', () => !!fs.lstatSync(file('newl'), { throwIfNoEntry: false }));
  attempt('symlink_slash_onto_dir', () => fs.symlinkSync('x', file('b') + '/'));
  attempt('link_slash', () => fs.linkSync(file('f'), file('newh') + '/'));
  attempt('link_slash_made', () => fs.existsSync(file('newh')));
  attempt('symlink_through_link', () => { fs.symlinkSync('../f', file('bl/viabl')); return fs.readFileSync(file('b/viabl'), 'utf8'); });
  attempt('readlink_file', () => fs.readlinkSync(file('f')));
  attempt('readlink_dir', () => fs.readlinkSync(file('b')));
  attempt('readlink_missing', () => fs.readlinkSync(file('nope')));
  attempt('readlink_trailing_slash', () => fs.readlinkSync(file('bl/')));
  attempt('link_exists', () => fs.linkSync(file('f'), file('copied')));
  attempt('link_onto_link', () => fs.linkSync(file('f'), file('dangling')));
  attempt('link_missing', () => fs.linkSync(file('nope'), file('h0')));
  attempt('link_missing_parent', () => fs.linkSync(file('f'), file('nodir/h')));
  attempt('link_dir', () => fs.linkSync(file('b'), file('bh')));
  attempt('link_loop_path', () => fs.linkSync(file('f'), file('cyc_a/h')));

  // Hard links share one file.
  attempt('hard', () => {
    fs.writeFileSync(file('orig'), 'one');
    fs.linkSync(file('orig'), file('hard'));
    fs.writeFileSync(file('hard'), 'two');
    fs.appendFileSync(file('orig'), '+');
    fs.chmodSync(file('hard'), 0o640);
    const a = fs.statSync(file('orig')), b = fs.statSync(file('hard'));
    return [fs.readFileSync(file('orig'), 'utf8'), a.nlink, b.nlink, a.ino === b.ino, (a.mode & 0o777).toString(8),
      a.ino === fs.statSync(file('f')).ino];
  });
  attempt('hard_through_symlink_dir', () => { fs.linkSync(file('bl/f'), file('hard_b')); return [fs.readFileSync(file('hard_b'), 'utf8'), fs.statSync(file('b/f')).nlink]; });
  attempt('hard_handle', () => {
    const fd = fs.openSync(file('hard'), 'a');
    fs.writeSync(fd, '!');
    fs.closeSync(fd);
    return fs.readFileSync(file('orig'), 'utf8');
  });
  attempt('hard_rename_onto_other', () => { fs.renameSync(file('orig'), file('hard')); return [fs.existsSync(file('orig')), fs.existsSync(file('hard')), fs.statSync(file('hard')).nlink]; });
  attempt('hard_unlink', () => { fs.unlinkSync(file('orig')); return [fs.readFileSync(file('hard'), 'utf8'), fs.statSync(file('hard')).nlink]; });
  attempt('hard_rename_over', () => {
    fs.writeFileSync(file('h1'), 'h1');
    fs.linkSync(file('h1'), file('h2'));
    fs.writeFileSync(file('other'), 'other');
    fs.renameSync(file('other'), file('h2'));
    return [fs.statSync(file('h1')).nlink, fs.readFileSync(file('h2'), 'utf8')];
  });

  // Calls that act on the link itself.
  attempt('unlink_link', () => { fs.symlinkSync('f', file('ul')); fs.unlinkSync(file('ul')); return [fs.existsSync(file('ul')), fs.existsSync(file('f'))]; });
  attempt('unlink_dir_link', () => { fs.symlinkSync('b', file('udl')); fs.unlinkSync(file('udl')); return fs.readdirSync(file('b')).length > 0; });
  attempt('rename_link', () => {
    fs.symlinkSync('f', file('rl'));
    fs.renameSync(file('rl'), file('rl2'));
    return [fs.existsSync(file('rl')), fs.readlinkSync(file('rl2')), fs.readFileSync(file('f'), 'utf8')];
  });
  attempt('rename_onto_link', () => {
    fs.writeFileSync(file('src'), 'src');
    fs.symlinkSync('f', file('dstlink'));
    fs.renameSync(file('src'), file('dstlink'));
    return [fs.lstatSync(file('dstlink')).isSymbolicLink(), fs.readFileSync(file('dstlink'), 'utf8'), fs.readFileSync(file('f'), 'utf8')];
  });
  attempt('rmdir_link', () => fs.rmdirSync(file('bl')));
  attempt('rm_link', () => { fs.symlinkSync('b', file('rml')); fs.rmSync(file('rml'), { recursive: true }); return [fs.existsSync(file('rml')), fs.readdirSync(file('b')).length > 0]; });
  attempt('rm_tree_with_links', () => {
    fs.mkdirSync(file('tree'));
    fs.symlinkSync('../b', file('tree/tob'));
    fs.symlinkSync('../loop', file('tree/toloop'));
    fs.rmSync(file('tree'), { recursive: true });
    return [fs.existsSync(file('tree')), fs.readdirSync(file('b')).length > 0];
  });
  attempt('unlink_loop', () => { fs.unlinkSync(file('loop')); return fs.existsSync(file('loop')); });

  // The callback and promise forms.
  await attemptAsync('cb_symlink', async () => { await callback((cb) => fs.symlink('f', file('cbl'), cb)); return fs.readFileSync(file('cbl'), 'utf8'); });
  await attemptAsync('cb_symlink_exists', () => callback((cb) => fs.symlink('f', file('cbl'), cb)));
  await attemptAsync('cb_readlink', async () => (await callback((cb) => fs.readlink(file('cbl'), cb)))[0]);
  await attemptAsync('cb_readlink_file', () => callback((cb) => fs.readlink(file('f'), cb)));
  await attemptAsync('cb_lstat', async () => kind((await callback((cb) => fs.lstat(file('cbl'), cb)))[0]));
  await attemptAsync('cb_stat_loop', () => callback((cb) => fs.stat(file('cyc_a'), cb)));
  await attemptAsync('cb_stat_dangling', () => callback((cb) => fs.stat(file('dangling2'), cb)));
  await attemptAsync('cb_link', async () => { await callback((cb) => fs.link(file('f'), file('cbh'), cb)); return fs.statSync(file('cbh')).nlink; });
  await attemptAsync('cb_link_exists', () => callback((cb) => fs.link(file('f'), file('cbh'), cb)));
  await attemptAsync('cb_link_dir', () => callback((cb) => fs.link(file('b'), file('cbbh'), cb)));
  await attemptAsync('cb_realpath', async () => (await callback((cb) => fs.realpath(file('cbl'), cb)))[0]);
  await attemptAsync('cb_realpath_native_loop', () => callback((cb) => fs.realpath.native(file('cyc_a'), cb)));
  await attemptAsync('cb_read_loop', () => callback((cb) => fs.readFile(file('cyc_a'), cb)));
  await attemptAsync('cb_unlink_link', async () => { await callback((cb) => fs.unlink(file('cbl'), cb)); return fs.existsSync(file('f')); });
  await attemptAsync('p_symlink', async () => { await fsp.symlink('b', file('pl')); return (await fsp.readdir(file('pl'))).sort(); });
  await attemptAsync('p_symlink_exists', () => fsp.symlink('b', file('pl')));
  await attemptAsync('p_readlink', () => fsp.readlink(file('pl')));
  await attemptAsync('p_readlink_dir', () => fsp.readlink(file('b')));
  await attemptAsync('p_lstat', async () => kind(await fsp.lstat(file('pl'))));
  await attemptAsync('p_stat_loop', () => fsp.stat(file('cyc_a')));
  await attemptAsync('p_link', async () => { await fsp.link(file('f'), file('ph')); return (await fsp.stat(file('f'))).nlink; });
  await attemptAsync('p_link_missing', () => fsp.link(file('nope'), file('ph2')));
  await attemptAsync('p_realpath', () => fsp.realpath(raw('pl/sub/..')));
  await attemptAsync('p_realpath_loop', () => fsp.realpath(file('cyc_a')));
  await attemptAsync('cb_realpath_loop', () => callback((cb) => fs.realpath(file('cyc_a'), cb)));
  await attemptAsync('p_read_loop', () => fsp.readFile(file('cyc_a')));
  await attemptAsync('p_open_loop', () => fsp.open(file('cyc_a'), 'r'));
  await attemptAsync('p_handle_through_link', async () => {
    const h = await fsp.open(file('abs'), 'r');
    try {
      return (await h.readFile('utf8'));
    } finally {
      await h.close();
    }
  });
  await attemptAsync('p_unlink_link', async () => { await fsp.unlink(file('pl')); return [fs.existsSync(file('pl')), fs.existsSync(file('b'))]; });
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
