// Package path implements the Node.js path module.
package path

import (
	_ "embed"
	"os"
	goruntime "runtime"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// pathJS is Node's lib/path.js: path.posix, path.win32, and the host's default.
//
//go:embed path.js
var pathJS string

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

	// The script reads process.platform and process.cwd() when they exist, and these
	// otherwise.
	native, err := ctx.NewObject()
	if err != nil {
		return err
	}
	platform := goruntime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	platformVal, err := ctx.NewString(platform)
	if err != nil {
		return err
	}
	if err := native.Set("platform", platformVal); err != nil {
		return err
	}
	cwdFn, err := iso.NewFunctionTemplate(func(info *v8.FunctionCallbackInfo) *v8.Value {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "/"
		}
		val, _ := info.Context().NewString(cwd)
		return val
	})
	if err != nil {
		return err
	}
	cwdVal, err := cwdFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := native.Set("cwd", cwdVal); err != nil {
		return err
	}
	if err := rt.SetGlobal("__path_native", native); err != nil {
		return err
	}
	_, err = rt.RunScript(pathJS, "path.js")
	return err
}
