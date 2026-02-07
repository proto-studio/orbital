// Package path implements the Node.js path module.
package path

import (
	"path/filepath"
	goruntime "runtime"
	"strings"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

// Path provides path manipulation functionality.
type Path struct {
	rt *runtime.Runtime
}

// New creates a new Path module.
func New() *Path {
	return &Path{}
}

// Name returns the module name.
func (p *Path) Name() string {
	return "path"
}

// Register sets up the path module.
func (p *Path) Register(rt *runtime.Runtime) error {
	p.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create path object
	pathObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// path.sep
	sep := "/"
	if goruntime.GOOS == "windows" {
		sep = "\\"
	}
	sepVal, _ := ctx.NewString(sep)
	if err := pathObj.Set("sep", sepVal); err != nil {
		return err
	}

	// path.delimiter
	delimiter := ":"
	if goruntime.GOOS == "windows" {
		delimiter = ";"
	}
	delimVal, _ := ctx.NewString(delimiter)
	if err := pathObj.Set("delimiter", delimVal); err != nil {
		return err
	}

	// path.basename
	basenameFn, err := iso.NewFunctionTemplate(p.basenameFunc)
	if err != nil {
		return err
	}
	basenameVal, err := basenameFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("basename", basenameVal); err != nil {
		return err
	}

	// path.dirname
	dirnameFn, err := iso.NewFunctionTemplate(p.dirnameFunc)
	if err != nil {
		return err
	}
	dirnameVal, err := dirnameFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("dirname", dirnameVal); err != nil {
		return err
	}

	// path.extname
	extnameFn, err := iso.NewFunctionTemplate(p.extnameFunc)
	if err != nil {
		return err
	}
	extnameVal, err := extnameFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("extname", extnameVal); err != nil {
		return err
	}

	// path.join
	joinFn, err := iso.NewFunctionTemplate(p.joinFunc)
	if err != nil {
		return err
	}
	joinVal, err := joinFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("join", joinVal); err != nil {
		return err
	}

	// path.resolve
	resolveFn, err := iso.NewFunctionTemplate(p.resolveFunc)
	if err != nil {
		return err
	}
	resolveVal, err := resolveFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("resolve", resolveVal); err != nil {
		return err
	}

	// path.normalize
	normalizeFn, err := iso.NewFunctionTemplate(p.normalizeFunc)
	if err != nil {
		return err
	}
	normalizeVal, err := normalizeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("normalize", normalizeVal); err != nil {
		return err
	}

	// path.isAbsolute
	isAbsoluteFn, err := iso.NewFunctionTemplate(p.isAbsoluteFunc)
	if err != nil {
		return err
	}
	isAbsoluteVal, err := isAbsoluteFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("isAbsolute", isAbsoluteVal); err != nil {
		return err
	}

	// path.relative
	relativeFn, err := iso.NewFunctionTemplate(p.relativeFunc)
	if err != nil {
		return err
	}
	relativeVal, err := relativeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("relative", relativeVal); err != nil {
		return err
	}

	// path.parse
	parseFn, err := iso.NewFunctionTemplate(p.parseFunc)
	if err != nil {
		return err
	}
	parseVal, err := parseFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("parse", parseVal); err != nil {
		return err
	}

	// path.format
	formatFn, err := iso.NewFunctionTemplate(p.formatFunc)
	if err != nil {
		return err
	}
	formatVal, err := formatFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := pathObj.Set("format", formatVal); err != nil {
		return err
	}

	// Set path as global module
	return rt.SetGlobal("__path_module", pathObj)
}

func (p *Path) basenameFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		val, _ := ctx.NewString("")
		return val
	}

	path := args[0].String()
	base := filepath.Base(path)

	// Remove extension if provided
	if len(args) >= 2 {
		ext := args[1].String()
		if strings.HasSuffix(base, ext) {
			base = base[:len(base)-len(ext)]
		}
	}

	val, _ := ctx.NewString(base)
	return val
}

