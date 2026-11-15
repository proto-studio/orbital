package nodejs

import (
	"strings"
	"testing"
)

// Import attributes are checked against the format of the module they load, with
// Node's errors. Each case runs in a fresh runtime; the expected logs are what Node
// 20.18.1 records for the same files, with the directory written as <dir>.
func TestImportAttributes_Checked(t *testing.T) {
	files := map[string]string{
		"data.json": `{"a":1}`,
		"dep.mjs":   `export default 1;`,
		"dep.cjs":   `module.exports = 2;`,
	}
	report := `
const report = async (spec, opts) => {
  try { const ns = await import(spec, opts); log.push(JSON.stringify(ns.default)); }
  catch (e) { log.push([e.name, e.code, e.message]); }
};
`
	cases := []struct {
		name, call, want string
	}{
		{"json without type", `report('./data.json')`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_MISSING","Module \"file://<dir>/data.json\" needs an import attribute of type \"json\""]`},
		{"json with empty attributes", `report('./data.json', { with: {} })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_MISSING","Module \"file://<dir>/data.json\" needs an import attribute of type \"json\""]`},
		{"json with type json", `report('./data.json', { with: { type: 'json' } })`, `"{\"a\":1}"`},
		{"unknown type", `report('./data.json', { with: { type: 'css' } })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_UNSUPPORTED","Import attribute type \"css\" is unsupported"]`},
		{"explicit javascript type", `report('./dep.mjs', { with: { type: 'javascript' } })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_UNSUPPORTED","Import attribute type \"javascript\" is unsupported"]`},
		{"unknown key", `report('./data.json', { with: { type: 'json', foo: 'x' } })`,
			`["TypeError","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","Import attribute \"foo\" with value \"x\" is not supported"]`},
		{"module as json", `report('./dep.mjs', { with: { type: 'json' } })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_FAILED","Module \"file://<dir>/dep.mjs\" is not of type \"json\""]`},
		{"commonjs as json", `report('./dep.cjs', { with: { type: 'json' } })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_FAILED","Module \"file://<dir>/dep.cjs\" is not of type \"json\""]`},
		{"builtin as json", `report('fs', { with: { type: 'json' } })`,
			`["TypeError","ERR_IMPORT_ASSERTION_TYPE_FAILED","Module \"node:fs\" is not of type \"json\""]`},
		{"module without attributes", `report('./dep.mjs')`, `"1"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			all := map[string]string{"entry.mjs": report + "await " + tc.call + ";"}
			for k, v := range files {
				all[k] = v
			}
			writeFiles(t, dir, all)
			got := strings.ReplaceAll(runLogModuleIn(t, dir), dir, "<dir>")
			if got != "["+tc.want+"]" {
				t.Fatalf("got  %s\nwant [%s]", got, tc.want)
			}
		})
	}
}

// A static import's attributes are checked while the graph links, so a bad one stops
// it before any module runs, as on Node.
func TestImportAttributes_StaticImport(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"data.json": `{"a":1}`,
		"ok.mjs":    `import d from './data.json' with { type: 'json' }; log.push(d.a);`,
		"bad.mjs":   `log.push('bad ran'); import d from './data.json';`,
		"entry.mjs": `await import('./ok.mjs');
try { await import('./bad.mjs'); } catch (e) { log.push(e.code); }`,
	})
	got := runLogModuleIn(t, dir)
	if want := `[1,"ERR_IMPORT_ASSERTION_TYPE_MISSING"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Like Node, a module imported again with a type it was already imported with reuses
// that first outcome without checking again.
func TestImportAttributes_CachedPerType(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"data.json": `{"a":1}`,
		"entry.mjs": `const r = async (o) => { try { await import('./data.json', o); log.push('ok'); } catch (e) { log.push(e.code); } };
await r();
await r({ with: { type: 'json' } });
await r({ with: { type: 'json', foo: 'x' } });
await r({ with: { type: 'javascript' } });`,
	})
	got := runLogModuleIn(t, dir)
	if want := `["ERR_IMPORT_ASSERTION_TYPE_MISSING","ok","ok","ERR_IMPORT_ASSERTION_TYPE_MISSING"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Hooks see the import's attributes in their context, a resolve hook can replace
// them, and the default load checks them against the chain's format; a load hook that
// short-circuits skips the check. Expected from Node 20.18.1.
func TestImportAttributes_ThroughHooks(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"data.json": `{"a":1}`,
		"raw.json":  `{"b":2}`,
		"dep.mjs":   `export default 1;`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s.endsWith('auto.json')) return { ...(await n(s.replace('auto', 'data'), c)), importAttributes: { type: 'json' } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('raw.json')) return { format: 'json', source: '{"short":true}', shortCircuit: true };
  if (u.endsWith('dep.mjs')) globalThis.depAttrs = JSON.stringify(c.importAttributes);
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const r = async (s, o) => { try { const ns = await import(s, o); log.push(JSON.stringify(ns.default)); } catch (e) { log.push(e.name + ' ' + e.code); } };
await r('./data.json');
await r('./data.json', { with: { type: 'json' } });
await r('./auto.json');
await r('./raw.json');
await r('./dep.mjs', { with: { type: 'json' } });`,
	})
	got := runLogModuleIn(t, dir)
	want := `["TypeError ERR_IMPORT_ASSERTION_TYPE_MISSING","{\"a\":1}","{\"a\":1}","{\"short\":true}","TypeError ERR_IMPORT_ASSERTION_TYPE_FAILED"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
