// Package os implements the Node.js os module.
package os

import (
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/system"
	"proto.zip/studio/orbital/pkg/v8go"
)

// OS provides operating system functionality.
type OS struct {
	rt   *runtime.Runtime
	info system.SystemInfo
}

// New creates a new OS module.
func New() *OS {
	return &OS{}
}

// Name returns the module name.
func (o *OS) Name() string {
	return "os"
}

// Register sets up the os module.
func (o *OS) Register(rt *runtime.Runtime) error {
	o.rt = rt
	o.info = rt.SystemInfo()

	iso := rt.Isolate()
	ctx := rt.Context()

	// Create os object
	osObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register all functions
	funcs := map[string]v8go.FunctionCallback{
		"hostname":          o.hostnameFunc,
		"platform":          o.platformFunc,
		"arch":              o.archFunc,
		"release":           o.releaseFunc,
		"type":              o.typeFunc,
		"version":           o.versionFunc,
		"machine":           o.machineFunc,
		"cpus":              o.cpusFunc,
		"totalmem":          o.totalmemFunc,
		"freemem":           o.freememFunc,
		"homedir":           o.homedirFunc,
		"tmpdir":            o.tmpdirFunc,
		"userInfo":          o.userInfoFunc,
		"networkInterfaces": o.networkInterfacesFunc,
		"uptime":            o.uptimeFunc,
		"loadavg":           o.loadavgFunc,
		"endianness":        o.endiannessFunc,
	}

	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := osObj.Set(name, val); err != nil {
			return err
		}
	}

	// Constants
	eol, _ := ctx.NewString(o.info.EOL())
	osObj.Set("EOL", eol)

	devNull, _ := ctx.NewString(o.info.DevNull())
	osObj.Set("devNull", devNull)

	// Constants object
	constants, _ := ctx.NewObject()
	signalsObj, _ := ctx.NewObject()
	// Common signals
	signals := map[string]int{
		"SIGHUP":  1,
		"SIGINT":  2,
		"SIGQUIT": 3,
		"SIGILL":  4,
		"SIGTRAP": 5,
		"SIGABRT": 6,
		"SIGFPE":  8,
		"SIGKILL": 9,
		"SIGSEGV": 11,
		"SIGPIPE": 13,
		"SIGALRM": 14,
		"SIGTERM": 15,
	}
	for name, val := range signals {
		signalsObj.Set(name, ctx.NewInteger(int64(val)))
	}
	constants.Set("signals", signalsObj)

	// Error numbers
	errnoObj, _ := ctx.NewObject()
	errnos := map[string]int{
		"EACCES":       13,
		"ENOENT":       2,
		"EEXIST":       17,
		"EISDIR":       21,
		"ENOTDIR":      20,
		"ENOTEMPTY":    39,
		"EPERM":        1,
		"EBUSY":        16,
		"EINVAL":       22,
		"EMFILE":       24,
		"ENFILE":       23,
		"EBADF":        9,
		"ENOSPC":       28,
		"EROFS":        30,
		"ELOOP":        40,
		"ENAMETOOLONG": 36,
	}
	for name, val := range errnos {
		errnoObj.Set(name, ctx.NewInteger(int64(val)))
	}
	constants.Set("errno", errnoObj)

	// Priority constants
	priorityObj, _ := ctx.NewObject()
	priorityObj.Set("PRIORITY_LOW", ctx.NewInteger(19))
	priorityObj.Set("PRIORITY_BELOW_NORMAL", ctx.NewInteger(10))
	priorityObj.Set("PRIORITY_NORMAL", ctx.NewInteger(0))
	priorityObj.Set("PRIORITY_ABOVE_NORMAL", ctx.NewInteger(-7))
	priorityObj.Set("PRIORITY_HIGH", ctx.NewInteger(-14))
	priorityObj.Set("PRIORITY_HIGHEST", ctx.NewInteger(-20))
	constants.Set("priority", priorityObj)

	osObj.Set("constants", constants)

	return rt.SetGlobal("__os_module", osObj)
}

