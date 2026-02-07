// Package fs implements the Node.js fs module.
package fs

import (
	"io/fs"

	"github.com/andrewcurioso/gnode/pkg/filesystem"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// FS provides file system functionality.
type FS struct {
	rt *runtime.Runtime
}

// New creates a new FS module.
func New() *FS {
	return &FS{}
}

// Name returns the module name.
func (f *FS) Name() string {
	return "fs"
}

// fs returns the filesystem from the runtime.
func (f *FS) fs() filesystem.Filesystem {
	return f.rt.Filesystem()
}

// Register sets up the fs module.
func (f *FS) Register(rt *runtime.Runtime) error {
	f.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create fs object
	fsObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Synchronous functions

	// fs.readFileSync
	readFileSyncFn, err := iso.NewFunctionTemplate(f.readFileSyncFunc)
	if err != nil {
		return err
	}
	readFileSyncVal, err := readFileSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("readFileSync", readFileSyncVal); err != nil {
		return err
	}

	// fs.writeFileSync
	writeFileSyncFn, err := iso.NewFunctionTemplate(f.writeFileSyncFunc)
	if err != nil {
		return err
	}
	writeFileSyncVal, err := writeFileSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("writeFileSync", writeFileSyncVal); err != nil {
		return err
	}

	// fs.existsSync
	existsSyncFn, err := iso.NewFunctionTemplate(f.existsSyncFunc)
	if err != nil {
		return err
	}
	existsSyncVal, err := existsSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("existsSync", existsSyncVal); err != nil {
		return err
	}

	// fs.mkdirSync
	mkdirSyncFn, err := iso.NewFunctionTemplate(f.mkdirSyncFunc)
	if err != nil {
		return err
	}
	mkdirSyncVal, err := mkdirSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("mkdirSync", mkdirSyncVal); err != nil {
		return err
	}

	// fs.rmdirSync
	rmdirSyncFn, err := iso.NewFunctionTemplate(f.rmdirSyncFunc)
	if err != nil {
		return err
	}
	rmdirSyncVal, err := rmdirSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("rmdirSync", rmdirSyncVal); err != nil {
		return err
	}

	// fs.unlinkSync
	unlinkSyncFn, err := iso.NewFunctionTemplate(f.unlinkSyncFunc)
	if err != nil {
		return err
	}
	unlinkSyncVal, err := unlinkSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("unlinkSync", unlinkSyncVal); err != nil {
		return err
	}

	// fs.readdirSync
	readdirSyncFn, err := iso.NewFunctionTemplate(f.readdirSyncFunc)
	if err != nil {
		return err
	}
	readdirSyncVal, err := readdirSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("readdirSync", readdirSyncVal); err != nil {
		return err
	}

	// fs.statSync
	statSyncFn, err := iso.NewFunctionTemplate(f.statSyncFunc)
	if err != nil {
		return err
	}
	statSyncVal, err := statSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("statSync", statSyncVal); err != nil {
		return err
	}

	// fs.renameSync
	renameSyncFn, err := iso.NewFunctionTemplate(f.renameSyncFunc)
	if err != nil {
		return err
	}
	renameSyncVal, err := renameSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("renameSync", renameSyncVal); err != nil {
		return err
	}

	// fs.copyFileSync
	copyFileSyncFn, err := iso.NewFunctionTemplate(f.copyFileSyncFunc)
	if err != nil {
		return err
	}
	copyFileSyncVal, err := copyFileSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("copyFileSync", copyFileSyncVal); err != nil {
		return err
	}

	// fs.appendFileSync
	appendFileSyncFn, err := iso.NewFunctionTemplate(f.appendFileSyncFunc)
	if err != nil {
		return err
	}
	appendFileSyncVal, err := appendFileSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("appendFileSync", appendFileSyncVal); err != nil {
		return err
	}

	// Async functions

	// fs.readFile
	readFileFn, err := iso.NewFunctionTemplate(f.readFileFunc)
	if err != nil {
		return err
	}
	readFileVal, err := readFileFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("readFile", readFileVal); err != nil {
		return err
	}

	// fs.writeFile
	writeFileFn, err := iso.NewFunctionTemplate(f.writeFileFunc)
	if err != nil {
		return err
	}
	writeFileVal, err := writeFileFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("writeFile", writeFileVal); err != nil {
		return err
	}

	// Set fs as global module
	return rt.SetGlobal("__fs_module", fsObj)
}

// readFileSyncFunc implements fs.readFileSync
func (f *FS) readFileSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	path := args[0].String()

	// Check for encoding option
	encoding := ""
	if len(args) >= 2 {
		if args[1].IsString() {
			encoding = args[1].String()
		} else if args[1].IsObject() {
			if encVal, err := args[1].Get("encoding"); err == nil && encVal != nil {
				encoding = encVal.String()
			}
		}
	}

	data, err := f.fs().ReadFile(path)
	if err != nil {
		return nil
	}

	if encoding == "utf8" || encoding == "utf-8" {
		val, _ := ctx.NewString(string(data))
		return val
	}

	// Return as string for now
	val, _ := ctx.NewString(string(data))
	return val
}

// writeFileSyncFunc implements fs.writeFileSync
func (f *FS) writeFileSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	path := args[0].String()
	data := args[1].String()

	if err := f.fs().WriteFile(path, []byte(data), 0644); err != nil {
		return nil
	}

	return nil
}

