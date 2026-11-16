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

// A non-string type from a resolve hook is refused before any load hook runs, and a
// module a hook placed at a custom URL is its children's parentURL as is. Expected
// from Node 20.18.1.
func TestImportAttributes_HookTypeCheckedEarlyAndCustomParent(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen.txt")
	writeFiles(t, dir, map[string]string{
		"dep.mjs": `export default 1;`,
		"hooks.mjs": `import { appendFileSync } from 'node:fs';
export async function resolve(s, c, n) {
  if (s === './t42') return { ...(await n('./dep.mjs', c)), importAttributes: { type: 42 } };
  if (s === 'mem:parent') return { url: 'mem:parent', shortCircuit: true };
  if (s === 'child') { appendFileSync(` + strconv.Quote(seen) + `, c.parentURL + '\n'); return { url: 'mem:child', shortCircuit: true }; }
  return n(s, c);
}
export async function load(u, c, n) {
  if (u === 'mem:parent') return { format: 'module', source: "import c from 'child'; export default c;", shortCircuit: true };
  if (u === 'mem:child') return { format: 'module', source: 'export default "child";', shortCircuit: true };
  return { format: 'module', source: 'export default "short"', shortCircuit: true };
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { log.push((await import('./t42')).default); } catch (e) { log.push(e.code, e.message); }
log.push((await import('mem:parent')).default);`,
	})
	got := runLogModuleIn(t, dir)
	want := `["ERR_INVALID_ARG_TYPE","The \"type\" argument must be of type string. Received type number (42)","child"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if b, _ := os.ReadFile(seen); string(b) != "mem:parent\n" {
		t.Fatalf("child's parentURL %q, want mem:parent", b)
	}
}

// Load hooks get a structured clone of the attributes a resolve hook returned, so a
// Date stays a Date and a Map a Map. Expected from Node 20.18.1.
func TestImportAttributes_StructuredCloneToLoadHooks(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"virt.js": ``,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './virt.js') return { ...(await n(s, c)), importAttributes: { d: new Date(0), m: new Map([['k', 1]]) } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('virt.js')) { const a = c.importAttributes; return { format: 'module', source: 'export default ' + JSON.stringify([a.d instanceof Date, a.m instanceof Map, a.m.get && a.m.get('k')]) + ';', shortCircuit: true }; }
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
log.push((await import('./virt.js')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `[[true,true,1]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A null-prototype type from a hook is refused before loading, hook-made JSON
// imported with custom types shares its first value (Node keeps JSON per file), and
// a hook replacing the error globals in its realm doesn't change the loader's
// errors. Expected from Node 20.18.1.
func TestImportAttributes_HookEdgeTypesAndCapturedErrors(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"virt.json": `{}`,
		"dep.mjs":   `export default 1;`,
		"hooks.mjs": `export function initialize() { globalThis.Error = function () { throw new RangeError('replaced'); }; globalThis.TypeError = globalThis.Error; }
export async function resolve(s, c, n) {
  if (s === './np') return { ...(await n('./dep.mjs', c)), importAttributes: { type: Object.create(null) } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('virt.json')) return { format: 'json', source: JSON.stringify({ t: c.importAttributes.type }), shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const a = await import('./virt.json', { with: { type: 'a' } }), b = await import('./virt.json', { with: { type: 'b' } });
log.push(a.default, b.default);
try { await import('./np'); log.push('loaded'); } catch (e) { log.push(e.code, e.message); }
try { await import('./nope.mjs'); } catch (e) { log.push(e instanceof Error, e.code); }`,
	})
	got := runLogModuleIn(t, dir)
	want := `[{"t":"a"},{"t":"a"},"ERR_INVALID_ARG_TYPE","The \"type\" argument must be of type string. Received an instance of Object",true,"ERR_MODULE_NOT_FOUND"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A resolve hook's undefined type is the implicit type for the module map, so a
// short-circuiting load hook can supply the module; the default load refuses it.
// Expected from Node 20.18.1.
func TestImportAttributes_UndefinedHookType(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"dep.mjs":  `export default 1;`,
		"dep2.mjs": `export default 2;`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './u1') return { ...(await n('./dep.mjs', c)), importAttributes: { type: undefined } };
  if (s === './u2') return { ...(await n('./dep2.mjs', c)), importAttributes: { type: undefined } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.endsWith('dep.mjs')) return { format: 'module', source: 'export default "short:" + ' + JSON.stringify(Object.keys(c.importAttributes)), shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['./u1', './u2']) { try { log.push((await import(s)).default); } catch (e) { log.push(e.code, e.message); } }`,
	})
	got := runLogModuleIn(t, dir)
	want := `["short:type","ERR_INVALID_ARG_TYPE","The \"type\" argument must be of type string. Received undefined"]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// The default load's refusal of an undefined type is that module job's outcome, so a
// later import of the same module with no type gets it too. Expected from Node 20.18.1.
func TestImportAttributes_UndefinedTypeFailureIsCached(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"dep.mjs": `export default 1;`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'undef') return { ...(await n('./dep.mjs', c)), importAttributes: { type: undefined } };
  return n(s, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['undef', './dep.mjs']) { try { log.push((await import(s)).default); } catch (e) { log.push(e.code); } }`,
	})
	if got, want := runLogModuleIn(t, dir), `["ERR_INVALID_ARG_TYPE","ERR_INVALID_ARG_TYPE"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A hook's initialize() replacing Object.keys or JSON in its realm doesn't hide
// attributes from the check, as Node's loader internals don't use the hook realm's
// globals.
func TestImportAttributes_HookRealmGlobalsReplaced(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"x.mjs": `export default 1;`,
		"hooks.mjs": `export function initialize() { Object.keys = () => []; JSON.stringify = () => '{}'; Array.isArray = () => true; }
