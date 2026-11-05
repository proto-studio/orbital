package nodejs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Setting this to a Node binary runs the tests that use parityCommand against Node, which
// shows that their expected values are Node's:
//
//	ORBITAL_PARITY_NODE=node go test ./pkg/nodejs -run 'WriteBeforeExit|ChildProcessInherit'
const parityNodeEnv = "ORBITAL_PARITY_NODE"

// helperScriptEnv makes the test binary act as the runtime's CLI for one script.
const helperScriptEnv = "ORBITAL_TEST_HELPER_SCRIPT"

// TestHelperRunScript runs a script as the CLI would, so what reaches its stdio and its
// peers when it exits can be seen from outside.
func TestHelperRunScript(t *testing.T) {
	path := os.Getenv(helperScriptEnv)
	if path == "" {
		t.Skip("only runs as a child of parityCommand")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	// process.exit ends the run with an *ExitError, and the child exits with its code as
	// node does.
	code := 0
	if _, err := inst.Runtime.Run(string(source), path); err != nil {
		var exit *runtime.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.Code
	}
	os.Exit(code)
}

func parityCommand(t *testing.T, script string, env ...string) *exec.Cmd {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", script))
	if err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if node := os.Getenv(parityNodeEnv); node != "" {
		cmd = exec.Command(node, path)
	} else {
		cmd = exec.Command(os.Args[0], "-test.run=^TestHelperRunScript$")
		cmd.Env = append(cmd.Env, helperScriptEnv+"="+path)
	}
	cmd.Env = append(append(os.Environ(), cmd.Env...), env...)
	return cmd
}

// Each script runs unchanged in Node; its expected output is what Node printed.
func TestParity_MatchesNode(t *testing.T) {
	for _, name := range []string{"child_process_parity", "net_parity"} {
		t.Run(name, func(t *testing.T) {
			script, err := os.ReadFile(filepath.Join("testdata", name+".js"))
			if err != nil {
				t.Fatal(err)
			}
			expected, err := os.ReadFile(filepath.Join("testdata", name+".node.json"))
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			if len(want) == 0 {
				t.Fatal("no expected values")
			}
			wantJSON(t, runJS(t, &runtime.Config{Timeout: 60 * time.Second}, string(script)), want)
		})
	}
}

func TestChildProcessInherit(t *testing.T) {
	cmd := parityCommand(t, "child_process_inherit.js")
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader("from-stdin\n")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	const wantOut = "from-stdin\nto-out\nby-number\nby-stream\nspawn-sync-out\n"
	const wantErr = "to-err\nswapped\nexec-sync-err\nexec-file-sync-err\nspawn-sync-err\n"
	if stdout.String() != wantOut {
		t.Errorf("stdout = %q, want %q", stdout.String(), wantOut)
	}
	if stderr.String() != wantErr {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantErr)
	}
}

// The expected bytes are what a peer received from Node.
func TestNetWriteBeforeExit(t *testing.T) {
	tests := []struct {
		mode string
		want string
		code int
	}{
		{mode: "beforeConnect", want: ""},
		{mode: "oneWrite", want: "one"},
		{mode: "severalWrites", want: "a\x00\xffc"},
		{mode: "endWithData", want: "ended"},
		{mode: "afterEarlierWrite", want: "firstsecond"},
		{mode: "largeWrite", want: strings.Repeat("x", 64*1024)},
		{mode: "corked", want: ""},
		{mode: "exitCode", want: "coded", code: 3},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			received := make(chan string, 1)
			go func() {
				var got bytes.Buffer
				defer func() { received <- got.String() }()
				conn, err := ln.Accept()
				if err != nil {
					return // closed: the process exited before it connected
				}
				defer conn.Close()
				conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				io.Copy(&got, conn)
			}()

			cmd := parityCommand(t, "net_exit_writer.js",
				"PARITY_PORT="+strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), "PARITY_MODE="+tt.mode)
			output, err := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tt.code {
				t.Fatalf("exit code %d, want %d: %v\n%s", code, tt.code, err, output)
			}
			ln.Close()
			if got := <-received; got != tt.want {
				if len(got) > 100 {
					t.Fatalf("received %d bytes, want %d", len(got), len(tt.want))
				}
				t.Fatalf("received %q, want %q", got, tt.want)
			}
		})
	}
}

