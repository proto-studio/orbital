package nodejs

import (
	"strings"
	"testing"
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
