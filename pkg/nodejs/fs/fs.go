// Package fs implements the Node.js fs module.
package fs

import (
	_ "embed"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed promises.js
var promisesJS string

//go:embed fs_setup.js
var fsSetupJS string

// FS provides file system functionality.
type FS struct {
	rt *runtime.Runtime

	// Open file-descriptor table backing the synchronous fd-based API
	// (openSync/writeSync/readSync/closeSync/fstatSync). File descriptors are
	// small integers handed back to JavaScript; 0-2 are reserved for stdio.
	fdMu   sync.Mutex
	fds    map[int]runtime.File
	nextFD int

	// Read-stream table backing fs.createReadStream. The bulk file reads run in
	// Go on a goroutine (off the JS thread) and hand chunks back through the
	// event loop, so the JS layer is only a thin Readable adapter.
	rsMu        sync.Mutex
	readStreams map[int]*readStream
	nextRS      int
}

// readStream is a native read-stream handle: an open file plus optional
// byte-range accounting (start/end from fs.createReadStream options).
type readStream struct {
	file      runtime.File
	remaining int64 // bytes left to read when limited
	limited   bool  // true when an explicit end offset was given
}

// New creates a new FS module.
func New() *FS {
	return &FS{
		fds:         make(map[int]runtime.File),
		nextFD:      3,
		readStreams: make(map[int]*readStream),
		nextRS:      1,
	}
}

// Name returns the module name.
func (f *FS) Name() string {
	return "fs"
}

// fs returns the filesystem from the runtime.
func (f *FS) fs() runtime.Filesystem {
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

	// Synchronous file-descriptor API. Real-world tools (the TypeScript compiler
	// host, etc.) emit output via openSync/writeSync/closeSync rather than
	// writeFileSync, so these must be backed by a genuine fd table.
	regFn := func(name string, cb v8.FunctionCallback) error {
		tmpl, err := iso.NewFunctionTemplate(cb)
		if err != nil {
			return err
		}
		fn, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		return fsObj.Set(name, fn)
	}
	if err := regFn("openSync", f.openSyncFunc); err != nil {
		return err
	}
	if err := regFn("closeSync", f.closeSyncFunc); err != nil {
		return err
	}
	if err := regFn("writeSync", f.writeSyncFunc); err != nil {
		return err
	}
	if err := regFn("readSync", f.readSyncFunc); err != nil {
		return err
	}
	if err := regFn("fstatSync", f.fstatSyncFunc); err != nil {
		return err
	}

	// Native async metadata ops. Node's stat/lstat/access are asynchronous; run
	// the syscall on a goroutine and deliver a Node-shaped result (real Error
	// with a `code`, or a Stats object with predicate methods) on the loop.
	if err := regFn("stat", f.statFunc); err != nil {
		return err
	}
	if err := regFn("lstat", f.statFunc); err != nil {
		return err
	}
	if err := regFn("access", f.accessFunc); err != nil {
		return err
	}

	// Native streaming file I/O backing fs.createReadStream. Opening is
	// synchronous (fast, and lets the JS layer report an error/'open' promptly);
	// the bulk reads are async and byte-range aware.
	if err := regFn("_readStreamOpen", f.readStreamOpenFunc); err != nil {
		return err
	}
	if err := regFn("_readStreamRead", f.readStreamReadFunc); err != nil {
		return err
	}
	if err := regFn("_readStreamClose", f.readStreamCloseFunc); err != nil {
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
	if err := rt.SetGlobal("__fs_module", fsObj); err != nil {
		return err
	}

	// Attach Node fs.Stats predicate methods (isFile/isDirectory/...) to the
	// native stat objects before anything consumes them.
	if _, err := rt.RunScript(fsSetupJS, "fs/fs_setup.js"); err != nil {
		return err
	}

	// Initialize fs/promises
	if _, err := rt.RunScript(promisesJS, "fs/promises.js"); err != nil {
		return err
	}

	return nil
}

// readFileSyncFunc implements fs.readFileSync
func (f *FS) readFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) writeFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) existsSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) mkdirSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) rmdirSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) unlinkSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) readdirSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	entries, err := f.fs().ReadDir(args[0].String())
	if err != nil {
		return nil
	}

	// Detect the { withFileTypes: true } option, which asks for Dirent-like
	// objects (name + type predicates) rather than plain filename strings.
	withFileTypes := false
	if len(args) >= 2 && args[1] != nil && args[1].IsObject() {
		if v, e := args[1].Get("withFileTypes"); e == nil && v != nil {
			withFileTypes = v.Boolean()
		}
	}

	arr, _ := ctx.NewArray(len(entries))
	for i, entry := range entries {
		if withFileTypes {
			obj, _ := ctx.NewObject()
			name, _ := ctx.NewString(entry.Name)
			obj.Set("name", name)
			obj.Set("_isDirectory", ctx.NewBoolean(entry.IsDir))
			obj.Set("_isFile", ctx.NewBoolean(!entry.IsDir))
			arr.SetIndex(i, obj)
		} else {
			name, _ := ctx.NewString(entry.Name)
			arr.SetIndex(i, name)
		}
	}

	return arr
}

