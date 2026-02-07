// Package module implements CommonJS module loading.
package module

import (
	_ "embed"
	"path/filepath"
	"strings"

	"proto.zip/studio/orbital/pkg/filesystem"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

//go:embed module.js
var moduleJS string

// Module provides CommonJS module functionality.
type Module struct {
	rt    *runtime.Runtime
	cache map[string]*v8go.Value // Module cache
}

// New creates a new Module system.
func New() *Module {
	return &Module{
		cache: make(map[string]*v8go.Value),
	}
}

// Name returns the module name.
func (m *Module) Name() string {
	return "module"
}

// Register sets up the CommonJS module system.
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

	// Run the embedded module system JavaScript
	_, err = rt.RunScript(moduleJS, "module.js")
	return err
}

// requireFileFunc reads a module file from the filesystem.
func (m *Module) requireFileFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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

// resolveFunc resolves a module path.
func (m *Module) resolveFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
	Stat(string) (*filesystem.FileInfo, error)
}

// resolveModulePath resolves a module request to a file path.
func (m *Module) resolveModulePath(request, basePath string, fs fsInterface) string {
	// Handle relative paths
	if strings.HasPrefix(request, "./") || strings.HasPrefix(request, "../") {
		return m.resolveAsFile(filepath.Join(basePath, request), fs)
	}

	// Handle absolute paths
	if strings.HasPrefix(request, "/") {
		return m.resolveAsFile(request, fs)
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
				resolved := m.resolveAsFile(mainPath, fs)
				if resolved != "" {
					return resolved
				}
			}
		}
	}

	// Try index.js
	indexPath := filepath.Join(path, "index.js")
	if fs.Exists(indexPath) {
		return indexPath
	}

	// Try index.json
	indexJsonPath := filepath.Join(path, "index.json")
	if fs.Exists(indexJsonPath) {
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
