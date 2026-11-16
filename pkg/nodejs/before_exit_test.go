package nodejs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
