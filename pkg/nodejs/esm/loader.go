package esm

// Go-native ES module resolution + loading.
//
// This is the DEFAULT loader (the innermost `nextResolve`/`nextLoad` in Node's
// hook terminology). It runs entirely in Go: filesystem access goes through the
// runtime's (sandboxed) Filesystem, package.json is parsed with encoding/json,
// and CJS/ESM classification is done here. It calls into JS to lex a CommonJS
// module's source for its export names (cjs_exports.go), to resolve what it
// re-exports, and to enumerate a builtin's exports.
//
// Registered loader hooks (module.register) wrap these defaults; see hooks.go.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"unicode/utf8"
)

var (
	identRe   = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	esmSynRe  = regexp.MustCompile(`(^|[\n;])\s*(import\s|import\{|import\(|export\s|export\{|export\*|export default)`)
	blockCmt  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	fileExts  = []string{".js", ".mjs", ".cjs", ".json", ".ts", ".mts", ".cts"}
	indexFile = []string{"index.js", "index.mjs", "index.cjs", "index.json", "index.ts"}
	// Conditions honored when resolving package.json "exports", most specific
	// first. We are a Node-like runtime, so "node" and "import" both apply.
	exportConditions = []string{"node", "import", "module", "default"}
)

// ---- filesystem helpers (through the sandboxed runtime FS) -----------------

func (e *ESM) fsRead(p string) (string, error) {
	b, err := e.rt.Filesystem().ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (e *ESM) fsIsFile(p string) bool {
	info, err := e.rt.Filesystem().Stat(p)
	return err == nil && !info.IsDir
}

func (e *ESM) fsIsDir(p string) bool {
	info, err := e.rt.Filesystem().Stat(p)
	return err == nil && info.IsDir
}

// ---- classification -------------------------------------------------------

// stripFileURL turns a file:// URL into a plain filesystem path (leaving other
// urls and plain paths unchanged).
// Percent-escapes are decoded, so a URL Node built for a path with spaces (as in
// an ERR_MODULE_NOT_FOUND error's url) names that path again.
func stripFileURL(u string) string {
	if !strings.HasPrefix(u, "file://") {
		return u
	}
	if parsed, err := url.Parse(u); err == nil && (parsed.Host == "" || parsed.Host == "localhost") && parsed.Path != "" {
		return parsed.Path
	}
	return u[len("file://"):]
}

// urlPath reads a relative, absolute or file: specifier the way Node does, as a
// URL (relative ones against the directory dir, a file path): the
// percent-decoded file path, the URL's path still encoded (Node's error for an
// encoded separator names it), whether the specifier encodes a separator, and
// the query and fragment, which make a distinct module but aren't part of the
// file's path.
//
// malformed reports an escape that decodes to invalid UTF-8, which Node's
// decodeURIComponent refuses ("URI malformed").
func urlPath(spec, dir string) (path, encoded, suffix string, encodedSep, malformed bool) {
	rest := spec
	if strings.HasPrefix(rest, "file:") {
		// file:///p, file://localhost/p and file:/p all name /p. (A host other
		// than localhost is rejected before this; see fileURLHost.)
		rest = rest[len("file:"):]
		if strings.HasPrefix(rest, "//") {
			// The host ends at the path, query or fragment ("\\" is "/" here).
			rest = rest[2:]
			if i := strings.IndexAny(rest, "/\\?#"); i >= 0 {
				rest = rest[i:]
			} else {
				rest = ""
			}
		}
		// A file: URL is absolute: file:a/b names /a/b.
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
	}
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest, suffix = rest[:i], normalizeSuffix(rest[i:])
	}
	// In a file: URL's path a backslash is a separator, as the URL parser
	// reads it.
	rest = strings.ReplaceAll(rest, "\\", "/")
	encodedSep = hasEncodedSeparator(rest)
	// Only the specifier is URL-encoded; dir is already a file path.
	decoded := rest
	if d, err := url.PathUnescape(rest); err == nil {
		decoded = d
		malformed = !utf8.ValidString(d) && utf8.ValidString(rest)
	} else {
		// An escape that isn't "%" and two hex digits.
		malformed = true
	}
	if strings.HasPrefix(rest, "/") {
		return filepath.Clean(decoded), pathpkg.Clean(rest), suffix, encodedSep, malformed
	}
	return filepath.Join(dir, decoded), pathpkg.Join(escapePath(dir), rest), suffix, encodedSep, malformed
}

