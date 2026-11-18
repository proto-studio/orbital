package nodejs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	// A symlink loop, for ELOOP. Orbital has no fs.symlinkSync to make it from the script.
	if err := os.Symlink(filepath.Join(dir, "loop_b"), filepath.Join(dir, "loop_a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "loop_a"), filepath.Join(dir, "loop_b")); err != nil {
		t.Fatal(err)
	}
	// Links for rm to remove: some pointing at nothing, some at a directory.
	if err := os.MkdirAll(filepath.Join(dir, "rm_target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rm_target", "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		if err := os.Symlink(filepath.Join(dir, "rm_nowhere"), filepath.Join(dir, fmt.Sprintf("rm_dangling_%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2; i++ {
		if err := os.Symlink(filepath.Join(dir, "rm_target"), filepath.Join(dir, fmt.Sprintf("rm_dirlink_%d", i))); err != nil {
			t.Fatal(err)
		}
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
	cfg := &runtime.Config{Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", dir, script))
	wantJSON(t, got, readExpected(t, expectedPath))
}

// fs.createWriteStream and the callback forms behave as in Node, and leave the same files.
func TestFSAPIs_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_apis.js", "testdata/fs_apis.node.json"
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
		filesOnDisk(t, dir, results)
		writeExpected(t, expectedPath, results)
		return
	}

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &runtime.Config{Timeout: 60 * time.Second}
	got := runJS(t, cfg, fmt.Sprintf("globalThis.FS_DIR = %q;\n%s", dir, script))
	filesOnDisk(t, dir, got)
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

// fs.constants holds this platform's open flags, as Node's does: O_APPEND and O_CREAT, for
// one, differ between Linux and macOS.
func TestFS_OpenFlagConstants(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const { constants } = require('fs');
done(Object.fromEntries(['O_RDONLY', 'O_WRONLY', 'O_RDWR', 'O_CREAT', 'O_EXCL', 'O_TRUNC', 'O_APPEND', 'O_SYNC']
  .map((name) => [name, constants[name]])));`)
	wantJSON(t, got, map[string]any{
		"O_RDONLY": float64(syscall.O_RDONLY),
		"O_WRONLY": float64(syscall.O_WRONLY),
		"O_RDWR":   float64(syscall.O_RDWR),
		"O_CREAT":  float64(syscall.O_CREAT),
		"O_EXCL":   float64(syscall.O_EXCL),
		"O_TRUNC":  float64(syscall.O_TRUNC),
		"O_APPEND": float64(syscall.O_APPEND),
		"O_SYNC":   float64(syscall.O_SYNC),
	})
}

// With the closed filesystem, scripts passed as source still run, and every file a script
// reads, writes or loads is refused, as Node refuses a directory it may not search: fs calls
// fail with EACCES, and require, import and a worker find no module.
func TestFS_ClosedFilesystem(t *testing.T) {
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewNoOpFilesystem()}, `
const fs = require('fs');
const path = require('path');
const { Worker } = require('worker_threads');
const describe = (e) => {
  let message = e.message;
  if (e.code.endsWith('MODULE_NOT_FOUND')) message = message.split('\n')[0].split(' imported from ')[0];
  return [e.code, e.syscall, message];
};
const attempt = (fn) => { try { fn(); return null; } catch (e) { return describe(e); } };
const results = {
  joined: path.join('a', 'b'),
  read: attempt(() => fs.readFileSync('/closed/x.js')),
  write: attempt(() => fs.writeFileSync('/closed/new.txt', 'x')),
  require: attempt(() => require('/closed/x.js')),
};
import('/closed/x.mjs').then(() => null, describe).then((imported) => {
  results.import = imported;
  const w = new Worker('/closed/x.js');
  w.on('error', (e) => { results.worker = describe(e); });
  w.on('exit', () => done(results));
});
`)
	// As Node v20.18.1 gives them for files in a directory with mode 000.
	wantJSON(t, got, map[string]any{
		"joined":  "a/b",
		"read":    []any{"EACCES", "open", "EACCES: permission denied, open '/closed/x.js'"},
		"write":   []any{"EACCES", "open", "EACCES: permission denied, open '/closed/new.txt'"},
		"require": []any{"MODULE_NOT_FOUND", nil, "Cannot find module '/closed/x.js'"},
		"import":  []any{"ERR_MODULE_NOT_FOUND", nil, "Cannot find module '/closed/x.mjs'"},
		"worker":  []any{"MODULE_NOT_FOUND", nil, "Cannot find module '/closed/x.js'"},
	})
}
