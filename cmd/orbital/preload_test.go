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

// The same goes for an --import that throws or rejects while it runs.
func TestImportPreload_HandledEvaluationFailureStopsStartup(t *testing.T) {
	dir := t.TempDir()
	pre := filepath.Join(dir, "pre.cjs")
	entry := filepath.Join(dir, "entry.mjs")
	files := map[string]string{
		pre:                           `process.on('uncaughtException', (e) => console.log('caught', e.message)); process.on('exit', (c) => console.log('exit', c));`,
		entry:                         `console.log('entry ran');`,
		filepath.Join(dir, "bad.mjs"): `throw new Error('boom');`,
		filepath.Join(dir, "rej.mjs"): `await Promise.reject(new Error('rej'));`,
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"bad", "rej"} {
		out, code := runOrbital(t, "", "-r", pre, "--import", filepath.Join(dir, m+".mjs"), entry)
		want := "caught boom\nexit 0\n"
		if m == "rej" {
			want = "caught rej\nexit 0\n"
		}
		if code != 0 || out != want {
			t.Fatalf("%s: exit %d, output %q", m, code, out)
		}
	}
}

// A missing entry module is an uncaught exception too, which a preload's listener can
// handle, as in Node.
func TestEntry_MissingModuleIsUncaught(t *testing.T) {
	dir := t.TempDir()
	pre := filepath.Join(dir, "pre.cjs")
	if err := os.WriteFile(pre, []byte(`process.on('uncaughtException', (e) => console.log('caught', e.code)); process.on('exit', (c) => console.log('exit', c));`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runOrbital(t, "", "-r", pre, filepath.Join(dir, "nope.mjs"))
	if code != 0 || out != "caught ERR_MODULE_NOT_FOUND\nexit 0\n" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}
