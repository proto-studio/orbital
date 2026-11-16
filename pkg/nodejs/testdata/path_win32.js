// Runs unchanged in Node and in Orbital. path_win32.node.json is this script's output under
// Node v20.18.1; regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestPath_PosixAndWin32MatchNode
const path = require('path');
const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const attempt = (fn) => {
  try {
    return fn();
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
};
const { win32, posix } = path;
const out = {};

out.shape = [typeof posix, typeof win32, posix === path, posix.posix === posix, posix.win32 === win32,
  win32.win32 === win32, win32.posix === posix, win32.sep, win32.delimiter, posix.sep, posix.delimiter,
  require('path/posix') === posix, require('path/win32') === win32, win32._makeLong === win32.toNamespacedPath];

const winPaths = ['C:\\foo\\test', 'C:\\foo\\test\\bar\\package.json', 'c:/a/b/../c', 'C:', 'C:foo', 'C:\\',
  '\\\\server\\share\\dir\\file.txt', '\\\\server\\share', '//server/share/x', '\\\\?\\C:\\x\\y',
  '\\\\.\\device', 'a\\b\\..\\..\\..\\c', '', '.', '..\\x', 'foo.tar.gz', '\\foo\\', 'C:\\x\\.hidden',
  'file:stream', 'a/b\\c', 'C:\\x\\y.', '\\\\', '\\\\a', 'D:\\x\\\\y\\\\'];
// toNamespacedPath resolves relative paths against the working directory, which
// differs between machines, so it is shown as <cwd>.
const cwdWin = process.cwd().replace(/\//g, '\\');
const cwdParentWin = cwdWin.slice(0, cwdWin.lastIndexOf('\\'));
const hideCwd = (v) => (typeof v === 'string' ? v.split(cwdWin).join('<cwd>').split(cwdParentWin).join('<cwd>\\..') : v);
for (const fn of ['normalize', 'isAbsolute', 'dirname', 'basename', 'extname', 'parse', 'toNamespacedPath']) {
  out['win32.' + fn] = winPaths.map((p) => hideCwd(attempt(() => win32[fn](p))));
}
out['win32.basename suffix'] = [win32.basename('C:\\a\\b.TXT', '.TXT'), win32.basename('C:\\a\\b.txt', 'b.txt'),
  win32.basename('aaa\\', 'a'), win32.basename('C:file.js', '.js')];
out['win32.join'] = [win32.join('C:\\a', 'b', '..\\c'), win32.join('\\\\server', 'share'), win32.join('//server', 'share', 'x'),
  win32.join('', ''), win32.join('a', '', 'C:'), win32.join('c:', 'file'), win32.join('\\\\', '/x')];
out['win32.resolve'] = [win32.resolve('C:\\a', 'b', '..\\c'), win32.resolve('C:\\a', 'D:\\b'),
  win32.resolve('\\\\server\\share\\a', '..\\..\\..\\b'), win32.resolve('C:\\a', '\\b'), win32.resolve('c:/ignore', 'c:/some/file')];
out['win32.relative'] = [win32.relative('C:\\foo\\test', 'C:\\foo\\test\\bar\\package.json'),
  win32.relative('C:\\orandea\\test\\aaa', 'C:\\orandea\\impl\\bbb'), win32.relative('c:\\a\\B', 'C:\\A\\b\\C'),
  win32.relative('C:\\a', 'D:\\a'), win32.relative('\\\\foo\\bar\\baz', '\\\\foo\\bar\\baz-quux'),
  win32.relative('C:\\baz', '\\\\foo\\bar\\baz'), win32.relative('C:\\a', 'C:\\a')];
out['win32.format'] = [win32.format({ root: 'C:\\', dir: 'C:\\path\\dir', base: 'file.txt' }),
  win32.format({ root: 'C:\\', name: 'file', ext: 'txt' }), win32.format({ dir: '\\\\s\\h', name: 'x', ext: '.y' })];

const posixPaths = ['/foo/bar//baz/asdf/quux/..', 'a/b/', '//a', '///a/b', '', '.', '..', '/', 'a\\b', '/x/.hidden',
  'x.', '.a.b', '/a/b.c.d', 'C:\\x'];
for (const fn of ['normalize', 'isAbsolute', 'dirname', 'basename', 'extname', 'parse', 'toNamespacedPath']) {
  out['posix.' + fn] = posixPaths.map((p) => attempt(() => posix[fn](p)));
}
out['posix.join'] = [posix.join('/a', 'b', '../c'), posix.join('a', '', 'b/'), posix.join(), posix.join('a\0b', 'c')];
out['posix.resolve'] = [posix.resolve('/a', 'b', '../c'), posix.resolve('/a/', '/b/'), posix.resolve('a') === posix.join(process.cwd(), 'a'),
  posix.resolve() === process.cwd()];
out['posix.relative'] = [posix.relative('/data/orandea/test/aaa', '/data/orandea/impl/bbb'), posix.relative('/a', '/a'),
  posix.relative('/a/b', '/a'), posix.relative('/', '/a/b')];
out['posix.format'] = [posix.format({ root: '/', base: 'x' }), posix.format({ dir: '/a', name: 'b', ext: 'c' }), posix.format({})];

out.errors = [
  attempt(() => path.join(5)), attempt(() => path.resolve('a', {})), attempt(() => path.basename('a', 3)),
  attempt(() => win32.relative(null, 'a')), attempt(() => path.format(null)), attempt(() => win32.format('x')),
  attempt(() => path.join(Symbol('s'))), attempt(() => path.format([])), attempt(() => win32.parse(1)),
];
finish(out);
