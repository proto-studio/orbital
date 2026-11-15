package nodejs

import (
	"os"
	"path/filepath"
	"strconv"
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

// A NUL inside an attribute key or value is kept, so it can't pass for `type` or
// `json`. With or without a loader hook, each (module, type) pair keeps its first
// outcome. Expected from Node 20.18.1.
func TestImportAttributes_NULAndCacheWithHooks(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		dir := t.TempDir()
		register := ""
		if hooked {
			register = "import { register } from 'node:module';\nregister('./hooks.mjs', import.meta.url);\n"
		}
		writeFiles(t, dir, map[string]string{
			"data.json": `{"a":1}`,
			"hooks.mjs": `export async function resolve(s, c, n) { return n(s, c); }`,
			"entry.mjs": register + `const r = async (o) => { try { await import('./data.json', o); log.push('ok'); } catch (e) { log.push(e.code); } };
await r({ with: { type: 'json\0css' } });
await r({ with: { 'type\0x': 'json' } });
await r();
await r({ with: { type: 'json' } });
await r({ with: { type: 'json', foo: 'x' } });
await r({ with: { type: 'javascript' } });`,
		})
		got := runLogModuleIn(t, dir)
		want := `["ERR_IMPORT_ASSERTION_TYPE_UNSUPPORTED","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","ok","ok","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED"]`
		if got != want {
			t.Fatalf("hooked=%v: got  %s\nwant %s", hooked, got, want)
		}
	}
}

