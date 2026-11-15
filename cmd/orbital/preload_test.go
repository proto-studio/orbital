package main

import (
	"os"
	"path/filepath"
	"testing"
)

// When an --import can't be resolved and an 'uncaughtException' listener handles the
// error, Node runs the 'exit' listeners and ends without running the entry point.
func TestImportPreload_HandledFailureStopsStartup(t *testing.T) {
	dir := t.TempDir()
	pre := filepath.Join(dir, "pre.cjs")
	entry := filepath.Join(dir, "entry.mjs")
	if err := os.WriteFile(pre, []byte(`process.on('uncaughtException', (e) => console.log('caught', e.code)); process.on('exit', (c) => console.log('exit', c));`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(`console.log('entry ran');`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runOrbital(t, "", "-r", pre, "--import", filepath.Join(dir, "nope.mjs"), entry)
	if code != 0 || out != "caught ERR_MODULE_NOT_FOUND\nexit 0\n" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}
