package esm

// Go-native ES module resolution + loading.
//
// This is the DEFAULT loader (the innermost `nextResolve`/`nextLoad` in Node's
// hook terminology). It runs entirely in Go: filesystem access goes through the
// runtime's (sandboxed) Filesystem, package.json is parsed with encoding/json,
// and CJS/ESM classification is done here. The only time it calls into JS is to
// enumerate a CommonJS module's live export names (which requires the runtime's
// real `require`) so the synthesized ESM wrapper can re-export them by name.
//
// Registered loader hooks (module.register) wrap these defaults; see hooks.go.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
func stripFileURL(u string) string {
	if strings.HasPrefix(u, "file://") {
		return u[len("file://"):]
	}
	return u
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
	r := referrer
	if strings.HasPrefix(r, "file://") {
		r = r[len("file://"):]
	}
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

// attemptedPath returns the path a relative/absolute specifier *would* resolve
// to (by simple join, without existence/extension probing). It is reported as
// the not-found error's `url` so resolve hooks can rewrite the extension and
// retry — e.g. ts-blank-space mapping a missing "./x.js" to "./x.ts".
func (e *ESM) attemptedPath(specifier, referrer string) string {
	spec := stripFileURL(specifier)
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		return filepath.Join(e.referrerDir(referrer), spec)
	case strings.HasPrefix(spec, "/") || winAbs(spec):
		return spec
	default:
		return ""
	}
}

// resolveToURL implements Node's default resolution and returns a stable module
// url (an absolute path, or "node:<name>" for builtins).
func (e *ESM) resolveToURL(specifier, referrer string) (string, error) {
	spec := specifier
	if strings.HasPrefix(spec, "file://") {
		spec = spec[len("file://"):]
	}

	// Builtins (with or without node: prefix).
	if bare, ok := e.builtinBare(spec); ok {
		return "node:" + bare, nil
	}

	dir := e.referrerDir(referrer)
	var abs string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		joined := filepath.Join(dir, spec)
		abs = e.resolveFile(joined)
		if abs == "" && e.fsIsDir(joined) {
			abs = e.resolveIndex(joined)
		}
	case strings.HasPrefix(spec, "/") || winAbs(spec):
		abs = e.resolveFile(spec)
		if abs == "" && e.fsIsDir(spec) {
			abs = e.resolveIndex(spec)
		}
	default:
		abs = e.resolveBare(spec, dir)
	}

	if abs == "" {
		err := errors.New("Cannot find module '" + specifier + "' imported from " + orEntry(referrer))
		return "", &notFoundError{msg: err.Error()}
	}
	return abs, nil
}

func orEntry(s string) string {
	if s == "" {
		return "<entry>"
	}
	return s
}

// notFoundError carries the ERR_MODULE_NOT_FOUND-style code so resolve hooks
// (e.g. ts-blank-space) can catch it and retry with a different extension.
type notFoundError struct{ msg string }

func (n *notFoundError) Error() string { return n.msg }

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

func (e *ESM) resolveBare(specifier, fromDir string) string {
	pkg, sub := splitBare(specifier)
	dir := fromDir
	for {
		pkgDir := filepath.Join(dir, "node_modules", pkg)
		if e.fsIsDir(pkgDir) {
			if r := e.resolvePackage(pkgDir, sub); r != "" {
				return r
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func (e *ESM) resolvePackage(pkgDir, sub string) string {
	pkgJSON := filepath.Join(pkgDir, "package.json")
	if e.fsIsFile(pkgJSON) {
		if src, err := e.fsRead(pkgJSON); err == nil {
			var pkg map[string]json.RawMessage
			if json.Unmarshal([]byte(src), &pkg) == nil {
				if raw, ok := pkg["exports"]; ok {
					var exp interface{}
					if json.Unmarshal(raw, &exp) == nil {
						if target := resolveExports(exp, sub); target != "" {
							abs := filepath.Join(pkgDir, target)
							if r := e.resolveFile(abs); r != "" {
								return r
							}
							if e.fsIsDir(abs) {
								if r := e.resolveIndex(abs); r != "" {
									return r
								}
							}
						}
					}
				}
				if sub == "." {
					main := jsonString(pkg["module"])
					if main == "" {
						main = jsonString(pkg["main"])
					}
					if main != "" {
						abs := filepath.Join(pkgDir, main)
						if r := e.resolveFile(abs); r != "" {
							return r
						}
						if e.fsIsDir(abs) {
							if r := e.resolveIndex(abs); r != "" {
								return r
							}
						}
					}
				}
			}
		}
	}
	target := pkgDir
	if sub != "." {
		target = filepath.Join(pkgDir, sub)
	}
	if r := e.resolveFile(target); r != "" {
		return r
	}
	if e.fsIsDir(target) {
		return e.resolveIndex(target)
	}
	return ""
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
// honoring condition objects and arrays of alternatives.
func resolveExportsTarget(exp interface{}) string {
	switch v := exp.(type) {
	case string:
		return v
	case []interface{}:
		for _, e := range v {
			if r := resolveExportsTarget(e); r != "" {
				return r
			}
		}
	case map[string]interface{}:
		for _, c := range exportConditions {
			if sub, ok := v[c]; ok {
				if r := resolveExportsTarget(sub); r != "" {
					return r
				}
			}
		}
	}
	return ""
}

func resolveExports(exports interface{}, sub string) string {
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
					if tgt := resolveExportsTarget(val); tgt != "" {
						return strings.Replace(tgt, "*", mid, 1)
					}
				}
			}
		}
		return ""
	}
	if sub == "." {
		return resolveExportsTarget(exports)
	}
	return ""
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
	if strings.HasPrefix(url, "node:") {
		return "", "builtin", nil
	}
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
		return e.cjsWrapper(bare)
	case "commonjs":
		return e.cjsWrapper(url)
	case "json":
		return "export default " + source + ";\n", nil
	default: // "module"
		return source, nil
	}
}

// cjsWrapper builds a synthetic ESM module wrapping a CJS module or builtin.
// reqArg is what require() receives (a builtin name or absolute path).
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
