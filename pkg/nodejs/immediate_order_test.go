package nodejs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// setImmediate callbacks run in the loop's check phase, after I/O and due timers, one batch
// per turn, as in Node. Each script in
// testdata/immediate_order logs with note() under Orbital and console.log under Node;
// immediate_order.node.json holds Node v20.18.1's lines, regenerated with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestTimers_ImmediateOrderMatchesNode
func TestTimers_ImmediateOrderMatchesNode(t *testing.T) {
	const expectedPath = "testdata/immediate_order.node.json"
	scripts, err := filepath.Glob("testdata/immediate_order/*.js")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no scripts: %v", err)
	}

	if captureFromNode(t) {
		results := map[string]any{}
		for _, script := range scripts {
			// The scripts exit non-zero on purpose, so only the output matters.
			output, _ := exec.Command("node", script).Output()
			lines := []string{}
			if text := strings.TrimSuffix(string(output), "\n"); text != "" {
				lines = strings.Split(text, "\n")
			}
			results[filepath.Base(script)] = lines
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
