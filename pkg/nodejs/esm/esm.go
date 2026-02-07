// Package esm implements ES Module loading.
package esm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// ESM provides ES Module functionality.
type ESM struct {
	rt *runtime.Runtime
}

// New creates a new ESM module system.
func New() *ESM {
	return &ESM{}
}

// Name returns the module name.
func (e *ESM) Name() string {
	return "esm"
}

// Register sets up the ES Module system.
func (e *ESM) Register(rt *runtime.Runtime) error {
	e.rt = rt
	return nil
}

// RunModule compiles and runs an ES module from source.
func (e *ESM) RunModule(source, filename string) (*v8go.Value, error) {
	if e.rt == nil {
		return nil, errors.New("esm: runtime not initialized")
	}

	ctx := e.rt.Context()

	// Compile the module
	mod, err := ctx.CompileModule(source, filename)
	if err != nil {
		return nil, err
	}

	// Instantiate with our resolver
	err = mod.Instantiate(e.resolveModule)
	if err != nil {
		return nil, err
	}

	// Evaluate the module
	result, err := mod.Evaluate()
	if err != nil {
		return nil, err
	}

	// Run any pending async operations
	e.rt.EventLoop().Run()

	return result, nil
}

// RunModuleFile loads and runs an ES module from a file.
func (e *ESM) RunModuleFile(filename string) (*v8go.Value, error) {
	fs := e.rt.Filesystem()

	// Normalize to absolute path
	absPath := filename
	if !filepath.IsAbs(filename) {
		cwd, _ := os.Getwd()
		absPath = filepath.Join(cwd, filename)
	}

	source, err := fs.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	return e.RunModule(string(source), absPath)
}

// GetModuleNamespace returns the namespace object of a module after evaluation.
func (e *ESM) GetModuleNamespace(source, filename string) (*v8go.Value, error) {
	ctx := e.rt.Context()

	mod, err := ctx.CompileModule(source, filename)
	if err != nil {
		return nil, err
	}

	err = mod.Instantiate(e.resolveModule)
	if err != nil {
		return nil, err
	}

	_, err = mod.Evaluate()
	if err != nil {
		return nil, err
	}

	e.rt.EventLoop().Run()

	return mod.GetNamespace()
}

// resolveModule is called by V8 when it encounters an import.
func (e *ESM) resolveModule(specifier, referrer string) (string, string, error) {
	// Check for native Go modules first
	if _, exists := e.rt.GetNativeModule(specifier); exists {
		// Wrap native module as ESM
		source := fmt.Sprintf(`
const __native = globalThis.__native_module_%s;
export default __native;
// Re-export all properties as named exports
if (__native && typeof __native === 'object') {
	for (const key of Object.keys(__native)) {
		if (key !== 'default') {
			// Note: We can't dynamically create named exports in ESM
			// Users should access via default import
		}
	}
}
`, specifier)
		return source, "native:" + specifier, nil
	}

	fs := e.rt.Filesystem()

	// Determine base path from referrer
	basePath := filepath.Dir(referrer)
	if basePath == "" || basePath == "." {
		basePath, _ = os.Getwd()
	}

	// Resolve the module path
	resolvedPath := e.resolveModulePath(specifier, basePath)
	if resolvedPath == "" {
		return "", "", errors.New("cannot resolve module: " + specifier)
	}

	// Read the module source
	source, err := fs.ReadFile(resolvedPath)
	if err != nil {
		return "", "", err
	}

	sourceStr := string(source)

	// Handle CommonJS interop: wrap .js files that don't have ESM syntax
	if strings.HasSuffix(resolvedPath, ".js") && !e.isESModule(sourceStr) {
		sourceStr = e.wrapCommonJS(sourceStr, resolvedPath)
	}

	// Handle JSON files
	if strings.HasSuffix(resolvedPath, ".json") {
		sourceStr = "export default " + sourceStr + ";"
	}

	return sourceStr, resolvedPath, nil
}

// isESModule checks if source code uses ES module syntax.
func (e *ESM) isESModule(source string) bool {
	// Simple heuristic: check for import/export statements
	// This is a basic check - a real implementation would use proper parsing
	lines := strings.Split(source, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip comments
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		// Check for ESM syntax
		if strings.HasPrefix(trimmed, "import ") ||
			strings.HasPrefix(trimmed, "import{") ||
			strings.HasPrefix(trimmed, "export ") ||
			strings.HasPrefix(trimmed, "export{") ||
			strings.HasPrefix(trimmed, "export default") {
			return true
		}
	}
	return false
}

