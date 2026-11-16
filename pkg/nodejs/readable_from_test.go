package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Readable.from pushes a sync iterable synchronously and handles values, errors and
// destruction as Node does.
func TestReadableFrom_MatchesNode(t *testing.T) {
	const scriptPath = "testdata/readable_from.js"
	const expectedPath = "testdata/readable_from.node.json"

	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath).Output()
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
	wantJSON(t, runJS(t, &runtime.Config{}, string(script)), readExpected(t, expectedPath))
}
