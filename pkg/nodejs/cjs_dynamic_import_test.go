package nodejs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// import() works from CommonJS code, resolving relative to the file that calls it: the
// entry script, a module it requires from another directory, and a package's CommonJS
// code loading an ES-module-only dependency. The expected log is what Node 20.18.1
// records for the same files.
func TestDynamicImport_FromCommonJS(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"m.mjs":                              `export default 'm';`,
		"sub/x.mjs":                          `export default 'sub';`,
		"sub/loader.js":                      `module.exports = () => import('./x.mjs');`,
		"sub/dep.cjs":                        `module.exports = 'cjs';`,
		"node_modules/esm-only/package.json": `{"name":"esm-only","type":"module","exports":"./index.js"}`,
		"node_modules/esm-only/index.js":     `export const v = 'pkg';`,
		"node_modules/uses-esm/package.json": `{"name":"uses-esm","main":"index.js"}`,
		"node_modules/uses-esm/index.js":     `module.exports = () => import('./lib.mjs');`,
		"node_modules/uses-esm/lib.mjs":      `export default 'pkg relative';`,
	}
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := fmt.Sprintf(`const dir = %q;
globalThis.log = [];
(async () => {
  log.push((await import('./m.mjs')).default);
  log.push((await require(dir + '/sub/loader.js')()).default);
  log.push((await import('./sub/dep.cjs')).default);
  log.push((await import('esm-only')).v);
  log.push((await require(dir + '/node_modules/uses-esm')()).default);
  log.push(typeof (await import('node:path')).join);
})().catch((e) => log.push('failed ' + e.message));
`, dir)

	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run(entry, filepath.Join(dir, "entry.js")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	logged, err := inst.Runtime.RunScript("JSON.stringify(globalThis.log)", "read.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `["m","sub","cjs","pkg","pkg relative","function"]`
	if logged.String() != want {
		t.Fatalf("logged %s, want %s", logged.String(), want)
	}
}