// validFileHost reports whether raw, a file: URL's authority as written, is a
// host the URL parser accepts there: its escapes decode to UTF-8, it holds no
// credentials or port (a decoded "@" or ":" too), no forbidden host character
// (space, "#", "%", "<", ">", "?", "[", "\\", "]", "^", "|", controls), and a
// bracketed one is an IPv6 address.
func validFileHost(raw string) bool {
	d, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(d) {
		return false
	}
	if strings.HasPrefix(raw, "[") {
		if !strings.HasSuffix(raw, "]") {
			return false
		}
		ip := net.ParseIP(raw[1 : len(raw)-1])
		return ip != nil && strings.Contains(raw, ":")
	}
	for i := 0; i < len(d); i++ {
		if c := d[i]; c <= ' ' || c == 0x7f || strings.IndexByte("#%/:<>?@[\\]^|", c) >= 0 {
			return false
		}
	}
	return true
}

// nodePlatform is process.platform for this OS.
func nodePlatform() string {
	if goruntime.GOOS == "windows" {
		return "win32"
	}
	return goruntime.GOOS
}

// fileURLHost returns the host of a file:// URL specifier, lowercased (the
// URL parser lowercases it), or "" when it has none.
func fileURLHost(spec string) string {
	if !strings.HasPrefix(spec, "file://") {
		return ""
	}
	rest := spec[len("file://"):]
	if i := strings.IndexAny(rest, "/\\?#"); i >= 0 {
		rest = rest[:i]
	}
	// The URL parser decodes a host's escapes (file://%6cocalhost is localhost).
	if d, err := url.PathUnescape(rest); err == nil {
		rest = d
	}
	return strings.ToLower(rest)
}

