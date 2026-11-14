// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

import _ "embed"

// JS is a JavaScript expression evaluating to { context, quote, string, key, more, view, binary,
// cut, reduce }: the pieces of util.inspect that console and util share, from quoting strings and
// keys to laying a value's entries out on one line or several within breakLength. inspect.js
// describes each.
//
//go:embed inspect.js
var JS string