// statSyncFunc implements fs.statSync
func (f *FS) statSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) createStatObject(ctx *v8.Context, stat *runtime.FileInfo) *v8.Value {
	obj, _ := ctx.NewObject()

	// Basic properties
	obj.Set("size", ctx.NewNumber(float64(stat.Size)))
	obj.Set("mode", ctx.NewInteger(int64(stat.Mode)))

	// Standard numeric Stats fields. Orbital's FileInfo doesn't track most of
	// these, but they must be present as numbers: the `etag` module's isStats()
	// guard requires `ino` (and `size`) to be numbers before it will hash a
	// Stats object, and `send` calls etag(stat) for file responses.
	obj.Set("dev", ctx.NewNumber(0))
	obj.Set("ino", ctx.NewNumber(0))
	obj.Set("nlink", ctx.NewNumber(1))
	obj.Set("uid", ctx.NewNumber(0))
	obj.Set("gid", ctx.NewNumber(0))
	obj.Set("rdev", ctx.NewNumber(0))
	obj.Set("blksize", ctx.NewNumber(4096))
	blocks := (stat.Size + 511) / 512
	obj.Set("blocks", ctx.NewNumber(float64(blocks)))

	// Timestamps as milliseconds. Node's fs.Stats exposes both the numeric
	// *Ms fields and Date objects (mtime/atime/ctime/birthtime); the Date
	// objects are built in fs_setup.js (__addStatsMethods) since constructing a
	// JS Date is far simpler there than across the V8 boundary. Real packages
	// depend on the Date form: `send` calls stat.mtime.toUTCString() and the
	// `etag` module calls stat.mtime.getTime(). Orbital's FileInfo only tracks
	// ModTime, so atime/ctime/birthtime mirror it.
	ms := float64(stat.ModTime.UnixMilli())
	obj.Set("mtimeMs", ctx.NewNumber(ms))
	obj.Set("atimeMs", ctx.NewNumber(ms))
	obj.Set("ctimeMs", ctx.NewNumber(ms))
	obj.Set("birthtimeMs", ctx.NewNumber(ms))

	isDir := ctx.NewBoolean(stat.IsDir)
	isFile := ctx.NewBoolean(!stat.IsDir)
	obj.Set("_isDirectory", isDir)
	obj.Set("_isFile", isFile)

	return obj
}

