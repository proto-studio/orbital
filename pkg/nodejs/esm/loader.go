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
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"proto.zip/studio/orbital/pkg/v8"
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
//
// lexical is the decoded path as the URL has it, dot segments resolved but
// repeated separators kept, which Node's error for a missing module names;
// path is that, cleaned, for the filesystem.
func urlPath(spec, dir string) (path, lexical, encoded, suffix string, encodedSep, malformed bool) {
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
	// encoded is the URL's path as the URL parser serializes it: escapes kept
	// as written, what the path percent-encode set covers encoded, dot segments
	// (encoded ones too) resolved and repeated separators kept.
	if strings.HasPrefix(rest, "/") {
		lexical = removeDotSegments(decoded, false)
		encoded = removeDotSegments(encodeURLPath(rest), true)
		return filepath.Clean(lexical), lexical, encoded, suffix, encodedSep, malformed
	}
	lexical = removeDotSegments(strings.TrimSuffix(filepath.ToSlash(dir), "/")+"/"+decoded, false)
	encoded = removeDotSegments(strings.TrimSuffix(escapePath(dir), "/")+"/"+encodeURLPath(rest), true)
	return filepath.Clean(lexical), lexical, encoded, suffix, encodedSep, malformed
}

// encodeURLPath percent-encodes what the URL parser encodes in a path:
// controls, space, non-ASCII bytes and " < > ` { }. Escapes stay as written.
func encodeURLPath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte("\"<>`{}", c) >= 0 {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// removeDotSegments resolves the "." and ".." segments of p, an absolute URL
// path, as the URL parser does: ".." drops the segment before it, even an
// empty one, and empty segments (repeated separators) are otherwise kept. For
// an encoded path, "%2e" is a "." too.
func removeDotSegments(p string, encoded bool) string {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	out := make([]string, 0, len(segs))
	for i, s := range segs {
		last := i == len(segs)-1
		if encoded {
			s2 := strings.ReplaceAll(strings.ReplaceAll(s, "%2e", "."), "%2E", ".")
			if s2 == "." || s2 == ".." {
				s = s2
			}
		}
		switch s {
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		case ".":
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, s)
		}
	}
	return "/" + strings.Join(out, "/")
}

// validFileHost reports whether raw, a file: URL's authority as written, is a
// host the URL parser accepts there (see parseFileHost).
func validFileHost(raw string) bool {
	_, ok := parseFileHost(raw)
	return ok
}

// hostProfile is the URL standard's domain-to-ASCII: UTS 46 with
// nontransitional mapping, without STD3 rules, hyphen checks or DNS lengths.
var hostProfile = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.StrictDomainName(false),
	idna.CheckHyphens(false), idna.CheckJoiners(true), idna.BidiRule(), idna.VerifyDNSLength(false))

// parseFileHost reads raw, a file: URL's authority as written, as the URL
// parser reads a host, and returns it serialized: a bracketed IPv6 address
// compressed, otherwise the escapes decoded and the domain mapped to ASCII
// (lowercase, punycode), which may not hold credentials, a port or another
// forbidden character, and which, ending in a number, must be an IPv4 address
// (then given in dotted form). It reports false when the URL parser would fail.
func parseFileHost(raw string) (string, bool) {
	if strings.HasPrefix(raw, "[") {
		if !strings.HasSuffix(raw, "]") || !strings.Contains(raw, ":") {
			return "", false
		}
		ip := net.ParseIP(raw[1 : len(raw)-1]).To16()
		if ip == nil {
			return "", false
		}
		return "[" + ipv6String(ip) + "]", true
	}
	d, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(d) {
		return "", false
	}
	host, err := hostProfile.ToASCII(d)
	if err != nil {
		return "", false
	}
	for i := 0; i < len(host); i++ {
		if c := host[i]; c <= ' ' || c == 0x7f || strings.IndexByte("#%/:<>?@[\\]^|", c) >= 0 {
			return "", false
		}
	}
	// A host ending in a number is an IPv4 address, and must parse as one.
	if addr, numeric, ok := ipv4Host(host); numeric {
		return addr, ok
	}
	return host, true
}

