package esm

// Bridges from the Go loader into the runtime's live JS state:
//   - the set of builtin module names (from `require('module').builtinModules`)
//   - a CJS module's own export names (via the real `require`), used to
//     synthesize named ESM re-exports.
//
// These are the only points where default loading touches JS; everything else
// (filesystem, package.json, classification) is pure Go.

import (
	"errors"

	"proto.zip/studio/orbital/pkg/v8"
)

// globalRequire returns the runtime's global require function.
func (e *ESM) globalRequire() (*v8.Value, error) {
	ctx := e.rt.Context()
	global, err := ctx.Global()
	if err != nil {
		return nil, err
	}
	req, err := global.Get("require")
	if err != nil {
		return nil, err
	}
	if req == nil || !req.IsFunction() {
		return nil, errors.New("esm: global require is not available")
	}
	return req, nil
}

// loadBuiltins populates the builtin-name set once from the module registry.
func (e *ESM) loadBuiltins() {
	if e.builtins != nil {
		return
	}
	e.builtins = map[string]bool{}
	req, err := e.globalRequire()
	if err != nil {
		return
	}
	ctx := e.rt.Context()
	arg, err := ctx.NewString("module")
	if err != nil {
		return
	}
	modVal, err := req.Call(nil, arg)
	if err != nil || modVal == nil || !modVal.IsObject() {
		return
	}
	list, _ := modVal.Get("builtinModules")
	if list == nil || !list.IsArray() {
		return
	}
	n := list.ArrayLength()
	for i := 0; i < n; i++ {
		item, _ := list.GetIndex(i)
		if item == nil {
			continue
		}
		name := item.String()
		e.builtins[name] = true
		if len(name) > 5 && name[:5] == "node:" {
			e.builtins[name[5:]] = true
		}
	}
}

// builtinBare reports whether specifier names a builtin and returns its bare
// name (without a node: prefix).
func (e *ESM) builtinBare(specifier string) (string, bool) {
	e.loadBuiltins()
	bare := specifier
	if len(specifier) > 5 && specifier[:5] == "node:" {
		bare = specifier[5:]
	}
	if e.builtins[bare] {
		return bare, true
	}
	return "", false
}

// requireKeys returns the own, identifier-shaped, enumerable export names of the
// module require(reqArg) resolves to (excluding "default"), for named re-export.
func (e *ESM) requireKeys(reqArg string) ([]string, error) {
	req, err := e.globalRequire()
	if err != nil {
		return nil, err
	}
	ctx := e.rt.Context()
	arg, err := ctx.NewString(reqArg)
	if err != nil {
		return nil, err
	}
	m, err := req.Call(nil, arg)
	if err != nil {
		return nil, err
	}
	if m == nil || !m.IsObject() {
		return nil, nil
	}
	names, err := m.GetPropertyNames()
	if err != nil || names == nil || !names.IsArray() {
		return nil, nil
	}
	n := names.ArrayLength()
	keys := make([]string, 0, n)
	seen := map[string]bool{"default": true}
	for i := 0; i < n; i++ {
		k, _ := names.GetIndex(i)
		if k == nil {
			continue
		}
		ks := k.String()
		if seen[ks] || !identRe.MatchString(ks) {
			continue
		}
		seen[ks] = true
		keys = append(keys, ks)
	}
	return keys, nil
}
