// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

// gen_widths.js writes inspect.js's table of character widths from the ICU in Node 20.18.1.
//go:generate node --expose-internals gen_widths.js

import (
	_ "embed"
	"errors"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// js is a JavaScript function that takes the native helpers below and returns an object of
// functions: the pieces of util.inspect that console and util share, from quoting strings and
// keys to laying a value's entries out on one line or several within breakLength. inspect.js
// lists and describes each.
//
//go:embed inspect.js
var js string

// Load evaluates the shared code in rt and returns the object of functions it makes. Each
// module that needs it loads its own copy, since an embedder can register either without the
// other.
func Load(rt *runtime.Runtime) (*v8.Value, error) {
	factory, err := rt.RunScript(js, "inspect.js")
	if err != nil {
		return nil, err
	}
	if factory == nil || !factory.IsFunction() {
		return nil, errors.New("inspectjs: inspect.js did not evaluate to a function")
	}
	// ownNonIndexKeys(value) lists value's own enumerable keys other than array indices, which
	// JavaScript can only find by listing every index too. It runs natively, so the arrays it
	// answers aren't kept alive in Go.
	ownNonIndexKeys, err := rt.Context().OwnNonIndexKeysFunction()
	if err != nil {
		return nil, err
	}
	return factory.Call(nil, ownNonIndexKeys)
}
