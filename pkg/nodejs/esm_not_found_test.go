package nodejs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A module or package that cannot be found makes import() reject with Node's
// ERR_MODULE_NOT_FOUND: its message names the resolved path (or the package) and the
// importer, and a missing module also carries its file: URL. Expected logs are what
// Node 20.18.1 records for the same files, with the directory written as <dir>.
func TestDynamicImport_NotFoundError(t *testing.T) {
	report := `
const report = async (spec) => {
  try { await import(spec); log.push(spec + ' loaded'); }
  catch (e) { log.push([e.constructor.name, e.code, e.message, e.url, Object.keys(e).join()]); }
};
`
	cases := []struct {
		name  string
		files map[string]string
		spec  string
		want  string
	}{
		{
			name: "relative file",
			spec: "./nope.mjs",
			want: `["Error","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/nope.mjs' imported from <dir>/entry.mjs","file://<dir>/nope.mjs","code,url"]`,
		},
		{
			name: "absolute file url",
			spec: "file:///orbital-missing/y.mjs",
			want: `["Error","ERR_MODULE_NOT_FOUND","Cannot find module '/orbital-missing/y.mjs' imported from <dir>/entry.mjs","file:///orbital-missing/y.mjs","code,url"]`,
		},
		{
			name: "missing package",
			spec: "nopkg-xyz",
			want: `["Error","ERR_MODULE_NOT_FOUND","Cannot find package 'nopkg-xyz' imported from <dir>/entry.mjs",null,"code"]`,
		},
		{
			name: "missing scoped package subpath",
			spec: "@sc/p/x.js",
			want: `["Error","ERR_MODULE_NOT_FOUND","Cannot find package '@sc/p' imported from <dir>/entry.mjs",null,"code"]`,
		},
		{
			name:  "missing package main",
			files: map[string]string{"node_modules/pk/package.json": `{"main":"nomain.js"}`},
			spec:  "pk",
			want:  `["Error","ERR_MODULE_NOT_FOUND","Cannot find package '<dir>/node_modules/pk/nomain.js' imported from <dir>/entry.mjs",null,"code"]`,
		},
		{
			name:  "package without main or index",
			files: map[string]string{"node_modules/pn/package.json": `{}`},
			spec:  "pn",
			want:  `["Error","ERR_MODULE_NOT_FOUND","Cannot find package '<dir>/node_modules/pn/index.js' imported from <dir>/entry.mjs",null,"code"]`,
		},
		{
			name:  "missing package subpath",
			files: map[string]string{"node_modules/pk/package.json": `{}`},
			spec:  "pk/sub",
			want:  `["Error","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/node_modules/pk/sub' imported from <dir>/entry.mjs","file://<dir>/node_modules/pk/sub","code,url"]`,
		},
		{
			name:  "missing exports target",
			files: map[string]string{"node_modules/pe/package.json": `{"exports":{"./a":"./a.js"}}`},
			spec:  "pe/a",
			want:  `["Error","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/node_modules/pe/a.js' imported from <dir>/entry.mjs","file://<dir>/node_modules/pe/a.js","code,url"]`,
		},
		{
			// Node stops at the first node_modules holding the package, even when the
			// file is missing there and present in an outer one.
			name: "nearest package decides",
			files: map[string]string{
				"node_modules/pk/sub.js":          `export default 1;`,
				"in/node_modules/pk/package.json": `{}`,
				"in/entry.mjs":                    "",
			},
			spec: "pk/sub.js",
			want: `["Error","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/in/node_modules/pk/sub.js' imported from <dir>/in/entry.mjs","file://<dir>/in/node_modules/pk/sub.js","code,url"]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{}
			for k, v := range tc.files {
				files[k] = v
			}
			entryDir := dir
			if _, ok := files["in/entry.mjs"]; ok {
				delete(files, "in/entry.mjs")
				entryDir = dir + "/in"
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(entryDir, dir), "/")
			entry := "entry.mjs"
			if rel != "" {
				entry = rel + "/entry.mjs"
			}
			files[entry] = report + "await report(" + jsString(tc.spec) + ");"
			writeFiles(t, dir, files)
			got := strings.ReplaceAll(runLogModuleIn(t, entryDir), dir, "<dir>")
			if got != "["+tc.want+"]" {
				t.Fatalf("got  %s\nwant [%s]", got, tc.want)
			}
		})
	}
}

// A resolve hook sees the default resolver's ERR_MODULE_NOT_FOUND (and can retry
// another candidate on it), and the importer gets that error with its code and url,
// as on Node 20.18.1.
func TestDynamicImport_NotFoundThroughHooks(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"hooks.mjs": `export async function resolve(specifier, context, next) {
  try { return await next(specifier, context); }
  catch (e) {
    if (e.code === 'ERR_MODULE_NOT_FOUND' && specifier.endsWith('.js')) return next(specifier.slice(0, -3) + '.mjs', context);
    throw e;
  }
}`,
		"real.mjs": `export default 'real';`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
log.push((await import('./real.js')).default);
try { await import('./gone.mjs'); } catch (e) { log.push(e.code, e.message, e.url); }
try { await import('nopkg-hook'); } catch (e) { log.push(e.code, e.message, e.url); }`,
	})
	got := strings.ReplaceAll(runLogModuleIn(t, dir), dir, "<dir>")
	want := `["real","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/gone.mjs' imported from <dir>/entry.mjs","file://<dir>/gone.mjs","ERR_MODULE_NOT_FOUND","Cannot find package 'nopkg-hook' imported from <dir>/entry.mjs",null]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func jsString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// The error's url is percent-encoded like Node's, and a hook can resolve it again: a
// directory name with a space round-trips through the file: URL.
func TestDynamicImport_NotFoundURLRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a dir")
	writeFiles(t, dir, map[string]string{
		"hooks.mjs": `export async function resolve(specifier, context, next) {
  try { return await next(specifier, context); }
  catch (e) {
    if (e.code === 'ERR_MODULE_NOT_FOUND' && e.url && e.url.endsWith('.js')) return next(e.url.slice(0, -3) + '.mjs', context);
    throw e;
  }
}`,
		"real.mjs": `export default 'real';`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('./gone.mjs'); } catch (e) { log.push(e.url.includes('/a%20dir/gone.mjs')); }
log.push((await import('./real.js')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `[true,"real"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A query or fragment makes a separate module instance and stays in the error's url,
// an encoded "/" or "\" is rejected, and file:/ with one slash is a file URL, as in
// Node 20.18.1.
func TestDynamicImport_FileURLForms(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"sub/m.mjs": `export const n = Math.random(); export const u = import.meta.url;`,
		"entry.mjs": `const d = 'file://' + import.meta.url.slice('file://'.length).replace(/[^/]*$/, '') + 'sub/';
const a = await import(d + 'm.mjs?v=1'), b = await import(d + 'm.mjs?v=2'), c = await import(d + 'm.mjs?v=1');
const f = await import('./sub/m.mjs#x'), g = await import('./sub/m.mjs?v=1'), h = await import(d.replace('file://', 'file:') + 'm.mjs');
log.push(a.n === b.n, a.n === c.n, a.n === g.n, a.u.endsWith('/sub/m.mjs?v=1'), f.u.endsWith('/sub/m.mjs#x'), h.u.endsWith('/sub/m.mjs'));
for (const s of [d + 'a%2Fb.mjs', d + 'a%5cb.mjs', './nope.mjs?q=1']) {
  try { await import(s); } catch (e) { log.push([e.name, e.code, e.message, e.url]); }
}`,
	})
	got := strings.ReplaceAll(runLogModuleIn(t, dir), dir, "<dir>")
	want := `[false,true,true,true,true,true,` +
		`["TypeError","ERR_INVALID_MODULE_SPECIFIER","Invalid module \"<dir>/sub/a%2Fb.mjs\" must not include encoded \"/\" or \"\\\" characters imported from <dir>/entry.mjs",null],` +
		`["TypeError","ERR_INVALID_MODULE_SPECIFIER","Invalid module \"<dir>/sub/a%5cb.mjs\" must not include encoded \"/\" or \"\\\" characters imported from <dir>/entry.mjs",null],` +
		`["Error","ERR_MODULE_NOT_FOUND","Cannot find module '<dir>/nope.mjs' imported from <dir>/entry.mjs","file://<dir>/nope.mjs?q=1"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The error stays a real Error even if code replaced the global constructor.
func TestDynamicImport_NotFoundIgnoresReplacedError(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"entry.mjs": `const E = Error; globalThis.Error = function () { return {}; };
try { await import('./nope.mjs'); } catch (e) { log.push(e instanceof E, e.code); }`,
	})
	if want := `[true,"ERR_MODULE_NOT_FOUND"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A static import that can't be resolved fails the entry the way Node fails it: as an
// uncaught exception printed with its stack, code and url, which an
// 'uncaughtException' listener can handle.
func TestStaticImport_NotFoundIsUncaught(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"entry.mjs": `import './nope.mjs';`})
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, err = inst.ESM.RunModuleFile(filepath.Join(dir, "entry.mjs"))
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("RunModuleFile: %v, want an uncaught exception", err)
	}
	msg := strings.ReplaceAll(uncaught.Message, dir, "<dir>")
	for _, part := range []string{
		"Error [ERR_MODULE_NOT_FOUND]: Cannot find module '<dir>/nope.mjs' imported from <dir>/entry.mjs\n",
		"code: 'ERR_MODULE_NOT_FOUND'",
		"url: 'file://<dir>/nope.mjs'",
	} {
		if !strings.Contains(msg, part) {
			t.Fatalf("message %q lacks %q", msg, part)
		}
	}

	dir = t.TempDir()
	writeFiles(t, dir, map[string]string{"entry.mjs": `import './nope.mjs';`})
	handled, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer handled.Runtime.Dispose()
	if _, err := handled.Runtime.RunScript(`globalThis.log = []; process.on('uncaughtException', (e) => log.push(e.code));`, "init.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := handled.ESM.RunModuleFile(filepath.Join(dir, "entry.mjs")); err != nil {
		t.Fatalf("with a listener: %v", err)
	}
	logged, _ := handled.Runtime.RunScript("JSON.stringify(log)", "read.js")
	if got := logged.String(); got != `["ERR_MODULE_NOT_FOUND"]` {
		t.Fatalf("listener saw %s", got)
	}
}

