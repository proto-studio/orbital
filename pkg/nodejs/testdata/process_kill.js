// Runs unchanged in Node and in Orbital, with a signaler that sends real signals.
// process_kill.node.json is this script's output under Node v20.18.1; regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestProcessKill_MatchesNode
const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));
const attempt = (fn) => {
  try {
    return { returned: fn() };
  } catch (e) {
    return { name: e.name, code: e.code, errno: e.errno, syscall: e.syscall, message: e.message };
  }
};
// No process has this pid: pids stay far below it on macOS and Linux.
const gone = 2147483646;

finish({
  exists: attempt(() => process.kill(process.pid, 0)),
  existsByString: attempt(() => process.kill(String(process.pid), 0)),
  unknownName: attempt(() => process.kill(process.pid, 'SIGFOO')),
  lowercaseName: attempt(() => process.kill(process.pid, 'sigterm')),
  fractionSignal: attempt(() => process.kill(process.pid, 1.5)),
  stringPid: attempt(() => process.kill('abc')),
  fractionPid: attempt(() => process.kill(1.5)),
  noPid: attempt(() => process.kill()),
  objectPid: attempt(() => process.kill({})),
  noSuchProcess: attempt(() => process.kill(gone, 0)),
  noSuchProcessTerm: attempt(() => process.kill(gone)),
  invalidSignalNumber: attempt(() => process.kill(process.pid, 999)),
});
