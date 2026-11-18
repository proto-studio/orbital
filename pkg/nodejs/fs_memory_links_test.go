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
		wantJSON(t, runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", t.TempDir(), script)), want)
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
