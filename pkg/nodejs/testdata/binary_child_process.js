// Runs unchanged in Node and in Orbital. binary_child_process.node.json is this script's
// output under Node v20.18.1; regenerate it with
//   node binary_child_process.js > binary_child_process.node.json
// Hex from `od` shows what the child really received, without trusting the read path.
const cp = require('child_process');

const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const bin = Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43]);
const all = Buffer.from(Array.from({ length: 256 }, (_, i) => i));
const OD = 'od -An -tx1 -v | tr -d " \\n"';
const EMIT = "printf '\\101\\000\\102\\377\\000\\103'";
const EMIT_UTF8 = "printf 'h\\303\\251'";

// Waits for 'close' and reports the exit code with everything read from stdout.
const closed = (child) => new Promise((resolve) => {
  const chunks = [];
  child.stdout.on('data', (d) => chunks.push(d));
  child.on('close', (code) => resolve({ code, output: Buffer.concat(chunks) }));
});
const shell = (cmd) => cp.spawn('/bin/sh', ['-c', cmd]);
const stdinHex = async (write) => {
  const child = shell(OD);
  const result = closed(child);
  write(child.stdin);
  return (await result).output.toString();
};
const stdoutHex = async (cmd) => {
  const { code, output } = await closed(shell(cmd));
  return [code, output.toString('hex')];
};
const execResult = (fn) => new Promise((resolve) => fn((err, stdout, stderr) => resolve({ err, stdout, stderr })));

