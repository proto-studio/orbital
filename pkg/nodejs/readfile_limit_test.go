package nodejs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// sparseFile makes a file that reports size bytes without using the disk for them.
func sparseFile(t *testing.T, name string, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file over 2 GiB fails as it does in Node 20.18.1, from the sync, callback and promise
// forms, without being read.
func TestFS_ReadFileTooLargeMatchesNode(t *testing.T) {
	path := sparseFile(t, "big.bin", 1<<31+10)
	got := runJS(t, &runtime.Config{}, `
const fs = require('fs');
const path = `+strconv.Quote(path)+`;
const describe = (e) => e.name + ' ' + e.code + ' ' + e.message;
const results = {};
try { fs.readFileSync(path); } catch (e) { results.sync = describe(e); }
fs.readFile(path, (e) => {
  results.callback = describe(e);
  fs.promises.readFile(path).catch((e) => { results.promise = describe(e); done(results); });
});
`)
	const want = "RangeError ERR_FS_FILE_TOO_LARGE File size (2147483658) is greater than 2 GiB"
	wantJSON(t, got, map[string]any{"sync": want, "callback": want, "promise": want})
}

// A file over the most one string can carry is refused with the same error, naming that limit.
func TestFS_ReadFileOverOrbitalsLimit(t *testing.T) {
	path := sparseFile(t, "mid.bin", 500<<20)
	got := runJS(t, &runtime.Config{}, `
try { require('fs').readFileSync(`+strconv.Quote(path)+`); done({ read: true }); }
catch (e) { done({ error: e.name + ' ' + e.code + ' ' + e.message }); }
`)
	wantJSON(t, got, map[string]any{"error": "RangeError ERR_FS_FILE_TOO_LARGE File size (524288000) is greater than 402653166 bytes"})
}

// require, import and worker entry files load at most one string's worth of source, and
// fail a larger file with Node 20.18.1's errors, without reading it into memory: a file
// over V8's longest string is ERR_STRING_TOO_LONG, except that import refuses one over
// 2 GiB with ERR_FS_FILE_TOO_LARGE first.
func TestModules_TooLargeMatchesNode(t *testing.T) {
	const tooLong = "Error ERR_STRING_TOO_LONG Cannot create a string longer than 0x1fffffe8 characters"
	for _, tc := range []struct {
		size       int64
		wantImport string
	}{
		{size: 600_000_000, wantImport: tooLong},
		{size: 1<<31 + 10, wantImport: "RangeError ERR_FS_FILE_TOO_LARGE File size (2147483658) is greater than 2 GiB"},
	} {
		t.Run(strconv.FormatInt(tc.size, 10), func(t *testing.T) {
			js := sparseFile(t, "big.js", tc.size)
			mjs := sparseFile(t, "big.mjs", tc.size)
			got := runJS(t, &runtime.Config{}, `
const describe = (e) => e.name + ' ' + e.code + ' ' + e.message;
const results = {};
try { require(`+strconv.Quote(js)+`); results.require = 'loaded'; } catch (e) { results.require = describe(e); }
import(`+strconv.Quote(mjs)+`).then(() => 'loaded', describe).then((outcome) => {
  results.import = outcome;
  const { Worker } = require('worker_threads');
  const w = new Worker(`+strconv.Quote(js)+`);
  w.on('error', (e) => { results.worker = describe(e); });
  w.on('exit', (code) => { results.workerExit = code; done(results); });
});
`)
			wantJSON(t, got, map[string]any{
				"require":    tooLong,
				"import":     tc.wantImport,
				"worker":     tooLong,
				"workerExit": float64(1),
			})
		})
	}
}

// With a loader hook in the chain, the default load fails a too-large file with the same
// errors, as Node's does.
func TestLoaderHook_TooLargeMatchesNode(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"hooks.mjs": `export async function load(u, c, n) { return n(u, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const name of ['mid.mjs', 'big.mjs']) {
  try { await import('./' + name); log.push('loaded'); } catch (e) { log.push(e.name + ' ' + e.code + ' ' + e.message); }
}`,
	})
	for name, size := range map[string]int64{"mid.mjs": 600_000_000, "big.mjs": 1<<31 + 10} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
	}
	got := runLogModuleIn(t, dir)
	want := `["Error ERR_STRING_TOO_LONG Cannot create a string longer than 0x1fffffe8 characters",` +
		`"RangeError ERR_FS_FILE_TOO_LARGE File size (2147483658) is greater than 2 GiB"]`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
