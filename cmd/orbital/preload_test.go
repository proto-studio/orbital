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

// The same goes for an --import that throws or rejects while it runs, or doesn't
// parse.
func TestImportPreload_HandledEvaluationFailureStopsStartup(t *testing.T) {
	dir := t.TempDir()
	pre := filepath.Join(dir, "pre.cjs")
	entry := filepath.Join(dir, "entry.mjs")
	files := map[string]string{
		pre:                           `process.on('uncaughtException', (e) => console.log('caught', e.message)); process.on('exit', (c) => console.log('exit', c));`,
		entry:                         `console.log('entry ran');`,
		filepath.Join(dir, "bad.mjs"): `throw new Error('boom');`,
		filepath.Join(dir, "rej.mjs"): `await Promise.reject(new Error('rej'));`,
		filepath.Join(dir, "syn.mjs"): `export default (`,
	}
	for p, src := range files {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wants := map[string]string{"bad": "boom", "rej": "rej", "syn": "Unexpected end of input"}
	for m, msg := range wants {
		out, code := runOrbital(t, "", "-r", pre, "--import", filepath.Join(dir, m+".mjs"), entry)
		want := "caught " + msg + "\nexit 0\n"
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

// A link error in the entry's graph (a dependency's syntax, a missing export) is an
// uncaught exception a listener can handle, and a top-level await that never
// settles ends the program with exit code 13, after the 'exit' listeners, as in Node.
func TestEntry_LinkErrorsAndUnsettledAwait(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"pre.cjs":    `process.on('uncaughtException', (e) => console.log('caught', e.name, e.message)); process.on('exit', (c) => console.log('exit', c));`,
		"badsyn.mjs": `export default (`,
		"e1.mjs":     `import './badsyn.mjs';`,
		"dep.mjs":    `export const a = 1;`,
		"e2.mjs":     `import { b } from './dep.mjs';`,
		"tla.mjs":    `await new Promise(() => {});`,
		"zero.cjs":   `process.on('exit', () => { process.exitCode = 0; });`,
		"seven.cjs":  `process.on('exit', () => { process.exitCode = 7; });`,
		"entry.mjs":  `console.log('entry');`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := func(n string) string { return filepath.Join(dir, n) }
	cases := []struct {
		args []string
		out  string
		code int
	}{
		{[]string{"-r", p("pre.cjs"), p("e1.mjs")}, "caught SyntaxError Unexpected end of input\nexit 0\n", 0},
		{[]string{"-r", p("pre.cjs"), p("e2.mjs")}, "caught SyntaxError The requested module './dep.mjs' does not provide an export named 'b'\nexit 0\n", 0},
		{[]string{"-r", p("pre.cjs"), p("tla.mjs")}, "exit 0\n", 13},
		{[]string{"-r", p("pre.cjs"), "--import", p("tla.mjs"), p("entry.mjs")}, "exit 0\n", 13},
		// An 'exit' listener's process.exitCode replaces 13, even when it is 0.
		{[]string{"-r", p("zero.cjs"), p("tla.mjs")}, "", 0},
		{[]string{"-r", p("seven.cjs"), "--import", p("tla.mjs"), p("entry.mjs")}, "", 7},
	}
	for _, tc := range cases {
		out, code := runOrbital(t, "", tc.args...)
		if code != tc.code || out != tc.out {
			t.Errorf("%v: exit %d, output %q; want %d, %q", tc.args, code, out, tc.code, tc.out)
		}
	}
}
