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

// The in-memory filesystem behaves like a real one for descriptors: each has its own offset,
// writes at a position change only those bytes, and open honors its flags.
func TestFS_MemoryFilesystemDescriptors(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	got := runJS(t, &runtime.Config{Filesystem: memory}, `
const fs = require('fs');
const code = (fn) => { try { fn(); return null; } catch (e) { return e.code; } };
fs.writeFileSync('/f', 'abcdef');
const fd = fs.openSync('/f', 'r+');
fs.writeSync(fd, 'XY', 2);
fs.writeSync(fd, 'Z');
const buf = Buffer.alloc(3);
fs.readSync(fd, buf, 0, 3, 3);
fs.closeSync(fd);
const grow = fs.openSync('/g', 'w');
fs.writeSync(grow, 'end', 4);
fs.closeSync(grow);
const a = fs.openSync('/f', 'a');
fs.writeSync(a, '!');
fs.closeSync(a);
done({
  file: fs.readFileSync('/f', 'utf8'),
  read: buf.toString(),
  grown: fs.readFileSync('/g').toString('hex'),
  wx: code(() => fs.openSync('/f', 'wx')),
  missing: code(() => fs.openSync('/nothing', 'r')),
  missingWrite: code(() => fs.writeFileSync('/nothing2', 'x', { flag: 'r+' })),
});`)
	wantJSON(t, got, map[string]any{
		"file":         "ZbXYef!",
		"read":         "Yef",
		"grown":        "00000000656e64",
		"wx":           "EEXIST",
		"missing":      "ENOENT",
		"missingWrite": "ENOENT",

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
