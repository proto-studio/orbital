package nodejs

import (
	"errors"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runExit runs script, which is expected to call process.exit, and returns the exit code and
// what the script recorded in globalThis.log.
func runExit(t *testing.T, script string) (int, string) {
	t.Helper()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, runErr := inst.Runtime.Run("globalThis.log = [];\n"+script, "exit.js")
	var exit *runtime.ExitError
	if !errors.As(runErr, &exit) {
		t.Fatalf("Run returned %v, want an *ExitError", runErr)
	}
	// The runtime has ended, so running anything more returns the same error.
	if _, again := inst.Runtime.Run("log.push('ran again')", "again.js"); !errors.As(again, &exit) {
		t.Fatalf("second Run returned %v", again)
	}
	logged, err := inst.Runtime.RunScript("JSON.stringify(globalThis.log)", "read.js")
	if err != nil {
		t.Fatal(err)
	}
	return exit.Code, logged.String()
}

// process.exit runs the 'exit' listeners once and ends the runtime, not the host process,
// with the code Node would exit with. Nothing after it runs. The expected values are what
// Node 20.18.1 prints and exits with for the same scripts.
func TestProcessExit_MatchesNode(t *testing.T) {
	for _, tc := range []struct {
		name, script, log string
		code              int
	}{
		{"listener exits again", `process.on('exit', (c) => { log.push('exit ' + c); process.exit(11); }); process.exit(2);`, `["exit 2"]`, 11},
		{"exitCode with no argument", `process.exitCode = 3; process.on('exit', (c) => log.push('exit ' + c)); process.exit();`, `["exit 3"]`, 3},
		{"listener sets exitCode", `process.on('exit', (c) => { log.push('exit ' + c); process.exitCode = 9; }); process.exit(1); log.push('after');`, `["exit 1"]`, 9},
		{"from a timer", `setTimeout(() => log.push('never'), 10); setTimeout(() => { log.push('t1'); process.exit(4); log.push('after'); }, 0);`, `["t1"]`, 4},
		{"from a promise", `Promise.resolve().then(() => { log.push('then'); process.exit(6); }).then(() => log.push('next'));`, `["then"]`, 6},
		{"skips finally", `try { process.exit(12); } finally { log.push('finally'); }`, `[]`, 12},
		{"string code", `process.on('exit', (c) => log.push(c + ' ' + process.exitCode)); process.exit('7');`, `["7 7"]`, 7},
		{"zero", `process.on('exit', (c) => log.push('exit ' + c)); process.exit(0);`, `["exit 0"]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, log := runExit(t, tc.script)
			if code != tc.code || log != tc.log {
				t.Errorf("exit %d with log %s, want exit %d with log %s", code, log, tc.code, tc.log)
			}
		})
	}
}

// process.exit in a worker ends only the worker, which reports the code to its parent.
func TestProcessExit_InWorker(t *testing.T) {
	got := runJS(t, &runtime.Config{Timeout: 30 * time.Second}, `
const { Worker } = require('worker_threads');
const w = new Worker("process.exit(5); throw new Error('ran on');", { eval: true });
const events = [];
w.on('error', (e) => events.push('error ' + e.message));
w.on('exit', (code) => {
  events.push('exit ' + code);
  setTimeout(() => done({ events, parent: 'still running' }), 10);
});`)
	wantJSON(t, got, map[string]any{"events": []any{"exit 5"}, "parent": "still running"})
}
