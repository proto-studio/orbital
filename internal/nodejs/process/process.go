// Package process implements the Node.js process module.
package process

import (
	"os"
	"runtime"
	"strconv"
	"time"

	goruntime "github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// Process provides the process global object.
type Process struct {
	rt        *goruntime.Runtime
	startTime time.Time
	exitCode  int
}

// New creates a new Process module.
func New() *Process {
	return &Process{
		startTime: time.Now(),
	}
}

// Name returns the module name.
func (p *Process) Name() string {
	return "process"
}

// Register sets up the process global object.
func (p *Process) Register(rt *goruntime.Runtime) error {
	p.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create process object
	processObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// process.version
	version, _ := ctx.NewString("v20.0.0") // Emulated Node version
	if err := processObj.Set("version", version); err != nil {
		return err
	}

	// process.versions
	versions, _ := ctx.NewObject()
	nodeVer, _ := ctx.NewString("20.0.0")
	v8Ver, _ := ctx.NewString("12.9.202.13")
	goVer, _ := ctx.NewString(runtime.Version())
	versions.Set("node", nodeVer)
	versions.Set("v8", v8Ver)
	versions.Set("gnode", goVer)
	if err := processObj.Set("versions", versions); err != nil {
		return err
	}

	// process.platform
	platform, _ := ctx.NewString(runtime.GOOS)
	if err := processObj.Set("platform", platform); err != nil {
		return err
	}

	// process.arch
	arch, _ := ctx.NewString(goArchToNode(runtime.GOARCH))
	if err := processObj.Set("arch", arch); err != nil {
		return err
	}

	// process.pid
	pid := ctx.NewInteger(int64(os.Getpid()))
	if err := processObj.Set("pid", pid); err != nil {
		return err
	}

	// process.ppid
	ppid := ctx.NewInteger(int64(os.Getppid()))
	if err := processObj.Set("ppid", ppid); err != nil {
		return err
	}

	// process.argv
	argv, err := p.createArgv(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("argv", argv); err != nil {
		return err
	}

	// process.env
	env, err := p.createEnv(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("env", env); err != nil {
		return err
	}

	// process.cwd()
	cwdFn, err := iso.NewFunctionTemplate(p.cwdFunc)
	if err != nil {
		return err
	}
	cwdVal, err := cwdFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("cwd", cwdVal); err != nil {
		return err
	}

	// process.chdir()
	chdirFn, err := iso.NewFunctionTemplate(p.chdirFunc)
	if err != nil {
		return err
	}
	chdirVal, err := chdirFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("chdir", chdirVal); err != nil {
		return err
	}

	// process.exit()
	exitFn, err := iso.NewFunctionTemplate(p.exitFunc)
	if err != nil {
		return err
	}
	exitVal, err := exitFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("exit", exitVal); err != nil {
		return err
	}

	// process.hrtime()
	hrtimeFn, err := iso.NewFunctionTemplate(p.hrtimeFunc)
	if err != nil {
		return err
	}
	hrtimeVal, err := hrtimeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("hrtime", hrtimeVal); err != nil {
		return err
	}

	// process.hrtime.bigint()
	hrtimeBigintFn, err := iso.NewFunctionTemplate(p.hrtimeBigintFunc)
	if err != nil {
		return err
	}
	hrtimeBigintVal, err := hrtimeBigintFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := hrtimeVal.Set("bigint", hrtimeBigintVal); err != nil {
		return err
	}

	// process.uptime()
	uptimeFn, err := iso.NewFunctionTemplate(p.uptimeFunc)
	if err != nil {
		return err
	}
	uptimeVal, err := uptimeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("uptime", uptimeVal); err != nil {
		return err
	}

	// process.memoryUsage()
	memoryUsageFn, err := iso.NewFunctionTemplate(p.memoryUsageFunc)
	if err != nil {
		return err
	}
	memoryUsageVal, err := memoryUsageFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("memoryUsage", memoryUsageVal); err != nil {
		return err
	}

	// process.nextTick()
	nextTickFn, err := iso.NewFunctionTemplate(p.nextTickFunc)
	if err != nil {
		return err
	}
	nextTickVal, err := nextTickFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("nextTick", nextTickVal); err != nil {
		return err
	}

	// Set process as global
	return rt.SetGlobal("process", processObj)
}

func (p *Process) createArgv(ctx *v8go.Context) (*v8go.Value, error) {
	args := os.Args
	argv, err := ctx.NewArray(len(args))
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		val, err := ctx.NewString(arg)
		if err != nil {
			return nil, err
		}
		if err := argv.SetIndex(i, val); err != nil {
			return nil, err
		}
	}
	return argv, nil
}

func (p *Process) createEnv(ctx *v8go.Context) (*v8go.Value, error) {
	env, err := ctx.NewObject()
	if err != nil {
		return nil, err
	}

	// Use the runtime's environment interface
	envProvider := p.rt.Environment()
	for key, value := range envProvider.All() {
		val, err := ctx.NewString(value)
		if err != nil {
			return nil, err
		}
		if err := env.Set(key, val); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func (p *Process) cwdFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	val, _ := info.Context().NewString(cwd)
	return val
}

func (p *Process) chdirFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	dir := args[0].String()
	if err := os.Chdir(dir); err != nil {
		// In a real implementation, we'd throw a proper error
		return nil
	}
	return nil
}

func (p *Process) exitFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	code := 0
	args := info.Args()
	if len(args) >= 1 {
		code = int(args[0].Integer())
	}
	p.exitCode = code
	p.rt.EventLoop().Stop()
	os.Exit(code)
	return nil
}

func (p *Process) hrtimeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	now := time.Now().UnixNano()
	args := info.Args()

	if len(args) >= 1 && args[0].IsArray() {
		// Subtract the previous hrtime
		prevSec, _ := args[0].GetIndex(0)
		prevNano, _ := args[0].GetIndex(1)
		if prevSec != nil && prevNano != nil {
			prev := prevSec.Integer()*1e9 + prevNano.Integer()
			now -= prev
		}
	}

	sec := now / 1e9
	nano := now % 1e9

	arr, _ := ctx.NewArray(2)
	secVal := ctx.NewInteger(sec)
	nanoVal := ctx.NewInteger(nano)
	arr.SetIndex(0, secVal)
	arr.SetIndex(1, nanoVal)
	return arr
}

func (p *Process) hrtimeBigintFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	// Return as a string since we don't have BigInt support yet
	now := time.Now().UnixNano()
	val, _ := ctx.NewString(strconv.FormatInt(now, 10))
	return val
}

func (p *Process) uptimeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	uptime := time.Since(p.startTime).Seconds()
	return info.Context().NewNumber(uptime)
}

func (p *Process) memoryUsageFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	obj, _ := ctx.NewObject()
	obj.Set("rss", ctx.NewNumber(float64(m.Sys)))
	obj.Set("heapTotal", ctx.NewNumber(float64(m.HeapSys)))
	obj.Set("heapUsed", ctx.NewNumber(float64(m.HeapAlloc)))
	obj.Set("external", ctx.NewNumber(0))
	obj.Set("arrayBuffers", ctx.NewNumber(0))
	return obj
}

func (p *Process) nextTickFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 || !args[0].IsFunction() {
		return nil
	}

	callback := args[0]
	var callArgs []*v8go.Value
	if len(args) > 1 {
		callArgs = args[1:]
	}

	p.rt.EventLoop().EnqueueMicrotask(func() {
		callback.Call(nil, callArgs...)
	})

	return nil
}

// goArchToNode converts Go architecture names to Node.js equivalents.
func goArchToNode(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}
