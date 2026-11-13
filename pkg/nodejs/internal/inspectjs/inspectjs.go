// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

import _ "embed"

// JS is a JavaScript expression evaluating to { quote, key, view }: quote(str) quotes a string,
// key(k) formats an object key, and view(v, maxArrayLength) prints a Buffer or typed array, as
// util.inspect does.
//
//go:embed inspect.js
var JS string
