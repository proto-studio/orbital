// Runs unchanged in Node and in Orbital. child_process_parity.node.json is this script's
// output under Node v20.18.1; regenerate it with
//   node child_process_parity.js > child_process_parity.node.json
// Paths and pids are fixed or reduced to their type, so the output is the same each run.
const cp = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');
const util = require('util');

const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const MISSING = '/nonexistent/orbital-parity-binary';

// The error fields a program can observe, in a stable order.
const errorShape = (err) => {
  if (!err) return err === undefined ? 'undefined' : err;
  const shape = { name: err.name, message: err.message };
  // ENOENT and EACCES have the same number everywhere. Others differ between platforms.
  if ('errno' in err) shape.errno = Math.abs(err.errno) <= 13 ? err.errno : typeof err.errno;
  for (const key of ['code', 'syscall', 'path', 'spawnargs', 'killed', 'signal', 'cmd', 'status', 'stdout', 'stderr', 'output']) {
    if (key in err) {
      const value = err[key];
      shape[key] = Buffer.isBuffer(value) ? { buffer: value.toString() }
        : Array.isArray(value) ? value.map((v) => (Buffer.isBuffer(v) ? { buffer: v.toString() } : v))
          : value;
    }
  }
  if ('pid' in err) shape.pidType = typeof err.pid;
  return shape;
};
const thrown = (fn) => {
  try {
    const value = fn();
    return { returned: Buffer.isBuffer(value) ? { buffer: value.toString() } : value };
  } catch (err) {
    return { threw: errorShape(err) };
  }
};
const syncResult = (r) => ({
  pidType: typeof r.pid,
  pidPositive: r.pid > 0,
  status: r.status,
  signal: r.signal,
  stdout: r.stdout === null ? null : r.stdout.toString(),
  stderr: r.stderr === null ? null : r.stderr.toString(),
  outputLength: r.output === null ? null : r.output.length,
  error: 'error' in r ? errorShape(r.error) : 'absent',
});

// Records the events of a child in the order they fire, with their arguments.
const events = (child, extra) => new Promise((resolve) => {
  const seen = [];
  const out = { events: seen, pidType: typeof child.pid };
  child.on('spawn', () => seen.push(['spawn']));
  child.on('error', (err) => seen.push(['error', errorShape(err)]));
  child.on('exit', (code, signal) => seen.push(['exit', code, signal]));
  child.on('close', (code, signal) => {
    seen.push(['close', code, signal]);
    out.exitCode = child.exitCode;
    out.signalCode = child.signalCode;
    out.killed = child.killed;
    out.spawnfile = child.spawnfile;
    out.spawnargs = child.spawnargs;
    resolve(out);
  });
  if (extra) extra(child, out);
});
const stdioKinds = (child) => ({
  stdio: child.stdio.map((s) => (s === null ? null : typeof s)),
  stdin: child.stdin === null ? null : typeof child.stdin.write,
  stdout: child.stdout === null ? null : typeof child.stdout.on,
  stderr: child.stderr === null ? null : typeof child.stderr.on,
});
const collect = (stream) => {
  const chunks = [];
  stream.on('data', (d) => chunks.push(Buffer.from(d)));
  return () => Buffer.concat(chunks).toString();
};
const execResult = (start) => new Promise((resolve) => {
  const child = start((err, stdout, stderr) => {
    resolve({ err: errorShape(err), stdout, stderr, kinds: stdioKinds(child) });
  });
});