(async () => {
  const out = {};

  // Bytes written to stdin.
  out.stdinBuffer = await stdinHex((s) => s.end(bin));
  out.stdinLeadingNul = await stdinHex((s) => s.end(Buffer.from([0, 0, 1])));
  out.stdinAllBytes = await stdinHex((s) => s.end(all));
  out.stdinLatin1 = await stdinHex((s) => s.end('A\0B\xffC', 'latin1'));
  out.stdinBinary = await stdinHex((s) => s.end('A\0B\xffC', 'binary'));
  out.stdinUtf8 = await stdinHex((s) => s.end('A\0BÿC'));
  out.stdinHexEncoding = await stdinHex((s) => s.end('410042ff0043', 'hex'));
  out.stdinUint8Array = await stdinHex((s) => s.end(new Uint8Array([1, 0, 2, 255, 0])));
  out.stdinWritesInOrder = await stdinHex((s) => {
    s.write(Buffer.from([1, 0]));
    s.write('\0', 'latin1');
    s.write(Buffer.from([0, 2]));
    s.write('é');
    s.write(new Uint8Array([0, 255]));
    s.end(Buffer.from([0]));
  });

  // Bytes read from stdout and stderr, which are not valid UTF-8.
  out.stdout = await stdoutHex(EMIT);
  out.stderr = await new Promise((resolve) => {
    const child = shell(`${EMIT} >&2`);
    const chunks = [];
    child.stderr.on('data', (d) => chunks.push(d));
    child.on('close', (code) => resolve([code, Buffer.concat(chunks).toString('hex')]));
  });
  out.readableEvent = await new Promise((resolve) => {
    const child = shell(EMIT);
    const chunks = [];
    child.stdout.on('readable', () => {
      let d;
      while ((d = child.stdout.read())) chunks.push(d);
    });
    child.on('close', () => resolve(Buffer.concat(chunks).toString('hex')));
  });
  out.setEncoding = await new Promise((resolve) => {
    const child = shell(EMIT_UTF8);
    child.stdout.setEncoding('utf8');
    let text = '';
    child.stdout.on('data', (d) => { text += `${typeof d}:${d}`; });
    child.on('close', () => resolve(text));
  });
  out.shellOption = await new Promise(async (resolve) => {
    const { code, output } = await closed(cp.spawn(EMIT, { shell: true }));
    resolve([code, output.toString('hex')]);
  });

  // Both directions at once, past the size of the pipe buffers.
  const big = Buffer.alloc(1 << 20);
  for (let i = 0; i < big.length; i++) big[i] = (i * 31) & 255;
  const cat = cp.spawn('/bin/cat');
  const catResult = closed(cat);
  cat.stdin.end(big);
  const { code: catCode, output: catOutput } = await catResult;
  out.megabyteThroughCat = [catCode, catOutput.length, big.equals(catOutput)];

  const from = shell(EMIT);
  const to = shell(OD);
  const piped = closed(to);
  from.stdout.pipe(to.stdin);
  out.pipeBetweenChildren = (await piped).output.toString();

  // The process lifecycle these streams depend on.
  const started = shell('exit 0');
  out.stdioAtSpawn = [typeof started.pid, !!started.stdin, !!started.stdout, !!started.stderr, started.stdio.length];
  await closed(started);
  out.eventOrder = await new Promise((resolve) => {
    const events = [];
    const child = shell(`${EMIT}; exit 3`);
    let bytes = 0;
    child.on('spawn', () => events.push('spawn'));
    child.on('exit', (code, signal) => { child.exitArgs = [code, signal]; });
    child.stdout.on('data', (d) => { bytes += d.length; });
    child.stdout.on('end', () => events.push(`stdout end after ${bytes}`));
    child.on('close', (code, signal) => {
      events.push(`close ${code} ${signal}`);
      resolve({ events, exit: child.exitArgs });
    });
  });
  out.closeWithUnreadOutput = await new Promise((resolve) => {
    shell(EMIT).on('close', resolve);
  });
  out.writeCallbacks = await new Promise((resolve) => {
    const events = [];
    const child = cp.spawn('/bin/cat');
    child.stdout.resume();
    child.stdin.write(bin, () => events.push('write'));
    child.stdin.on('finish', () => events.push('finish'));
    child.on('close', () => resolve(events));
    child.stdin.end();
  });
  out.writeToClosedStdin = await new Promise((resolve) => {
    const errors = [];
    const child = shell('exec 0<&-; sleep 2');
    child.stdin.on('error', (e) => errors.push(`${e.code} ${e.syscall}`));
    child.on('close', (code) => resolve([code, errors]));
    setTimeout(() => child.stdin.write(bin), 1000);
  });
  out.writeAfterExit = await new Promise((resolve) => {
    const events = [];
    const child = shell('exit 0');
    child.stdin.on('error', (e) => events.push(`error ${e.code}`));
    child.on('close', () => {
      child.stdin.write(bin, (e) => events.push(`callback ${e && e.code}`));
      setTimeout(() => resolve(events), 50);
    });
  });
  out.failedSpawn = await new Promise((resolve) => {
    const events = [];
    const child = cp.spawn('/nonexistent/program');
    events.push(`stdout ${!!child.stdout}`);
    child.on('spawn', () => events.push('spawn'));
    child.on('error', () => events.push('error'));
    child.on('close', (code) => {
      events.push(`close ${typeof code}`);
      resolve(events);
    });
  });
  out.killResult = await new Promise((resolve) => {
    const child = cp.spawn('/bin/sleep', ['30']);
    const first = child.kill();
    child.on('close', () => resolve([first, child.killed, child.kill()]));
  });

  // Buffered asynchronous calls.
  let r = await execResult((cb) => cp.exec(EMIT_UTF8, cb));
  out.execDefaultsToUtf8 = [r.err, r.stdout, typeof r.stderr];
  r = await execResult((cb) => cp.exec(EMIT, { encoding: 'buffer' }, cb));
  out.execBuffer = [Buffer.isBuffer(r.stdout), r.stdout.toString('hex')];
  r = await execResult((cb) => cp.exec(EMIT, { encoding: null }, cb));
  out.execNullEncoding = [Buffer.isBuffer(r.stdout), r.stdout.toString('hex')];
  r = await execResult((cb) => cp.execFile('/bin/sh', ['-c', EMIT], { encoding: 'latin1' }, cb));
  out.execFileLatin1 = Buffer.from(r.stdout, 'latin1').toString('hex');

  // Synchronous calls.
  const execSyncOut = cp.execSync(EMIT);
  out.execSyncDefaultsToBuffer = [Buffer.isBuffer(execSyncOut), execSyncOut.toString('hex')];
  out.execSyncUtf8 = cp.execSync(EMIT_UTF8, { encoding: 'utf8' });
  out.execSyncInput = cp.execSync(OD, { input: bin }).toString();
  const execFileOut = cp.execFileSync('/bin/sh', ['-c', EMIT]);
  out.execFileSyncDefaultsToBuffer = [Buffer.isBuffer(execFileOut), execFileOut.toString('hex')];
  out.execFileSyncInputAllBytes = cp.execFileSync('/bin/sh', ['-c', OD], { input: all }).toString();
  out.execFileSyncInputUint8Array = cp.execFileSync('/bin/sh', ['-c', OD], { input: new Uint8Array([0, 1, 0]) }).toString();
  out.execFileSyncInputDataView = cp.execFileSync('/bin/sh', ['-c', OD], {
    input: new DataView(bin.buffer, bin.byteOffset + 1, 3),
  }).toString();
  out.execFileSyncInputString = cp.execFileSync('/bin/sh', ['-c', OD], { input: 'A\0é' }).toString();
  out.execFileSyncInputLatin1 = cp.execFileSync('/bin/sh', ['-c', OD], { input: 'A\0\xff', encoding: 'latin1' });
  try {
    cp.execFileSync('/bin/cat', [], { input: 123 });
    out.invalidInput = 'did not throw';
  } catch (e) {
    out.invalidInput = e.code;
  }
  const sync = cp.spawnSync('/bin/sh', ['-c', `${EMIT}; ${EMIT} >&2; exit 4`]);
  out.spawnSync = {
    stdoutIsBuffer: Buffer.isBuffer(sync.stdout),
    stdout: sync.stdout.toString('hex'),
    stderr: sync.stderr.toString('hex'),
    status: sync.status,
    signal: sync.signal,
    outputIsBuffers: [sync.output[0], Buffer.isBuffer(sync.output[1]), Buffer.isBuffer(sync.output[2])],
  };
  const syncText = cp.spawnSync('/bin/sh', ['-c', EMIT_UTF8], { encoding: 'utf8' });
  out.spawnSyncEncoding = [syncText.stdout, syncText.output[1]];
  out.spawnSyncInput = cp.spawnSync('/bin/sh', ['-c', OD], { input: bin }).stdout.toString();

  finish(out);
})().catch((e) => finish({ failed: String(e && e.stack || e) }));