// existsSyncFunc implements fs.existsSync
func (f *FS) existsSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.False()
	}

	if f.fs().Exists(args[0].String()) {
		return ctx.True()
	}
	return ctx.False()
}

// mkdirSyncFunc implements fs.mkdirSync
func (f *FS) mkdirSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	path := args[0].String()
	recursive := false

	if len(args) >= 2 && args[1].IsObject() {
		if recVal, err := args[1].Get("recursive"); err == nil && recVal != nil {
			recursive = recVal.Boolean()
		}
	}

	var err error
	if recursive {
		err = f.fs().MkdirAll(path, 0755)
	} else {
		err = f.fs().Mkdir(path, 0755)
	}

	if err != nil {
		return nil
	}

	return nil
}

// rmdirSyncFunc implements fs.rmdirSync
func (f *FS) rmdirSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	path := args[0].String()
	recursive := false

	if len(args) >= 2 && args[1].IsObject() {
		if recVal, err := args[1].Get("recursive"); err == nil && recVal != nil {
			recursive = recVal.Boolean()
		}
	}

	var err error
	if recursive {
		err = f.fs().RemoveAll(path)
	} else {
		err = f.fs().Remove(path)
	}

	if err != nil {
		return nil
	}

	return nil
}

// unlinkSyncFunc implements fs.unlinkSync
func (f *FS) unlinkSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	if err := f.fs().Remove(args[0].String()); err != nil {
		return nil
	}

	return nil
}

// readdirSyncFunc implements fs.readdirSync
func (f *FS) readdirSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	entries, err := f.fs().ReadDir(args[0].String())
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(entries))
	for i, entry := range entries {
		name, _ := ctx.NewString(entry.Name)
		arr.SetIndex(i, name)
	}

	return arr
}

// statSyncFunc implements fs.statSync
func (f *FS) statSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	stat, err := f.fs().Stat(args[0].String())
	if err != nil {
		return nil
	}

	return f.createStatObject(ctx, stat)
}

// createStatObject creates a stat object from FileInfo
func (f *FS) createStatObject(ctx *v8go.Context, stat *filesystem.FileInfo) *v8go.Value {
	obj, _ := ctx.NewObject()

	// Basic properties
	obj.Set("size", ctx.NewNumber(float64(stat.Size)))
	obj.Set("mtime", ctx.NewNumber(float64(stat.ModTime.UnixMilli())))
	obj.Set("atime", ctx.NewNumber(float64(stat.ModTime.UnixMilli())))
	obj.Set("ctime", ctx.NewNumber(float64(stat.ModTime.UnixMilli())))
	obj.Set("mode", ctx.NewInteger(int64(stat.Mode)))

	isDir := ctx.NewBoolean(stat.IsDir)
	isFile := ctx.NewBoolean(!stat.IsDir)
	obj.Set("_isDirectory", isDir)
	obj.Set("_isFile", isFile)

	return obj
}

// renameSyncFunc implements fs.renameSync
func (f *FS) renameSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	if err := f.fs().Rename(args[0].String(), args[1].String()); err != nil {
		return nil
	}

	return nil
}

// copyFileSyncFunc implements fs.copyFileSync
func (f *FS) copyFileSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	if err := f.fs().Copy(args[0].String(), args[1].String()); err != nil {
		return nil
	}

	return nil
}

// appendFileSyncFunc implements fs.appendFileSync
func (f *FS) appendFileSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	if err := f.fs().AppendFile(args[0].String(), []byte(args[1].String())); err != nil {
		return nil
	}

	return nil
}

// readFileFunc implements fs.readFile (async)
func (f *FS) readFileFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	path := args[0].String()
	var callback *v8go.Value
	encoding := ""

	// Handle (path, callback) or (path, options, callback)
	if len(args) == 2 {
		callback = args[1]
	} else if len(args) >= 3 {
		if args[1].IsString() {
			encoding = args[1].String()
		} else if args[1].IsObject() {
			if encVal, err := args[1].Get("encoding"); err == nil && encVal != nil {
				encoding = encVal.String()
			}
		}
		callback = args[2]
	}

	if callback == nil || !callback.IsFunction() {
		return nil
	}

	ctx := info.Context()
	fsys := f.fs()

	// Execute async
	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		data, err := fsys.ReadFile(path)

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				errVal, _ := ctx.NewString(err.Error())
				callback.Call(nil, errVal, ctx.Null())
			} else {
				var dataVal *v8go.Value
				if encoding == "utf8" || encoding == "utf-8" {
					dataVal, _ = ctx.NewString(string(data))
				} else {
					dataVal, _ = ctx.NewString(string(data))
				}
				callback.Call(nil, ctx.Null(), dataVal)
			}
		})
	}()

	return nil
}

// writeFileFunc implements fs.writeFile (async)
func (f *FS) writeFileFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	path := args[0].String()
	data := args[1].String()
	callback := args[len(args)-1]

	if !callback.IsFunction() {
		return nil
	}

	ctx := info.Context()
	fsys := f.fs()

	// Execute async
	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		err := fsys.WriteFile(path, []byte(data), fs.FileMode(0644))

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				errVal, _ := ctx.NewString(err.Error())
				callback.Call(nil, errVal)
			} else {
				callback.Call(nil, ctx.Null())
			}
		})
	}()

	return nil
}