(async () => {
  const out = {};
  // Unique names, not a directory: all this script needs of a temporary location.
  const tmp = path.join(os.tmpdir(), `orbital-parity-${process.pid}-`);
  const made = [];
  const file = (name) => {
    made.push(tmp + name);
    return tmp + name;
  };

  // Signals.
  out.killDefault = await events(cp.spawn('sleep', ['30']), (c, o) => c.once('spawn', () => {
    o.returned = c.kill();
  }));
  out.killSigkill = await events(cp.spawn('sleep', ['30']), (c) => c.once('spawn', () => c.kill('SIGKILL')));
  out.killNumber = await events(cp.spawn('sleep', ['30']), (c) => c.once('spawn', () => c.kill(2)));
  out.selfSignal = await events(cp.spawn('/bin/sh', ['-c', 'kill -USR2 $$']));
  out.killTwice = await new Promise((resolve) => {
    const c = cp.spawn('/bin/sh', ['-c', 'trap "" TERM; echo up; sleep 30']);
    c.stdout.once('data', () => {
      const first = c.kill('SIGTERM');
      setTimeout(() => {
        const second = c.kill('SIGKILL');
        c.on('close', (code, signal) => {
          resolve({ first, second, code, signal, afterExit: c.kill() });
        });
      }, 100);
    });
  });
  out.killUnknown = await new Promise((resolve) => {
    const c = cp.spawn('sleep', ['30']);
    const result = thrown(() => c.kill('SIGNOPE'));
    c.on('close', () => resolve(result));
    c.kill('SIGKILL');
  });
  out.exitNonZero = await events(cp.spawn('/bin/sh', ['-c', 'exit 3']));

  // Spawn failures.
  out.spawnEnoent = await events(cp.spawn(MISSING, ['a', 'b']), (c, o) => { o.kinds = stdioKinds(c); });
  out.spawnEnoentPath = await events(cp.spawn('orbital-parity-not-on-path'));
  out.spawnBadCwd = await events(cp.spawn('ls', [], { cwd: '/nonexistent/orbital-parity' }));
  fs.writeFileSync(file('plain'), 'not executable');
  const plain = await events(cp.spawn(file('plain'), ['x']));
  out.spawnEacces = JSON.parse(JSON.stringify(plain).split(tmp).join('<tmp>'));

  // Argument validation.
  out.nulArg = thrown(() => cp.spawn('echo', ['a\0b']));
  out.nulArgSecond = thrown(() => cp.spawn('echo', ['ok', 'a\0b']));
  out.nulFile = thrown(() => cp.spawn('ec\0ho'));
  out.nulEnvValue = thrown(() => cp.spawn('echo', [], { env: { A: 'x\0y' } }));
  out.nulEnvKey = thrown(() => cp.spawn('echo', [], { env: { 'A\0B': 'x' } }));
  out.nulCwd = thrown(() => cp.spawn('echo', [], { cwd: '/tm\0p' }));
  out.nulArgSpawnSync = thrown(() => cp.spawnSync('echo', ['a\0b']));
  out.nulArgExecFileSync = thrown(() => cp.execFileSync('echo', ['a\0b']));
  out.nulArgExecFile = thrown(() => cp.execFile('echo', ['a\0b'], () => {}));
  out.nulExec = thrown(() => cp.exec('echo a\0b', () => {}));
  out.nulExecSync = thrown(() => cp.execSync('echo a\0b'));
  out.emptyFile = thrown(() => cp.spawn(''));
  out.fileNotString = thrown(() => cp.spawn(1));
  out.argsNotArray = thrown(() => cp.spawn('echo', 'x'));
  out.optionsNotObject = thrown(() => cp.spawn('echo', [], 'x'));
  out.badStdioString = thrown(() => cp.spawn('echo', [], { stdio: 'nope' }));
  out.badStdioEntry = thrown(() => cp.spawn('echo', [], { stdio: ['nope'] }));
  out.execNotString = thrown(() => cp.exec(1, () => {}));

  // exec and execFile.
  out.execOk = await execResult((cb) => cp.exec('echo out; echo err >&2', cb));
  out.execFails = await execResult((cb) => cp.exec('echo out; echo err >&2; exit 4', cb));
  out.execFileOk = await execResult((cb) => cp.execFile('/bin/sh', ['-c', 'echo fine'], cb));
  out.execFileFails = await execResult((cb) => cp.execFile('/bin/sh', ['-c', 'echo bad >&2; exit 5'], cb));
  out.execFileMissing = await execResult((cb) => cp.execFile(MISSING, ['x'], cb));
  // exec runs its command with /bin/sh. Some shells, such as dash on Linux, fork sleep
  // instead of becoming it, and the sleep would keep the pipes open after the shell is
  // killed. exec makes every shell become sleep, so the signal reaches it.
  out.execKilled = await execResult((cb) => {
    const c = cp.exec('exec sleep 30', cb);
    setTimeout(() => c.kill(), 50);
    return c;
  });
  out.execTimeout = await execResult((cb) => cp.exec('exec sleep 30', { timeout: 50 }, cb));
  out.execFileTimeoutSignal = await execResult((cb) => cp.execFile('sleep', ['30'], { timeout: 50, killSignal: 'SIGKILL' }, cb));
  out.execMaxBuffer = await execResult((cb) => cp.exec('echo 0123456789', { maxBuffer: 4 }, cb));
  out.execBufferEncoding = await new Promise((resolve) => {
    cp.exec("printf 'h\\303\\251'", { encoding: 'buffer' }, (err, stdout, stderr) => {
      resolve({ err, stdout: stdout.toString('hex'), isBuffer: Buffer.isBuffer(stderr) });
    });
  });
  out.execStreams = await new Promise((resolve) => {
    const c = cp.exec('cat');
    const read = collect(c.stdout);
    c.on('close', (code, signal) => resolve({ code, signal, read: read(), spawnfile: c.spawnfile, spawnargs: c.spawnargs, kinds: stdioKinds(c) }));
    c.stdin.end('through exec');
  });
  out.execFileStreams = await new Promise((resolve) => {
    const c = cp.execFile('cat');
    const read = collect(c.stdout);
    c.on('close', (code, signal) => resolve({ code, signal, read: read(), spawnfile: c.spawnfile, spawnargs: c.spawnargs, kinds: stdioKinds(c) }));
    c.stdin.end('through execFile');
  });
  out.execEvents = await events(cp.exec('exit 6'));
  const promisifiedExec = util.promisify(cp.exec);
  const promisifiedExecFile = util.promisify(cp.execFile);
  out.promisifyOk = await promisifiedExec('echo promised');
  out.promisifyFails = await promisifiedExec('echo partial; exit 7').catch(errorShape);
  out.promisifyExecFile = await promisifiedExecFile('/bin/sh', ['-c', 'echo promised']);

  // The synchronous calls. Silenced stderr keeps the output of the test run clean.
  const quiet = { stdio: 'pipe' };
  out.execSyncOk = thrown(() => cp.execSync('echo fine'));
  out.execSyncString = thrown(() => cp.execSync('echo fine', { encoding: 'utf8' }));
  out.execSyncFails = thrown(() => cp.execSync('echo out; echo err >&2; exit 3', quiet));
  out.execSyncFailsNoStderr = thrown(() => cp.execSync('exit 3', quiet));
  out.execSyncFailsEncoded = thrown(() => cp.execSync('echo out; echo err >&2; exit 3', { ...quiet, encoding: 'utf8' }));
  out.execSyncSignal = thrown(() => cp.execSync('kill -TERM $$', quiet));
  out.execSyncTimeout = thrown(() => cp.execSync('exec sleep 30', { ...quiet, timeout: 50 }));
  // Whether the child exits before the kill arrives is a race, in Node too.
  const noRace = (shape) => {
    const { status, signal, ...rest } = shape;
    return rest;
  };
  out.execSyncMaxBuffer = noRace(thrown(() => cp.execSync('echo 0123456789', { ...quiet, maxBuffer: 4 })).threw);
  out.execFileSyncOk = thrown(() => cp.execFileSync('/bin/sh', ['-c', 'echo fine']));
  out.execFileSyncFails = thrown(() => cp.execFileSync('/bin/sh', ['-c', 'echo err >&2; exit 2'], quiet));
  out.execFileSyncMissing = thrown(() => cp.execFileSync(MISSING, ['x'], quiet));
  out.execFileSyncTimeout = thrown(() => cp.execFileSync('sleep', ['30'], { ...quiet, timeout: 50, killSignal: 'SIGKILL' }));

  out.spawnSyncOk = syncResult(cp.spawnSync('/bin/sh', ['-c', 'echo out; echo err >&2']));
  out.spawnSyncFails = syncResult(cp.spawnSync('/bin/sh', ['-c', 'exit 9']));
  out.spawnSyncSignal = syncResult(cp.spawnSync('/bin/sh', ['-c', 'kill -TERM $$']));
  out.spawnSyncMissing = syncResult(cp.spawnSync(MISSING, ['x']));
  out.spawnSyncTimeout = syncResult(cp.spawnSync('sleep', ['30'], { timeout: 50 }));
  out.spawnSyncMaxBuffer = noRace(syncResult(cp.spawnSync('/bin/sh', ['-c', 'echo 0123456789'], { maxBuffer: 4 })));
  out.spawnSyncShell = syncResult(cp.spawnSync('echo shelled | tr a-z A-Z', { shell: true }));
  out.spawnSyncIgnore = syncResult(cp.spawnSync('/bin/sh', ['-c', 'echo out'], { stdio: 'ignore' }));
  out.spawnSyncIgnoreOne = syncResult(cp.spawnSync('/bin/sh', ['-c', 'echo out; echo err >&2'], { stdio: ['pipe', 'ignore', 'pipe'] }));
  const syncTarget = fs.openSync(file('sync-fd'), 'w');
  out.spawnSyncToFd = syncResult(cp.spawnSync('/bin/sh', ['-c', 'echo to-file'], { stdio: ['pipe', syncTarget, 'pipe'] }));
  fs.closeSync(syncTarget);
  out.spawnSyncToFdContents = fs.readFileSync(file('sync-fd'), 'utf8');

  // The stdio option.
  const ran = (child) => new Promise((resolve) => {
    const kinds = stdioKinds(child);
    child.on('close', (code, signal) => resolve({ code, signal, kinds }));
  });
  out.stdioDefault = await ran(cp.spawn('true'));
  out.stdioPipe = await ran(cp.spawn('true', [], { stdio: 'pipe' }));
  out.stdioIgnore = await ran(cp.spawn('/bin/sh', ['-c', 'echo discarded'], { stdio: 'ignore' }));
  out.stdioArray = await ran(cp.spawn('true', [], { stdio: ['ignore', 'pipe', 'ignore'] }));
  out.stdioShortArray = await ran(cp.spawn('true', [], { stdio: ['ignore'] }));
  out.stdioNullEntries = await ran(cp.spawn('true', [], { stdio: [null, undefined, 'ignore'] }));
  out.stdioIgnoreStdinIsEmpty = await new Promise((resolve) => {
    const c = cp.spawn('cat', [], { stdio: ['ignore', 'pipe', 'pipe'] });
    const read = collect(c.stdout);
    c.on('close', (code) => resolve({ code, read: read() }));
  });
  out.stdioFdWrite = await (async () => {
    const fd = fs.openSync(file('fd-out'), 'w');
    const result = await ran(cp.spawn('/bin/sh', ['-c', 'echo via-fd; echo via-err >&2'], { stdio: ['ignore', fd, fd] }));
    fs.closeSync(fd);
    result.contents = fs.readFileSync(file('fd-out'), 'utf8');
    return result;
  })();
  out.stdioFdRead = await (async () => {
    fs.writeFileSync(file('fd-in'), 'from-file');
    const fd = fs.openSync(file('fd-in'), 'r');
    const child = cp.spawn('cat', [], { stdio: [fd, 'pipe', 'ignore'] });
    const read = collect(child.stdout);
    const result = await ran(child);
    fs.closeSync(fd);
    result.read = read();
    return result;
  })();
  // What an inheriting child writes is checked by TestChildProcessInherit.
  out.stdioInherit = await ran(cp.spawn('true', [], { stdio: 'inherit' }));
  out.stdioExtraIgnore = await ran(cp.spawn('/bin/sh', ['-c', 'if (echo x >&3) 2>/dev/null; then exit 0; else exit 7; fi'], { stdio: ['ignore', 'ignore', 'ignore', 'ignore'] }));
  out.stdioExtraFd = await (async () => {
    const fd = fs.openSync(file('fd-extra'), 'w');
    const result = await ran(cp.spawn('/bin/sh', ['-c', 'echo on-three >&3'], { stdio: ['ignore', 'ignore', 'ignore', fd] }));
    fs.closeSync(fd);
    result.contents = fs.readFileSync(file('fd-extra'), 'utf8');
    return result;
  })();
  out.stdioExtraPipe = await new Promise((resolve) => {
    const c = cp.spawn('/bin/sh', ['-c', 'echo on-three >&3'], { stdio: ['ignore', 'ignore', 'ignore', 'pipe'] });
    const read = collect(c.stdio[3]);
    c.on('close', (code, signal) => resolve({ code, signal, read: read(), kinds: stdioKinds(c), three: typeof c.stdio[3] }));
  });
  out.shellOption = await new Promise((resolve) => {
    const c = cp.spawn('echo a b | tr a-z A-Z', { shell: true });
    const read = collect(c.stdout);
    c.on('close', (code) => resolve({ code, read: read(), spawnfile: c.spawnfile, spawnargs: c.spawnargs }));
  });
  out.shellWithArgs = await new Promise((resolve) => {
    const c = cp.spawn('echo', ['joined', 'args'], { shell: true });
    const read = collect(c.stdout);
    c.on('close', (code) => resolve({ code, read: read(), spawnfile: c.spawnfile, spawnargs: c.spawnargs }));
  });

  for (const name of new Set(made)) fs.unlinkSync(name);
  // maxBuffer counts bytes, whatever the encoding; for the sync calls it covers all output
  // together, and 0 turns it off.
  out.execFileHexMaxBuffer = await execResult((cb) => cp.execFile('printf', ['abc'], { encoding: 'hex', maxBuffer: 4 }, cb));
  out.execFileBase64Chunks = await execResult((cb) => cp.execFile('/bin/sh', ['-c', 'printf a; sleep 0.1; printf b'], { encoding: 'base64' }, cb));
  out.execFileBase64Url = await execResult((cb) => cp.execFile('printf', ['abc\\373'], { encoding: 'base64url' }, cb));
  out.execFileUtf8Recovery = await execResult((cb) => cp.execFile('printf', ['\\342A\\360\\237\\230\\200'], cb));
  out.execFileUtf16OddByte = await execResult((cb) => cp.execFile('printf', ['a\\000b'], { encoding: 'utf16le' }, cb));
  out.spawnSyncMaxBufferZero = syncResult(cp.spawnSync('printf', ['ok'], { maxBuffer: 0 }));
  // How much output arrives before the kill is a race, so only the error is compared.
  out.spawnSyncMaxBufferCombined = (cp.spawnSync('/bin/sh', ['-c', 'printf %040d 0; printf %040d 0 >&2'], { maxBuffer: 60 }).error || {}).code;
  // A negative stdio entry asks for the default, a pipe.
  out.spawnSyncNegativeStdio = syncResult(cp.spawnSync('/bin/echo', ['ok'], { stdio: [-1, -1, -1] }));
  out.spawnNegativeStdio = await ran(cp.spawn('/bin/echo', ['ok'], { stdio: [-1, -1, -1] }));

  finish(out);
})();
