package nodejs

import (
	"errors"
	"path/filepath"
	goruntime "runtime"
	"strconv"
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

// Equivalent spellings of a query or fragment are one module, as the URL parser
// serializes them; different ones are not. Expected from Node 20.18.1.
func TestDynamicImport_EquivalentQueriesShareAModule(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"dep.mjs": `export const n = Math.random();`,
		"entry.mjs": "const r = async (s) => (await import(s)).n;\n" +
			"const a = await r('./dep.mjs?q=hello world'), b = await r('./dep.mjs?q=hello%20world'), c = await r(\"./dep.mjs?q='x'\"), d = await r('./dep.mjs?q=%27x%27');\n" +
			"const f1 = await r('./dep.mjs#a b'), f2 = await r('./dep.mjs#a%20b'), f3 = await r('./dep.mjs#`'), f4 = await r('./dep.mjs#%60'), u1 = await r('./dep.mjs?é'), u2 = await r('./dep.mjs?%C3%A9');\n" +
			"const g1 = await r('./dep.mjs?a<b>\"c\"'), g2 = await r('./dep.mjs?a%3Cb%3E%22c%22'), h1 = await r('./dep.mjs?x{y}|'), h2 = await r('./dep.mjs?x%7By%7D%7C');\n" +
			"log.push(a === b, c === d, f1 === f2, f3 === f4, u1 === u2, g1 === g2, h1 === h2);",
	})
	if want := `[true,true,true,true,true,true,false]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// More URL forms read as Node reads them: a file: URL without a slash is still
// rooted, tabs and newlines are dropped, and a bare package import keeps its query.
// Expected from Node 20.18.1.
func TestDynamicImport_MoreURLForms(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"relative/x.mjs":                "export default 'local';",
		"dep.mjs":                       `export const n = Math.random();`,
		"node_modules/pkg/package.json": `{"type":"module"}`,
		"node_modules/pkg/sub.js":       `export const u = import.meta.url;`,
		"entry.mjs": `try { log.push((await import('file:relative/x.mjs')).default); } catch (e) { log.push(e.code, e.message.split(' imported')[0]); }
const a = (await import('./dep.mjs?q=ab')).n, b = (await import('./dep.mjs?q=a\tb')).n, c = (await import('./de\np.mjs')).n, d = (await import('./dep.mjs')).n;
log.push(a === b, c === d, (await import('pkg/sub.js?worker')).u.split('/').slice(-2).join('/'));`,
	})
	got := runLogModuleIn(t, dir)
	want := `["ERR_MODULE_NOT_FOUND","Cannot find module '/relative/x.mjs'",true,true,"pkg/sub.js?worker"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Replacing the loader's global helper doesn't change how a link error is reported.
func TestStaticImport_NotFoundIgnoresReplacedHelper(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"entry.mjs": `import './nope.mjs';`})
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.RunScript(`globalThis.__esmRejected = () => Promise.resolve();`, "init.js"); err != nil {
		t.Fatal(err)
	}
	_, err = inst.ESM.RunModuleFile(filepath.Join(dir, "entry.mjs"))
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || !strings.Contains(uncaught.Message, "ERR_MODULE_NOT_FOUND") {
		t.Fatalf("RunModuleFile: %v, want the uncaught ERR_MODULE_NOT_FOUND", err)
	}
}

// An empty query or fragment names the same module as none, and a file: URL's host
// must be empty or localhost (in any case). Expected from Node 20.18.1.
func TestDynamicImport_EmptySuffixesAndHosts(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"m.mjs": `export const n = Math.random(); export const u = import.meta.url.replace(/.*\//, '');`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { const ns = await import(s); return [ns.n, ns.u]; } catch (e) { return [e.name, e.code, e.message]; } };
const base = await r('./m.mjs'), q = await r('./m.mjs?a');
for (const s of ['./m.mjs?', './m.mjs#', './m.mjs?#', 'file://LOCALHOST' + d + '/m.mjs', 'file://LocalHost' + d + '/m.mjs']) { const x = await r(s); log.push(x[0] === base[0], x[1]); }
const qa = await r('./m.mjs?a#'); log.push(qa[0] === q[0], qa[1]);
for (const s of ['file://tmp' + d + '/m.mjs', 'file://example.com' + d + '/m.mjs']) log.push(await r(s));`,
	})
	got := runLogModuleIn(t, dir)
	host := `["TypeError","ERR_INVALID_FILE_URL_HOST","File URL host must be \"localhost\" or empty on ` + goruntime.GOOS + `"]`
	want := `[true,"m.mjs",true,"m.mjs",true,"m.mjs",true,"m.mjs",true,"m.mjs",true,"m.mjs?a",` + host + `,` + host + `]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// More of Node's URL rules: a resolve hook's file: URL needs a local host too, a
