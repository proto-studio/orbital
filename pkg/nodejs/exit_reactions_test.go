package nodejs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Nothing queued runs after process.exit(): not promise reactions, queueMicrotask callbacks
// or ticks, wherever process.exit() is called from. Each script in
// testdata/exit_reactions logs with note() under Orbital and console.log under Node;
// exit_reactions.node.json holds Node v20.18.1's lines, regenerated with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestProcess_ExitDropsQueuedReactions
func TestProcess_ExitDropsQueuedReactions(t *testing.T) {
	const expectedPath = "testdata/exit_reactions.node.json"
	scripts, err := filepath.Glob("testdata/exit_reactions/*.js")
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
