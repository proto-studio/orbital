package esm

// Named exports of an imported CommonJS module, found the way Node 20 finds them
// (lib/internal/modules/esm/translators.js, cjsPreparseModuleExports): by lexing
// the module's source with cjs-module-lexer before it runs, and following the
// modules it re-exports (`module.exports = require('./x')`). Names the lexer can't
// see in the source, such as keys added to an object later assigned to
// module.exports, are not named exports, exactly as on Node.

import (
	_ "embed"
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed cjs-module-lexer.js
var cjsLexerJS string

// cjsNames is the ordered set of a CommonJS module's export names.
type cjsNames struct {
	list []string
	seen map[string]bool
}

func (n *cjsNames) add(name string) {
	if !n.seen[name] {
		n.seen[name] = true
		n.list = append(n.list, name)
	}
}

// cjsLexer returns cjs-module-lexer's parse(source) function, compiling the
// lexer once per runtime.
func (e *ESM) cjsLexer() (*v8.Value, error) {
	if e.lexer != nil {
		return e.lexer, nil
	}
	fn, err := e.rt.RunScript("(function () {\nvar module = { exports: {} };\n"+cjsLexerJS+
		"\nreturn module.exports.parse;\n})()", "cjs-module-lexer.js")
	if err != nil {
		return nil, err
	}
	if fn == nil || !fn.IsFunction() {
		return nil, errors.New("esm: cjs-module-lexer did not load")
	}
	e.lexer = fn
	return fn, nil
}

// lexCJS returns the exports and re-exported specifiers the lexer finds in
// source. Like Node, a source the lexer can't parse has none.
func (e *ESM) lexCJS(source string) (exports, reexports []string) {
	parse, err := e.cjsLexer()
	if err != nil {
		return nil, nil
	}
	arg, err := e.rt.Context().NewString(source)
	if err != nil {
		return nil, nil
	}
	res, err := parse.Call(nil, arg)
	if err != nil || res == nil || !res.IsObject() {
		return nil, nil
	}
	return stringList(res, "exports"), stringList(res, "reexports")
}

func stringList(obj *v8.Value, key string) []string {
	list, _ := obj.Get(key)
	if list == nil || !list.IsArray() {
		return nil
	}
	n := list.ArrayLength()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if item, _ := list.GetIndex(i); item != nil {
			out = append(out, item.String())
		}
	}
	return out
}

// cjsExportNames returns the export names of the CommonJS module at filename
// with the given source, merging in those of the modules it re-exports. Results
// are remembered per file; an entry is recorded before its re-exports are
// followed, so a cycle sees the names found so far, as in Node.
func (e *ESM) cjsExportNames(filename, source string) []string {
	if names, ok := e.cjsExports[filename]; ok {
		return names.list
	}
	names := &cjsNames{seen: map[string]bool{}}
	if e.cjsExports == nil {
		e.cjsExports = map[string]*cjsNames{}
	}
	e.cjsExports[filename] = names

	exports, reexports := e.lexCJS(source)
	for _, name := range exports {
		names.add(name)
	}
	for _, spec := range reexports {
		resolved := e.resolveCJS(spec, filepath.Dir(filename))
		// Node follows only absolute paths (not builtins) that CommonJS doesn't
		// load as JSON or a native addon.
		if resolved == "" || !filepath.IsAbs(resolved) {
			continue
		}
		if ext := filepath.Ext(resolved); ext == ".json" || ext == ".node" {
			continue
		}
		src, err := e.fsRead(resolved)
		if err != nil {
			continue
		}
		for _, name := range e.cjsExportNames(resolved, src) {
			names.add(name)
		}
	}
	return names.list
}

// resolveCJS resolves a require() specifier from dir with the CommonJS
// resolver, returning "" when it can't be resolved.
func (e *ESM) resolveCJS(spec, dir string) string {
	global, err := e.rt.Context().Global()
	if err != nil {
		return ""
	}
	fn, _ := global.Get("__resolveModule")
	if fn == nil || !fn.IsFunction() {
		return ""
	}
	ctx := e.rt.Context()
	a, err := ctx.NewString(spec)
	if err != nil {
		return ""
	}
	b, err := ctx.NewString(dir)
	if err != nil {
		return ""
	}
	res, err := fn.Call(nil, a, b)
	if err != nil || res == nil || !res.IsString() {
		return ""
	}
	return res.String()
}

// cjsModuleWrapper builds the ES module standing in for the CommonJS module at
// path: like Node's, its default export is module.exports, and each name the
// lexer found is a named export holding module.exports' own property of that
// name (undefined when there is none). The module runs when the wrapper is
// evaluated, in order with the rest of the graph.
func (e *ESM) cjsModuleWrapper(path, source string) string {
	names := e.cjsExportNames(path, source)
	var b strings.Builder
	b.WriteString("const __m = require(")
	b.WriteString(jsQuote(path))
	b.WriteString(");\nexport default __m;\n")
	var exported []string
	for _, name := range names {
		if name == "default" {
			continue
		}
		local := "__e" + strconv.Itoa(len(exported))
		exported = append(exported, local+" as "+jsQuote(name))
		b.WriteString("let " + local + ";\n")
		b.WriteString("if (Object.prototype.hasOwnProperty.call(__m, " + jsQuote(name) + ")) try { " +
			local + " = __m[" + jsQuote(name) + "]; } catch {}\n")
	}
	if len(exported) > 0 {
		b.WriteString("export { " + strings.Join(exported, ", ") + " };\n")
	}
	return b.String()
}
