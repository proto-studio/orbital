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
// url is the module url as Node reports it (displayURL).
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
			`Module "`+url+`" needs an import attribute of type "`+want+`"`)
	default:
		return invalidAttrType(url, typ)
	}
}

// invalidAttrType is the error for a type the module's format doesn't take:
// json (the one type Node accepts) on another format, or any other type.
func invalidAttrType(url, typ string) error {
	if typ == "json" {
		return attrTypeError("ERR_IMPORT_ASSERTION_TYPE_FAILED",
			`Module "`+url+`" is not of type "`+typ+`"`)
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

// displayURL is a module's url as Node reports it: a file: URL (with any query
// or fragment) for a file, node:<name> for a builtin.
func (e *ESM) displayURL(key string) string {
	if i := strings.IndexByte(key, typeKeySep); i >= 0 {
		key = key[:i]
	}
	if strings.HasPrefix(key, "node:") || strings.HasPrefix(key, "native:") || strings.HasPrefix(key, "file:") {
		return key
	}
	if p, ok := e.keyPaths[key]; ok && p != key {
		// Already the encoded path with its query or fragment (moduleKey).
		return "file://" + key
	}
	return fileURL(key)
}

// typeKeySep separates a module url from the import type in its cache key.
const typeKeySep = '\x01'

// typedKey is the module cache key for url imported with a type: like Node,
// which keys modules by url and type, so a hook can load a url differently per
// type. import.meta.url leaves the type out. JSON is the exception: Node keeps
// a JSON module's value per file, so every import of it shares the first.
func (e *ESM) typedKey(url, format string, attrs []v8.ImportAttribute) string {
	typ, ok := attrType(attrs)
	// No type and the implicit one are the same module, as in attrCheckKey.
	if !ok || typ == implicitType || format == "json" {
		return url
	}
	key := url + string(typeKeySep) + typ
	if e.keyPaths == nil {
		e.keyPaths = map[string]string{}
	}
	e.keyPaths[key] = e.pathOf(url)
	return key
}

// checkAttributesOnce validates an import's attributes the first time a module
// is imported with a given type and remembers the outcome. Node caches each
// (url, type) pair's module job, failed or not, and later imports of that pair
// reuse it without checking again.
func (e *ESM) checkAttributesOnce(url, format string, attrs []v8.ImportAttribute) error {
	key := attrCheckKey(url, attrs)
	if err, seen := e.attrChecks[key]; seen {
		return err
	}
	err := checkImportAttributes(e.displayURL(url), format, attrs)
	e.recordAttrCheck(key, err)
	return err
}

// attrCheckKey identifies the module job Node would reuse: the url and the
// import's type (javascript when it names none).
func attrCheckKey(url string, attrs []v8.ImportAttribute) string {
	typ, ok := attrType(attrs)
	if !ok {
		typ = implicitType
	}
	return url + "\x00" + typ
}

func (e *ESM) recordAttrCheck(key string, err error) {
	if e.attrChecks == nil {
		e.attrChecks = map[string]error{}
	}
	e.attrChecks[key] = err
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

// parseAttrsJSON decodes the attributes the loader realm passes back: a JSON
// array of [key, value] pairs (see encodeAttrs in loader-hooks.js), keeping
// their order. A value that isn't a string becomes its string form, as Node
// prints it.
func parseAttrsJSON(s string) []v8.ImportAttribute {
	var pairs [][2]json.RawMessage
	if json.Unmarshal([]byte(s), &pairs) != nil {
		return nil
	}
	attrs := make([]v8.ImportAttribute, 0, len(pairs))
	for _, p := range pairs {
		var key, val string
		if json.Unmarshal(p[0], &key) != nil {
			continue
		}
		var marker struct {
			Undefined bool `json:"undefined"`
		}
		switch {
		case json.Unmarshal(p[1], &val) == nil:
		case json.Unmarshal(p[1], &marker) == nil && marker.Undefined:
			val = "undefined"
		default:
			val = string(p[1])
		}
		attrs = append(attrs, v8.ImportAttribute{Key: key, Value: val})
	}
	return attrs
}
