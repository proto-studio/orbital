// Package inspectjs holds JavaScript that more than one module needs to format values the way
// Node's util.inspect does.
package inspectjs

import _ "embed"

// QuoteJS is a JavaScript expression evaluating to { quote, key }: quote(str) quotes a string,
// and key(k) formats an object key, as util.inspect does.
//
//go:embed quote.js
var QuoteJS string