// parseOpenFlags maps a Node open-flag string (or numeric flag) to os flags.
func parseOpenFlags(v *v8.Value) (int, bool) {
	if v == nil {
		return os.O_RDONLY, true
	}
	if v.IsNumber() {
		return int(v.Integer()), true
	}
	switch strings.TrimSpace(v.String()) {
	case "r", "rs":
		return os.O_RDONLY, true
	case "r+", "rs+":
		return os.O_RDWR, true
	case "w":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC, true
	case "wx", "xw":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC | os.O_EXCL, true
	case "w+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC, true
	case "wx+", "xw+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_EXCL, true
	case "a":
		return os.O_WRONLY | os.O_APPEND | os.O_CREATE, true
	case "ax", "xa":
		return os.O_WRONLY | os.O_APPEND | os.O_CREATE | os.O_EXCL, true
	case "a+":
		return os.O_RDWR | os.O_APPEND | os.O_CREATE, true
	case "ax+", "xa+":
		return os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_EXCL, true
	default:
		return 0, false
	}
}

// openSyncFunc implements fs.openSync(path[, flags[, mode]]) -> fd.
func (f *FS) openSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("openSync: path is required")
	}
	path := args[0].String()

	var flagArg *v8.Value
	if len(args) >= 2 {
		flagArg = args[1]
	}
	flags, ok := parseOpenFlags(flagArg)
	if !ok {
		return ctx.Throw("openSync: invalid flags")
	}

	perm := fs.FileMode(0666)
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		perm = fs.FileMode(args[2].Integer())
	}

	file, err := f.fs().OpenFile(path, flags, perm)
	if err != nil {
		return ctx.Throw(err.Error())
	}

	f.fdMu.Lock()
	fd := f.nextFD
	f.nextFD++
	f.fds[fd] = file
	f.fdMu.Unlock()

	return ctx.NewInteger(int64(fd))
}

// fileForFD returns the open File for a descriptor, if any.
func (f *FS) fileForFD(fd int) (runtime.File, bool) {
	f.fdMu.Lock()
	defer f.fdMu.Unlock()
	file, ok := f.fds[fd]
	return file, ok
}

// closeSyncFunc implements fs.closeSync(fd).
func (f *FS) closeSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("closeSync: fd is required")
	}
	fd := int(args[0].Integer())

	f.fdMu.Lock()
	file, ok := f.fds[fd]
	if ok {
		delete(f.fds, fd)
	}
	f.fdMu.Unlock()

	if !ok {
		return ctx.Throw("EBADF: bad file descriptor, close")
	}
	if err := file.Close(); err != nil {
		return ctx.Throw(err.Error())
	}
	return nil
}

// writeSyncFunc implements the string form of fs.writeSync:
//
//	writeSync(fd, string[, position[, encoding]])
//
// and a best-effort buffer form writeSync(fd, data[, offset[, length[, position]]])
// where data is coerced to its string contents.
func (f *FS) writeSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("writeSync: fd and data are required")
	}
	fd := int(args[0].Integer())
	file, ok := f.fileForFD(fd)
	if !ok {
		return ctx.Throw("EBADF: bad file descriptor, write")
	}

	data := []byte(args[1].String())

	// Optional explicit position (3rd arg, when numeric) seeks before writing;
	// otherwise the write continues at the current offset.
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		if _, err := file.Seek(args[2].Integer(), 0); err != nil {
			return ctx.Throw(err.Error())
		}
	}

	n, err := file.Write(data)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	return ctx.NewInteger(int64(n))
}

// readSyncFunc implements a minimal fs.readSync(fd, buffer, offset, length, position).
// Orbital buffers are string-backed here, so it returns the bytes read and the
// caller is expected to use the string form where possible.
func (f *FS) readSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("readSync: fd is required")
	}
	fd := int(args[0].Integer())
	file, ok := f.fileForFD(fd)
	if !ok {
		return ctx.Throw("EBADF: bad file descriptor, read")
	}

	length := 0
	if len(args) >= 4 && args[3] != nil && args[3].IsNumber() {
		length = int(args[3].Integer())
	}
	if len(args) >= 5 && args[4] != nil && args[4].IsNumber() {
		if _, err := file.Seek(args[4].Integer(), 0); err != nil {
			return ctx.Throw(err.Error())
		}
	}
	if length <= 0 {
		return ctx.NewInteger(0)
	}
	buf := make([]byte, length)
	n, err := file.Read(buf)
	if err != nil && n == 0 {
		return ctx.NewInteger(0)
	}
	return ctx.NewInteger(int64(n))
}

