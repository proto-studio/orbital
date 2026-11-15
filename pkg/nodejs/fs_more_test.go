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

// The fs calls that change a file's size, mode, owner and times, check access, describe the
// filesystem, read and write vectors, open directories, copy trees, make Blobs and watch
// files behave as in Node, with the same errors and the same Stats, Dirent and Dir classes.
func TestFSMore_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_more.js", "testdata/fs_more.node.json"
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
