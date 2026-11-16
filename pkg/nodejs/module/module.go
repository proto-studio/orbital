// Package module implements CommonJS module loading.
package module

import (
	_ "embed"
	"path/filepath"
	"strings"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed module.js
var moduleJS string

// Module provides CommonJS module functionality.
type Module struct {
	rt    *runtime.Runtime
	cache map[string]*v8.Value // Module cache
	// compileSite is where the last failed native compile of a wrapper placed its error.
	compileSite *v8.Value
}

// New creates a new Module runtime.
func New() *Module {
	return &Module{
		cache: make(map[string]*v8.Value),
	}
}

// Name returns the module name.
func (m *Module) Name() string {
	return "module"
}

// Register sets up the CommonJS module runtime.
func (m *Module) Register(rt *runtime.Runtime) error {
	m.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create the internal require function that handles file loading
	requireFileFn, err := iso.NewFunctionTemplate(m.requireFileFunc)
	if err != nil {
		return err
	}
	requireFileVal, err := requireFileFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__requireFile", requireFileVal); err != nil {
		return err
	}

	// Create resolve function
	resolveFn, err := iso.NewFunctionTemplate(m.resolveFunc)
	if err != nil {
		return err
	}
	resolveVal, err := resolveFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__resolveModule", resolveVal); err != nil {
		return err
	}

	// Create the function that compiles a module's wrapper as a script named after
	// its file
	compileFn, err := iso.NewFunctionTemplate(m.compileWrapperFunc)
	if err != nil {
		return err
	}
	compileVal, err := compileFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__compileModuleWrapper", compileVal); err != nil {
		return err
	}
	compiledFn, err := iso.NewFunctionTemplate(m.wrapperCompiledFunc)
	if err != nil {
		return err
	}
	// When that compile throws too, module.js hands the error here: the header shows
	// where the native compile placed it, in the module, not where eval ran.
	failedFn, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		site := m.compileSite
		m.compileSite = nil
		if args := info.Args(); len(args) >= 1 && site != nil {
			m.rt.RecordThrowSite(args[0], site)
		}
		return info.Context().Undefined()
	})
	if err != nil {
		return err
	}
	failedVal, err := failedFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__moduleCompileFailed", failedVal); err != nil {
		return err
	}
	compiledVal, err := compiledFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__moduleWrapperCompiled", compiledVal); err != nil {
		return err
	}

	// Run the embedded module system JavaScript
	_, err = rt.RunScript(moduleJS, "module.js")
	return err
}

// requireFileFunc reads a module file from the runtime.
func (m *Module) requireFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 1 {
		return ctx.Null()
	}

	filename := args[0].String()
	fs := m.rt.Filesystem()

	// Try to read the file
	data, err := fs.ReadFile(filename)
	if err != nil {
		return ctx.Null()
	}

	result, _ := ctx.NewString(string(data))
	return result
}

// compileWrapperFunc evaluates a module's function wrapper (args: wrapper source,
// filename) as a script whose name is the module's filename and returns the
// function. V8 hands that name to import() as the referrer, so a module's import()
// calls resolve relative to its own file; code compiled by eval would report the
// script that called eval instead. It returns undefined when the wrapper does not
// compile, leaving the caller to compile it another way and throw the real error.
func (m *Module) compileWrapperFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Undefined()
	}
	wrapper, name := args[0].String(), args[1].String()
	m.compileSite = nil
	fn, err := ctx.RunScript(wrapper, name)
	if err != nil || fn == nil || !fn.IsFunction() {
		// RunScript keeps what a failed script threw, and where, for reporting an
		// uncaught error from the main script. This failure is not one: the caller
		// throws it again.
		ctx.TakeLastScriptException()
		// Where the compile failed, for the error the caller's own compile throws.
		// The module's own record goes away with the failed compile, so the wrapper's
		// opening is taken off the line here.
		msg := ctx.TakeLastExceptionMessage()
		if msg != nil && msg.Line == 1 && strings.HasPrefix(msg.SourceLine, moduleWrapperPrefix) {
			msg.SourceLine = msg.SourceLine[len(moduleWrapperPrefix):]
			msg.StartColumn -= len(moduleWrapperPrefix)
			msg.EndColumn -= len(moduleWrapperPrefix)
			if len(args) >= 3 && args[2].Boolean() {
				msg.SourceLine = "\uFEFF" + msg.SourceLine
				msg.StartColumn++
				msg.EndColumn++
			}
		}
		m.compileSite = m.rt.ThrowSite(msg)
		return ctx.Undefined()
	}
	// The wrapper's opening shares the module's first line, so columns there count it too.
	if strings.HasPrefix(wrapper, moduleWrapperPrefix) {
		// The module's text as compiled, with the BOM the loader took off, for the line
		// printed above a fatal exception from it.
		text := wrapper[len(moduleWrapperPrefix):]
		// module.js closes the wrapper after the source: "\n})\n//# sourceURL=...".
		if i := strings.LastIndex(text, "\n})\n//# sourceURL="); i != -1 {
			text = text[:i]
		}
		if len(args) >= 3 && args[2].Boolean() {
			text = "\uFEFF" + text
		}
		// A fourth argument of false compiles only to learn where a syntax error is; the
		// module's own text is kept by whoever compiles it for real.
		if len(args) < 4 || args[3].Boolean() {
			m.rt.KeepModuleSource(name, text)
			m.rt.SetFirstLineOffset(name, len(moduleWrapperPrefix))
		}
	}
	return fn
}

