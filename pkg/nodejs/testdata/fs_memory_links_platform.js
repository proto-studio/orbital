// Run by TestFSMemoryLinks_PlatformCasesMatchDisk on the in-memory filesystem and on the real
// disk, which must agree. These cases differ between platforms, so no single capture from
// Node covers them: a "/" after a link makes unlink, rmdir and rename act on where the link
// leads on macOS, and Linux refuses it. A "/" after a link to a file, or after a link that
// leads nowhere as a new link's name, is left out: older macOS differs from current macOS
// there, and Go tests check those.
const fs = require('fs');

const dir = globalThis.FS_DIR;
let n = 0;
// Each case gets its own directory holding target/x, empty/, f, and links to each of them
// and to nothing.
function fresh() {
  const d = dir + '/c' + n++;
  fs.mkdirSync(d);
  fs.mkdirSync(d + '/target');
  fs.writeFileSync(d + '/target/x', 'x');
  fs.mkdirSync(d + '/empty');
  fs.writeFileSync(d + '/f', 'f');
  fs.symlinkSync('target', d + '/link');
  fs.symlinkSync('empty', d + '/elink');
  fs.symlinkSync('f', d + '/flink');
  fs.symlinkSync('missing', d + '/dangling');
  return d;
}
const there = (p) => !!fs.lstatSync(p, { throwIfNoEntry: false });
const state = (d) => ['target/x', 'empty', 'f', 'link', 'elink', 'flink', 'missing', 'moved'].filter((name) => there(d + '/' + name));
const cases = {
  unlink_dir: (d) => fs.unlinkSync(d + '/empty'),
  unlink_dir_slash: (d) => fs.unlinkSync(d + '/empty/'),
  unlink_file_slash: (d) => fs.unlinkSync(d + '/f/'),
  unlink_link: (d) => fs.unlinkSync(d + '/elink'),
  unlink_link_slash: (d) => fs.unlinkSync(d + '/link/'),
  unlink_empty_link_slash: (d) => fs.unlinkSync(d + '/elink/'),
  unlink_dangling_slash: (d) => fs.unlinkSync(d + '/dangling/'),
  rmdir_link: (d) => fs.rmdirSync(d + '/elink'),
  rmdir_link_slash: (d) => fs.rmdirSync(d + '/elink/'),
  rmdir_full_link_slash: (d) => fs.rmdirSync(d + '/link/'),
  rmdir_full: (d) => fs.rmdirSync(d + '/target'),
  rename_link_slash: (d) => fs.renameSync(d + '/elink/', d + '/moved'),
  mkdir_dangling_slash: (d) => fs.mkdirSync(d + '/dangling/'),
  mkdir_dir_link_slash: (d) => fs.mkdirSync(d + '/link/'),
  symlink_dir_link_slash: (d) => fs.symlinkSync('x', d + '/link/'),
  mkdirp_file_link_child: (d) => fs.mkdirSync(d + '/flink/a', { recursive: true }),
  mkdirp_link: (d) => fs.mkdirSync(d + '/link', { recursive: true }),
  mkdirp_through_link: (d) => fs.mkdirSync(d + '/link/a/b', { recursive: true }),
};
const results = {};
for (const [name, fn] of Object.entries(cases)) {
  const d = fresh();
  let r;
  try {
    const value = fn(d);
    r = { value: value === undefined ? null : value.split(d).join('<d>') };
  } catch (e) {
    r = { code: e.code, syscall: e.syscall, message: e.message.split(d).join('<d>') };
  }
  r.state = state(d);
  results[name] = r;
}
done(results);
