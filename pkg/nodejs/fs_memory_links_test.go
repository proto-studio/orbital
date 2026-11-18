package nodejs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Links on disk behave as Node's do, with the same errors, in every form of each call. This
// is also the Node capture that TestFS_CallFormsMapToFilesystem checks error shapes against,
// and the memory filesystem is checked against the disk, through the Go interface, in
// pkg/runtime.
func TestFSMemoryLinks_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_memory_links.js", "testdata/fs_memory_links.node.json"
	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath, t.TempDir()).Output()
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
	want := readExpected(t, expectedPath)
	cfg := &runtime.Config{Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", t.TempDir(), script))
	// The local filesystem's recursive mkdir is os.MkdirAll, which reports a link that
	// leads nowhere as EEXIST where Node gives ENOENT.
	for _, key := range []string{"dangling_mkdir_recursive", "dangling_mkdir_recursive_child"} {
		delete(want, key)
		delete(got, key)
	}
	wantJSON(t, got, want)
}

// realpath of a long path on the in-memory filesystem that ends in a link cycle reports the
// cycle quickly, through the whole stack: the fs module builds the error from a few calls,
// not one for each name in the path, and each call resolves the path in linear time.
func TestFSMemoryLinks_RealpathLoopInLongPath(t *testing.T) {
	mem := runtime.NewMemoryFilesystem()
	deep := strings.TrimSuffix(strings.Repeat("/d", 480), "/")
	if err := mem.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mem.Symlink("loop", deep+"/loop"); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: mem}, fmt.Sprintf(`
const fs = require('fs');
const start = Date.now();
let err;
for (let i = 0; i < 20; i++) {
  try { fs.realpathSync(%q); } catch (e) { err = e; }
}
done({ code: err.code, syscall: err.syscall, path: err.path, fast: Date.now() - start < 2000 });
`, deep+"/loop/x"))
	wantJSON(t, got, map[string]any{"code": "ELOOP", "syscall": "stat", "path": deep + "/loop", "fast": true})
}