// wrapperCompiledFunc implements __moduleWrapperCompiled(name, text, failed, nul), which
// module.js calls for a wrapper it compiles by eval instead: its opening shares the first
// line too. module.js calls it again with failed true if the eval doesn't compile, and the
// text is dropped.
func (m *Module) wrapperCompiledFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if args := info.Args(); len(args) >= 2 {
		name := args[0].String()
		if len(args) >= 3 && args[2].Boolean() {
			m.rt.ForgetModuleSource(name, args[1].String())
			return info.Context().Undefined()
		}
		nul := ""
		if len(args) >= 4 {
			nul = args[3].String()
		}
		m.rt.KeepModuleSourceWithNUL(name, args[1].String(), nul)
		m.rt.SetFirstLineOffset(name, len(moduleWrapperPrefix))
	}
	return info.Context().Undefined()
}

// moduleWrapperPrefix is how module.js opens the function a CommonJS module runs in.
const moduleWrapperPrefix = "(function(exports, require, module, __filename, __dirname) {"

// resolveFunc resolves a module path.
func (m *Module) resolveFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 2 {
		return ctx.Null()
	}

	request := args[0].String()
	basePath := args[1].String()
	fs := m.rt.Filesystem()

	resolved := m.resolveModulePath(request, basePath, fs)
	if resolved == "" {
		return ctx.Null()
	}

	result, _ := ctx.NewString(resolved)
	return result
}

// fileExistsAndIsFile checks if a path exists and is a file (not directory)
func fileExistsAndIsFile(path string, fs fsInterface) bool {
	info, err := fs.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir
}

// fsInterface combines the filesystem methods needed for module resolution
type fsInterface interface {
	Exists(string) bool
	ReadFile(string) ([]byte, error)
	Stat(string) (*runtime.FileInfo, error)
}

// resolveModulePath resolves a module request to a file path.
func (m *Module) resolveModulePath(request, basePath string, fs fsInterface) string {
	// Handle relative paths
	if strings.HasPrefix(request, "./") || strings.HasPrefix(request, "../") {
		return m.resolveAsFileOrDirectory(filepath.Join(basePath, request), fs)
	}

	// Handle absolute paths
	if strings.HasPrefix(request, "/") {
		return m.resolveAsFileOrDirectory(request, fs)
	}

	// Handle node_modules (simplified)
	// Walk up directories looking for node_modules
	dir := basePath
	for {
		nodeModulesPath := filepath.Join(dir, "node_modules", request)
		resolved := m.resolveAsFile(nodeModulesPath, fs)
		if resolved != "" {
			return resolved
		}

		// Try as directory with package.json or index.js
		resolved = m.resolveAsDirectory(nodeModulesPath, fs)
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

	return ""
}

// resolveAsFileOrDirectory resolves a path first as a file, then, failing
// that, as a directory (package.json "main" or an index file). This matches
// Node's LOAD_AS_FILE followed by LOAD_AS_DIRECTORY behavior and is required
// for relative/absolute requires that point at a folder (e.g. require('./parse')).
func (m *Module) resolveAsFileOrDirectory(path string, fs fsInterface) string {
	if resolved := m.resolveAsFile(path, fs); resolved != "" {
		return resolved
	}
	return m.resolveAsDirectory(path, fs)
}

// resolveAsFile tries to resolve a path as a file.
func (m *Module) resolveAsFile(path string, fs fsInterface) string {
	// Try exact path
	if fileExistsAndIsFile(path, fs) {
		return path
	}

	// Try with .js extension
	jsPath := path + ".js"
	if fileExistsAndIsFile(jsPath, fs) {
		return jsPath
	}

	// Try with .json extension
	jsonPath := path + ".json"
	if fileExistsAndIsFile(jsonPath, fs) {
		return jsonPath
	}

	return ""
}

// resolveAsDirectory tries to resolve a path as a directory.
func (m *Module) resolveAsDirectory(path string, fs fsInterface) string {
	// Try package.json
	pkgPath := filepath.Join(path, "package.json")
	if fs.Exists(pkgPath) {
		data, err := fs.ReadFile(pkgPath)
		if err == nil {
			// Simple JSON parsing for "main" field
			main := m.parsePackageMain(string(data))
			if main != "" {
				mainPath := filepath.Join(path, main)
				// Node.js first tries to load "main" as a file, then, if
				// that fails, as a directory (i.e. main/index.js). Packages
				// such as relateurl set "main": "lib", pointing at a folder.
				resolved := m.resolveAsFile(mainPath, fs)
				if resolved != "" {
					return resolved
				}
				resolved = m.resolveIndex(mainPath, fs)
				if resolved != "" {
					return resolved
				}
			}
		}
	}

	return m.resolveIndex(path, fs)
}

// resolveIndex tries to resolve a directory to its index file.
func (m *Module) resolveIndex(path string, fs fsInterface) string {
	// Try index.js
	indexPath := filepath.Join(path, "index.js")
	if fileExistsAndIsFile(indexPath, fs) {
		return indexPath
	}

	// Try index.json
	indexJsonPath := filepath.Join(path, "index.json")
	if fileExistsAndIsFile(indexJsonPath, fs) {
		return indexJsonPath
	}

	return ""
}

// parsePackageMain extracts the "main" field from package.json.
func (m *Module) parsePackageMain(content string) string {
	// Simple parsing - find "main": "value"
	idx := strings.Index(content, `"main"`)
	if idx == -1 {
		return ""
	}

	rest := content[idx+6:]
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
