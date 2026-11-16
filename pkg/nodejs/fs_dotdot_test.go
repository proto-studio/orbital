package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Without a root, a ".." after a symbolic link applies to where the link leads, as the OS and
// Node resolve it, not to the link's own name.
func TestFSDotDotAfterLink_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_dotdot.js", "testdata/fs_dotdot.node.json"
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"f": "top", "b/f": "inner"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("b/sub", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}

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
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(start)
	cfg := &runtime.Config{Timeout: 20 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", dir, script))
	wantJSON(t, got, readExpected(t, expectedPath))
}
