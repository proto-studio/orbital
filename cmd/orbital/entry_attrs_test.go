package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Attributes a resolve hook gives the entry point are checked like any
// import's: a JavaScript entry given type "json" doesn't run. Expected from
// Node 20.18.1.
func TestEntry_HookAttributesAreChecked(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s.endsWith('entry.mjs')) { const r = await n(s, c); return { ...r, importAttributes: { type: 'json' } }; }
  return n(s, c);
}`,
		"reg.mjs":   `import { register } from 'node:module'; register('./hooks.mjs', import.meta.url);`,
		"entry.mjs": `console.log('entry ran');`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runOrbital(t, "", "--import", filepath.Join(dir, "reg.mjs"), filepath.Join(dir, "entry.mjs"))
	if code != 1 || strings.Contains(out, "entry ran") || !strings.Contains(out, "ERR_IMPORT_ASSERTION_TYPE_FAILED") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// An entry point given a type by its resolve hooks is keyed by that type, so
// importing itself gets the same module rather than running again. Expected
// from Node 20.18.1.
func TestEntry_HookTypedEntryIsOneModule(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s.endsWith('entry.mjs')) { const r = await n(s, c); return { ...r, importAttributes: { type: 'a' } }; }
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('entry.mjs')) { const r = await n(u, { ...c, importAttributes: {} }); return { ...r, shortCircuit: true }; }
  return n(u, c);
}`,
		"reg.mjs": `import { register } from 'node:module'; register('./hooks.mjs', import.meta.url);`,
		"entry.mjs": `globalThis.__runs = (globalThis.__runs || 0) + 1;
export const obj = {};
import('./entry.mjs').then((self) => console.log(JSON.stringify([globalThis.__runs, self.obj === obj])));`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runOrbital(t, "", "--import", filepath.Join(dir, "reg.mjs"), filepath.Join(dir, "entry.mjs"))
	if code != 0 || out != "[1,true]\n" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}