// wrapCommonJS wraps CommonJS source code as an ES module.
func (e *ESM) wrapCommonJS(source, filename string) string {
	// Create a synthetic ESM that runs the CJS code and exports module.exports
	// This mimics Node.js behavior where CJS module.exports becomes the default export
	dirname := filepath.Dir(filename)

	return fmt.Sprintf(`
// Synthetic ESM wrapper for CommonJS module
const __cjs_module = { exports: {} };
const __cjs_exports = __cjs_module.exports;
const __cjs_filename = %q;
const __cjs_dirname = %q;

// Use existing require if available, otherwise create a stub
const __cjs_require = typeof require !== 'undefined' ? require : function(id) {
	throw new Error('require is not available in this context: ' + id);
};

(function(exports, require, module, __filename, __dirname) {
%s
})(__cjs_exports, __cjs_require, __cjs_module, __cjs_filename, __cjs_dirname);

// Export the CommonJS module.exports as default
export default __cjs_module.exports;

// Also export named exports if module.exports is an object
const __cjs_result = __cjs_module.exports;
if (__cjs_result && typeof __cjs_result === 'object' && !Array.isArray(__cjs_result)) {
	for (const key of Object.keys(__cjs_result)) {
		if (key !== 'default') {
			// Create named exports dynamically (note: this won't work for static analysis)
		}
	}
}
`, filename, dirname, source)
}

// resolveModulePath resolves a module specifier to a file path.
func (e *ESM) resolveModulePath(specifier, basePath string) string {
	// Handle relative paths
	if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") {
		return e.resolveAsFile(filepath.Join(basePath, specifier))
	}

	// Handle absolute paths
	if strings.HasPrefix(specifier, "/") {
		return e.resolveAsFile(specifier)
	}

	// Handle bare specifiers (node_modules)
	// Walk up directories looking for node_modules
	dir := basePath
	for {
		nodeModulesPath := filepath.Join(dir, "node_modules", specifier)
		resolved := e.resolveAsFile(nodeModulesPath)
		if resolved != "" {
			return resolved
		}

		// Try as directory with package.json or index
		resolved = e.resolveAsDirectory(nodeModulesPath)
		if resolved != "" {
			return resolved
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Also try resolving from cwd for top-level modules
	cwd, _ := os.Getwd()
	nodeModulesPath := filepath.Join(cwd, "node_modules", specifier)
	resolved := e.resolveAsFile(nodeModulesPath)
	if resolved != "" {
		return resolved
	}
	return e.resolveAsDirectory(nodeModulesPath)
}

// resolveAsFile tries to resolve a path as a file.
func (e *ESM) resolveAsFile(path string) string {
	fs := e.rt.Filesystem()

	// Helper to check if path is a file (not directory)
	isFile := func(p string) bool {
		info, err := fs.Stat(p)
		if err != nil {
			return false
		}
		return !info.IsDir
	}

	// Try exact path
	if isFile(path) {
		return path
	}

	// Try with .js extension
	jsPath := path + ".js"
	if isFile(jsPath) {
		return jsPath
	}

	// Try with .mjs extension (ES module specific)
	mjsPath := path + ".mjs"
	if isFile(mjsPath) {
		return mjsPath
	}

	// Try with .json extension
	jsonPath := path + ".json"
	if isFile(jsonPath) {
		return jsonPath
	}

	return ""
}

// resolveAsDirectory tries to resolve a path as a directory.
func (e *ESM) resolveAsDirectory(path string) string {
	fs := e.rt.Filesystem()

	// Try package.json
	pkgPath := filepath.Join(path, "package.json")
	if fs.Exists(pkgPath) {
		data, err := fs.ReadFile(pkgPath)
		if err == nil {
			// Check for "module" field first (ESM entry), then "main"
			main := e.parsePackageField(string(data), "module")
			if main == "" {
				main = e.parsePackageField(string(data), "main")
			}
			if main != "" {
				mainPath := filepath.Join(path, main)
				resolved := e.resolveAsFile(mainPath)
				if resolved != "" {
					return resolved
				}
			}
		}
	}

	// Try index.mjs (ES module specific)
	indexMjsPath := filepath.Join(path, "index.mjs")
	if fs.Exists(indexMjsPath) {
		return indexMjsPath
	}

	// Try index.js
	indexPath := filepath.Join(path, "index.js")
	if fs.Exists(indexPath) {
		return indexPath
	}

	return ""
}

// parsePackageField extracts a field from package.json.
func (e *ESM) parsePackageField(content, field string) string {
	// Simple parsing - find "field": "value"
	searchStr := `"` + field + `"`
	idx := strings.Index(content, searchStr)
	if idx == -1 {
		return ""
	}

	rest := content[idx+len(searchStr):]
	// Find the colon
	colonIdx := strings.Index(rest, ":")
	if colonIdx == -1 {
		return ""
	}

	rest = strings.TrimSpace(rest[colonIdx+1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}

	// Find closing quote
	rest = rest[1:]
	endIdx := strings.Index(rest, `"`)
	if endIdx == -1 {
		return ""
	}

	return rest[:endIdx]
}