func (o *OS) hostnameFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Hostname())
	return val
}

func (o *OS) platformFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Platform())
	return val
}

func (o *OS) archFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Arch())
	return val
}

func (o *OS) releaseFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Release())
	return val
}

func (o *OS) typeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Type())
	return val
}

func (o *OS) versionFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Version())
	return val
}

func (o *OS) machineFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Machine())
	return val
}

func (o *OS) cpusFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	cpus := o.info.CPUs()

	arr, _ := ctx.NewArray(len(cpus))
	for i, cpu := range cpus {
		obj, _ := ctx.NewObject()
		model, _ := ctx.NewString(cpu.Model)
		obj.Set("model", model)
		obj.Set("speed", ctx.NewNumber(float64(cpu.Speed)))

		times, _ := ctx.NewObject()
		times.Set("user", ctx.NewNumber(float64(cpu.Times.User)))
		times.Set("nice", ctx.NewNumber(float64(cpu.Times.Nice)))
		times.Set("sys", ctx.NewNumber(float64(cpu.Times.Sys)))
		times.Set("idle", ctx.NewNumber(float64(cpu.Times.Idle)))
		times.Set("irq", ctx.NewNumber(float64(cpu.Times.IRQ)))
		obj.Set("times", times)

		arr.SetIndex(i, obj)
	}

	return arr
}

func (o *OS) totalmemFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return info.Context().NewNumber(float64(o.info.TotalMem()))
}

func (o *OS) freememFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return info.Context().NewNumber(float64(o.info.FreeMem()))
}

func (o *OS) homedirFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.HomeDir())
	return val
}

func (o *OS) tmpdirFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.TmpDir())
	return val
}

func (o *OS) userInfoFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	userInfo, err := o.info.UserInfo()
	if err != nil {
		return nil
	}

	obj, _ := ctx.NewObject()
	uid, _ := ctx.NewString(userInfo.UID)
	gid, _ := ctx.NewString(userInfo.GID)
	username, _ := ctx.NewString(userInfo.Username)
	homedir, _ := ctx.NewString(userInfo.HomeDir)
	shell, _ := ctx.NewString(userInfo.Shell)

	obj.Set("uid", uid)
	obj.Set("gid", gid)
	obj.Set("username", username)
	obj.Set("homedir", homedir)
	obj.Set("shell", shell)

	return obj
}

func (o *OS) networkInterfacesFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	ifaces := o.info.NetworkInterfaces()

	obj, _ := ctx.NewObject()
	for name, addrs := range ifaces {
		arr, _ := ctx.NewArray(len(addrs))
		for i, addr := range addrs {
			addrObj, _ := ctx.NewObject()
			address, _ := ctx.NewString(addr.Address)
			netmask, _ := ctx.NewString(addr.Netmask)
			family, _ := ctx.NewString(addr.Family)
			mac, _ := ctx.NewString(addr.MAC)
			cidr, _ := ctx.NewString(addr.CIDR)

			addrObj.Set("address", address)
			addrObj.Set("netmask", netmask)
			addrObj.Set("family", family)
			addrObj.Set("mac", mac)
			addrObj.Set("internal", ctx.NewBoolean(addr.Internal))
			addrObj.Set("cidr", cidr)

			arr.SetIndex(i, addrObj)
		}
		obj.Set(name, arr)
	}

	return obj
}

func (o *OS) uptimeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return info.Context().NewNumber(o.info.Uptime())
}

func (o *OS) loadavgFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	load := o.info.LoadAvg()
	arr, _ := ctx.NewArray(3)
	arr.SetIndex(0, ctx.NewNumber(load[0]))
	arr.SetIndex(1, ctx.NewNumber(load[1]))
	arr.SetIndex(2, ctx.NewNumber(load[2]))
	return arr
}

func (o *OS) endiannessFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	val, _ := info.Context().NewString(o.info.Endianness())
	return val
}