// escapePath is a file path as it appears in a file: URL.
// Like Node's pathToFileURL: "%", "\\" and control characters, spaces,
// non-ASCII bytes and the URL path's own reserved characters (" # < > ? ` { })
// are percent-encoded; everything else, "!" and "(" included, is kept.
func escapePath(p string) string {
	p = filepath.ToSlash(p)
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("%\\\"#<>?`{}", c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// normalizeSuffix serializes a URL's query and fragment the way the URL parser
// does (percent-encoding what it encodes, leaving existing escapes alone), so
// equivalent spellings such as "?a b" and "?a%20b" name the same module.
func normalizeSuffix(s string) string {
	query, fragment := s, ""
	if i := strings.IndexByte(s, '#'); i >= 0 {
		query, fragment = s[:i], s[i:]
	}
	// An empty query or fragment ("?", "#") names the same module as none.
	if query == "?" {
		query = ""
	}
	if fragment == "#" {
		fragment = ""
	}
	return percentEncode(query, "\"#<>'") + percentEncode(fragment, "\"<>`")
}

// percentEncode escapes controls, spaces, non-ASCII bytes and the given
// characters (the leading "?" or "#" is never among them).
func percentEncode(s, set string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || (i > 0 && strings.IndexByte(set, c) >= 0) {
			b.WriteString(fmt.Sprintf("%%%02X", c))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// urlWhitespace drops what the URL parser drops: tabs and newlines.
var urlWhitespace = strings.NewReplacer("\t", "", "\n", "", "\r", "")

// isURLSpecifier reports a specifier Node reads as a URL: relative, absolute or
// file:.
func isURLSpecifier(spec string) bool {
	return strings.HasPrefix(spec, "file:") || strings.HasPrefix(spec, "./") ||
		strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/")
}

// hasEncodedSeparator reports an escaped "/" or "\" in a URL path, which Node
// refuses to turn into a file path.
func hasEncodedSeparator(p string) bool {
	l := strings.ToLower(p)
	return strings.Contains(l, "%2f") || strings.Contains(l, "%5c")
}

// moduleKey is the stable module url for the file at path imported with a
// query or fragment: like Node, each distinct URL is its own module instance.
// The key is the URL path (percent-encoded where a file: URL encodes, such as
// spaces, "?", "#", "%" and controls like the \x01 module keys reserve) plus
// the suffix, so a query never looks like part of a file name and
// import.meta.url is the URL Node gives. A path a URL writes as is stays as is.
func (e *ESM) moduleKey(path, suffix string) string {
	if suffix == "" && escapePath(path) == path {
		return path
	}
	key := escapePath(path) + suffix
	if e.keyPaths == nil {
		e.keyPaths = map[string]string{}
	}
	e.keyPaths[key] = path
	return key
}

// pathOf is the file path behind a module url (dropping a query or fragment
// moduleKey added; decoding a file: URL).
func (e *ESM) pathOf(key string) string {
	if p, ok := e.keyPaths[key]; ok {
		return p
	}
	return stripFileURL(key)
}

// hookURLKey is the module url for a url a resolve hook returned: a file: URL
// becomes its path (keeping a query or fragment as a distinct module).
func (e *ESM) hookURLKey(u string) (string, error) {
	if !strings.HasPrefix(u, "file:") {
		return u, nil
	}
	if err := checkFileURLHost(u, u, ""); err != nil {
		return "", err
	}
	path, encoded, suffix, _, malformed := urlPath(u, "/")
	if malformed {
		return "", uriMalformed()
	}
	// Node turns a hook's url into a path with fileURLToPath, which refuses an
	// encoded "/".
	if strings.Contains(strings.ToLower(encoded), "%2f") {
		return "", &moduleError{
			msg:       "File URL path must not include encoded / characters",
			code:      "ERR_INVALID_FILE_URL_PATH",
			typeError: true,
		}
	}
	return e.moduleKey(path, suffix), nil
}

// checkFileURLHost is Node's error for a file: URL whose host is neither empty
// nor localhost.
// input is the specifier as given, which an ERR_INVALID_URL reports.
// module, when set, is the importer's URL, which Node also reports on the host
// error (its "input" being the URL itself).
func checkFileURLHost(u, input, module string) error {
	if strings.HasPrefix(u, "file://") {
		// A host whose escapes are malformed or not UTF-8 isn't a URL at all.
		raw := u[len("file://"):]
		if i := strings.IndexAny(raw, "/\\?#"); i >= 0 {
			raw = raw[:i]
		}
		if !validFileHost(raw) {
			return &moduleError{msg: "Invalid URL", code: "ERR_INVALID_URL", typeError: true, input: input}
		}
	}
	if host := fileURLHost(u); host != "" && host != "localhost" {
		return &moduleError{
			msg:       `File URL host must be "localhost" or empty on ` + nodePlatform(),
			code:      "ERR_INVALID_FILE_URL_HOST",
			typeError: true,
			input:     u,
			module:    module,
		}
	}
	return nil
}

func looksLikeESM(src string) bool {
	return esmSynRe.MatchString(blockCmt.ReplaceAllString(src, ""))
}

// nearestPackageType walks up from dir looking for the closest package.json and
// returns "module" or "commonjs" (Node's default when absent/unreadable).
func (e *ESM) nearestPackageType(dir string) string {
	for {
		pkg := filepath.Join(dir, "package.json")
		if e.fsIsFile(pkg) {
			if src, err := e.fsRead(pkg); err == nil {
				var m struct {
					Type string `json:"type"`
				}
				if json.Unmarshal([]byte(src), &m) == nil && m.Type == "module" {
					return "module"
				}
			}
			return "commonjs"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "commonjs"
		}
		dir = parent
	}
}

// ---- default resolution ---------------------------------------------------

// referrerDir returns the directory used to resolve relative/bare specifiers
// imported from referrer.
func (e *ESM) referrerDir(referrer string) string {
	if referrer == "" || strings.HasPrefix(referrer, "node:") || strings.HasPrefix(referrer, "native:") {
		return e.cwd()
	}
	r := e.pathOf(referrer)
	if strings.HasPrefix(r, "/") || winAbs(r) {
		return filepath.Dir(r)
	}
	return e.cwd()
}

func (e *ESM) cwd() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func winAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// resolveFile returns p if it is a file, else p with a probed extension.
func (e *ESM) resolveFile(p string) string {
	if e.fsIsFile(p) {
		return p
	}
	for _, ext := range fileExts {
		if e.fsIsFile(p + ext) {
			return p + ext
		}
	}
	return ""
}

func (e *ESM) resolveIndex(dir string) string {
	for _, idx := range indexFile {
		p := filepath.Join(dir, idx)
		if e.fsIsFile(p) {
			return p
		}
	}
	return ""
}

// resolveToURL implements Node's default resolution and returns a stable module
// url (an absolute path, or "node:<name>" for builtins).
func (e *ESM) resolveToURL(specifier, referrer string) (string, error) {
	// Builtins and native modules match the specifier exactly. After that Node
	// reads it as a URL (a package name too, when it looks the package up), and
	// the URL parser drops tabs and newlines wherever they appear.
	spec := specifier

	// Builtins (with or without node: prefix).
	if bare, ok := e.builtinBare(spec); ok {
		return "node:" + bare, nil
	}

	// Native Go modules registered via Runtime.RegisterNativeModule.
	nativeName := spec
	if strings.HasPrefix(nativeName, "native:") {
		nativeName = nativeName[len("native:"):]
	}
	if _, ok := e.rt.GetNativeModule(nativeName); ok {
		return "native:" + nativeName, nil
	}

	rawSpec := spec
	spec = urlWhitespace.Replace(spec)
	dir := e.referrerDir(referrer)
	var attempted, encoded, suffix string
	if isURLSpecifier(spec) {
		// The URL parser drops tabs and newlines wherever they appear.
		spec = urlWhitespace.Replace(spec)
	}
	switch {
	case isURLSpecifier(spec):
		if err := checkFileURLHost(spec, rawSpec, e.importerURL(referrer)); err != nil {
			return "", err
		}
		var encodedSep, malformed bool
		attempted, encoded, suffix, encodedSep, malformed = urlPath(spec, dir)
		if encodedSep {
			return "", e.encodedSeparator(encoded, referrer)
		}
		if malformed {
			return "", uriMalformed()
		}
	case winAbs(spec):
		attempted = spec
	default:
		abs, suffix, err := e.resolveBare(spec, rawSpec, dir, referrer)
		if err != nil {
			return "", err
		}
		return e.moduleKey(abs, suffix), nil
	}
	abs := e.resolveFile(attempted)
	if abs == "" && e.fsIsDir(attempted) {
		abs = e.resolveIndex(attempted)
	}
	if abs == "" {
		err := e.moduleNotFound(attempted, referrer)
		err.url += suffix
		return "", err
	}
	return e.moduleKey(abs, suffix), nil
}

// moduleError is a module-loading failure with a Node error code, thrown into
// the importing realm as an Error carrying that code (and the url Node reports
// for a missing module), so code checking err.code works as it does on Node.
type moduleError struct {
	msg  string
	code string
	// typeError makes it a TypeError, as some of Node's are.
	typeError bool
	// uriError makes it a URIError, Node's error for a malformed escape.
	uriError bool
	// input is the string ERR_INVALID_URL couldn't parse, its "input" property.
	input string
	// module is the importer's URL on an ERR_INVALID_FILE_URL_HOST.
	module string
	// hookPayload, when set, describes an error a loader hook threw (see
	// __describeRejection), which the importer gets rebuilt as it was.
	hookPayload string
	// url is the file: URL of the missing module, empty when Node reports none
	// (a missing package).
	url string
}

func (m *moduleError) Error() string { return m.msg }

// kind is the error's class name.
func (m *moduleError) kind() string {
	switch {
	case m.uriError:
		return "URIError"
	case m.typeError:
		return "TypeError"
	}
	return "Error"
}

// encodedSeparator is Node's error for a module URL path that encodes "/" or
// "\"; encoded is that path.
func (e *ESM) encodedSeparator(encoded, referrer string) *moduleError {
	return &moduleError{
		msg: `Invalid module "` + filepath.ToSlash(encoded) + `" must not include encoded "/" or "\" characters imported from ` +
			e.importerPath(referrer),
		code:      "ERR_INVALID_MODULE_SPECIFIER",
		typeError: true,
	}
}

// validPackageTarget reports whether an "exports" target is one Node accepts:
// "./" followed by segments none of which is ".", "..", or node_modules (in
// any case, percent-encoded or not).
func validPackageTarget(target string) bool {
	if !strings.HasPrefix(target, "./") {
		return false
	}
	// The path only: a query or fragment isn't part of it.
	path := target[2:]
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for _, seg := range strings.Split(strings.ReplaceAll(path, "\\", "/"), "/") {
		if d, err := url.PathUnescape(seg); err == nil {
			seg = d
		}
		// An empty segment ("dist//x") is deprecated in Node but allowed.
		switch strings.ToLower(seg) {
		case ".", "..", "node_modules":
			return false
		}
	}
	return true
}

// invalidPackageTarget is Node's ERR_INVALID_PACKAGE_TARGET.
func (e *ESM) invalidPackageTarget(target, sub, pkgJSON, referrer string) *moduleError {
	where := `main target "` + target + `" defined`
	if sub != "." {
		where = `target "` + target + `" defined for '` + sub + `'`
	}
	return &moduleError{
		msg:  `Invalid "exports" ` + where + ` in the package config ` + pkgJSON + ` imported from ` + e.importerPath(referrer),
		code: "ERR_INVALID_PACKAGE_TARGET",
	}
}

// uriMalformed is decodeURIComponent's error for an escape that isn't UTF-8.
func uriMalformed() *moduleError {
	return &moduleError{msg: "URI malformed", uriError: true}
}

// moduleNotFound is Node's error for a module file that does not exist.
func (e *ESM) moduleNotFound(path, referrer string) *moduleError {
	return &moduleError{
		msg:  "Cannot find module '" + path + "' imported from " + e.importerPath(referrer),
		code: "ERR_MODULE_NOT_FOUND",
		url:  fileURL(path),
	}
}

// packageNotFound is Node's error for a package that is not installed, or one
// whose main entry does not exist (Node then names that entry's path).
func (e *ESM) packageNotFound(name, referrer string) *moduleError {
	return &moduleError{
		msg:  "Cannot find package '" + name + "' imported from " + e.importerPath(referrer),
		code: "ERR_MODULE_NOT_FOUND",
	}
}

// importerPath is how Node names the importer in a resolution error: its path,
// or the working directory (with a trailing separator) for an entry point or an
// --import preload.
func (e *ESM) importerPath(referrer string) string {
	if referrer == "" {
		return strings.TrimSuffix(e.cwd(), string(filepath.Separator)) + string(filepath.Separator)
	}
	return e.pathOf(referrer)
}

// importerURL is the importer's URL as Node names it on a resolution error:
// its module URL, or the working directory's for an entry point.
func (e *ESM) importerURL(referrer string) string {
	if referrer == "" {
		return fileURL(strings.TrimSuffix(e.cwd(), "/") + "/")
	}
	if urlSchemeRe.MatchString(referrer) {
		return referrer
	}
	if p, ok := e.keyPaths[referrer]; ok && p != referrer {
		return "file://" + referrer
	}
	return fileURL(referrer)
}

// urlSchemeRe matches a string that starts with a URL scheme.
var urlSchemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// fileURL is the file: URL for an absolute path, as Node's pathToFileURL gives it.
func fileURL(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + escapePath(p)
}

// ---- bare / package.json "exports" resolution -----------------------------

func splitBare(specifier string) (pkg, sub string) {
	if strings.HasPrefix(specifier, "@") {
		parts := strings.Split(specifier, "/")
		pkg = strings.Join(parts[:min(2, len(parts))], "/")
		rest := ""
		if len(parts) > 2 {
			rest = strings.Join(parts[2:], "/")
		}
		if rest != "" {
			return pkg, "./" + rest
		}
		return pkg, "."
	}
	idx := strings.Index(specifier, "/")
	if idx == -1 {
		return specifier, "."
	}
	return specifier[:idx], "./" + specifier[idx+1:]
}

// resolveBare resolves a package specifier the way Node does: the first
// node_modules directory (walking up from fromDir) that contains the package
// decides the result, and a missing package or entry is an error.
// It also returns the query or fragment the module's url keeps (see resolvePackage).
// rawSpecifier is the specifier before tabs and newlines were dropped, whose
// package name Node's error names.
func (e *ESM) resolveBare(specifier, rawSpecifier, fromDir, referrer string) (string, string, error) {
	pkg, sub := splitBare(specifier)
	dir := fromDir
	for {
		pkgDir := filepath.Join(dir, "node_modules", pkg)
		if e.fsIsDir(pkgDir) {
			return e.resolvePackage(pkgDir, sub, referrer)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	rawPkg, _ := splitBare(rawSpecifier)
	return "", "", e.packageNotFound(rawPkg, referrer)
}

// resolvePackage resolves sub (".", or "./path") inside the package at pkgDir.
// Like Node, an "exports" map is matched against the whole subpath, query and
// fragment included; otherwise the subpath is read as a URL path, whose query
// or fragment is returned to stay on the module's url.
func (e *ESM) resolvePackage(pkgDir, sub, referrer string) (string, string, error) {
	main := ""
	pkgJSON := filepath.Join(pkgDir, "package.json")
	if e.fsIsFile(pkgJSON) {
		if src, err := e.fsRead(pkgJSON); err == nil {
			var pkg map[string]json.RawMessage
			if json.Unmarshal([]byte(src), &pkg) == nil {
				if raw, ok := pkg["exports"]; ok {
					var exp interface{}
					if json.Unmarshal(raw, &exp) == nil {
						if target, found := resolveExports(exp, sub); found {
							if !validPackageTarget(target) {
								return "", "", e.invalidPackageTarget(target, sub, pkgJSON, referrer)
							}
							// The target is a URL relative to the package: its
							// path is decoded and a query or fragment kept.
							abs, encoded, suffix, encodedSep, malformed := urlPath(target, pkgDir)
							if encodedSep {
								return "", "", e.encodedSeparator(encoded, referrer)
							}
							if malformed {
								return "", "", uriMalformed()
							}
							if r := e.resolveFile(abs); r != "" {
								return r, suffix, nil
							}
							if e.fsIsDir(abs) {
								if r := e.resolveIndex(abs); r != "" {
									return r, suffix, nil
								}
							}
							err := e.moduleNotFound(abs, referrer)
							err.url += suffix
							return "", "", err
						}
					}
				}
				if sub == "." {
					main = jsonString(pkg["module"])
					if main == "" {
						main = jsonString(pkg["main"])
					}
					if main != "" {
						abs := filepath.Join(pkgDir, main)
						if r := e.resolveFile(abs); r != "" {
							return r, "", nil
						}
						if e.fsIsDir(abs) {
							if r := e.resolveIndex(abs); r != "" {
								return r, "", nil
							}
						}
					}
				}
			}
		}
	}
	target, suffix := pkgDir, ""
	if sub != "." {
		// As a URL path relative to the package: decoded, with the suffix kept
		// apart, and refused if it encodes a separator.
		path, encoded, sfx, encodedSep, malformed := urlPath(sub, pkgDir)
		if encodedSep {
			return "", "", e.encodedSeparator(encoded, referrer)
		}
		if malformed {
			return "", "", uriMalformed()
		}
		target, suffix = path, sfx
	}
	if r := e.resolveFile(target); r != "" {
		return r, suffix, nil
	}
	if e.fsIsDir(target) {
		if r := e.resolveIndex(target); r != "" {
			return r, suffix, nil
		}
	}
	if sub != "." {
		err := e.moduleNotFound(target, referrer)
		err.url += suffix
		return "", "", err
	}
	// Node names the package's main entry, or its index.js when it has none.
	if main == "" {
		main = "index.js"
	}
	return "", "", e.packageNotFound(filepath.Join(pkgDir, main), referrer)
}

func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// resolveExportsTarget picks a concrete target string from an "exports" value,
// honoring condition objects and arrays of alternatives. ok reports that a
// target was found, which may be invalid (even ""), for resolvePackage to
// refuse as Node does.
func resolveExportsTarget(exp interface{}) (target string, ok bool) {
	switch v := exp.(type) {
	case string:
		return v, true
	case []interface{}:
		// Alternatives: an invalid target is skipped for the next one, as in Node.
		// With no valid one left, the last invalid one is returned, for its error.
		last, sawInvalid := "", false
		for _, e := range v {
			r, ok := resolveExportsTarget(e)
			if !ok {
				continue
			}
			if validPackageTarget(r) {
				return r, true
			}
			last, sawInvalid = r, true
		}
		return last, sawInvalid
	case map[string]interface{}:
		for _, c := range exportConditions {
			if sub, ok := v[c]; ok {
				if r, ok := resolveExportsTarget(sub); ok {
					return r, true
				}
			}
		}
	}
	return "", false
}

// resolveExports finds the target the "exports" value maps sub to; ok as in
// resolveExportsTarget.
func resolveExports(exports interface{}, sub string) (target string, ok bool) {
	// exports may be the "." target directly (string/array/conditions with no
	// subpath keys), or a subpath map.
	if m, ok := exports.(map[string]interface{}); ok && hasSubpathKeys(m) {
		if t, ok := m[sub]; ok {
			return resolveExportsTarget(t)
		}
		// wildcard patterns: "./*": "./dist/*.js"
		for key, val := range m {
			if strings.Contains(key, "*") {
				parts := strings.SplitN(key, "*", 2)
				pre, post := parts[0], parts[1]
				if strings.HasPrefix(sub, pre) && strings.HasSuffix(sub, post) {
					mid := sub[len(pre) : len(sub)-len(post)]
					if tgt, ok := resolveExportsTarget(val); ok {
						return strings.Replace(tgt, "*", mid, 1), true
					}
				}
			}
		}
		return "", false
	}
	if sub == "." {
		return resolveExportsTarget(exports)
	}
	return "", false
}

func hasSubpathKeys(m map[string]interface{}) bool {
	for k := range m {
		if k == "." || strings.HasPrefix(k, "./") {
			return true
		}
	}
	return false
}

// ---- default load ---------------------------------------------------------

// loadURL reads a resolved url and returns its raw source + Node "format"
// ("builtin"|"json"|"commonjs"|"module").
func (e *ESM) loadURL(url string) (source, format string, err error) {
	if strings.HasPrefix(url, "node:") || strings.HasPrefix(url, "native:") {
		return "", "builtin", nil
	}
	url = e.pathOf(url)
	switch {
	case strings.HasSuffix(url, ".json"):
		src, err := e.fsRead(url)
		return src, "json", err
	case strings.HasSuffix(url, ".mjs"), strings.HasSuffix(url, ".mts"):
		src, err := e.fsRead(url)
		return src, "module", err
	case strings.HasSuffix(url, ".cjs"), strings.HasSuffix(url, ".cts"):
		src, err := e.fsRead(url)
		return src, "commonjs", err
	}
	src, err := e.fsRead(url)
	if err != nil {
		return "", "", err
	}
	// .ts follows nearest package type; .js decides by package type then syntax.
	if strings.HasSuffix(url, ".ts") {
		if e.nearestPackageType(filepath.Dir(url)) == "module" || looksLikeESM(src) {
			return src, "module", nil
		}
		return src, "commonjs", nil
	}
	if e.nearestPackageType(filepath.Dir(url)) == "module" {
		return src, "module", nil
	}
	if looksLikeESM(src) {
		return src, "module", nil
	}
	return src, "commonjs", nil
}

// ---- finalize (format -> compilable ESM source) ---------------------------

// finalize turns a loaded (url, source, format) into the ESM source string V8
// compiles. CommonJS/builtins become a synthetic ESM module that re-exports the
// live require()'d object's own keys by name.
func (e *ESM) finalize(url, source, format string) (string, error) {
	switch format {
	case "builtin":
		bare := strings.TrimPrefix(url, "node:")
		bare = strings.TrimPrefix(bare, "native:")
		return e.cjsWrapper(bare)
	case "commonjs":
		// A query or fragment makes another wrapper around the same CommonJS
		// module, as in Node.
		return e.cjsModuleWrapper(e.pathOf(url), source), nil
	case "json":
		return "export default " + source + ";\n", nil
	default: // "module"
		return source, nil
	}
}

// cjsWrapper builds a synthetic ESM module wrapping a builtin, whose named
// exports are its exports object's keys. reqArg is its name.
func (e *ESM) cjsWrapper(reqArg string) (string, error) {
	keys, err := e.requireKeys(reqArg)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("const __m = require(")
	b.WriteString(jsQuote(reqArg))
	b.WriteString(");\n")
	// Node interop: default is module.exports (or its .default for __esModule).
	b.WriteString("export default (__m && __m.__esModule && \"default\" in __m) ? __m.default : __m;\n")
	for _, k := range keys {
		b.WriteString("export const ")
		b.WriteString(k)
		b.WriteString(" = __m[")
		b.WriteString(jsQuote(k))
		b.WriteString("];\n")
	}
	return b.String(), nil
}

func jsQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---- combined default (fast path, no hooks) -------------------------------

// resolveAndLoadDefault is the no-hooks path: resolve, load, finalize.
func (e *ESM) resolveAndLoadDefault(specifier, referrer string) (esmSource, url string, err error) {
	url, err = e.resolveToURL(specifier, referrer)
	if err != nil {
		return "", "", err
	}
	src, format, err := e.loadURL(url)
	if err != nil {
		return "", "", err
	}
	final, err := e.finalize(url, src, format)
	if err != nil {
		return "", "", err
	}
	return final, url, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
