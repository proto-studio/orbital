// Package fs implements the Node.js fs module.
package fs

import (
	_ "embed"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
	fdMu sync.Mutex
	fds  map[int]runtime.File
	// fdSlots give back each descriptor's place under Config.MaxOpenFiles.
	fdSlots map[int]func()
	// disposed is set, under fdMu, once the runtime's cleanup has closed the open files.
	disposed bool
	nextFD  int

	// The calls on each descriptor, which run one at a time and in order (ops.go).
	queueMu sync.Mutex
	queues  map[int]*fdQueue
	// The descriptors of pipes, sockets and devices, recorded when they are opened.
	streams map[int]bool

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
	slot      func() // gives back the stream's place under Config.MaxOpenFiles
	remaining int64  // bytes left to read when limited
	limited   bool   // true when an explicit end offset was given
}

// New creates a new FS module.
func New() *FS {
	return &FS{
		fds:         make(map[int]runtime.File),
		fdSlots:     make(map[int]func()),
		queues:      make(map[int]*fdQueue),
		streams:     make(map[int]bool),
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
	// Files still open when the runtime is disposed close with it, giving back their
	// places under Config.MaxOpenFiles, which a worker shares with its parent.
	rt.TrackResource(closeAll{f})
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
	if err := regFn("lstatSync", f.lstatSyncFunc); err != nil {
		return err
	}
	if err := f.registerOps(regFn); err != nil {
		return err
	}

	// Native async metadata ops. Node's stat/lstat/access are asynchronous; run
	// the syscall on a goroutine and deliver a Node-shaped result (real Error
	// with a `code`, or a Stats object with predicate methods) on the loop.
	if err := regFn("stat", func(info *v8.FunctionCallbackInfo) *v8.Value { return f.statFunc(info, "stat") }); err != nil {
		return err
	}
	if err := regFn("lstat", func(info *v8.FunctionCallbackInfo) *v8.Value { return f.statFunc(info, "lstat") }); err != nil {
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

	// The open flags fs.constants exports, which fs_setup.js adds to it.
	openFlags, err := openFlagConstants(ctx)
	if err != nil {
		return err
	}
	if err := fsObj.Set("__openFlags", openFlags); err != nil {
		return err
	}
	// Whether relative paths name files from the working directory, as on an unrestricted
	// filesystem, rather than from a root.
	if err := fsObj.Set("__cwdRelative", ctx.NewBoolean(runtime.FilesystemIsUnrestricted(rt.Filesystem()))); err != nil {
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

// File contents cross between JavaScript and Go as base64, because Value.String() stops at
// the first NUL and goes through UTF-8. The functions below take and return base64; the
// wrappers in fs_setup.js give them Node's signatures and handle encodings. Only a read that
// asks for UTF-8 text skips base64, since NewString keeps every byte of its input and most
// reads are of source files.

// decodeData reads a base64 argument.
func decodeData(v *v8.Value) ([]byte, error) {
	return base64.StdEncoding.DecodeString(v.String())
}

// fileContents is what a read hands to JavaScript: UTF-8 text or base64.
func fileContents(data []byte, asText bool) string {
	if asText {
		return string(data)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// readFileSyncFunc backs fs.readFileSync(path, asText). On failure it returns an fsFailure.
func (f *FS) readFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	path := stringArg(args, 0)
	release, err := f.rt.OpenFile()
	if err != nil {
		return fsFailure(ctx, err, "open", path)
	}
	data, err := runtime.ReadFileLimited(f.fs(), path, runtime.MaxReadFileSize)
	release()
	if err != nil {
		if tooLarge := fileTooLarge(ctx, err); tooLarge != nil {
			return tooLarge
		}
		return fsFailure(ctx, err, readSyscall(err), path)
	}

	val, _ := ctx.NewString(fileContents(data, args[1].Boolean()))
	return val
}

// writeFileSyncFunc backs fs.writeFileSync(path, base64Data, flag, mode) and
// fs.appendFileSync, which passes an append flag. On failure it returns an fsFailure.
func (f *FS) writeFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	data, err := decodeData(args[1])
	if err != nil {
		return info.Context().Throw(err.Error())
	}

	path := stringArg(args, 0)
	if failed, err := f.writeWithFlags(path, data, argAt(args, 2), modeArg(args, 3)); err != nil {
		return fsFailure(info.Context(), err, failed, path)
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

	if f.fs().Exists(stringArg(args, 0)) {
		return ctx.True()
	}
	return ctx.False()
}

// dirMode turns a Unix mode into Go's FileMode, whose setuid, setgid and sticky bits are not
// the Unix ones.
func dirMode(mode int64) fs.FileMode {
	perm := fs.FileMode(mode & 0o777)
	if mode&0o4000 != 0 {
		perm |= fs.ModeSetuid
	}
	if mode&0o2000 != 0 {
		perm |= fs.ModeSetgid
	}
	if mode&0o1000 != 0 {
		perm |= fs.ModeSticky
	}
	return perm
}

// direntType is libuv's number for an entry's type, such as UV_DIRENT_FILE (1).
func direntType(entry runtime.DirEntry) int64 {
	switch {
	case entry.IsDir:
		return 2
	case entry.Type&fs.ModeSymlink != 0:
		return 3 // UV_DIRENT_LINK
	case entry.Type&fs.ModeNamedPipe != 0:
		return 4 // UV_DIRENT_FIFO
	case entry.Type&fs.ModeSocket != 0:
		return 5 // UV_DIRENT_SOCKET
	case entry.Type&fs.ModeCharDevice != 0:
		return 6 // UV_DIRENT_CHAR
	case entry.Type&fs.ModeDevice != 0:
		return 7 // UV_DIRENT_BLOCK
	}
	return 1
}

// statSyncFunc implements fs.statSync.
func (f *FS) statSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return f.statSync(info, "stat")
}

// lstatSyncFunc implements fs.lstatSync, which describes a final symbolic link itself.
func (f *FS) lstatSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return f.statSync(info, "lstat")
}

// statSync describes a path, or returns an fsFailure naming syscallName that fs_setup.js
// throws as Node's error.
func (f *FS) statSync(info *v8.FunctionCallbackInfo, syscallName string) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	path := stringArg(args, 0)
	stat, err := f.statFor(syscallName)(path)
	if err != nil {
		return fsFailure(ctx, err, syscallName, path)
	}
	return f.createStatObject(ctx, stat)
}

// statFor gives Stat for stat, and Lstat, which doesn't follow a final link, for lstat.
func (f *FS) statFor(syscallName string) func(string) (*runtime.FileInfo, error) {
	fsys := f.fs()
	if syscallName == "lstat" {
		return func(path string) (*runtime.FileInfo, error) { return runtime.Lstat(fsys, path) }
	}
	return fsys.Stat
}

// createStatObject describes stat with Node's fs.Stats fields. fs_setup.js turns it into an
// fs.Stats (__addStatsMethods), which adds the Date fields and the isFile()-style methods.
// What the Filesystem doesn't report gets a plain default, and its times mirror ModTime.
func (f *FS) createStatObject(ctx *v8.Context, stat *runtime.FileInfo) *v8.Value {
	obj, _ := ctx.NewObject()
	num := func(key string, v float64) { obj.Set(key, ctx.NewNumber(v)) }
	ms := func(t time.Time) float64 {
		if t.IsZero() {
			t = stat.ModTime
		}
		return millis(t)
	}

	// These must be numbers even when unknown: the `etag` module's isStats() guard requires
	// `ino` and `size` to be, and `send` calls etag(stat) for file responses.
	dev, ino, nlink, rdev := float64(stat.Dev), float64(stat.Ino), float64(stat.Nlink), float64(stat.Rdev)
	blksize, blocks := float64(stat.Blksize), float64(stat.Blocks)
	if !stat.HasDetails {
		nlink, blksize, blocks = 1, 4096, float64((stat.Size+511)/512)
	}
	num("dev", dev)
	num("mode", float64(posixMode(stat.Mode)))
	num("nlink", nlink)
	num("uid", float64(stat.Uid))
	num("gid", float64(stat.Gid))
	num("rdev", rdev)
	num("blksize", blksize)
	num("ino", ino)
	num("size", float64(stat.Size))
	num("blocks", blocks)
	// The same ten numbers exactly, as decimal strings, for { bigint: true }, since an inode
	// or size past 2^53 doesn't fit a Number.
	exact, _ := ctx.NewArray(10)
	for i, v := range []string{
		strconv.FormatUint(stat.Dev, 10), strconv.FormatUint(uint64(posixMode(stat.Mode)), 10),
		strconv.FormatFloat(nlink, 'f', 0, 64), strconv.Itoa(stat.Uid), strconv.Itoa(stat.Gid),
		strconv.FormatUint(stat.Rdev, 10), strconv.FormatFloat(blksize, 'f', 0, 64),
		strconv.FormatUint(stat.Ino, 10), strconv.FormatInt(stat.Size, 10), strconv.FormatFloat(blocks, 'f', 0, 64),
	} {
		sv, _ := ctx.NewString(v)
		exact.SetIndex(i, sv)
	}
	obj.Set("exact", exact)
	num("atimeMs", ms(stat.AccessTime))
	num("mtimeMs", ms(stat.ModTime))
	num("ctimeMs", ms(stat.ChangeTime))
	num("birthtimeMs", ms(stat.BirthTime))
	// The same times in whole nanoseconds, as decimal strings, for { bigint: true }, since a
	// Number can't hold them exactly.
	nsTimes, _ := ctx.NewArray(4)
	for i, t := range []time.Time{stat.AccessTime, stat.ModTime, stat.ChangeTime, stat.BirthTime} {
		if t.IsZero() {
			t = stat.ModTime
		}
		v, _ := ctx.NewString(nanos(t))
		nsTimes.SetIndex(i, v)
	}
	obj.Set("timesNs", nsTimes)
	return obj
}

// millis gives t in milliseconds since 1970, with a fraction, as fs.Stats does. It works from
// seconds and nanoseconds, since nanoseconds since 1970 overflow an int64 past 2262.
func millis(t time.Time) float64 {
	return float64(t.Unix())*1000 + float64(t.Nanosecond())/1e6
}

// nanos gives t in nanoseconds since 1970 as a decimal string, exactly, past 2262 too.
func nanos(t time.Time) string {
	n := new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(1e9))
	return n.Add(n, big.NewInt(int64(t.Nanosecond()))).String()
}

// POSIX file type and mode bits, as st_mode carries them and Node's fs.Stats reports them.
const (
	sIFIFO  = 0o010000
	sIFCHR  = 0o020000
	sIFDIR  = 0o040000
	sIFBLK  = 0o060000
	sIFREG  = 0o100000
	sIFLNK  = 0o120000
	sIFSOCK = 0o140000
)

// posixMode turns a Go FileMode into the st_mode number Node reports, such as 0o100644 for
// a regular file.
func posixMode(mode fs.FileMode) uint32 {
	m := uint32(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		m |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		m |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		m |= 0o1000
	}
	switch {
	case mode&fs.ModeDir != 0:
		m |= sIFDIR
	case mode&fs.ModeSymlink != 0:
		m |= sIFLNK
	case mode&fs.ModeNamedPipe != 0:
		m |= sIFIFO
	case mode&fs.ModeSocket != 0:
		m |= sIFSOCK
	case mode&fs.ModeCharDevice != 0:
		m |= sIFCHR
	case mode&fs.ModeDevice != 0:
		m |= sIFBLK
	default:
		m |= sIFREG
	}
	return m
}

// openFlagConstants returns the open flags for fs.constants. They are the platform's own
// values, which differ between Linux and macOS, and the ones parseOpenFlags passes a numeric
// flag straight through with, so O_WRONLY | O_APPEND opens for appending.
func openFlagConstants(ctx *v8.Context) (*v8.Value, error) {
	obj, err := ctx.NewObject()
	if err != nil {
		return nil, err
	}
	for _, flag := range []struct {
		name  string
		value int
	}{
		{"O_RDONLY", os.O_RDONLY},
		{"O_WRONLY", os.O_WRONLY},
		{"O_RDWR", os.O_RDWR},
		{"O_CREAT", os.O_CREATE},
		{"O_EXCL", os.O_EXCL},
		{"O_TRUNC", os.O_TRUNC},
		{"O_APPEND", os.O_APPEND},
		{"O_SYNC", os.O_SYNC},
	} {
		if err := obj.Set(flag.name, ctx.NewInteger(int64(flag.value))); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

// parseOpenFlags maps a Node open-flag string (or numeric flag) to os flags. A number is
// made of the flags in fs.constants, which are os's own, so it passes through unchanged.
func parseOpenFlags(v *v8.Value) (int, bool) {
	if v == nil {
		return os.O_RDONLY, true
	}
	if v.IsNumber() {
		return int(v.Integer()), true
	}
	// Node's flags; the s forms add O_SYNC, so a write reaches the disk before it returns.
	switch strings.TrimSpace(v.String()) {
	case "r":
		return os.O_RDONLY, true
	case "rs", "sr":
		return os.O_RDONLY | os.O_SYNC, true
	case "r+":
		return os.O_RDWR, true
	case "rs+", "sr+":
		return os.O_RDWR | os.O_SYNC, true
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
	case "as", "sa":
		return os.O_WRONLY | os.O_APPEND | os.O_CREATE | os.O_SYNC, true
	case "a+":
		return os.O_RDWR | os.O_APPEND | os.O_CREATE, true
	case "ax+", "xa+":
		return os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_EXCL, true
	case "as+", "sa+":
		return os.O_RDWR | os.O_APPEND | os.O_CREATE | os.O_SYNC, true
	default:
		return 0, false
	}
}

// fileForFD returns the open File for a descriptor, if any.
func (f *FS) fileForFD(fd int) (runtime.File, bool) {
	f.fdMu.Lock()
	defer f.fdMu.Unlock()
	file, ok := f.fds[fd]
	return file, ok
}

// FileForFD returns the file open at a descriptor from fs.openSync. These numbers belong to
// this table, so other modules that take a descriptor look it up here.
func (f *FS) FileForFD(fd int) (runtime.File, bool) {
	return f.fileForFD(fd)
}

// atPosition runs op with the file at position, then puts the offset back, because a read or
// write at a position leaves the current offset alone. A negative position means the current
// offset.
func atPosition(file runtime.File, position int64, op func() (int, error)) (int, error) {
	if position < 0 {
		return op()
	}
	saved, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(position, io.SeekStart); err != nil {
		return 0, err
	}
	n, opErr := op()
	if _, err := file.Seek(saved, io.SeekStart); err != nil && opErr == nil {
		opErr = err
	}
	return n, opErr
}

// appendFileSyncFunc backs fs.appendFileSync(path, base64Data, flag, mode). It is
// writeFileSync with Node's default flag for appending.
func (f *FS) appendFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return f.writeFileSyncFunc(info)
}

// readFileFunc backs fs.readFile(path, asText, callback).
func (f *FS) readFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 || !args[2].IsFunction() {
		return nil
	}

	path := stringArg(args, 0)
	asText := args[1].Boolean()
	callback := args[2]

	ctx := info.Context()
	fsys := f.fs()

	// Execute async
	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		// The file holds one of Config.MaxOpenFiles while it's read.
		var data []byte
		release, err := f.rt.OpenFile()
		if err == nil {
			data, err = runtime.ReadFileLimited(fsys, path, runtime.MaxReadFileSize)
			release()
		}
		contents := ""
		if err == nil {
			contents = fileContents(data, asText)
		}

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				if tooLarge := f.asyncFileTooLarge(ctx, err); tooLarge != nil {
					f.rt.MakeCallback(callback, nil, tooLarge, ctx.Null())
					return
				}
				f.rt.MakeCallback(callback, nil, f.asyncFsError(ctx, err, readSyscall(err), path), ctx.Null())
			} else {
				dataVal, _ := ctx.NewString(contents)
				f.rt.MakeCallback(callback, nil, ctx.Null(), dataVal)
			}
		})
	}()

	return nil
}

