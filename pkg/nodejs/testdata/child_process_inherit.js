// Runs unchanged in Node and in Orbital. TestChildProcessInherit gives it stdin and checks
// what reaches stdout and stderr, which script cannot see for itself. The expected values
// there are what Node v20.18.1 wrote.
const cp = require('child_process');

const quietly = (child) => new Promise((resolve) => child.on('close', resolve));

(async () => {
  await quietly(cp.spawn('cat', [], { stdio: 'inherit' }));
  await quietly(cp.spawn('/bin/sh', ['-c', 'echo to-out; echo to-err >&2'], { stdio: ['ignore', 'inherit', 'inherit'] }));
  // A descriptor number and a stream with one mean the same as inheriting.
  await quietly(cp.spawn('/bin/sh', ['-c', 'echo by-number'], { stdio: ['ignore', 1, 2] }));
  await quietly(cp.spawn('/bin/sh', ['-c', 'echo by-stream'], { stdio: ['ignore', process.stdout, process.stderr] }));
  // Swapped: the child's stdout is this process's stderr.
  await quietly(cp.spawn('/bin/sh', ['-c', 'echo swapped'], { stdio: ['ignore', 2, 1] }));
  // Without a stdio option, a synchronous call's stderr also reaches this process's.
  cp.execSync('echo exec-sync-err >&2');
  cp.execFileSync('/bin/sh', ['-c', 'echo exec-file-sync-err >&2']);
  // With one it does not.
  cp.execSync('echo not-shown >&2', { stdio: 'pipe' });
  cp.spawnSync('/bin/sh', ['-c', 'echo spawn-sync-out; echo spawn-sync-err >&2'], { stdio: 'inherit' });
  process.exit(0);
})();
