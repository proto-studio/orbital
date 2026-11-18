package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// With every interface call refused, every fs call, and loading code from files, fails as it
// does in Node for a directory it may not access. The closed filesystem refuses everything
// with EACCES (pkg/runtime checks that it does), so this is what scripts see with it.
func TestFS_RefusedCallsMatchNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_closed.js", "testdata/fs_closed.node.json"
	if captureFromNode(t) {
		dir := t.TempDir()
		for name, content := range map[string]string{
			"sub/x.js":  "module.exports = 'loaded';",
			"sub/x.mjs": "export default 'loaded';",
		} {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command("node", scriptPath, dir).Output()
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
	cfg := &runtime.Config{Filesystem: newMockFSFailing(syscall.EACCES), Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", "/closed", script))
	wantJSON(t, got, readExpected(t, expectedPath))
}

// Scripts passed as source still run with the filesystem closed.
func TestFS_ClosedFilesystemRunsSource(t *testing.T) {
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewNoOpFilesystem()}, `
const path = require('path');
Promise.resolve().then(() => done({ joined: path.join('a', 'b') }));
`)
	wantJSON(t, got, map[string]any{"joined": "a/b"})
}
