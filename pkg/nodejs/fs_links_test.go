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

// symlink, readlink, link, lstat, realpath and mkdtemp behave as in Node.
func TestFSLinks_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/fs_links.js", "testdata/fs_links.node.json"
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

// With a root, the link calls name paths from the root as every other call does, and a link
// to a place beside the root can be made, read and removed but not followed.
func TestFSLinks_WithRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "beside.txt"), []byte("beside"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewLocalFilesystem(root)}, `
const fs = require('fs');
const code = (fn) => { try { const v = fn(); return v === undefined ? 'ok' : v; } catch (e) { return e.code; } };
fs.mkdirSync('/d');
fs.writeFileSync('/d/f', 'inside');
done({
  absolute: code(() => { fs.symlinkSync('/d/f', '/abs'); return fs.readFileSync('/abs', 'utf8'); }),
  readlinkAbsolute: code(() => fs.readlinkSync('/abs')),
  realpath: code(() => fs.realpathSync('/abs')),
  realpathRoot: code(() => fs.realpathSync('/')),
  relative: code(() => { fs.symlinkSync('d/f', '/rel'); return fs.readFileSync('/rel', 'utf8'); }),
  beside: code(() => { fs.symlinkSync('../beside.txt', '/beside'); return fs.readFileSync('/beside', 'utf8'); }),
  besideLstat: code(() => fs.lstatSync('/beside').isSymbolicLink()),
  besideReadlink: code(() => fs.readlinkSync('/beside')),
  besideRealpath: code(() => fs.realpathSync('/beside')),
  besideWrite: code(() => fs.writeFileSync('/beside', 'changed')),
  besideHardLink: code(() => fs.linkSync('/beside', '/hard')),
  hardLinkMade: fs.existsSync('/hard'),
  hardLink: code(() => { fs.linkSync('/abs', '/hard2'); return fs.readFileSync('/hard2', 'utf8'); }),
  linkInBeside: code(() => fs.symlinkSync('/d/f', '/beside/x')),
  mkdtemp: code(() => /^\/tmp-[A-Za-z0-9]{6}$/.test(fs.mkdtempSync('/tmp-'))),
  mkdtempBeside: code(() => fs.mkdtempSync('/beside/x-')),
  unlinkBeside: code(() => fs.unlinkSync('/beside')),
});
`)
	wantJSON(t, got, map[string]any{
		"absolute": "inside", "readlinkAbsolute": "/d/f", "realpath": "/d/f", "realpathRoot": "/",
		"relative": "inside", "beside": "EACCES", "besideLstat": true, "besideReadlink": "../beside.txt",
		"besideRealpath": "EACCES", "besideWrite": "EACCES", "besideHardLink": "EACCES", "hardLinkMade": false,
		"hardLink": "inside", "linkInBeside": "EACCES", "mkdtemp": true, "mkdtempBeside": "EACCES",
		"unlinkBeside": "ok",
	})
	if data, err := os.ReadFile(filepath.Join(base, "beside.txt")); err != nil || string(data) != "beside" {
		t.Errorf("beside.txt = %q, %v; want it unchanged", data, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "abs")); err != nil || target != filepath.Join(root, "d", "f") {
		t.Errorf("abs links to %q, %v; want the file under the root", target, err)
	}
}
