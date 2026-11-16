package nodejs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// process emits 'beforeExit' when the event loop runs out of work, runs the loop again if a
// listener scheduled more, and doesn't emit it on process.exit(). Each script in
// testdata/before_exit logs with note() under Orbital and console.log under Node;
// before_exit.node.json holds Node v20.18.1's lines, regenerated with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestProcess_BeforeExitMatchesNode
func TestProcess_BeforeExitMatchesNode(t *testing.T) {
	const expectedPath = "testdata/before_exit.node.json"
	scripts, err := filepath.Glob("testdata/before_exit/*.js")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts: %v", err)
	}

	if captureFromNode(t) {
		results := map[string]any{}
		for _, script := range scripts {
			// The scripts exit non-zero on purpose, so only the output matters.
			output, _ := exec.Command("node", script).Output()
			results[filepath.Base(script)] = strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
		}
		writeExpected(t, expectedPath, results)
		return
	}

	want := readExpected(t, expectedPath)
	for _, script := range scripts {
		name := filepath.Base(script)
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(script)
			if err != nil {
				t.Fatal(err)
			}
			notes, _ := runUncaught(t, string(source))
			got := make([]any, len(notes))
			for i, n := range notes {
				got[i] = n
			}
			if !reflect.DeepEqual(got, want[name]) {
				t.Errorf("got %q, want %q", got, want[name])
			}
		})
	}
}

// A preloaded ES module is not the program: its loop running out of work emits no
// 'beforeExit', which comes once, after the entry.
func TestProcess_BeforeExitNotForPreloads(t *testing.T) {
	dir := t.TempDir()
	preload := filepath.Join(dir, "preload.mjs")
	entry := filepath.Join(dir, "entry.mjs")
	if err := os.WriteFile(preload, []byte("process.on('beforeExit', () => note('beforeExit'));\nnote('preload');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("note('entry');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.RunScript(`globalThis.__notes = []; globalThis.note = (...a) => __notes.push(a.join(' '));`, "prelude.js"); err != nil {
		t.Fatal(err)
	}
	if err := inst.ESM.Preload(preload); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.ESM.RunModuleFile(entry); err != nil {
		t.Fatal(err)
	}
	val, err := inst.Runtime.RunScript(`__notes.join(',')`, "notes.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := val.String(), "preload,entry,beforeExit"; got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}
}
