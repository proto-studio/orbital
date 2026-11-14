// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

import _ "embed"

// JS is a JavaScript expression evaluating to { quote, key, view, binary, reduce }: quote(str)
// quotes a string, key(k) formats an object key, view(v, maxArrayLength) prints a Buffer or typed
// array, binary(v, maxArrayLength) describes an ArrayBuffer or DataView, and reduce(entries, open,
// close, options) lays a value's entries out on one line or several, as util.inspect does.
//
//go:embed inspect.js
var JS string
