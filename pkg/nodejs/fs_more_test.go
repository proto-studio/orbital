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
fs.cp('/f', '/f', (e) => done({ sync: code, async: e && e.code, contents: fs.readFileSync('/f', 'utf8') }));
`)
	wantJSON(t, got, map[string]any{"sync": "ERR_FS_CP_EINVAL", "async": "ERR_FS_CP_EINVAL", "contents": "keep"})
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
