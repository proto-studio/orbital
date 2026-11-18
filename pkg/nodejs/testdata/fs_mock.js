// Run by TestFS_CallFormsMapToFilesystem against a mock runtime.Filesystem (fs_mock_test.go).
// Each case marks where it starts (see mockFS), so the test can check the interface calls it
// made. Error cases fail at the interface with a chosen errno; those named for a case in
// fs_memory_links.js make the same call, and must fail as Node did there.
const fs = require('fs');
const fsp = require('fs/promises');
const os = require('os');
const path = require('path');

const dir = globalThis.FS_DIR;
const file = (name) => path.join(dir, name);
const raw = (name) => dir + '/' + name;
const results = {};

// As in fs_memory_links.js, so results compare with fs_memory_links.node.json.
const local = (s) => (typeof s === 'string' ? s.split(dir).join('<dir>') : Array.isArray(s) ? s.map(local) : s);
function describe(e) {
  if (!e) return null;
  const errno = typeof e.errno === 'number' && os.constants.errno[e.code] === -e.errno ? 'matches code' : e.errno;
  return { name: e.name, code: e.code, errno, syscall: e.syscall, path: local(e.path), dest: local(e.dest), message: local(e.message) };
}
const callback = (fn) => new Promise((resolve, reject) => fn((err, ...values) => (err ? reject(err) : resolve(values))));
const drain = (stream) => new Promise((resolve, reject) => {
  const chunks = [];
  stream.on('data', (c) => chunks.push(c)).on('error', reject).on('end', () => resolve(Buffer.concat(chunks).toString()));
});
const finished = (stream) => new Promise((resolve, reject) => stream.on('error', reject).on('close', () => resolve(null)));

// The same call in its synchronous, callback and promise forms.
function forms(name, ...args) {
  return [
    [name + 'Sync', () => fs[name + 'Sync'](...args)],
    [name, () => callback((cb) => fs[name](...args, cb))],
    ['promises.' + name, () => fsp[name](...args)],
  ];
}