// fstatSyncFunc implements fs.fstatSync(fd).
func (f *FS) fstatSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("fstatSync: fd is required")
	}
	fd := int(args[0].Integer())
	file, ok := f.fileForFD(fd)
	if !ok {
		return ctx.Throw("EBADF: bad file descriptor, fstat")
	}
	stat, err := file.Stat()
	if err != nil {
		return ctx.Throw(err.Error())
	}
	return f.createStatObject(ctx, stat)
}

// renameSyncFunc implements fs.renameSync
func (f *FS) renameSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) copyFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) appendFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (f *FS) readFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	path := args[0].String()
	var callback *v8.Value
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
				var dataVal *v8.Value
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
func (f *FS) writeFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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

// latin1String maps each raw byte to the Unicode code point of the same value,
// producing a valid UTF-8 Go string that survives the crossing into V8 without
// the lossy replacement NewString would apply to invalid UTF-8. The JS side
// recovers the exact bytes with Buffer.from(s, 'latin1'). This is the same
// byte-safe convention the net/http layers use on the wire.
func latin1String(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// callFsHelper invokes a helper function defined on globalThis.__fs_module (set
// up in fs_setup.js) so the Go async layer can build Node-shaped values (Error
// objects, Stats with predicate methods) without reimplementing them in Go.
func (f *FS) callFsHelper(ctx *v8.Context, name string, args ...*v8.Value) *v8.Value {
	g, err := ctx.Global()
	if err != nil || g == nil {
		return nil
	}
	fsMod, err := g.Get("__fs_module")
	if err != nil || fsMod == nil {
		return nil
	}
	fn, err := fsMod.Get(name)
	if err != nil || fn == nil || !fn.IsFunction() {
		return nil
	}
	res, err := fn.Call(fsMod, args...)
	if err != nil {
		return nil
	}
	return res
}

// makeFsError builds a real JS Error (via fs.__makeFsError) with a Node errno
// `code` attached, falling back to a bare string if the helper is unavailable.
func (f *FS) makeFsError(ctx *v8.Context, code, message, path, syscall string) *v8.Value {
	codeVal, _ := ctx.NewString(code)
	msgVal, _ := ctx.NewString(message)
	pathVal, _ := ctx.NewString(path)
	sysVal, _ := ctx.NewString(syscall)
	if e := f.callFsHelper(ctx, "__makeFsError", msgVal, codeVal, pathVal, sysVal); e != nil {
		return e
	}
	fallback, _ := ctx.NewString(message)
	return fallback
}

// statFunc implements the async fs.stat / fs.lstat. Orbital's filesystem does
// not track symlinks separately, so both resolve through the same Stat.
func (f *FS) statFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	path := args[0].String()
	callback := args[len(args)-1]
	if !callback.IsFunction() {
		return nil
	}

	ctx := info.Context()
	fsys := f.fs()

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		stat, err := fsys.Stat(path)

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				code := "ENOENT"
				if fsys.Exists(path) {
					code = "EACCES"
				}
				errVal := f.makeFsError(ctx, code, err.Error(), path, "stat")
				callback.Call(nil, errVal, ctx.Null())
				return
			}
			statObj := f.createStatObject(ctx, stat)
			f.callFsHelper(ctx, "__addStatsMethods", statObj)
			callback.Call(nil, ctx.Null(), statObj)
		})
	}()

	return nil
}

// accessFunc implements the async fs.access. Only existence is checked (the R/W/X
// mode bits are ignored), which is what send/express.static rely on.
func (f *FS) accessFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	path := args[0].String()
	callback := args[len(args)-1]
	if !callback.IsFunction() {
		return nil
	}

	ctx := info.Context()
	fsys := f.fs()

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		exists := fsys.Exists(path)

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if exists {
				callback.Call(nil, ctx.Null())
				return
			}
			errVal := f.makeFsError(ctx, "ENOENT", "ENOENT: no such file or directory, access '"+path+"'", path, "access")
			callback.Call(nil, errVal)
		})
	}()

	return nil
}