// ipv6String serializes a 16-byte address as the URL parser does: lowercase
// hex pieces, the first longest run of two or more zero pieces as "::" (an
// embedded IPv4 address included, unlike net.IP's String).
func ipv6String(ip net.IP) string {
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(ip[2*i])<<8 | uint16(ip[2*i+1])
	}
	start, length := -1, 1
	for i := 0; i < 8; {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && pieces[j] == 0 {
			j++
		}
		if j-i > length {
			start, length = i, j-i
		}
		i = j
	}
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == start {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteByte(':')
			}
			i += length - 1
			continue
		}
		fmt.Fprintf(&b, "%x", pieces[i])
		if i < 7 {
			b.WriteByte(':')
		}
	}
	return b.String()
}

// ipv4Host reads host as the URL parser does when it ends in a number (its last
// dot-separated label, a trailing dot aside, is decimal digits or a 0x hex
// number): as an IPv4 address of one to four decimal, octal (leading 0) or hex
// (0x) parts, the last filling the remaining bytes. It returns the address in
// dotted form, whether host is numeric at all, and whether it parsed.
func ipv4Host(host string) (addr string, numeric, ok bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return "", false, false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last == "" {
		return "", false, false
	}
	if strings.Trim(last, "0123456789") != "" {
		if _, valid := ipv4Number(last); !valid {
			return "", false, false
		}
	}
	if len(parts) > 4 {
		return "", true, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, valid := ipv4Number(p)
		if !valid {
			return "", true, false
		}
		nums[i] = n
	}
	for _, n := range nums[:len(nums)-1] {
		if n > 255 {
			return "", true, false
		}
	}
	if nums[len(nums)-1] >= 1<<(8*(5-len(nums))) {
		return "", true, false
	}
	v := nums[len(nums)-1]
	for i, n := range nums[:len(nums)-1] {
		v += n << (8 * (3 - i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", v>>24, v>>16&255, v>>8&255, v&255), true, true
}

// ipv4Number parses one part of an IPv4 host: decimal, octal after a leading
// "0", or hex after "0x" ("0x" alone is 0). Values past 2^32 are capped there,
// which the range checks refuse all the same.
func ipv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base := uint64(10)
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s, base = s[2:], 16
	} else if len(s) >= 2 && s[0] == '0' {
		s, base = s[1:], 8
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, false
		}
		if d >= base {
			return 0, false
		}
		if n = n*base + d; n > 1<<32 {
			n = 1 << 32
		}
	}
	return n, true
}

// nodePlatform is process.platform for this OS.
func nodePlatform() string {
	if goruntime.GOOS == "windows" {
		return "win32"
	}
	return goruntime.GOOS
}

// canonicalFileURL reads s as the URL parser reads a file: URL: edge controls
// and spaces trimmed, tabs and newlines dropped, the scheme in any case, a
// backslash before the query or fragment a "/", and file:p, file:/p and
// file:///p alike. It returns "file://<host><path><query/fragment>", and false
// when s isn't a file: URL.
func canonicalFileURL(s string) (string, bool) {
	t := strings.TrimFunc(s, func(r rune) bool { return r <= ' ' })
	t = urlWhitespace.Replace(t)
	if len(t) < 5 || !strings.EqualFold(t[:5], "file:") {
		return "", false
	}
	rest, suffix := t[5:], ""
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest, suffix = rest[:i], rest[i:]
	}
	rest = strings.ReplaceAll(rest, "\\", "/")
	host := ""
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			host, rest = rest[:i], rest[i:]
		} else {
			host, rest = rest, ""
		}
		// A drive letter where the host goes ("file://C:/p") starts the path.
		if isDriveLetter(host) {
			host, rest = "", "/"+host+rest
		}
	}
	if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	// A drive letter opening a file URL's path is written with ":" ("C|" too).
	if len(rest) >= 3 && isDriveLetter(rest[1:3]) && (len(rest) == 3 || rest[3] == '/') {
		rest = "/" + rest[1:2] + ":" + rest[3:]
	}
	return "file://" + host + rest + suffix, true
}

// isDriveLetter reports whether s is a Windows drive letter as the URL parser
// knows one: an ASCII letter and ":" or "|".
func isDriveLetter(s string) bool {
	return len(s) == 2 && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z') && (s[1] == ':' || s[1] == '|')
}