// Only the specifier is URL-decoded, not the importer's directory (here one whose
// name holds a literal "%20"), and a query never collides with a file whose name
// holds "?". Expected from Node 20.18.1.
func TestDynamicImport_LiteralURLCharactersInPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p%20lit")
	writeFiles(t, dir, map[string]string{
		"dep.mjs":           `export default 'dep';`,
		"m.mjs":             `export default 'm:' + import.meta.url.split('/').pop();`,
		"m.mjs?variant.mjs": `export default 'q-file:' + import.meta.url.split('/').pop();`,
		"entry.mjs": `log.push((await import('./dep.mjs')).default);
log.push((await import('./m.mjs?variant.mjs')).default);
log.push((await import('./m.mjs%3Fvariant.mjs')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `["dep","m:m.mjs?variant.mjs","q-file:m.mjs%3Fvariant.mjs"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A loader hook in the chain doesn't change the class of the default resolver's
// errors.
func TestDynamicImport_ResolveErrorClassThroughHooks(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) { return n(s, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['./a%2Fb.mjs', './nope.mjs']) {
  try { await import(s); } catch (e) { log.push(e.name, e instanceof TypeError, e.code); }
}`,
	})
	if want := `["TypeError",true,"ERR_INVALID_MODULE_SPECIFIER","Error",false,"ERR_MODULE_NOT_FOUND"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