// bare import's subpath drops tabs and newlines while a suffix on a bare package
// name is part of the name. Expected from Node 20.18.1. The error properties are
// also own data properties even with a setter on Error.prototype (Node itself
// aborts there, so that part is Orbital's own guarantee).
func TestDynamicImport_HookHostsAndBareSuffixes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/pkg/package.json":        `{"type":"module","main":"index.js"}`,
		"node_modules/pkg/index.js":            `export const n = 0;`,
		"node_modules/pkg/sub.js":              `export const n = Math.random(); export const u = import.meta.url.split('/').pop();`,
		"node_modules/@scope/pkg/package.json": `{"type":"module","main":"index.js"}`,
		"node_modules/@scope/pkg/index.js":     `export default 1;`,
		"a\x01b.mjs":                           `export const u = import.meta.url.split('/').pop();`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'remote') return { url: 'file://example.com/tmp/x.mjs', shortCircuit: true };
  return n(s, c);
}`,
		"entry.mjs": `const r = async (s) => { try { const ns = await import(s); return [ns.n, ns.u]; } catch (e) { return [e.name, e.code, e.message.split(' imported')[0]]; } };
Object.defineProperty(Error.prototype, 'code', { set() { throw new Error('setter'); }, configurable: true });
const a = await r('pkg/sub.js?q=ab'), b = await r('pkg/sub.js?q=a\tb'), c = await r('pkg/su\nb.js');
log.push(a[0] === b[0], b[1], c[1], await r('pkg?variant'), await r('@scope/pkg#x'), (await r('./a%01b.mjs'))[1]);
const { register } = await import('node:module');
register('./hooks.mjs', import.meta.url);
log.push(await r('remote'));`,
	})
	got := runLogModuleIn(t, dir)
	want := `[true,"sub.js?q=ab","sub.js",["Error","ERR_MODULE_NOT_FOUND","Cannot find package 'pkg?variant'"],` +
		`["Error","ERR_MODULE_NOT_FOUND","Cannot find package '@scope/pkg#x'"],"a%01b.mjs",` +
		`["TypeError","ERR_INVALID_FILE_URL_HOST","File URL host must be \"localhost\" or empty on ` + goruntime.GOOS + `"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An --import that can't be resolved is an uncaught exception, as in Node.
func TestPreload_NotFoundIsUncaught(t *testing.T) {
	dir := t.TempDir()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	err = inst.ESM.Preload(filepath.Join(dir, "nope.mjs"))
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || !strings.Contains(uncaught.Message, "Error [ERR_MODULE_NOT_FOUND]: Cannot find module") ||
		!strings.Contains(uncaught.Message, "url: 'file://") {
		t.Fatalf("Preload: %v, want the uncaught ERR_MODULE_NOT_FOUND", err)
	}
}

// A package's "exports" map is matched against the whole subpath, query included,
// and a resolve hook's file: URL with an encoded "/" is refused. Expected from
// Node 20.18.1.
func TestDynamicImport_ExportsSubpathQueryAndHookEncodedSlash(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/p/package.json": `{"type":"module","exports":{"./sub":"./plain.mjs","./sub?raw":"./raw.mjs"}}`,
		"node_modules/p/plain.mjs":    `export default 'plain:' + import.meta.url.split('/').pop();`,
		"node_modules/p/raw.mjs":      `export default 'raw:' + import.meta.url.split('/').pop();`,
		"node_modules/q/package.json": `{"type":"module","exports":{"./sub?raw":"./raw.mjs"}}`,
		"node_modules/q/raw.mjs":      `export default 'raw:' + import.meta.url.split('/').pop();`,
		"dep.mjs":                     `export default 'dep';`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'enc') return { url: import.meta.url.replace(/[^/]*$/, 'a%2Fdep.mjs'), shortCircuit: true };
  return n(s, c);
}`,
		"entry.mjs": `const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.name, e.code, e.message]; } };
