package nodejs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
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

// fullFS is a MemoryFilesystem whose files are full: every write fails with ENOSPC.
type fullFS struct{ *runtime.MemoryFilesystem }

type fullFile struct{ runtime.File }

func (fullFile) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func (f fullFS) OpenFile(path string, flag int, perm fs.FileMode) (runtime.File, error) {
	file, err := f.MemoryFilesystem.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	return fullFile{file}, nil
}

// A write that fails after the open reports the write, with no path, as Node does for
// writeFileSync('/dev/full', 'x') on Linux. Appending to a directory fails at open and
// leaves the directory alone.
func TestFSWriteFailures(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	if err := memory.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: fullFS{memory}}, `
const fs = require('fs');
const describe = (e) => e && { code: e.code, syscall: e.syscall, path: e.path === undefined ? null : e.path, message: e.message };
const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
fs.writeFile('/full', 'x', (err) => done({
  sync: attempt(() => fs.writeFileSync('/full', 'x')),
  async: describe(err),
  dir: attempt(() => fs.appendFileSync('/dir', 'x')),
  stillDir: fs.statSync('/dir').isDirectory(),
}));`)
	write := map[string]any{"code": "ENOSPC", "syscall": "write", "path": nil, "message": "ENOSPC: no space left on device, write"}
	wantJSON(t, got, map[string]any{
		"sync":     write,
		"async":    write,
		"dir":      map[string]any{"code": "EISDIR", "syscall": "open", "path": "/dir", "message": "EISDIR: illegal operation on a directory, open '/dir'"},
		"stillDir": true,
	})
}

// Reading a directory on the in-memory filesystem fails with EISDIR at read, as on disk, and
// a missing file with ENOENT at open.
func TestFS_MemoryFilesystemReadErrors(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	if err := memory.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: memory}, `
const fs = require('fs');
const describe = (e) => e && [e.code, e.syscall, e.message];
const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
fs.readFile('/dir', (err) => require('fs/promises').readFile('/dir').catch((perr) => done({
  sync: attempt(() => fs.readFileSync('/dir')),
  callback: describe(err),
  promise: perr.code,
  missing: attempt(() => fs.readFileSync('/nothing')),
})));`)
	wantJSON(t, got, map[string]any{
		"sync":     []any{"EISDIR", "read", "EISDIR: illegal operation on a directory, read"},
		"callback": []any{"EISDIR", "read", "EISDIR: illegal operation on a directory, read"},
		"promise":  "EISDIR",
		"missing":  []any{"ENOENT", "open", "ENOENT: no such file or directory, open '/nothing'"},
	})
}
