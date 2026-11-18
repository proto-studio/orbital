package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A zero Config, and DefaultConfig, give scripts no filesystem, network or processes. Each
// refusal is the one Node reports when the OS refuses that operation.
func TestClosedDefaultsMatchNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/closed_defaults.js", "testdata/closed_defaults.node.json"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	if captureFromNode(t) {
		if goruntime.GOOS != "darwin" {
			t.Fatal("the expected values are captured under sandbox-exec, on macOS")
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		profile := `(version 1)(allow default)(deny network*)` +
			`(deny process-exec (literal "/bin/sh") (literal "/usr/bin/id"))`
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("sandbox-exec", "-p", profile, "node", scriptPath, real).Output()
		if chmodErr := os.Chmod(dir, 0o755); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		var results map[string]any
		if err := json.Unmarshal(output, &results); err != nil {
			t.Fatal(err)
		}
		writeExpected(t, expectedPath, results)
		return
	}

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("globalThis.CLOSED_DIR = %q;\n%s", dir, script)
	want := readExpected(t, expectedPath)
	for name, cfg := range map[string]*runtime.Config{
		"zero":    {},
		"default": runtime.DefaultConfig(),
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Timeout = 30 * time.Second
			inst, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Runtime.Dispose()
			wantJSON(t, runIn(t, inst, source), want)
		})
	}
	if data, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(data) != "a" {
		t.Errorf("a.txt = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Errorf("b.txt was written: %v", err)
	}
}

// Nor do they show the host's environment or details, or let process.kill reach a process.
func TestClosedDefaultsHideTheHost(t *testing.T) {
	t.Setenv("ORBITAL_CLOSED_DEFAULTS_SECRET", "leaked")
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]*runtime.Config{
		"zero":    {},
		"default": runtime.DefaultConfig(),
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Timeout = 30 * time.Second
			inst, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Runtime.Dispose()
			got := runIn(t, inst, `
let kill = null;
try { process.kill(process.pid, 0); } catch (e) { kill = { code: e.code, syscall: e.syscall, message: e.message }; }
done({ env: Object.keys(process.env), hostname: require('os').hostname(), kill });
`)
			if env, _ := got["env"].([]any); len(env) != 0 {
				t.Errorf("process.env has %v", env)
			}
			if got["hostname"] == host {
				t.Errorf("os.hostname() = the host's name, %q", host)
			}
			wantJSON(t, map[string]any{"kill": got["kill"]}, map[string]any{
				"kill": map[string]any{"code": "EPERM", "syscall": "kill", "message": "kill EPERM"},
			})
		})
	}
}

// With a filesystem, process.chdir still reaches the OS, and throws what it refuses as
// Node does (captured from Node v20.18.1).
func TestChdirWithFilesystemThrowsAsNode(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{}, `
let err = null;
try { process.chdir('no-such-dir'); } catch (e) {
  err = { code: e.code, errno: e.errno, syscall: e.syscall, path: e.path, dest: e.dest, message: e.message };
}
done({ err, unchanged: process.cwd() === `+fmt.Sprintf("%q", cwd)+` });
`)
	wantJSON(t, got, map[string]any{
		"err": map[string]any{
			"code": "ENOENT", "errno": float64(-2), "syscall": "chdir", "path": cwd, "dest": "no-such-dir",
			"message": "ENOENT: no such file or directory, chdir '" + cwd + "' -> 'no-such-dir'",
		},
		"unchanged": true,
	})
}
