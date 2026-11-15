package esm

// Import attributes (`import x from './d.json' with { type: 'json' }`), checked
// the way Node 20's default load does (lib/internal/modules/esm/assert.js): only
// a `type` key is allowed, a JSON module needs `type: 'json'`, and other formats
// must not name a type.

import (
	"encoding/json"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

// implicitType is the type of every format except JSON. Node's HTML-derived rule
// is that it may not be written out: `with { type: 'javascript' }` is an error.
const implicitType = "javascript"

// formatTypes maps a module format to the attribute type it requires.
var formatTypes = map[string]string{
	"builtin":  implicitType,
	"commonjs": implicitType,
	"json":     "json",
	"module":   implicitType,
	"wasm":     implicitType,
}

// checkImportAttributes validates an import's attributes against the format of
// the module it loads. url is the module's resolved url.
func checkImportAttributes(url, format string, attrs []v8.ImportAttribute) error {
	for _, a := range attrs {
		if a.Key != "type" {
			return attrTypeError("ERR_IMPORT_ATTRIBUTE_UNSUPPORTED",
				`Import attribute "`+a.Key+`" with value "`+a.Value+`" is not supported`)
		}
	}
	typ, hasType := attrType(attrs)
	want, known := formatTypes[format]
	switch {
	case !known:
		// Node ignores attributes for formats it doesn't recognize.
		return nil
	case want == implicitType:
		if !hasType {
			return nil
		}
		return invalidAttrType(url, typ)
	case hasType && typ == want:
		return nil
	case !hasType:
		return attrTypeError("ERR_IMPORT_ASSERTION_TYPE_MISSING",
			`Module "`+moduleURL(url)+`" needs an import attribute of type "`+want+`"`)
	default:
		return invalidAttrType(url, typ)
	}
}

// invalidAttrType is the error for a type the module's format doesn't take:
// json (the one type Node accepts) on another format, or any other type.
func invalidAttrType(url, typ string) error {
	if typ == "json" {
		return attrTypeError("ERR_IMPORT_ASSERTION_TYPE_FAILED",
			`Module "`+moduleURL(url)+`" is not of type "`+typ+`"`)
	}
	return attrTypeError("ERR_IMPORT_ASSERTION_TYPE_UNSUPPORTED",
		`Import attribute type "`+typ+`" is unsupported`)
}

func attrTypeError(code, msg string) error {
	return &moduleError{msg: msg, code: code, typeError: true}
}

// attrType returns the value of the `type` attribute, if there is one.
func attrType(attrs []v8.ImportAttribute) (string, bool) {
	for _, a := range attrs {
		if a.Key == "type" {
			return a.Value, true
		}
	}
	return "", false
}

// moduleURL is a module's url as Node reports it: file: URLs for files, builtins
// as node:<name>.
func moduleURL(url string) string {
	if strings.HasPrefix(url, "node:") || strings.HasPrefix(url, "native:") || strings.HasPrefix(url, "file:") {
		return url
	}
	return fileURL(url)
}

// checkAttributesOnce validates an import's attributes the first time a module
// is imported with a given type and remembers the outcome. Node caches each
// (url, type) pair's module job, failed or not, and later imports of that pair
// reuse it without checking again.
func (e *ESM) checkAttributesOnce(url, format string, attrs []v8.ImportAttribute) error {
	typ, ok := attrType(attrs)
	if !ok {
		typ = implicitType
	}
	key := url + "\x00" + typ
	if err, seen := e.attrChecks[key]; seen {
		return err
	}
	err := checkImportAttributes(url, format, attrs)
	if e.attrChecks == nil {
		e.attrChecks = map[string]error{}
	}
	e.attrChecks[key] = err
	return err
}

// attrsJSON encodes attributes as the object a hook sees as importAttributes.
func attrsJSON(attrs []v8.ImportAttribute) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, a := range attrs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsQuote(a.Key))
		b.WriteByte(':')
		b.WriteString(jsQuote(a.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// parseAttrsJSON decodes an importAttributes object a hook returned, keeping its
// key order. Non-string values are kept as their JSON text.
func parseAttrsJSON(s string) []v8.ImportAttribute {
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var attrs []v8.ImportAttribute
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return attrs
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return attrs
		}
		var val string
		if json.Unmarshal(raw, &val) != nil {
			val = string(raw)
		}
		attrs = append(attrs, v8.ImportAttribute{Key: key, Value: val})
	}
	return attrs
}