// writeFileFunc backs fs.writeFile(path, base64Data, flag, mode, callback) and
// fs.appendFile, which passes an append flag.
func (f *FS) writeFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 || !args[4].IsFunction() {
		return nil
	}

	path := stringArg(args, 0)
	encoded := args[1].String()
	flags, flagsOK := parseOpenFlags(args[2])
	perm := modeArg(args, 3)
	callback := args[4]

	ctx := info.Context()
	fsys := f.fs()

	// Execute async
	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		failed := "open"
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && !flagsOK {
			err = syscall.EINVAL
		}
		if err == nil {
			failed, err = f.writeFileCounted(fsys, path, data, flags, perm)
		}

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				f.rt.MakeCallback(callback, nil, f.asyncFsError(ctx, err, failed, path))
			} else {
				f.rt.MakeCallback(callback, nil, ctx.Null())
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

// statFunc implements the async fs.stat and fs.lstat; syscallName says which, and is the
// call an error names.
func (f *FS) statFunc(info *v8.FunctionCallbackInfo, syscallName string) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	path := stringArg(args, 0)
	callback := args[len(args)-1]
	if !callback.IsFunction() {
		return nil
	}

	ctx := info.Context()
	statPath := f.statFor(syscallName)

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		stat, err := statPath(path)

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				f.rt.MakeCallback(callback, nil, f.asyncFsError(ctx, err, syscallName, path), ctx.Null())
				return
			}
			statObj := f.callFsHelper(ctx, "__addStatsMethods", f.createStatObject(ctx, stat))
			f.rt.MakeCallback(callback, nil, ctx.Null(), statObj)
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
	path := stringArg(args, 0)

	start := int64(-1)
	if len(args) >= 2 && args[1] != nil && args[1].IsNumber() {
		start = args[1].Integer()
	}
	end := int64(-1)
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		end = args[2].Integer()
	}

	result, _ := ctx.NewObject()

	release, err := f.rt.OpenFile()
	if err != nil {
		// Node's error at the descriptor limit: "EMFILE: too many open files, open 'path'".
		errStr, _ := ctx.NewString(fsErrorMessage("EMFILE", errnoDescriptions["EMFILE"], "open", path))
		codeStr, _ := ctx.NewString("EMFILE")
		result.Set("error", errStr)
		result.Set("code", codeStr)
		return result
	}
	file, err := f.fs().OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		release()
		code, description, _ := errnoOf(err)
		errStr, _ := ctx.NewString(fsErrorMessage(code, description, "open", path))
		codeStr, _ := ctx.NewString(code)
		result.Set("error", errStr)
		result.Set("code", codeStr)
		return result
	}

	if start > 0 {
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			file.Close()
			release()
			errStr, _ := ctx.NewString(err.Error())
			codeStr, _ := ctx.NewString("EINVAL")
			result.Set("error", errStr)
			result.Set("code", codeStr)
			return result
		}
	}

	h := &readStream{file: file, slot: release}
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
			f.rt.MakeCallback(callback, nil, errVal, ctx.Null())
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
					f.rt.MakeCallback(callback, nil, ctx.Null(), ctx.Null())
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
				f.rt.MakeCallback(callback, nil, ctx.Null(), dataVal)
				return
			}
			if err == nil || err == io.EOF {
				// EOF
				f.rt.MakeCallback(callback, nil, ctx.Null(), ctx.Null())
				return
			}
			errVal, _ := ctx.NewString(err.Error())
			f.rt.MakeCallback(callback, nil, errVal, ctx.Null())
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
	if ok && h.slot != nil {
		h.slot()
	}
	return nil
}

// readSyscall names the call Node reports for a failed read: the one that failed, open or
// read. Reading a directory always fails at read, as in Node, which opens it first.
func readSyscall(err error) string {
	if errors.Is(err, syscall.EISDIR) {
		return "read"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Op == "read" {
		return "read"
	}
	return "open"
}