// fileURLHost returns the host of a file:// URL specifier as the URL parser
// serializes it (see parseFileHost), or "" when it has none. A host it can't
// parse comes back lowercased.
func fileURLHost(spec string) string {
	if !strings.HasPrefix(spec, "file://") {
		return ""
	}
	rest := spec[len("file://"):]
	if i := strings.IndexAny(rest, "/\\?#"); i >= 0 {
		rest = rest[:i]
	}
	if host, ok := parseFileHost(rest); ok {
		return host
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
func (e *ESM) hookURLKey(raw string) (string, error) {
	// The loader's own keys for builtins and native modules stay as they are.
	if strings.HasPrefix(raw, "node:") || strings.HasPrefix(raw, "native:") {
		return raw, nil
	}
	u, isFile := canonicalFileURL(raw)
	if !isFile {
		// Serialized as the URL parser would (controls and non-ASCII escaped),
		// which also keeps the \x01 typed keys use out of it.
		return encodeOpaque(urlWhitespace.Replace(raw)), nil
	}
	if err := checkFileURLHost(u, raw, ""); err != nil {
		return "", err
	}
	path, _, encoded, suffix, _, malformed := urlPath(u, "/")
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

// encodeOpaque percent-encodes controls and non-ASCII bytes, as the URL parser
// does in a URL with an opaque path (mem:a b stays mem:a b).
func encodeOpaque(u string) string {
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		if c := u[i]; c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
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
		// The error's input is the URL as parsed: host, path (escaped, dot
		// segments resolved), query and fragment serialized.
		rest := u[len("file://"):]
		if i := strings.IndexAny(rest, "/\\?#"); i >= 0 {
			rest = rest[i:]
		} else {
			rest = ""
		}
		path, query, fragment := rest, "", ""
		if i := strings.IndexByte(path, '#'); i >= 0 {
			path, fragment = path[:i], path[i:]
		}
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path, query = path[:i], path[i:]
		}
		path = strings.ReplaceAll(path, "\\", "/")
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		u = "file://" + host + removeDotSegments(encodeURLPath(path), true) +
			percentEncode(query, "\"#<>'") + percentEncode(fragment, "\"<>`")
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
	dir := e.referrerDir(referrer)
	var attempted, lexical, encoded, suffix string
	// A relative or absolute path is told from its raw spelling (a leading tab
	// makes it a bare name, as in Node); a file: URL as the URL parser reads it.
	// "//host/p" is a network-path reference: against a file: base it names the
	// file URL with that host. In a special URL "\\" reads as "/", so "/\\host/p"
	// is one too.
	urlSpec := spec
	if strings.HasPrefix(rawSpec, "//") || strings.HasPrefix(rawSpec, "/\\") {
		urlSpec = "file:" + spec
	}
	canon, isFile := canonicalFileURL(urlSpec)
	pathLike := strings.HasPrefix(rawSpec, "./") || strings.HasPrefix(rawSpec, "../") || strings.HasPrefix(rawSpec, "/")
	if isFile {
		spec = canon
	} else {
		spec = urlWhitespace.Replace(spec)
	}
	switch {
	case pathLike || isFile:
		if err := checkFileURLHost(spec, rawSpec, e.importerURL(referrer)); err != nil {
			return "", err
		}
		var encodedSep, malformed bool
		attempted, lexical, encoded, suffix, encodedSep, malformed = urlPath(spec, dir)
		if encodedSep {
			return "", e.encodedSeparator(encoded, referrer)
		}
		if malformed {
			return "", uriMalformed()
		}
	case winAbs(spec):
		attempted, lexical, encoded = spec, spec, strings.TrimPrefix(fileURL(spec), "file://")
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
		err := e.moduleNotFound(lexical, encoded, referrer)
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
	// hookValue, when set, is a non-error value a loader hook threw, serialized
	// with V8's ValueSerializer for the importer to get a structured clone.
	hookValue []byte
	// hookProps is a hook error's own enumerable properties and own cause,
	// serialized with V8's ValueSerializer (see __rejectionProps).
	hookProps []byte
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

// moduleNotFound is Node's error for a module file that does not exist: its
// message names the file path, its url the URL path as resolved (see urlPath).
func (e *ESM) moduleNotFound(path, encoded, referrer string) *moduleError {
	return &moduleError{
		msg:  "Cannot find module '" + path + "' imported from " + e.importerPath(referrer),
		code: "ERR_MODULE_NOT_FOUND",
		url:  "file://" + encoded,
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

// validPackageName reports whether a bare specifier's package name is one
// Node's parsePackageName accepts: a scope ("@scope") needs a name after it,
// and the name can't start with "." or hold "%" or "\\".
func validPackageName(specifier string) bool {
	sep := strings.IndexByte(specifier, '/')
	if strings.HasPrefix(specifier, "@") {
		if sep == -1 {
			return false
		}
		if next := strings.IndexByte(specifier[sep+1:], '/'); next >= 0 {
			sep += 1 + next
		} else {
			sep = -1
		}
	}
	name := specifier
	if sep >= 0 {
		name = specifier[:sep]
	}
	return !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "%\\")
}

// resolveBare resolves a package specifier the way Node does: the first
// node_modules directory (walking up from fromDir) that contains the package
// decides the result, and a missing package or entry is an error.
// It also returns the query or fragment the module's url keeps (see resolvePackage).
// rawSpecifier is the specifier before tabs and newlines were dropped, whose
// package name Node's error names.
func (e *ESM) resolveBare(specifier, rawSpecifier, fromDir, referrer string) (string, string, error) {
	if !validPackageName(rawSpecifier) {
		return "", "", &moduleError{
			msg:       `Invalid module "` + rawSpecifier + `" is not a valid package name imported from ` + e.importerPath(referrer),
			code:      "ERR_INVALID_MODULE_SPECIFIER",
			typeError: true,
		}
	}
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
							abs, lexical, encoded, suffix, encodedSep, malformed := urlPath(target, pkgDir)
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
							err := e.moduleNotFound(lexical, encoded, referrer)
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
	target, lexical, encodedTarget, suffix := pkgDir, pkgDir, "", ""
	if sub != "." {
		// As a URL path relative to the package: decoded, with the suffix kept
		// apart, and refused if it encodes a separator.
		path, lex, encoded, sfx, encodedSep, malformed := urlPath(sub, pkgDir)
		if encodedSep {
			return "", "", e.encodedSeparator(encoded, referrer)
		}
		if malformed {
			return "", "", uriMalformed()
		}
		target, lexical, encodedTarget, suffix = path, lex, encoded, sfx
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
		err := e.moduleNotFound(lexical, encodedTarget, referrer)
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
	t, kind := exportsTarget(exp)
	return t, kind == targetFound
}

// What an "exports" value resolves to: nothing, a target, or null (explicitly
// not exported), which Node keeps apart in arrays of alternatives.
const (
	targetAbsent = iota
	targetFound
	targetNull
)

func exportsTarget(exp interface{}) (string, int) {
	switch v := exp.(type) {
	case nil:
		return "", targetNull
	case string:
		return v, targetFound
	case []interface{}:
		// No alternatives at all is null, as in Node.
		if len(v) == 0 {
			return "", targetNull
		}
		// Alternatives: an invalid target is skipped for the next one, as in
		// Node; a null (even nested, or an empty array) clears an earlier
		// invalid one. With no valid one left, the last invalid one is returned,
		// for its error.
		last, sawInvalid, sawNull := "", false, false
		for _, e := range v {
			r, kind := exportsTarget(e)
			switch kind {
			case targetNull:
				last, sawInvalid, sawNull = "", false, true
			case targetFound:
				if validPackageTarget(r) {
					return r, targetFound
				}
				last, sawInvalid = r, true
			}
		}
		if sawInvalid {
			return last, targetFound
		}
		if sawNull {
			return "", targetNull
		}
	case map[string]interface{}:
		for _, c := range exportConditions {
			if sub, ok := v[c]; ok {
				if r, kind := exportsTarget(sub); kind != targetAbsent {
					return r, kind
				}
			}
		}
	}
	return "", targetAbsent
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

// resolveAndLoadDefault is the no-hooks path: resolve, load, check the import's
// attributes (attrs is nil for an entry point, which has none to check), finalize.
func (e *ESM) resolveAndLoadDefault(specifier, referrer string, attrs []v8.ImportAttribute) (esmSource, url string, err error) {
	url, err = e.resolveToURL(specifier, referrer)
	if err != nil {
		return "", "", err
	}
	src, format, err := e.loadURL(url)
	if err != nil {
		return "", "", err
	}
	if attrs != nil {
		if err := e.checkAttributesOnce(url, format, attrs); err != nil {
			return "", "", err
		}
	} else if _, seen := e.attrChecks[attrCheckKey(url, nil)]; !seen {
		// An entry point is its module's implicit-type job, which a later
		// import of it (even with type 'javascript') reuses, as in Node.
		e.recordAttrCheck(attrCheckKey(url, nil), nil)
	}
	final, err := e.finalize(url, src, format)
	if err != nil {
		return "", "", err
	}
	return final, e.typedKey(url, format, attrs), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
