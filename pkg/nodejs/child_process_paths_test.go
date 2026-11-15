package nodejs

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// lookPath returns the resolved path of a program on the host's PATH.
func lookPath(t *testing.T, name string) string {
	t.Helper()
	found, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found: %v", name, err)
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// A file the script writes is not an allowed program, whatever it is named.
func TestChildProcessAllowListUsesResolvedPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &runtime.Config{
		Filesystem:     runtime.NewLocalFilesystem(root),
		ProcessSpawner: runtime.NewAllowListProcessSpawner(lookPath(t, "echo")),
		Timeout:        20 * time.Second,
	}
	rootJSON, _ := json.Marshal(root)
	got := runJS(t, cfg, "const ROOT = "+string(rootJSON)+";"+`
const fs = require('fs');
const cp = require('child_process');
const attempt = (fn) => {
  try { return String(fn()).trim(); } catch (e) { const why = e.code || e.message; return /EACCES/.test(why) ? 'EACCES' : why; }
};
fs.writeFileSync('/echo', '#!/bin/sh\necho written\n', { mode: 0o755 });
done({
  allowed: attempt(() => cp.execFileSync('echo', ['ok'])),
  written: attempt(() => cp.execFileSync(`+"`${ROOT}/echo`"+`, ['ok'])),
});`)
	wantJSON(t, got, map[string]any{
		"allowed": "ok",
		"written": "EACCES",
	})
}
