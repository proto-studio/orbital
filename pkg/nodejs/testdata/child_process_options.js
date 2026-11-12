// Runs unchanged in Node and in Orbital; TestChildProcessOptions checks its output, which
// matches Node v20.18.1 on macOS. TOOL_DIR holds an executable named orbital-only-here.
const cp = require('child_process');
const toolDir = globalThis.TOOL_DIR || process.argv[2];
const results = {};
const exitOf = (child) => new Promise((resolve) => child.on('exit', (code, signal) => resolve([code, signal])));
const outputOf = (child) => new Promise((resolve) => {
  let out = '';
  child.stdout.on('data', (d) => { out += d; });
  child.on('close', () => resolve(out.trim()));
});

async function main() {
  const started = Date.now();
  results.timeout = await exitOf(cp.spawn('sleep', ['5'], { timeout: 100 }));
  results.killSignal = await exitOf(cp.spawn('sleep', ['5'], { timeout: 100, killSignal: 'SIGINT' }));
  results.timeout_fast = Date.now() - started < 3000;

  const controller = new AbortController();
  const aborted = cp.spawn('sleep', ['5'], { signal: controller.signal });
  const errors = [];
  aborted.on('error', (e) => errors.push([e.name, e.code, e.message]));
  setTimeout(() => controller.abort(), 50);
  results.abort_exit = await exitOf(aborted);
  results.abort_errors = errors;

  const early = cp.spawn('sleep', ['5'], { signal: AbortSignal.abort() });
  const earlyErrors = [];
  early.on('error', (e) => earlyErrors.push(e.code));
  results.abort_before = await exitOf(early);
  results.abort_before_errors = earlyErrors;

  results.argv0 = await outputOf(cp.spawn('sh', ['-c', 'echo $0'], { argv0: 'custom0' }));
  results.spawnargs = cp.spawn('true', ['x'], { argv0: 'named' }).spawnargs;
  results.env_path = await outputOf(cp.spawn('orbital-only-here', [], { env: { PATH: toolDir + ':/usr/bin:/bin' } }));

  // A detached child leads its own process group, so its group id is its pid.
  const detached = cp.spawn('sh', ['-c', 'echo $$ $(ps -o pgid= -p $$)'], { detached: true });
  const [pid, pgid] = (await outputOf(detached)).split(/\s+/);
  results.detached_leader = pid === pgid;
  const attached = cp.spawn('sh', ['-c', 'echo $$ $(ps -o pgid= -p $$)']);
  const [pid2, pgid2] = (await outputOf(attached)).split(/\s+/);
  results.attached_leader = pid2 === pgid2;

  const ignored = cp.spawn('sleep', ['2'], { stdio: 'ignore' });
  ignored.unref();
  results.unref_pid = typeof ignored.pid === 'number';
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