// Through hooks: an attribute a resolve hook sets to undefined is still checked, a
// module a load hook generates per type is a separate instance for each type (but a
// JSON module keeps its first value, and import.meta.url leaves the type out), and a
// query stays in the url an attribute error names. Expected from Node 20.18.1.
func TestImportAttributes_HookEdgeCases(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"data.json": `{"a":1}`,
		"virt.json": `{}`,
		"virt.js":   ``,
		"dep.mjs":   `export default import.meta.url.split('/').pop();`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s.endsWith('undef.mjs')) return { ...(await n('./dep.mjs', c)), importAttributes: { foo: undefined } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('virt.json')) return { format: 'json', source: JSON.stringify({ t: c.importAttributes.type }), shortCircuit: true };
  if (u.endsWith('virt.js')) return { format: 'module', source: 'export default ' + JSON.stringify(c.importAttributes.type) + '; export const u = import.meta.url;', shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const r = async (s, o) => { try { const ns = await import(s, o); log.push(JSON.stringify(ns.default)); } catch (e) { log.push(e.code + ' ' + e.message.replace(/file:\/\/\S*\//, '')); } };
await r('./undef.mjs');
await r('./virt.json', { with: { type: 'json' } });
await r('./virt.json', { with: { type: 'xml' } });
await r('./data.json?x');
await r('./dep.mjs?q', {});
const a = await import('./virt.js', { with: { type: 'a' } });
const b = await import('./virt.js', { with: { type: 'b' } });
const c = await import('./virt.js', { with: { type: 'a' } });
log.push([a.default, b.default, c.default, a === c, a.u.split('/').pop()]);`,
	})
	got := runLogModuleIn(t, dir)
	want := `["ERR_IMPORT_ATTRIBUTE_UNSUPPORTED Import attribute \"foo\" with value \"undefined\" is not supported",` +
		`"{\"t\":\"json\"}","{\"t\":\"json\"}",` +
		`"ERR_IMPORT_ASSERTION_TYPE_MISSING Module \"data.json?x\" needs an import attribute of type \"json\"",` +
		`"\"dep.mjs?q\"",["a","b","a",true,"virt.js"]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A resolve hook's importAttributes must be an object (null and arrays pass), with
// Node's error naming the hook that returned last. Expected from Node 20.18.1.
func TestImportAttributes_HookResultChecked(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"dep.mjs": `export default 'dep';`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s.includes('prim')) return { ...(await n('./dep.mjs', c)), importAttributes: 'oops' };
  if (s.includes('nul')) return { ...(await n('./dep.mjs', c)), importAttributes: null };
  if (s.includes('arr')) return { ...(await n('./dep.mjs', c)), importAttributes: ['x'] };
  if (s.includes('own')) return { url: new URL('./dep.mjs', import.meta.url).href, importAttributes: 42, shortCircuit: true };
  return n(s, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['./prim', './nul', './arr', './own']) {
  try { log.push((await import(s)).default); } catch (e) { log.push([e.name, e.code, e.message]); }
}`,
	})
	got := runLogModuleIn(t, dir)
	want := `[["TypeError","ERR_INVALID_RETURN_PROPERTY_VALUE","Expected an object to be returned for the \"importAttributes\" from the \"node:internal/modules/esm/resolve 'resolve'\" hook but got type string ('oops')."],` +
		`"dep","dep",` +
		`["TypeError","ERR_INVALID_RETURN_PROPERTY_VALUE","Expected an object to be returned for the \"importAttributes\" from the \"./hooks.mjs 'resolve'\" hook but got type number (42)."]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An explicit type 'javascript' that a hook lets through is the same module as no
// type, and a "__proto__" attribute key survives the hook bridge to be rejected.
// Expected from Node 20.18.1.
func TestImportAttributes_ImplicitTypeAndProtoKey(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"dep.mjs":  `export default Math.random();`,
		"dep2.mjs": `export default 1;`,
		"hooks.mjs": `export async function resolve(s, c, n) { return n(s, c); }
export async function load(u, c, n) {
  if (u.endsWith('dep.mjs')) return { format: 'module', source: 'export default Math.random();', shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const a = await import('./dep.mjs');
const b = await import('./dep.mjs', { with: { type: 'javascript' } });
log.push(a === b);
try { await import('./dep2.mjs', { with: { ['__proto__']: 'x' } }); log.push('loaded'); } catch (e) { log.push(e.code, e.message); }`,
	})
	got := runLogModuleIn(t, dir)
	want := `[true,"ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","Import attribute \"__proto__\" with value \"x\" is not supported"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A resolve hook's context.parentURL is the importer's file: URL as Node gives it,
// with its query but without the type a typed import adds to the cache key.
func TestImportAttributes_HookParentURL(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"child.mjs": `export default 'child';`,
		"virt.js":   ``,
		"hooks.mjs": `import { appendFileSync } from 'node:fs';
export async function resolve(s, c, n) {
  if (s === './child.mjs') appendFileSync(` + strconv.Quote(filepath.Join(dir, "seen.txt")) + `, c.parentURL.replace(/.*\//, '') + ' ' + c.parentURL.startsWith('file:///') + '\n');
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('virt.js')) return { format: 'module', source: "import d from './child.mjs'; export default d;", shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
log.push((await import('./virt.js', { with: { type: 'a' } })).default, (await import('./virt.js?x')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `["child","child"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	seen, err := os.ReadFile(filepath.Join(dir, "seen.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(seen), "virt.js true\nvirt.js?x true\n"; got != want {
		t.Fatalf("parentURLs %q, want %q", got, want)
	}
}

// Attribute values a resolve hook supplies that aren't strings get Node's errors:
// ERR_INVALID_ARG_TYPE for `type`, and the value as Node prints it for any other key.
// A type holding a NUL keeps its own module. Expected from Node 20.18.1, each case in
// a fresh runtime.
func TestImportAttributes_NonStringHookValues(t *testing.T) {
	hooks := `const table = { t42: { type: 42 }, tbig: { type: 10n }, tobj: { type: {} }, tnull: { type: null }, f42: { foo: 42 }, fobj: { foo: {} }, fbig: { foo: 10n } };
export async function resolve(s, c, n) {
  const m = s.match(/^\.\/(json|js)-(\w+)$/);
  if (m) return { ...(await n(m[1] === 'json' ? './data.json' : './dep.mjs', c)), importAttributes: table[m[2]] };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('virt.js')) return { format: 'module', source: 'export default ' + JSON.stringify(c.importAttributes.type) + ';', shortCircuit: true };
  return n(u, c);
}`
	argType := `The \"type\" argument must be of type string. Received `
	cases := map[string]string{
		"t42":   `["TypeError","ERR_INVALID_ARG_TYPE","` + argType + `type number (42)"]`,
		"tbig":  `["TypeError","ERR_INVALID_ARG_TYPE","` + argType + `type bigint (10n)"]`,
		"tobj":  `["TypeError","ERR_INVALID_ARG_TYPE","` + argType + `an instance of Object"]`,
		"tnull": `["TypeError","ERR_INVALID_ARG_TYPE","` + argType + `null"]`,
		"f42":   `["TypeError","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","Import attribute \"foo\" with value \"42\" is not supported"]`,
		"fobj":  `["TypeError","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","Import attribute \"foo\" with value \"{}\" is not supported"]`,
		"fbig":  `["TypeError","ERR_IMPORT_ATTRIBUTE_UNSUPPORTED","Import attribute \"foo\" with value \"10n\" is not supported"]`,
	}
	for name, want := range cases {
		for _, f := range []string{"json", "js"} {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				"data.json": `{"a":1}`,
				"dep.mjs":   `export default 1;`,
				"hooks.mjs": hooks,
				"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('./` + f + `-` + name + `'); log.push('ok'); } catch (e) { log.push([e.name, e.code, e.message]); }`,
			})
			if got := runLogModuleIn(t, dir); got != "["+want+"]" {
				t.Errorf("%s %s: got %s, want [%s]", f, name, got, want)
			}
		}
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"virt.js":   ``,
		"hooks.mjs": hooks,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const a = await import('./virt.js', { with: { type: 'a\0x' } }), b = await import('./virt.js', { with: { type: 'a' } });
log.push(a.default, b.default, a === b);`,
	})
	if got, want := runLogModuleIn(t, dir), `["a\u0000x","a",false]`; got != want {
		t.Fatalf("NUL type: got %s, want %s", got, want)
	}
}

// An array a resolve hook returns as importAttributes reaches the load hooks as an
// array, and a file name holding the key separator byte keeps its whole url.
// Expected from Node 20.18.1.
func TestImportAttributes_ArrayShapeAndSeparatorInPath(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"virt.js":    ``,
		"a\x01b.mjs": `export default import.meta.url.split('/').pop();`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './virt.js') return { ...(await n(s, c)), importAttributes: ['x'] };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('virt.js')) return { format: 'module', source: 'export default ' + JSON.stringify([Array.isArray(c.importAttributes), c.importAttributes.length, c.importAttributes[0]]) + ';', shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
log.push((await import('./virt.js')).default, (await import('./a%01b.mjs')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `[[true,1,"x"],"a%01b.mjs"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
