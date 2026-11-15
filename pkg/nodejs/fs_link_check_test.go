package nodejs

import (
	"os"
	"path/filepath"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A path the filesystem refuses after resolving its links fails in fs with EACCES, as the
// refusal's message says, and a link itself can still be removed.
func TestFS_LinkCheckReportsEACCES(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	other := filepath.Join(base, "other")
	for _, d := range []string{root, other} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other", filepath.Join(root, "far")); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{Filesystem: runtime.NewLocalFilesystem(root)}, `
const fs = require('fs');
const attempt = (fn) => { try { fn(); return 'ok'; } catch (e) { return [e.code, e.errno < 0, e.syscall].join(' '); } };
const promised = (p) => p.then(() => 'ok', (e) => e.code);
(async () => {
  done({
    readFileSync: attempt(() => fs.readFileSync('/far/f.txt')),
    writeFileSync: attempt(() => fs.writeFileSync('/far/new.txt', 'x')),
    readdirSync: attempt(() => fs.readdirSync('/far')),
    mkdirSync: attempt(() => fs.mkdirSync('/far/sub')),
    openSync: attempt(() => fs.openSync('/far/f.txt', 'r')),
    readFile: await promised(fs.promises.readFile('/far/f.txt')),
    rmSync: attempt(() => fs.rmSync('/far')),
    linkGone: fs.existsSync('/far'),
  });
})();
`)
	wantJSON(t, got, map[string]any{
		"readFileSync":  "EACCES true open",
		"writeFileSync": "EACCES true open",
		"readdirSync":   "EACCES true scandir",
		"mkdirSync":     "EACCES true mkdir",
		"openSync":      "EACCES true open",
		"readFile":      "EACCES",
		"rmSync":        "ok",
		"linkGone":      false,
	})
	if _, err := os.Stat(filepath.Join(other, "f.txt")); err != nil {
		t.Errorf("other/f.txt: %v; want it kept", err)
	}
}
