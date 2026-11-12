package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// fs calls that fail, or that pass a flag or mode, behave as they do in Node: the same
// errors with the same codes, and the same files left on disk.
func TestFSErrors_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_errors.js", "testdata/fs_errors.node.json"
	dir := t.TempDir()

	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath, dir).Output()
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
	cfg := &runtime.Config{Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", dir, script))
	wantJSON(t, got, readExpected(t, expectedPath))
}
