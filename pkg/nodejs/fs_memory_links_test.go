package nodejs

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// memoryDir is where the scripts below work in the in-memory filesystem.
const memoryDir = "/work"

// runOnMemory runs script with FS_DIR set to memoryDir in mem.
func runOnMemory(t *testing.T, mem *runtime.MemoryFilesystem, script []byte) map[string]any {
	t.Helper()
	cfg := &runtime.Config{Filesystem: mem, Timeout: 60 * time.Second}
	return runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", memoryDir, script))
}

// The in-memory filesystem follows, makes and removes symbolic and hard links as Node does on
// a real disk, with the same errors, in every form of each call. The local filesystem runs the
// same script, so both are checked against Node.
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
	t.Run("memory", func(t *testing.T) {
		mem := runtime.NewMemoryFilesystem()
		if err := mem.Mkdir(memoryDir, 0o755); err != nil {
			t.Fatal(err)
		}
		wantJSON(t, runOnMemory(t, mem, script), want)
	})
	t.Run("local", func(t *testing.T) {
		cfg := &runtime.Config{Timeout: 60 * time.Second}
		got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", t.TempDir(), script))
		// The local filesystem's recursive mkdir is os.MkdirAll, which reports a link that
		// leads nowhere as EEXIST where Node gives ENOENT.
		local := maps.Clone(want)
		for _, key := range []string{"dangling_mkdir_recursive", "dangling_mkdir_recursive_child"} {
			delete(local, key)
			delete(got, key)
		}
		wantJSON(t, got, local)
	})
}

// The scripts written for links on the local filesystem give the same results on the
// in-memory one.
func TestFSMemoryLinks_LocalScripts(t *testing.T) {
	t.Run("fs_links", func(t *testing.T) {
		script, err := os.ReadFile("testdata/fs_links.js")
		if err != nil {
			t.Fatal(err)
		}
		mem := runtime.NewMemoryFilesystem()
		if err := mem.Mkdir(memoryDir, 0o755); err != nil {
			t.Fatal(err)
		}
		want := readExpected(t, "testdata/fs_links.node.json")
		// The in-memory filesystem makes a directory without its parent, so mkdtemp there
		// succeeds; that is not about links.
		got := runOnMemory(t, mem, script)
		delete(want, "mkdtemp_missing")
		delete(got, "mkdtemp_missing")
		wantJSON(t, got, want)
	})
	t.Run("fs_dotdot", func(t *testing.T) {
		script, err := os.ReadFile("testdata/fs_dotdot.js")
		if err != nil {
			t.Fatal(err)
		}
		// The layout TestFSDotDotAfterLink_MatchesNode makes on disk.
		mem := runtime.NewMemoryFilesystem()
		if err := mem.MkdirAll(memoryDir+"/b/sub", 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string]string{"f": "top", "b/f": "inner"} {
			if err := mem.WriteFile(memoryDir+"/"+name, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := mem.Symlink("b/sub", memoryDir+"/alias"); err != nil {
			t.Fatal(err)
		}
		want := readExpected(t, "testdata/fs_dotdot.node.json")
		// That case changes the process's working directory, which the in-memory
		// filesystem doesn't use.
		got := runOnMemory(t, mem, script)
		delete(want, "relative")
		delete(got, "relative")
		wantJSON(t, got, want)
	})
}

// Where Linux and macOS differ, the in-memory filesystem does what the disk under it does.
func TestFSMemoryLinks_PlatformCasesMatchDisk(t *testing.T) {
	script, err := os.ReadFile("testdata/fs_memory_links_platform.js")
	if err != nil {
		t.Fatal(err)
	}
	mem := runtime.NewMemoryFilesystem()
	if err := mem.Mkdir(memoryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := runOnMemory(t, mem, script)
	disk := runJS(t, &runtime.Config{Timeout: 60 * time.Second}, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", t.TempDir(), script))
	wantJSON(t, got, disk)
}

// realpath of a long path that ends in a link cycle reports the cycle quickly: the error is
// built from a few calls, not one for each name in the path.
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
