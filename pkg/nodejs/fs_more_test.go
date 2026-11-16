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

// cp refuses to copy a path onto itself on a filesystem without inode numbers too, rather
// than removing the file before copying it.
func TestFS_CpSamePathInMemory(t *testing.T) {
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewMemoryFilesystem()}, `
const fs = require('fs');
fs.writeFileSync('/f', 'keep');
let code = null;
try { fs.cpSync('/f', '/./f'); } catch (e) { code = e.code; }
let relative = null;
try { fs.cpSync('/f', 'f'); } catch (e) { relative = e.code; }
fs.cp('/f', '/f', (e) => done({ sync: code, relative, async: e && e.code, contents: fs.readFileSync('/f', 'utf8') }));
`)
	wantJSON(t, got, map[string]any{"sync": "ERR_FS_CP_EINVAL", "relative": "ERR_FS_CP_EINVAL", "async": "ERR_FS_CP_EINVAL", "contents": "keep"})
}

// cp refuses to copy a directory into itself through a link to it, as Node does, rather than
// copying it into itself again and again.
func TestFS_CpIntoSelfThroughLink(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const fs = require('fs');
const dir = %q;
const code = (fn) => { try { fn(); return 'ok'; } catch (e) { return e.code; } };
const sync = code(() => fs.cpSync(dir + '/src', dir + '/alias/out', { recursive: true }));
const deeper = code(() => fs.cpSync(dir + '/src', dir + '/alias/sub/out', { recursive: true }));
fs.cp(dir + '/src', dir + '/alias/out', { recursive: true }, (e) => done({
  sync, deeper, async: e && e.code, made: fs.existsSync(dir + '/src/out'),
}));
`, dir))
	wantJSON(t, got, map[string]any{"sync": "ERR_FS_CP_EINVAL", "deeper": "ERR_FS_CP_EINVAL", "async": "ERR_FS_CP_EINVAL", "made": false})
}

// A recursive watch enters each directory once, so links back into the tree don't make it
// walk every path through them.
func TestFS_RecursiveWatchSkipsLinkCycles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Symlink(".", filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	got := runJS(t, &runtime.Config{Timeout: 10 * time.Second}, fmt.Sprintf(`
const fs = require('fs');
const start = Date.now();
const w = fs.watch(%q, { recursive: true });
w.close();
done({ quick: Date.now() - start < 2000 });
`, dir))
	wantJSON(t, got, map[string]any{"quick": true})
}

// cp takes Buffer paths, and fs.watch reports the watched directory going away by its name,
// as Node does on Linux.
func TestFS_CpBufferPathsAndWatchedDirectoryRemoved(t *testing.T) {
	dir := t.TempDir()
	got := runJS(t, &runtime.Config{Timeout: 10 * time.Second}, fmt.Sprintf(`
const fs = require('fs');
const dir = %q;
fs.writeFileSync(dir + '/f', 'f');
fs.cpSync(Buffer.from(dir + '/f'), Buffer.from(dir + '/new/f'));
const copied = fs.readFileSync(dir + '/new/f', 'utf8');
fs.mkdirSync(dir + '/gone');
const events = [];
const w = fs.watch(dir + '/gone', (type, name) => events.push(type + ' ' + name));
setTimeout(() => fs.rmdirSync(dir + '/gone'), 150);
setTimeout(() => { w.close(); done({ copied, events }); }, 600);
`, dir))
	wantJSON(t, got, map[string]any{"copied": "f", "events": []any{"rename gone"}})
}

// A relative path given to watchFile or watch keeps naming the same file after
// process.chdir, as in Node.
func TestFS_WatchKeepsPathAfterChdir(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "f"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(start)
	got := runJS(t, &runtime.Config{Timeout: 10 * time.Second}, fmt.Sprintf(`
const fs = require('fs');
const dir = %q;
process.chdir(dir + '/one');
const polled = [];
fs.watchFile('f', { interval: 20 }, (curr) => polled.push(curr.size));
const watched = [];
const w = fs.watch('f', (type) => watched.push(type));
process.chdir(dir + '/two');
setTimeout(() => fs.writeFileSync(dir + '/two/f', 'changed in two'), 150);
setTimeout(() => fs.writeFileSync(dir + '/one/f', 'changed'), 400);
setTimeout(() => { fs.unwatchFile(dir + '/one/f'); w.close(); done({ polled, watched: watched.length > 0 }); }, 800);
`, dir))
	wantJSON(t, got, map[string]any{"polled": []any{7.0}, "watched": true})
}

// cp refuses to copy the root directory into a directory under it.
func TestFS_CpRootIntoItself(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	if err := memory.Mkdir("/", 0o755); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: memory}, `
const fs = require('fs');
fs.writeFileSync('/f', 'x');
let code = null;
try { fs.cpSync('/', '/out', { recursive: true }); } catch (e) { code = e.code; }
done({ code, made: fs.existsSync('/out') });
`)
	wantJSON(t, got, map[string]any{"code": "ERR_FS_CP_EINVAL", "made": false})
}

// { bigint: true } gives the exact nanosecond times, readv returns what a pipe holds, and
// cp sees a destination through a link under the root as inside the source.
func TestFS_ExactTimesPipesAndRootedCp(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1700000000, 123456789)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(file); !info.ModTime().Equal(when) {
		t.Skip("the filesystem doesn't keep nanoseconds")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Timeout: 10 * time.Second}, fmt.Sprintf(`
const fs = require('fs');
const s = fs.statSync(%q, { bigint: true });
const fd = fs.openSync(%q, 'r+');
fs.writeSync(fd, 'abcd');
const a = Buffer.alloc(4), b = Buffer.alloc(4);
const n = fs.readvSync(fd, [a, b]);
fs.closeSync(fd);
done({ mtimeNs: String(s.mtimeNs), n, a: a.toString() });
`, file, fifo))
	wantJSON(t, got, map[string]any{"mtimeNs": "1700000000123456789", "n": 4.0, "a": "abcd"})

	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(root, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	got = runJS(t, &runtime.Config{Filesystem: runtime.NewLocalFilesystem(root)}, `
const fs = require('fs');
let code = null;
try { fs.cpSync('src', 'alias/out', { recursive: true }); } catch (e) { code = e.code; }
done({ code, made: fs.existsSync('/src/out') });
`)
	wantJSON(t, got, map[string]any{"code": "ERR_FS_CP_EINVAL", "made": false})
}

// With a root, watchFile and unwatchFile find a watcher by the path from the root, whichever
// way it is spelled.
func TestFS_UnwatchFileWithRoot(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	got := runJS(t, &runtime.Config{Filesystem: memory, Timeout: 10 * time.Second}, `
const fs = require('fs');
fs.writeFileSync('/f', 'a');
let calls = 0;
fs.watchFile('f', { interval: 20 }, () => { calls++; });
setTimeout(() => {
  fs.unwatchFile('/f');
  fs.writeFileSync('/f', 'abcdef');
  setTimeout(() => done({ calls }), 150);
}, 60);
`)
	wantJSON(t, got, map[string]any{"calls": 0.0})
}

// BigInt milliseconds come from the exact nanoseconds, not a Number that rounds up.
func TestFS_BigIntMillisecondsFromNanoseconds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1794779860, 999999999)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(file); !info.ModTime().Equal(when) {
		t.Skip("the filesystem doesn't keep nanoseconds")
	}
	got := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const s = require('fs').statSync(%q, { bigint: true });
done({ ms: String(s.mtimeMs), ns: String(s.mtimeNs) });
`, file))
	wantJSON(t, got, map[string]any{"ms": "1794779860999", "ns": "1794779860999999999"})
}