func (p *Path) dirnameFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		val, _ := ctx.NewString(".")
		return val
	}

	path := args[0].String()
	dir := filepath.Dir(path)
	val, _ := ctx.NewString(dir)
	return val
}

func (p *Path) extnameFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		val, _ := ctx.NewString("")
		return val
	}

	path := args[0].String()
	ext := filepath.Ext(path)
	val, _ := ctx.NewString(ext)
	return val
}

func (p *Path) joinFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()

	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = arg.String()
	}

	result := filepath.Join(parts...)
	val, _ := ctx.NewString(result)
	return val
}

func (p *Path) resolveFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) == 0 {
		cwd, _ := filepath.Abs(".")
		val, _ := ctx.NewString(cwd)
		return val
	}

	// Start from the last absolute path or cwd
	result := ""
	for _, arg := range args {
		path := arg.String()
		if filepath.IsAbs(path) {
			result = path
		} else if result == "" {
			result = path
		} else {
			result = filepath.Join(result, path)
		}
	}

	abs, _ := filepath.Abs(result)
	val, _ := ctx.NewString(abs)
	return val
}

func (p *Path) normalizeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		val, _ := ctx.NewString(".")
		return val
	}

	path := args[0].String()
	result := filepath.Clean(path)
	val, _ := ctx.NewString(result)
	return val
}

func (p *Path) isAbsoluteFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.False()
	}

	path := args[0].String()
	if filepath.IsAbs(path) {
		return ctx.True()
	}
	return ctx.False()
}

func (p *Path) relativeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		val, _ := ctx.NewString("")
		return val
	}

	from := args[0].String()
	to := args[1].String()

	rel, err := filepath.Rel(from, to)
	if err != nil {
		val, _ := ctx.NewString(to)
		return val
	}

	val, _ := ctx.NewString(rel)
	return val
}

func (p *Path) parseFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		obj, _ := ctx.NewObject()
		return obj
	}

	path := args[0].String()

	obj, _ := ctx.NewObject()

	// root
	root := ""
	if filepath.IsAbs(path) {
		if goruntime.GOOS == "windows" {
			root = filepath.VolumeName(path) + "\\"
		} else {
			root = "/"
		}
	}
	rootVal, _ := ctx.NewString(root)
	obj.Set("root", rootVal)

	// dir
	dir := filepath.Dir(path)
	dirVal, _ := ctx.NewString(dir)
	obj.Set("dir", dirVal)

	// base
	base := filepath.Base(path)
	baseVal, _ := ctx.NewString(base)
	obj.Set("base", baseVal)

	// ext
	ext := filepath.Ext(path)
	extVal, _ := ctx.NewString(ext)
	obj.Set("ext", extVal)

	// name
	name := base
	if ext != "" {
		name = base[:len(base)-len(ext)]
	}
	nameVal, _ := ctx.NewString(name)
	obj.Set("name", nameVal)

	return obj
}

func (p *Path) formatFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 || !args[0].IsObject() {
		val, _ := ctx.NewString("")
		return val
	}

	obj := args[0]

	var dir, root, base, name, ext string

	if v, err := obj.Get("dir"); err == nil && v != nil && !v.IsUndefined() {
		dir = v.String()
	}
	if v, err := obj.Get("root"); err == nil && v != nil && !v.IsUndefined() {
		root = v.String()
	}
	if v, err := obj.Get("base"); err == nil && v != nil && !v.IsUndefined() {
		base = v.String()
	}
	if v, err := obj.Get("name"); err == nil && v != nil && !v.IsUndefined() {
		name = v.String()
	}
	if v, err := obj.Get("ext"); err == nil && v != nil && !v.IsUndefined() {
		ext = v.String()
	}

	// If base is set, use it; otherwise construct from name + ext
	if base == "" && (name != "" || ext != "") {
		base = name + ext
	}

	// Use dir if set, otherwise use root
	var result string
	if dir != "" {
		result = filepath.Join(dir, base)
	} else if root != "" {
		result = root + base
	} else {
		result = base
	}

	val, _ := ctx.NewString(result)
	return val
}