// A peer that never reads must not keep the process from exiting.
func TestNetWriteBeforeExit_StalledPeer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := ln.Accept(); err == nil {
			accepted <- conn
		}
	}()
	defer func() {
		select {
		case conn := <-accepted:
			conn.Close()
		default:
		}
	}()

	cmd := parityCommand(t, "net_exit_writer.js",
		"PARITY_PORT="+strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), "PARITY_MODE=stalledPeer")
	done := make(chan error, 1)
	go func() {
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%v\n%s", err, output)
		}
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("process did not exit")
	}
}

// The embedder's rules still decide what runs now that every call goes through Spawn.
func TestChildProcess_SpawnerRules(t *testing.T) {
	const script = `
const cp = require('child_process');
const thrown = (fn) => { try { fn(); return 'no throw'; } catch (e) { return [e.code, e.syscall, e.errno]; } };
const failed = (child) => new Promise((resolve) => {
  let error = null;
  child.on('error', (e) => { error = [e.code, e.message]; });
  child.on('close', (code) => resolve([error, code]));
});
const exec = (run) => new Promise((resolve) => run((err, stdout) => resolve([err && err.code, stdout])));
(async () => {
  const out = {};
  out.spawnAllowed = await failed(cp.spawn('/bin/echo', ['ok']));
  out.spawnDenied = await failed(cp.spawn('/bin/ls'));
  // The allowed name comes first, but the shell would run the rest of the line too.
  out.shellDenied = await failed(cp.spawn('/bin/echo', ['ok;', 'id'], { shell: true }));
  out.execDenied = await exec((cb) => cp.exec('/bin/echo ok', cb));
  out.execFileAllowed = await exec((cb) => cp.execFile('/bin/echo', ['ok'], cb));
  out.spawnSyncDenied = cp.spawnSync('/bin/ls').error.code;
  out.execSyncDenied = thrown(() => cp.execSync('/bin/echo ok', { stdio: 'pipe' }));
  done(out);
})();`
	result := runJS(t, &runtime.Config{ProcessSpawner: runtime.NewAllowListProcessSpawner("/bin/echo")}, script)
	wantJSON(t, result, map[string]any{
		"spawnAllowed":    []any{nil, float64(0)},
		"spawnDenied":     []any{[]any{"EACCES", "spawn /bin/ls EACCES"}, float64(-13)},
		"shellDenied":     []any{[]any{"EACCES", "spawn /bin/sh EACCES"}, float64(-13)},
		"execDenied":      []any{"EACCES", ""},
		"execFileAllowed": []any{nil, "ok\n"},
		"spawnSyncDenied": "EACCES",
		"execSyncDenied":  []any{"EACCES", "spawnSync /bin/sh", float64(-13)},
	})

	// As in Node, a failure other than the few it treats as run-time errors is thrown.
	result = runJS(t, &runtime.Config{ProcessSpawner: runtime.NewNoOpProcessSpawner()}, `
const cp = require('child_process');
let caught = null;
try { cp.spawn('/bin/echo'); } catch (e) { caught = [e.code, e.syscall, e.errno, e.message]; }
done({ caught, sync: cp.spawnSync('/bin/echo').error.code });`)
	wantJSON(t, result, map[string]any{
		"caught": []any{"EPERM", "spawn", float64(-1), "spawn EPERM"},
		"sync":   "EPERM",
	})
}

// A file only JavaScript's filesystem knows about has no descriptor to give a child, and a
// number fs.openSync did not hand out does not reach a descriptor of the host process.
func TestChildProcess_StdioDescriptorMustBeOpen(t *testing.T) {
	result := runJS(t, &runtime.Config{Filesystem: runtime.NewMemoryFilesystem()}, `
const cp = require('child_process');
const fs = require('fs');
const code = (fd) => { try { cp.spawn('/bin/echo', [], { stdio: ['ignore', fd, 'ignore'] }); return 'no throw'; } catch (e) { return e.code; } };
const fd = fs.openSync('/file', 'w');
done({ memory: code(fd), unknown: code(977) });`)
	wantJSON(t, result, map[string]any{"memory": "EBADF", "unknown": "EBADF"})
}
