package nodejs

import "testing"

// The named exports of an imported CommonJS module are the ones cjs-module-lexer
// finds in its source, as on Node: keys added to an object later assigned to
// module.exports and getters the lexer doesn't recognize are not named exports, a
// name the lexer saw that module.exports lacks is still in the namespace (as
// undefined), names need not be identifiers, re-exports are followed, and the
// default export is module.exports itself. Expected logs are Node 20.18.1's.
func TestImportCommonJS_NamedExportsFromSource(t *testing.T) {
	files := map[string]string{
		"cjsdep.cjs": `exports.a = 1; module.exports.b = 2;
Object.defineProperty(exports, 'c', { enumerable: true, get() { return 3; } });`,
		"dep2.cjs":   `const o = {}; o.r = 1; module.exports = o;`,
		"esm.cjs":    `exports.__esModule = true; exports.default = 5; exports.x = 1;`,
		"odd.cjs":    `exports["a-b"] = 1; if (false) exports.ghost = 2; exports.default = 9;`,
		"re.cjs":     `module.exports = { ...require("./cjsdep.cjs"), own: 1 };`,
		"re2.cjs":    `module.exports = require("./dep2.cjs");`,
		"cycle.cjs":  `exports.p = 1; module.exports = { ...require('./cycle2.cjs'), ...module.exports };`,
		"cycle2.cjs": `exports.q = 1; module.exports = { ...require('./cycle.cjs'), ...module.exports };`,
		"entry.mjs": `for (const f of ['./cjsdep.cjs', './dep2.cjs', './esm.cjs', './odd.cjs', './re.cjs', './re2.cjs', './cycle.cjs']) {
  const ns = await import(f);
  log.push([f, Object.keys(ns).sort().join(','), JSON.stringify(ns.default), 'ghost' in ns ? String(ns.ghost) : '-', ns['a-b']]);
}`,
	}
	got := runLogModule(t, files)
	want := `[["./cjsdep.cjs","a,b,default","{\"a\":1,\"b\":2,\"c\":3}","-",null],` +
		`["./dep2.cjs","default","{\"r\":1}","-",null],` +
		`["./esm.cjs","__esModule,default,x","{\"__esModule\":true,\"default\":5,\"x\":1}","-",null],` +
		`["./odd.cjs","a-b,default,ghost","{\"a-b\":1,\"default\":9}","undefined",1],` +
		`["./re.cjs","a,b,default","{\"a\":1,\"b\":2,\"c\":3,\"own\":1}","-",null],` +
		`["./re2.cjs","default","{\"r\":1}","-",null],` +
		`["./cycle.cjs","default,p,q","{\"p\":1,\"q\":1}","-",null]]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// An imported CommonJS module runs when the graph evaluates, after the modules
// imported before it, not while the graph links.
func TestImportCommonJS_RunsInGraphOrder(t *testing.T) {
	got := runLogModule(t, map[string]string{
		"first.mjs":  `log.push('mjs ran');`,
		"second.cjs": `log.push('cjs ran'); exports.v = 1;`,
		"entry.mjs":  `import './first.mjs'; import { v } from './second.cjs'; log.push('entry ' + v);`,
	})
	if want := `["mjs ran","cjs ran","entry 1"]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