// readStreamOpenFunc opens a file for streaming and returns { id } on success or
// { error, code } on failure. start/end (both -1 when unset) select a byte range
// with an inclusive end, matching Node's fs.createReadStream semantics.
func (f *FS) readStreamOpenFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	path := args[0].String()

	start := int64(-1)
	if len(args) >= 2 && args[1] != nil && args[1].IsNumber() {
		start = args[1].Integer()
	}
	end := int64(-1)
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		end = args[2].Integer()
	}

	result, _ := ctx.NewObject()

	file, err := f.fs().OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		code := "ENOENT"
		if f.fs().Exists(path) {
			code = "EACCES"
		}
		errStr, _ := ctx.NewString(err.Error())
		codeStr, _ := ctx.NewString(code)
		result.Set("error", errStr)
		result.Set("code", codeStr)
		return result
	}

	if start > 0 {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			file.Close()
			errStr, _ := ctx.NewString(err.Error())
			codeStr, _ := ctx.NewString("EINVAL")
			result.Set("error", errStr)
			result.Set("code", codeStr)
			return result
		}
	}

	h := &readStream{file: file}
	if end >= 0 {
		s := start
		if s < 0 {
			s = 0
		}
		h.limited = true
		h.remaining = end - s + 1
		if h.remaining < 0 {
			h.remaining = 0
		}
	}

	f.rsMu.Lock()
	id := f.nextRS
	f.nextRS++
	f.readStreams[id] = h
	f.rsMu.Unlock()

	result.Set("id", ctx.NewInteger(int64(id)))
	return result
}

// readStreamReadFunc reads the next chunk from a read-stream handle on a
// goroutine and delivers (err, chunk) to the callback. A null chunk signals EOF.
func (f *FS) readStreamReadFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	id := int(args[0].Integer())
	size := int(args[1].Integer())
	callback := args[2]
	if !callback.IsFunction() {
		return nil
	}
	if size <= 0 {
		size = 65536
	}

	ctx := info.Context()

	f.rsMu.Lock()
	h, ok := f.readStreams[id]
	f.rsMu.Unlock()
	if !ok {
		f.rt.EventLoop().EnqueueMicrotask(func() {
			errVal, _ := ctx.NewString("EBADF: bad file descriptor, read")
			callback.Call(nil, errVal, ctx.Null())
		})
		return nil
	}

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		toRead := size
		if h.limited {
			if h.remaining <= 0 {
				f.rt.EventLoop().EnqueueMicrotask(func() {
					callback.Call(nil, ctx.Null(), ctx.Null())
				})
				return
			}
			if int64(toRead) > h.remaining {
				toRead = int(h.remaining)
			}
		}

		buf := make([]byte, toRead)
		n, err := h.file.Read(buf)
		if h.limited && n > 0 {
			h.remaining -= int64(n)
		}

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if n > 0 {
				dataVal, _ := ctx.NewString(latin1String(buf[:n]))
				callback.Call(nil, ctx.Null(), dataVal)
				return
			}
			if err == nil || err == io.EOF {
				// EOF
				callback.Call(nil, ctx.Null(), ctx.Null())
				return
			}
			errVal, _ := ctx.NewString(err.Error())
			callback.Call(nil, errVal, ctx.Null())
		})
	}()

	return nil
}

// readStreamCloseFunc closes and forgets a read-stream handle.
func (f *FS) readStreamCloseFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int(args[0].Integer())

	f.rsMu.Lock()
	h, ok := f.readStreams[id]
	if ok {
		delete(f.readStreams, id)
	}
	f.rsMu.Unlock()

	if ok && h.file != nil {
		h.file.Close()
	}
	return nil
}