log.push(await r('p/sub?raw'), await r('p/sub'), await r('q/sub?raw'));
const { register } = await import('node:module');
register('./hooks.mjs', import.meta.url);
log.push(await r('enc'));`,
	})
	got := runLogModuleIn(t, dir)
	want := `["raw:raw.mjs","plain:plain.mjs","raw:raw.mjs",["TypeError","ERR_INVALID_FILE_URL_PATH","File URL path must not include encoded / characters"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A missing file in a package keeps its query and fragment in the error's url, and a
// file: URL's host ends at a query. Expected from Node 20.18.1.
func TestDynamicImport_PackageSuffixInErrorAndHostlessQuery(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/pkg/package.json": `{"type":"module"}`,
		"dep.mjs":                       `export default 'dep';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.code, e.url.split('/').pop()]; } };
log.push(await r('pkg/nope.mjs?variant=1#x'), await r('file://localhost?next=' + d + '/dep.mjs'));`,
	})
	got := runLogModuleIn(t, dir)
	if want := `[["ERR_MODULE_NOT_FOUND","nope.mjs?variant=1#x"],["ERR_MODULE_NOT_FOUND","dep.mjs"]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Package subpaths and "exports" targets are URL paths: decoded, with encoded
// separators refused and a target's query kept; whitespace anywhere in a specifier
// is dropped. The error stringifies with its code and survives a throwing
// Error.prepareStackTrace. Expected from Node 20.18.1.
func TestDynamicImport_PackageURLPathsAndErrorShape(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/pkg/package.json": `{"type":"module"}`,
		"node_modules/pkg/sub.js":       `export default 'sub:' + import.meta.url.split('/').pop();`,
		"node_modules/pkg/a b.js":       `export default 'ab:' + import.meta.url.split('/').pop();`,
		"node_modules/pkg/a%2Fb.js":     `export default 'lit';`,
		"node_modules/ex/package.json":  `{"type":"module","exports":{".":"./raw.mjs?x","./sp":"./a%20b.mjs"}}`,
		"node_modules/ex/raw.mjs":       `export default 'raw:' + import.meta.url.split('/').pop();`,
		"node_modules/ex/a b.mjs":       `export default 'exab:' + import.meta.url.split('/').pop();`,
		"x.mjs":                         `export default 'x';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.code, e.message.split(' imported')[0].replace(d, '<d>')]; } };
for (const s of ['p\nkg/sub.js', 'fi\nle://' + d + '/x.mjs', 'pkg/a%20b.js', 'pkg/a%2Fb.js', 'ex', 'ex/sp']) log.push(await r(s));
try { await import('./nope.mjs'); } catch (e) { log.push(String(e).split(' imported')[0].replace(d, '<d>'), e instanceof Error, e.constructor === Error); }
Error.prepareStackTrace = () => { throw new Error('custom'); };
try { await import('./nope.mjs'); } catch (e) { log.push(e.code); }`,
	})
	got := runLogModuleIn(t, dir)
	want := `["sub:sub.js","x","ab:a%20b.js",["ERR_INVALID_MODULE_SPECIFIER","Invalid module \"<d>/node_modules/pkg/a%2Fb.js\" must not include encoded \"/\" or \"\\\" characters"],` +
		`"raw:raw.mjs?x","exab:a%20b.mjs","Error [ERR_MODULE_NOT_FOUND]: Cannot find module '<d>/nope.mjs'",true,true,"ERR_MODULE_NOT_FOUND"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// "exports" targets are checked as Node checks them, an escape that isn't UTF-8 is a
// URIError, and import.meta.url encodes a file name the way Node's pathToFileURL
// does. Expected from Node 20.18.1.
func TestDynamicImport_PackageTargetsAndURLEscaping(t *testing.T) {
	dir := t.TempDir()
	names := []string{"a!b", "a'b", "a(b)", "a*b", "a{b}", "a`b", "a~b", "a;b", "a=b", "a@b", "a&b", "a+b", "a,b", "a$b", "a[b]", "a|b", "a^b", "aéb"}
	files := map[string]string{
		"node_modules/e1/package.json": `{"type":"module","exports":{".":"./a%2Fb.mjs"}}`,
		"node_modules/e1/a/b.mjs":      `export default 'nested';`,
		"node_modules/e2/package.json": `{"type":"module","exports":{".":"./%2e%2e/out.mjs"}}`,
		"node_modules/out.mjs":         `export default 'out';`,
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		files[n+".mjs"] = `export default import.meta.url.split('/').pop();`
		quoted[i] = strconv.Quote(n)
	}
	files["entry.mjs"] = `const d = decodeURIComponent(import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, ''));
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.name, e.code, e.message.split(' imported')[0].replace(d, '<d>')]; } };
log.push(await r('e1'), await r('e2'), await r('./x%FF.mjs'));
for (const n of [` + strings.Join(quoted, ",") + `]) log.push(await r('./' + encodeURIComponent(n) + '.mjs'));`
	writeFiles(t, dir, files)
	got := runLogModuleIn(t, dir)
	want := `[["TypeError","ERR_INVALID_MODULE_SPECIFIER","Invalid module \"<d>/node_modules/e1/a%2Fb.mjs\" must not include encoded \"/\" or \"\\\" characters"],` +
		`["Error","ERR_INVALID_PACKAGE_TARGET","Invalid \"exports\" main target \"./%2e%2e/out.mjs\" defined in the package config <d>/node_modules/e2/package.json"],` +
		`["URIError",null,"URI malformed"],` +
		`"a!b.mjs","a'b.mjs","a(b).mjs","a*b.mjs","a%7Bb%7D.mjs","a%60b.mjs","a~b.mjs","a;b.mjs","a=b.mjs","a@b.mjs","a&b.mjs","a+b.mjs","a,b.mjs","a$b.mjs","a[b].mjs","a|b.mjs","a^b.mjs","a%C3%A9b.mjs"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A builtin is matched before tabs and newlines are dropped, a package name after;
// an "exports" target may repeat a separator, an array skips invalid alternatives,
// and a malformed escape is a URIError. Expected from Node 20.18.1.
func TestDynamicImport_BareWhitespaceTargetsAndBadEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/pkg/package.json":  `{"type":"module"}`,
		"node_modules/pkg/sub.js":        `export default 'sub';`,
		"node_modules/dd/package.json":   `{"type":"module","exports":"./dist//index.mjs"}`,
		"node_modules/dd/dist/index.mjs": `export default 'dd';`,
		"node_modules/arr/package.json":  `{"type":"module","exports":{".":["../invalid.mjs","./valid.mjs"]}}`,
		"node_modules/arr/valid.mjs":     `export default 'valid';`,
		"bad%ZZ.mjs":                     `export default 'lit';`,
		"a%2.mjs":                        `export default 'lit2';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.name, e.code, e.message.split(' imported')[0].replace(d, '<d>')]; } };
