// Runs unchanged in Node and in Orbital. fs_links.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSLinks_MatchesNode'
// It covers symlink, readlink, link, lstat of links, realpath and mkdtemp.
const fs = require('fs');
const fsp = require('fs/promises');
const path = require('path');

const dir = fs.realpathSync(globalThis.FS_DIR || process.argv[2]);
const file = (name) => path.join(dir, name);
const results = {};

const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : Array.isArray(s) ? s.map(local) : s);
function describe(e) {
  if (!e) return null;
  return { name: e.name, code: e.code, errno: e.errno, syscall: e.syscall, path: local(e.path), dest: local(e.dest), message: local(e.message) };
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
  fs.writeFileSync(file('f'), 'hi');
  fs.mkdirSync(file('d'));
  fs.writeFileSync(file('d/inner'), 'inner');

  attempt('symlink', () => fs.symlinkSync('f', file('l')));
  attempt('symlink_dir', () => fs.symlinkSync('d', file('ld'), 'dir'));
  attempt('symlink_exists', () => fs.symlinkSync('f', file('l')));
  attempt('symlink_missing_dir', () => fs.symlinkSync('f', file('nodir/l')));
  attempt('symlink_bad_type', () => fs.symlinkSync('f', file('l3'), 'bogus'));
  attempt('readlink', () => fs.readlinkSync(file('l')));
  attempt('readlink_buffer', () => Array.from(fs.readlinkSync(file('l'), 'buffer')));
  attempt('url_paths', () => {
    const { pathToFileURL } = require('url');
    fs.symlinkSync('f', pathToFileURL(file('lu')));
    fs.linkSync(pathToFileURL(file('f')), pathToFileURL(file('hu')));
    return [fs.readlinkSync(pathToFileURL(file('lu'))), fs.readFileSync(file('hu'), 'utf8'), local(fs.realpathSync(pathToFileURL(file('lu'))))];
  });
  attempt('url_paths_everywhere', () => {
    const { pathToFileURL } = require('url');
    const u = pathToFileURL(file('f'));
    fs.chmodSync(u, 0o640);
    fs.utimesSync(u, 1000, 2000);
    return [fs.readFileSync(u, 'utf8'), (fs.statSync(u).mode & 0o777).toString(8), fs.statSync(u).mtimeMs, fs.existsSync(u),
      fs.readdirSync(pathToFileURL(file('d'))), fs.existsSync(new URL('http://x/y'))];
  });
  attempt('url_encoded_slash', () => fs.symlinkSync('/x', new URL('file://' + file('a%2Fb'))));
  attempt('url_wrong_scheme', () => fs.readFileSync(new URL('http://example.com/f')));
  attempt('readlink_file', () => fs.readlinkSync(file('f')));
  attempt('readlink_missing', () => fs.readlinkSync(file('nope')));
  attempt('lstat_link', () => kind(fs.lstatSync(file('l'))));
  attempt('stat_link', () => kind(fs.statSync(file('l'))));
  attempt('lstat_dir_link', () => kind(fs.lstatSync(file('ld'))));
  attempt('read_through_link', () => fs.readFileSync(file('l'), 'utf8'));
  attempt('readdir_through_link', () => fs.readdirSync(file('ld')));
  attempt('dirents', () => fs.readdirSync(dir, { withFileTypes: true })
    .map((d) => [d.name, d.isSymbolicLink(), d.isFile(), d.isDirectory()])
    .sort((a, b) => a[0].localeCompare(b[0])));
  attempt('link', () => { fs.linkSync(file('f'), file('h')); return [fs.readFileSync(file('h'), 'utf8'), fs.statSync(file('f')).nlink]; });
  attempt('link_exists', () => fs.linkSync(file('f'), file('h')));
  attempt('link_missing', () => fs.linkSync(file('nope'), file('h2')));
  attempt('realpath', () => fs.realpathSync(file('l')));
  attempt('realpath_dir', () => fs.realpathSync(file('ld/inner')));
  attempt('realpath_native', () => fs.realpathSync.native(file('ld/inner')));
  attempt('realpath_missing', () => fs.realpathSync(file('nope')));
  attempt('realpath_native_missing', () => fs.realpathSync.native(file('nope')));
  fs.symlinkSync('nowhere', file('dangling'));
  attempt('lstat_dangling', () => kind(fs.lstatSync(file('dangling'))));
  attempt('realpath_dangling', () => fs.realpathSync(file('dangling')));
  attempt('unlink_link', () => { fs.unlinkSync(file('dangling')); return fs.existsSync(file('nowhere')); });
  attempt('rm_dir_link', () => { fs.symlinkSync('d', file('ld2')); fs.rmSync(file('ld2')); return fs.readdirSync(file('d')); });
  attempt('mkdtemp', () => {
    const p = fs.mkdtempSync(file('tmp-'));
    return [path.basename(p).length, /^tmp-[A-Za-z0-9]{6}$/.test(path.basename(p)), (fs.statSync(p).mode & 0o777).toString(8)];
  });
  attempt('mkdtemp_missing', () => fs.mkdtempSync(file('nodir/tmp-')));
  attempt('mkdtemp_bad', () => fs.mkdtempSync(1));
  attempt('cp_link', () => {
    fs.mkdirSync(file('cpsrc'));
    fs.symlinkSync('../f', file('cpsrc/rel'));
    fs.cpSync(file('cpsrc'), file('cpdst'), { recursive: true });
    return [fs.lstatSync(file('cpdst/rel')).isSymbolicLink(), fs.readlinkSync(file('cpdst/rel')), fs.readFileSync(file('cpdst/rel'), 'utf8')];
  });

  attempt('cp_dereference_onto_source', () => {
    fs.mkdirSync(file('dsrc/a'), { recursive: true });
    fs.writeFileSync(file('dsrc/a/f'), 'keep');
    fs.mkdirSync(file('ddst'));
    fs.symlinkSync(file('dsrc/a'), file('ddst/a'));
    try {
      fs.cpSync(file('dsrc'), file('ddst'), { recursive: true, dereference: true });
    } finally {
      results.cp_dereference_kept = { value: fs.readFileSync(file('dsrc/a/f'), 'utf8') };
    }
  });
  await attemptAsync('symlink_callback', async () => { await callback((cb) => fs.symlink('f', file('lc'), cb)); return fs.readlinkSync(file('lc')); });
  await attemptAsync('readlink_callback', async () => (await callback((cb) => fs.readlink(file('lc'), cb)))[0]);
  await attemptAsync('lstat_callback', async () => kind((await callback((cb) => fs.lstat(file('lc'), cb)))[0]));
  await attemptAsync('link_callback', async () => { await callback((cb) => fs.link(file('f'), file('hc'), cb)); return fs.readFileSync(file('hc'), 'utf8'); });
  await attemptAsync('realpath_callback', async () => (await callback((cb) => fs.realpath(file('lc'), cb)))[0]);
  await attemptAsync('realpath_callback_missing', () => callback((cb) => fs.realpath(file('nope'), cb)));
  await attemptAsync('realpath_native_callback', async () => (await callback((cb) => fs.realpath.native(file('lc'), cb)))[0]);
  await attemptAsync('mkdtemp_callback', async () => path.basename((await callback((cb) => fs.mkdtemp(file('cb-'), cb)))[0]).length);
  await attemptAsync('readlink_callback_missing', () => callback((cb) => fs.readlink(file('nope'), cb)));
  await attemptAsync('promises', async () => {
    await fsp.symlink('f', file('lp'));
    await fsp.link(file('f'), file('hp'));
    const p = await fsp.mkdtemp(file('p-'));
    return [await fsp.readlink(file('lp')), await fsp.realpath(file('lp')), kind(await fsp.lstat(file('lp'))), path.basename(p).length];
  });
  await attemptAsync('promises_symlink_exists', () => fsp.symlink('f', file('lp')));

  results.types = ['symlink', 'symlinkSync', 'readlink', 'readlinkSync', 'link', 'linkSync', 'mkdtemp', 'mkdtempSync']
    .map((name) => name + ' ' + typeof fs[name]);
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
