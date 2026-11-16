// Runs unchanged in Node and in Orbital. fs_dotdot.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestFSDotDotAfterLink_MatchesNode'
// The directory holds f, b/f, b/sub/ and alias -> b/sub. A ".." after alias applies to where
// alias leads, b/sub, so alias/.. is b.
const fs = require('fs');
const path = require('path');

const dir = globalThis.FS_DIR || process.argv[2];
const results = {};
function attempt(name, fn) {
  try {
    const value = fn();
    results[name] = { value: value === undefined ? null : value };
  } catch (e) {
    results[name] = { error: e.code };
  }
}
const at = (p) => path.join(dir, 'alias') + '/' + p;

attempt('readFile', () => fs.readFileSync(at('../f'), 'utf8'));
attempt('stat', () => fs.statSync(at('../f')).size);
attempt('exists', () => fs.existsSync(at('../f')));
attempt('readdir', () => fs.readdirSync(at('..')).sort());
attempt('writeFile', () => { fs.writeFileSync(at('../written'), 'w'); return fs.existsSync(path.join(dir, 'b', 'written')); });
attempt('mkdir', () => { fs.mkdirSync(at('../made')); return fs.statSync(path.join(dir, 'b', 'made')).isDirectory(); });
attempt('rename', () => { fs.renameSync(at('../written'), at('../moved')); return fs.existsSync(path.join(dir, 'b', 'moved')); });
attempt('unlink', () => { fs.unlinkSync(at('../moved')); return fs.existsSync(path.join(dir, 'b', 'moved')); });
attempt('relative', () => {
  const cwd = process.cwd();
  process.chdir(dir);
  try {
    return fs.readFileSync('alias/../f', 'utf8');
  } finally {
    process.chdir(cwd);
  }
});

if (globalThis.done) done(results);
else process.stdout.write(JSON.stringify(results));