log.push(await r('f\ns'), await r('p\nkg/sub.js'), await r('p\nkg'), await r('dd'), await r('arr'), await r('./bad%ZZ.mjs'), await r('./a%2.mjs'), await r('./x%'));`,
	})
	got := runLogModuleIn(t, dir)
	want := `[["Error","ERR_MODULE_NOT_FOUND","Cannot find package 'f\ns'"],"sub",` +
		`["Error","ERR_MODULE_NOT_FOUND","Cannot find package '<d>/node_modules/pkg/index.js'"],"dd","valid",` +
		`["URIError",null,"URI malformed"],["URIError",null,"URI malformed"],["URIError",null,"URI malformed"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An exports array skips an alternative whose condition resolves to an invalid
// target, and an escaped "localhost" host is localhost. Expected from Node 20.18.1.
func TestDynamicImport_NestedTargetAlternativesAndEscapedHost(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/arr2/package.json": `{"type":"module","exports":{".":[{"node":"../invalid.mjs"},"./valid.mjs"]}}`,
		"node_modules/arr2/valid.mjs":    `export default 'valid2';`,
		"x.mjs":                          `export default 'x';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.code, e.message]; } };
log.push(await r('arr2'), await r('file://%6cocalhost' + d + '/x.mjs'));`,
	})
	if got, want := runLogModuleIn(t, dir), `["valid2","x"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// An exports target's dot segments are checked without its query or fragment, an
// array of only invalid targets reports the last one, and an installed
// Error.prepareStackTrace runs only when the stack is read. Expected from Node 20.18.1.
func TestDynamicImport_TargetSuffixesAndLazyStack(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/q1/package.json": `{"type":"module","exports":"./..?x"}`,
		"node_modules/q2/package.json": `{"type":"module","exports":"./%2e%2e#x"}`,
		"node_modules/q3/package.json": `{"type":"module","exports":["../bad.mjs"]}`,
		"node_modules/q3/index.js":     `export default 'idx';`,
		"node_modules/index.js":        `export default 'nmidx';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.code, e.message.split(' imported')[0].replace(d, '<d>')]; } };
