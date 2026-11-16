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
