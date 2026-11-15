package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// path.posix and path.win32 work on every platform, as in Node.
func TestPath_PosixAndWin32MatchNode(t *testing.T) {
	const scriptPath = "testdata/path_win32.js"
	const expectedPath = "testdata/path_win32.node.json"

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