let calls = 0;
log.push(await r('q1'), await r('q2'), await r('q3'));
Error.prepareStackTrace = () => { calls++; return 'custom'; };
try { await import('./nope.mjs'); } catch (e) { log.push(calls, e.code); void e.stack; log.push(calls); }
delete Error.prepareStackTrace;`,
	})
	got := runLogModuleIn(t, dir)
	target := func(t, pkg string) string {
		return `["ERR_INVALID_PACKAGE_TARGET","Invalid \"exports\" main target \"` + t + `\" defined in the package config <d>/node_modules/` + pkg + `/package.json"]`
	}
	want := `[` + target("./..?x", "q1") + `,` + target("./%2e%2e#x", "q2") + `,` + target("../bad.mjs", "q3") + `,0,"ERR_MODULE_NOT_FOUND",1]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An exports array of only "" is an invalid target, and a coded error's stack is
// formatted when first read, by an Error.prepareStackTrace installed after the
// failure. Expected from Node 20.18.1.
func TestDynamicImport_EmptyTargetAndStackFormattedOnRead(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"node_modules/q4/package.json": `{"type":"module","exports":[""]}`,
		"node_modules/q4/index.js":     `export default 'q4idx';`,
		"entry.mjs": `try { await import('q4'); log.push('loaded'); } catch (e) { log.push(e.code); }
let err; try { await import('./nope.mjs'); } catch (e) { err = e; }
Error.prepareStackTrace = () => 'custom';
log.push(err.stack, String(err).slice(0, 30));
delete Error.prepareStackTrace;
let err2; try { await import('./nope2.mjs'); } catch (e) { err2 = e; }
log.push(err2.stack.split('\n')[0].slice(0, 40));`,
	})
	got := runLogModuleIn(t, dir)
	want := `["ERR_INVALID_PACKAGE_TARGET","custom","Error [ERR_MODULE_NOT_FOUND]: ","Error [ERR_MODULE_NOT_FOUND]: Cannot fin"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A file: URL host with a malformed or non-UTF-8 escape is an invalid URL, and a
// program's own stack formatter's result is kept as it is. Expected from Node 20.18.1.
func TestDynamicImport_InvalidHostAndCustomFormatter(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"entry.mjs": `for (const u of ['file://%ZZ/tmp/x.mjs', 'file://%FF/tmp/x.mjs']) { try { await import(u); } catch (e) { log.push([e.name, e.code, e.message, e.input, Object.keys(e).join()]); } }
let err; try { await import('./nope.mjs'); } catch (e) { err = e; }
Error.prepareStackTrace = () => 'Error: custom';
log.push(err.stack);
delete Error.prepareStackTrace;`,
	})
	want := `[["TypeError","ERR_INVALID_URL","Invalid URL","file://%ZZ/tmp/x.mjs","code,input"],["TypeError","ERR_INVALID_URL","Invalid URL","file://%FF/tmp/x.mjs","code,input"],"Error: custom"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An invalid URL's error keeps its input property through a loader hook.
func TestDynamicImport_InvalidURLInputThroughHooks(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) { return n(s, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('file://%ZZ/tmp/x.mjs'); } catch (e) { log.push(e.name, e.code, e.input, Object.keys(e).join()); }`,
	})
	if want := `["TypeError","ERR_INVALID_URL","file://%ZZ/tmp/x.mjs","code,input"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Backslashes in a file: URL are separators, an invalid URL's input is the
// specifier as given, and the stack's code header uses the error's current name.
// Expected from Node 20.18.1.
func TestDynamicImport_BackslashesInputAndRenamedStack(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"x.mjs": `export default 'x';`,
		"entry.mjs": `const d = import.meta.url.slice('file://'.length).replace(/\/[^/]*$/, '');
const r = async (s) => { try { return (await import(s)).default; } catch (e) { return [e.code, e.message.split(' imported')[0], e.input]; } };
log.push(await r('file://localhost' + d.replace(/\//g, '\\') + '\\x.mjs'), await r('file:' + d.replace(/\//g, '\\') + '\\x.mjs'), await r('file://%Z\nZ/tmp/x'));
let err; try { await import('./nope.mjs'); } catch (e) { err = e; }
err.name = 'X'; log.push(err.stack.split('\n')[0].slice(0, 35));`,
	})
	if want := `["x","x",["ERR_INVALID_URL","Invalid URL","file://%Z\nZ/tmp/x"],"X [ERR_MODULE_NOT_FOUND]: Cannot fi"]`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A coded error's stack header and string use the name and message it has when
// read and the code it was created with; a file: URL with credentials or a port is
// an invalid URL, and a non-local host error names its input and module. Expected
// from Node 20.18.1.
func TestDynamicImport_CodedErrorShapeAndURLAuthority(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"entry.mjs": `let err; try { await import('./nope.mjs'); } catch (e) { err = e; }
err.message = ''; log.push(err.stack.split('\n')[0]);
let err2; try { await import('./nope2.mjs'); } catch (e) { err2 = e; }
err2.code = 'X'; log.push(String(err2).slice(0, 30)); delete err2.code; log.push(String(err2).slice(0, 30));
let err3; try { await import('./nope3.mjs'); } catch (e) { err3 = e; }
err3.name = ''; log.push(err3.stack.split('\n')[0].slice(0, 30));
for (const u of ['file://example.com/tmp/x.mjs', 'file://user@localhost/tmp/x.mjs', 'file://localhost:123/tmp/x.mjs']) {
  try { await import(u); } catch (e) { log.push([e.code, e.input, e.module && e.module.split('/').pop(), Object.keys(e).join()]); }
}`,
	})
	want := `["Error [ERR_MODULE_NOT_FOUND]: ","Error [ERR_MODULE_NOT_FOUND]: ","Error [ERR_MODULE_NOT_FOUND]: "," [ERR_MODULE_NOT_FOUND]: Canno",` +
		`["ERR_INVALID_FILE_URL_HOST","file://example.com/tmp/x.mjs","entry.mjs","code,input,module"],` +
		`["ERR_INVALID_URL","file://user@localhost/tmp/x.mjs",null,"code,input"],["ERR_INVALID_URL","file://localhost:123/tmp/x.mjs",null,"code,input"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// What a loader hook throws reaches the importer as it was: class, message, stack
// and own properties, or the thrown value itself; the loader's own errors arrive the
// same way, as plain errors carrying their code. Expected from Node 20.18.1.
func TestDynamicImport_HookErrorsArriveAsThrown(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'custom') throw Object.assign(new Error('custom'), { code: 'MY_CODE', extra: 42 });
  if (s === 'range') throw new RangeError('r');
  if (s === 'prim') throw 'just a string';
  return n(s, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['custom', 'range', 'prim']) {
  try { await import(s); } catch (e) { log.push(typeof e === 'object' ? [e.constructor.name, e.message, e.code, e.extra, Object.keys(e).join(), String(e), e.stack.split('\n')[0]] : e); }
}
try { await import('./nope.mjs'); } catch (e) { log.push(String(e).slice(0, 26), e.stack.split('\n')[0].slice(0, 30), e.code); }`,
	})
	want := `[["Error","custom","MY_CODE",42,"code,extra","Error: custom","Error: custom"],["RangeError","r",null,null,"","RangeError: r","RangeError: r"],"just a string",` +
		`"Error: Cannot find module ","Error [ERR_MODULE_NOT_FOUND]: ","ERR_MODULE_NOT_FOUND"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A non-error value a hook throws arrives as that value, and a file: URL host
// is checked against the URL grammar (decoded delimiters, forbidden characters,
// IPv6). Expected from Node 20.18.1.
func TestDynamicImport_HookValuesAndHostGrammar(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'obj') throw { foo: 1 };
  if (s === 'date') throw new Date(0);
  if (s === 'arr') throw [1, 2];
  return n(s, c);
}`,
		"entry.mjs": `for (const u of ['file://local%20host/tmp/x', 'file://[abc]/tmp/x', 'file://user%40localhost/tmp/x', 'file://localhost%3A12/tmp/x', 'file://[::1]/tmp/x']) {
  try { await import(u); } catch (e) { log.push([e.code, e.input]); }
}
const { register } = await import('node:module');
register('./hooks.mjs', import.meta.url);
for (const s of ['obj', 'date', 'arr']) { try { await import(s); } catch (e) { log.push([Object.prototype.toString.call(e), e instanceof Error, JSON.stringify(e), e.message, e.stack === undefined]); } }`,
	})
	want := `[["ERR_INVALID_URL","file://local%20host/tmp/x"],["ERR_INVALID_URL","file://[abc]/tmp/x"],["ERR_INVALID_URL","file://user%40localhost/tmp/x"],["ERR_INVALID_URL","file://localhost%3A12/tmp/x"],["ERR_INVALID_FILE_URL_HOST","file://[::1]/tmp/x"],` +
		`["[object Object]",false,"{\"foo\":1}",null,true],["[object Date]",false,"\"1970-01-01T00:00:00.000Z\"",null,true],["[object Array]",false,"[1,2]",null,true]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
