package esm

// Import attributes (`import x from './d.json' with { type: 'json' }`), checked
// the way Node 20's default load does (lib/internal/modules/esm/assert.js): only
// a `type` key is allowed, a JSON module needs `type: 'json'`, and other formats
// must not name a type.

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
	"unicode/utf8"

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
// the module it loads. url is the module url as Node reports it (displayURL).
func checkImportAttributes(url, format string, attrs []v8.ImportAttribute) error {
	return checkHookAttributes(url, format, attrs, "")
}

// checkHookAttributes is checkImportAttributes for attributes a loader hook
// supplied, where `type` may not be a string: typeReceived then describes the
// value as Node's ERR_INVALID_ARG_TYPE does ("type number (42)").
func checkHookAttributes(url, format string, attrs []v8.ImportAttribute, typeReceived string) error {
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
		return invalidAttrType(url, typ, typeReceived)
	case hasType && typ == want && typeReceived == "":
		return nil
	case !hasType:
		return attrTypeError("ERR_IMPORT_ASSERTION_TYPE_MISSING",
			`Module "`+url+`" needs an import attribute of type "`+want+`"`)
	default:
		return invalidAttrType(url, typ, typeReceived)
	}
}

// invalidAttrType is the error for a type the module's format doesn't take:
// json (the one type Node accepts) on another format, or any other type.
func invalidAttrType(url, typ, typeReceived string) error {
	if typeReceived != "" {
		return attrTypeError("ERR_INVALID_ARG_TYPE",
			`The "type" argument must be of type string. Received `+typeReceived)
	}
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
	// Any absolute URL (node:, file:, or a scheme a hook made up) is kept.
	if urlSchemeRe.MatchString(key) {
		return key
	}
	if p, ok := e.keyPaths[key]; ok && p != key {
		// Already the encoded path with its query or fragment (moduleKey).
		return "file://" + key
	}
	return fileURL(key)
}

// urlSchemeRe matches a string that starts with a URL scheme.
var urlSchemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

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
	// Escaped, so the key has no NUL to cut it short in the glue.
	key := url + string(typeKeySep) + neturl.QueryEscape(typ)
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
	// Escaped, so no type can spell keyAttrs's tagged form.
	return url + "\x00" + neturl.QueryEscape(typ)
}

func (e *ESM) recordAttrCheck(key string, err error) {
	if e.attrChecks == nil {
		e.attrChecks = map[string]error{}
	}
	e.attrChecks[key] = err
}

// keyAttrs returns the attributes to key a hook's module by: the type's exact
// UTF-16 units (typeKey in loader-hooks.js's encoding) in place of its text,
// which may have lost a lone surrogate on the way.
func keyAttrs(attrs []v8.ImportAttribute, encoded string) []v8.ImportAttribute {
	var e struct {
		TypeKey *string `json:"typeKey"`
	}
	if json.Unmarshal([]byte(encoded), &e) != nil || e.TypeKey == nil {
		return attrs
	}
	out := make([]v8.ImportAttribute, len(attrs))
	for i, a := range attrs {
		if a.Key == "type" {
			// Tagged with \x02, which the keys' escaping of an ordinary type
			// never leaves in it.
			a.Value = "\x02" + *e.TypeKey
		}
		out[i] = a
	}
	return out
}

// withoutType returns attrs without its `type` entry.
func withoutType(attrs []v8.ImportAttribute) []v8.ImportAttribute {
	out := make([]v8.ImportAttribute, 0, len(attrs))
	for _, a := range attrs {
		if a.Key != "type" {
			out = append(out, a)
		}
	}
	return out
}

// attrsJSON encodes attributes as the object a hook sees as importAttributes.
func attrsJSON(attrs []v8.ImportAttribute) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, a := range attrs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(wtf8Quote(a.Key))
		b.WriteByte(':')
		b.WriteString(wtf8Quote(a.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// wtf8Quote is a JSON string for s, which may hold WTF-8 (a lone surrogate as
// its own three bytes, as the glue passes attributes): such a surrogate becomes
// its \uXXXX escape, so JavaScript gets the same string back.
func wtf8Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			unit := 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
			fmt.Fprintf(&b, "\\u%04x", unit)
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		q := jsQuote(string(r))
		b.WriteString(q[1 : len(q)-1])
		i += size
	}
	b.WriteByte('"')
	return b.String()
}

// parseAttrsJSON decodes the attributes the loader realm passes back: a JSON
// object whose "pairs" are [key, value] arrays (see encodeAttrs in loader-hooks.js), keeping
// their order. A value that isn't a string comes as {"text": String(value),
// "received": how Node describes it}; it becomes its text, and for `type` the
// description is returned too.
func parseAttrsJSON(s string) (attrs []v8.ImportAttribute, typeReceived string) {
	var encoded struct {
		Pairs [][2]json.RawMessage `json:"pairs"`
	}
	if json.Unmarshal([]byte(s), &encoded) != nil {
		return nil, ""
	}
	pairs := encoded.Pairs
	attrs = make([]v8.ImportAttribute, 0, len(pairs))
	for _, p := range pairs {
		var key, val string
		if json.Unmarshal(p[0], &key) != nil {
			continue
		}
		if json.Unmarshal(p[1], &val) != nil {
			var other struct {
				Text      string `json:"text"`
				Received  string `json:"received"`
				Undefined bool   `json:"undefined"`
			}
			_ = json.Unmarshal(p[1], &other)
			val = other.Text
			if key == "type" {
				// Any non-string marks it, even one JS couldn't describe.
				typeReceived = other.Received
				if other.Undefined {
					typeReceived = "undefined"
				}
				if typeReceived == "" {
					typeReceived = "a value of another type"
				}
			}
		}
		attrs = append(attrs, v8.ImportAttribute{Key: key, Value: val})
	}
	return attrs, typeReceived
}
