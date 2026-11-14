package nodejs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runLogModule writes files to a temporary directory, runs entry.mjs from it as an ES
// module and returns what the files recorded in globalThis.log.
func runLogModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.RunScript("globalThis.log = [];", "init.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.ESM.RunModuleFile(filepath.Join(dir, "entry.mjs")); err != nil {
		t.Fatalf("RunModuleFile: %v", err)
	}
	logged, err := inst.Runtime.RunScript("JSON.stringify(globalThis.log)", "read.js")
	if err != nil {
		t.Fatal(err)
	}
	return logged.String()
}

// import() loads the module asynchronously: the code after the call, the microtasks it
// queues and process.nextTick callbacks run before the imported module does. The expected
// logs are what Node 20.18.1 records for the same files.
func TestDynamicImport_RunsAfterCurrentCode(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "ordering",
			files: map[string]string{
				"m.mjs": `log.push('m ran'); export default 1;`,
				"entry.mjs": `const p = import('./m.mjs');
log.push('after import()');
Promise.resolve().then(() => log.push('microtask'));
process.nextTick(() => log.push('nextTick'));
const ns = await p;
log.push('awaited ' + ns.default);`,
			},
			want: `["after import()","microtask","nextTick","m ran","awaited 1"]`,
		},
		{
			name: "module throws",
			files: map[string]string{
				"throws.mjs": `log.push('throws ran'); throw new Error('boom');`,
				"entry.mjs": `const p = import('./throws.mjs').catch((e) => log.push('rejected ' + e.message));
log.push('after import()');
await p;`,
			},
			want: `["after import()","throws ran","rejected boom"]`,
		},
		{
			name: "missing module",
			files: map[string]string{
				"entry.mjs": `const p = import('./missing.mjs').catch(() => log.push('rejected'));
log.push('after import()');
await p;`,
			},
			want: `["after import()","rejected"]`,
		},
		{
			name: "CommonJS module",
			files: map[string]string{
				"dep.cjs": `log.push('cjs ran'); module.exports = 2;`,
				"entry.mjs": `const p = import('./dep.cjs');
log.push('after import()');
log.push('default ' + (await p).default);`,
			},
			want: `["after import()","cjs ran","default 2"]`,
		},
		{
			name: "same module twice",
			files: map[string]string{
				"m.mjs": `log.push('m ran'); export default 1;`,
				"entry.mjs": `const a = import('./m.mjs');
const b = import('./m.mjs');
log.push('after import()');
const [x, y] = await Promise.all([a, b]);
log.push('same ' + (x === y));`,
			},
			want: `["after import()","m ran","same true"]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runLogModule(t, tc.files); got != tc.want {
				t.Fatalf("logged %s, want %s", got, tc.want)
			}
		})
	}
}