export async function resolve(s, c, n) { return n(s, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('./x.mjs', { with: { x: 'y' } }); log.push('loaded'); } catch (e) { log.push(e.code); }`,
	})
	if got, want := runLogModuleIn(t, dir), `["ERR_IMPORT_ATTRIBUTE_UNSUPPORTED"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Attributes that can't be cloned to the load hooks fail the import, as Node's hooks
// thread boundary does, and a hook's custom URL holding \x01 can't alias a typed key.
func TestImportAttributes_UncloneableAndCustomURLSeparator(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"dep.mjs": `export default 1;`,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './fn') return { ...(await n('./dep.mjs', c)), importAttributes: { f: () => 1 } };
  if (s === 'm1') return { url: 'mem:a\x01b', shortCircuit: true };
  if (s === 'm2') return { url: 'mem:a', shortCircuit: true };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.startsWith('mem:')) return { format: 'module', source: 'export default ' + JSON.stringify(u + '|' + (c.importAttributes.type || '')), shortCircuit: true };
  return { format: 'module', source: 'export default "short"', shortCircuit: true };
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('./fn'); log.push('loaded'); } catch (e) { log.push('rejected'); }
log.push((await import('m1')).default, (await import('m2', { with: { type: 'b' } })).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `["rejected","mem:a%01b|","mem:a|b"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// The attributes a resolve hook returns are cloned once (a getter runs once), a
// sparse array keeps its length, and types differing only in a lone surrogate stay
// separate modules. Expected from Node 20.18.1.
func TestImportAttributes_CloneOnceSparseAndSurrogates(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"v.js": ``,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './g') { let i = 0; const a = {}; Object.defineProperty(a, 'type', { enumerable: true, get() { return ['a', 'b'][i++]; } }); return { ...(await n('./v.js', c)), importAttributes: a }; }
  if (s === './sp') return { ...(await n('./v.js?sp', c)), importAttributes: new Array(5) };
  if (s === './s1') return { ...(await n('./v.js?s1', c)), importAttributes: { type: '\uD800' } };
  if (s === './s2') return { ...(await n('./v.js?s1', c)), importAttributes: { type: '\uD801' } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('v.js')) { const a = c.importAttributes; return { format: 'module', source: 'export default ' + JSON.stringify(Array.isArray(a) ? ['len', a.length, Object.keys(a).length] : a.type === undefined ? null : [...a.type].map(ch => ch.charCodeAt(0))), shortCircuit: true }; }
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['./g', './sp', './s1', './s2']) { try { log.push((await import(s)).default); } catch (e) { log.push(e.code); } }`,
	})
	if got, want := runLogModuleIn(t, dir), `[[97],["len",5,0],[55296],[55297]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A lone-surrogate type keeps its own module (apart from the literal 'ud800') and
// reaches hooks intact from an import() too, and an entry point's job is reused by
// a self-import with type 'javascript'. Expected from Node 20.18.1.
func TestImportAttributes_SurrogateKeysAndEntryJob(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"v.js": ``,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === './a') return { ...(await n('./v.js', c)), importAttributes: { type: '\uD800' } };
  if (s === './b') return { ...(await n('./v.js', c)), importAttributes: { type: 'ud800' } };
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('v.js')) return { format: 'module', source: 'export default ' + JSON.stringify([...String(c.importAttributes.type)].map(ch => ch.charCodeAt(0))), shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import './entry.mjs' with { type: 'javascript' };
import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const s of ['./a', './b']) log.push((await import(s)).default);
log.push((await import('./v.js', { with: { type: '\uDC00' } })).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `[[55296],[117,100,56,48,48],[56320]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A resolve hook's format is the module's format in the default load too; a getter
// on the result's importAttributes, or inside attributes that need rebuilding, runs
// once; and a setter a hook puts on TypeError.prototype doesn't touch the loader's
// errors. Expected from Node 20.18.1.
func TestImportAttributes_HookFormatGettersAndPrototypes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"data.txt": `{"j":1}`,
		"v.js":     ``,
		"d.mjs":    `export default 1;`,
		"hooks.mjs": `export function initialize() { Object.defineProperty(TypeError.prototype, 'code', { set() { throw new Error('setter'); }, configurable: true }); }
export async function resolve(s, c, n) {
  if (s === './txt') return { ...(await n('./data.txt', c)), format: 'json' };
  if (s === './rg') { let i = 0; const r = await n('./v.js?rg', c); return Object.defineProperty({ ...r }, 'importAttributes', { enumerable: true, get() { return i++ === 0 ? { type: 'a' } : 5; } }); }
  if (s === './sg') { let i = 0; const a = []; a[3] = 1; Object.defineProperty(a, 0, { enumerable: true, get() { return ['a', 'b'][i++]; } }); return { ...(await n('./v.js?sg', c)), importAttributes: a }; }
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('v.js')) { const a = c.importAttributes; return { format: 'module', source: 'export default ' + JSON.stringify(Array.isArray(a) ? [a.length, a[0]] : a.type), shortCircuit: true }; }
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
for (const [s, o] of [['./txt', { with: { type: 'json' } }], ['./rg'], ['./sg'], ['./d.mjs', { with: { type: 'css' } }]]) {
  try { log.push((await import(s, o)).default); } catch (e) { log.push(e.code); }
}`,
	})
	if got, want := runLogModuleIn(t, dir), `[{"j":1},"a",[4,"a"],"ERR_IMPORT_ASSERTION_TYPE_UNSUPPORTED"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// JSON a hook serves at a custom URL is a module per type; only file-backed JSON
// shares its first value. Expected from Node 20.18.1.
func TestImportAttributes_CustomURLJSONPerType(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"hooks.mjs": `export async function resolve(s, c, n) { if (s === 'mem:x') return { url: 'mem:x', shortCircuit: true }; return n(s, c); }
export async function load(u, c, n) { if (u === 'mem:x') return { format: 'json', source: JSON.stringify({ t: c.importAttributes.type }), shortCircuit: true }; return n(u, c); }`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
const a = await import('mem:x', { with: { type: 'a' } }), b = await import('mem:x', { with: { type: 'b' } });
log.push(a.default, b.default);`,
	})
	if got, want := runLogModuleIn(t, dir), `[{"t":"a"},{"t":"b"}]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// A BigInt a hook throws arrives as that BigInt, mutating context.importAttributes in
// place changes nothing, and a resolve result's format is read once. Expected from
// Node 20.18.1.
func TestImportAttributes_BigIntMutationAndFormatOnce(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"v.js": ``,
		"hooks.mjs": `export async function resolve(s, c, n) {
  if (s === 'big') throw 10n;
  if (s === './mut') { c.importAttributes.type = 'zzz'; return n('./v.js', c); }
  if (s === './fmt') { let i = 0; const r = await n('./v.js?f', c); return Object.defineProperty({ ...r }, 'format', { enumerable: true, get() { return i++ === 0 ? 'module' : 'json'; } }); }
  return n(s, c);
}
export async function load(u, c, n) {
  if (u.includes('v.js')) return { format: c.format === 'json' ? 'json' : 'module', source: c.format === 'json' ? '{"j":1}' : 'export default ' + JSON.stringify([u.endsWith('?f') ? c.format : null, c.importAttributes.type]), shortCircuit: true };
  return n(u, c);
}`,
		"entry.mjs": `import { register } from 'node:module';
register('./hooks.mjs', import.meta.url);
try { await import('big'); } catch (e) { log.push(typeof e, String(e)); }
log.push((await import('./mut')).default, (await import('./fmt')).default);`,
	})
	if got, want := runLogModuleIn(t, dir), `["bigint","10",[null,null],["module",null]]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
