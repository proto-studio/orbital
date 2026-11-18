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

// With the filesystem that refuses everything, every fs call, and loading code from files,
// fails as it does in Node for a directory it may not access. Nothing on the host changes.
func TestFS_ClosedFilesystemMatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_closed.js", "testdata/fs_closed.node.json"
	dir := t.TempDir()
	files := map[string]string{
		"sub/x.js":  "module.exports = 'loaded';",
		"sub/x.mjs": "export default 'loaded';",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if captureFromNode(t) {
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
	cfg := &runtime.Config{Filesystem: runtime.NewNoOpFilesystem(), Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", dir, script))
	wantJSON(t, got, readExpected(t, expectedPath))

	// The host directory is as it was: the same files, unchanged.
	var found []string
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		found = append(found, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if want, ok := files[filepath.ToSlash(rel)]; !ok || string(data) != want {
			t.Errorf("%s changed: %q", rel, data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != len(files) {
		t.Errorf("host files = %v, want %d", found, len(files))
	}
}

// Scripts passed as source still run with the filesystem closed.
func TestFS_ClosedFilesystemRunsSource(t *testing.T) {
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewNoOpFilesystem()}, `
const path = require('path');
Promise.resolve().then(() => done({ joined: path.join('a', 'b') }));
`)
	wantJSON(t, got, map[string]any{"joined": "a/b"})
}
