// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

// gen_widths.js writes inspect.js's table of character widths from the ICU in Node 20.18.1.
//go:generate node --expose-internals gen_widths.js

import _ "embed"

// JS is a JavaScript expression evaluating to an object of functions: the pieces of util.inspect
// that console and util share, from quoting strings and keys to laying a value's entries out on
// one line or several within breakLength. inspect.js lists and describes each.
//
//go:embed inspect.js
var JS string