// [case, errno or '-', the only method that fails or undefined, call]
const cases = [
  // Every form of each call, with nothing failing.
  ...forms('readFile', file('f'), 'utf8').map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('writeFile', file('f'), 'data').map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('appendFile', file('f'), 'more').map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('stat', file('f')).map(([n, fn]) => [n, '-', undefined, () => Promise.resolve(fn()).then(() => null)]),
  ...forms('lstat', file('f')).map(([n, fn]) => [n, '-', undefined, () => Promise.resolve(fn()).then(() => null)]),
  ...forms('readdir', file('dir')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('mkdir', file('dir')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('mkdir', file('dir/a/b'), { recursive: true }).map(([n, fn]) => [n + '_recursive', '-', undefined, fn]),
  ...forms('rmdir', file('dir')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('rmdir', file('dir'), { recursive: true }).map(([n, fn]) => [n + '_recursive', '-', undefined, fn]),
  ...forms('unlink', file('f')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('rename', file('f'), file('g')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('copyFile', file('f'), file('g')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('access', file('f'), fs.constants.R_OK).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('chmod', file('f'), 0o600).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('chown', file('f'), 1, 2).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('utimes', file('f'), 1000, 2000).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('truncate', file('f'), 2).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('symlink', 'target', file('l')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('readlink', file('l')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('link', file('f'), file('h')).map(([n, fn]) => [n, '-', undefined, fn]),
  ...forms('statfs', file('f')).map(([n, fn]) => [n, '-', undefined, () => Promise.resolve(fn()).then(() => null)]),
  ['realpathSync.native', '-', undefined, () => fs.realpathSync.native(file('f'))],
  ['realpath.native', '-', undefined, () => callback((cb) => fs.realpath.native(file('f'), cb))],
  ['promises.realpath', '-', undefined, () => fsp.realpath(file('f'))],
  ['existsSync', '-', undefined, () => fs.existsSync(file('f'))],
  ['exists', '-', undefined, () => new Promise((resolve) => fs.exists(file('f'), resolve))],
  ['descriptorSync', '-', undefined, () => {
    const fd = fs.openSync(file('f'), 'r+');
    const buf = Buffer.alloc(3);
    fs.readSync(fd, buf, 0, 3, 1);
    fs.writeSync(fd, 'XY', 2);
    fs.closeSync(fd);
    return buf.toString();
  }],
  ['descriptor', '-', undefined, async () => {
    const [fd] = await callback((cb) => fs.open(file('f'), 'a', cb));
    await callback((cb) => fs.write(fd, 'Z', cb));
    await callback((cb) => fs.close(fd, cb));
    return null;
  }],
  ['promises.FileHandle', '-', undefined, async () => {
    const handle = await fsp.open(file('f'), 'r');
    const { bytesRead, buffer } = await handle.read(Buffer.alloc(5), 0, 5, 0);
    await handle.close();
    return buffer.toString('utf8', 0, bytesRead);
  }],
  ['createReadStream', '-', undefined, () => drain(fs.createReadStream(file('f')))],
  ['createWriteStream', '-', undefined, () => {
    const stream = fs.createWriteStream(file('f'));
    stream.end('streamed');
    return finished(stream);
  }],

  // Errors from the interface, in each form, as Node gives them.
  ['loop_stat', 'ELOOP', undefined, () => fs.statSync(file('loop'))],
  ['loop_read', 'ELOOP', undefined, () => fs.readFileSync(file('cyc_a'))],
  ['loop_write', 'ELOOP', undefined, () => fs.writeFileSync(file('cyc_a'), 'x')],
  ['loop_open', 'ELOOP', undefined, () => fs.openSync(file('cyc_b'), 'r')],
  ['loop_readdir', 'ELOOP', undefined, () => fs.readdirSync(file('cyc_a'))],
  ['loop_access', 'ELOOP', undefined, () => fs.accessSync(file('cyc_a'))],
  ['loop_chmod', 'ELOOP', undefined, () => fs.chmodSync(file('cyc_a'), 0o644)],
  ['loop_mkdir_inner', 'ELOOP', undefined, () => fs.mkdirSync(file('cyc_a/x'))],
  ['loop_realpath_native', 'ELOOP', undefined, () => fs.realpathSync.native(file('cyc_a'))],
  ['loop_copy', 'ELOOP', undefined, () => fs.copyFileSync(file('f'), file('cyc_a'))],
  ['link_loop_path', 'ELOOP', undefined, () => fs.linkSync(file('f'), file('cyc_a/h'))],
  ['dangling_stat', 'ENOENT', undefined, () => fs.statSync(file('dangling'))],
  ['dangling_read', 'ENOENT', undefined, () => fs.readFileSync(file('dangling'))],
  ['dangling_open_excl', 'EEXIST', undefined, () => fs.openSync(file('dangling'), 'wx')],
  ['dangling_mkdir', 'EEXIST', undefined, () => fs.mkdirSync(file('dangling'))],
  ['dangling_realpath_native', 'ENOENT', undefined, () => fs.realpathSync.native(file('dangling'))],
  ['name_too_long_stat', 'ENAMETOOLONG', undefined, () => fs.statSync(file('n'.repeat(256)))],
  ['name_too_long_write', 'ENAMETOOLONG', undefined, () => fs.writeFileSync(file('n'.repeat(256)), 'x')],
  ['name_too_long_symlink', 'ENAMETOOLONG', undefined, () => fs.symlinkSync('f', file('n'.repeat(256)))],
  ['symlink_onto_file', 'EEXIST', undefined, () => fs.symlinkSync('x', file('f'))],
  ['symlink_missing_parent', 'ENOENT', undefined, () => fs.symlinkSync('x', file('nodir/l'))],
  ['symlink_parent_file', 'ENOTDIR', undefined, () => fs.symlinkSync('x', file('f/l'))],
  ['readlink_file', 'EINVAL', undefined, () => fs.readlinkSync(file('f'))],
  ['readlink_missing', 'ENOENT', undefined, () => fs.readlinkSync(file('nope'))],
  ['link_exists', 'EEXIST', undefined, () => fs.linkSync(file('f'), file('copied'))],
  ['link_missing', 'ENOENT', undefined, () => fs.linkSync(file('nope'), file('h0'))],
  ['link_dir', 'EPERM', undefined, () => fs.linkSync(file('b'), file('bh'))],
  ['read_through_file', 'ENOTDIR', undefined, () => fs.readFileSync(file('f/x'))],
  ['through_file', 'ENOTDIR', undefined, () => fs.statSync(file('abs/x'))],
  ['file_trailing_slash_lstat', 'ENOTDIR', undefined, () => fs.lstatSync(raw('f/'))],
  ['rmdir_link', 'ENOTDIR', undefined, () => fs.rmdirSync(file('bl'))],
  ['cb_symlink_exists', 'EEXIST', undefined, () => callback((cb) => fs.symlink('f', file('cbl'), cb))],
  ['cb_readlink_file', 'EINVAL', undefined, () => callback((cb) => fs.readlink(file('f'), cb))],
  ['cb_stat_loop', 'ELOOP', undefined, () => callback((cb) => fs.stat(file('cyc_a'), cb))],
  ['cb_stat_dangling', 'ENOENT', undefined, () => callback((cb) => fs.stat(file('dangling2'), cb))],
  ['cb_link_exists', 'EEXIST', undefined, () => callback((cb) => fs.link(file('f'), file('cbh'), cb))],
  ['cb_link_dir', 'EPERM', undefined, () => callback((cb) => fs.link(file('b'), file('cbbh'), cb))],
  ['cb_realpath_native_loop', 'ELOOP', undefined, () => callback((cb) => fs.realpath.native(file('cyc_a'), cb))],
  ['cb_read_loop', 'ELOOP', undefined, () => callback((cb) => fs.readFile(file('cyc_a'), cb))],
  ['p_symlink_exists', 'EEXIST', undefined, () => fsp.symlink('b', file('pl'))],
  ['p_readlink_dir', 'EINVAL', undefined, () => fsp.readlink(file('b'))],
  ['p_stat_loop', 'ELOOP', undefined, () => fsp.stat(file('cyc_a'))],
  ['p_link_missing', 'ENOENT', undefined, () => fsp.link(file('nope'), file('ph2'))],
  ['p_realpath_loop', 'ELOOP', undefined, () => fsp.realpath(file('cyc_a'))],
  ['p_read_loop', 'ELOOP', undefined, () => fsp.readFile(file('cyc_a'))],
  ['p_open_loop', 'ELOOP', undefined, () => fsp.open(file('cyc_a'), 'r')],
];

async function main() {
  for (const [name, errno, method, fn] of cases) {
    fs.existsSync('/@mark/' + name + '/' + errno + (method ? '/' + method : ''));
    try {
      const value = await fn();
      results[name] = { value: value === undefined ? null : local(value) };
    } catch (e) {
      results[name] = { error: describe(e) };
    }
  }
  fs.existsSync('/@mark/end/-');
  return results;
}

main().then(done, (err) => done({ failed: String(err && err.stack || err) }));
