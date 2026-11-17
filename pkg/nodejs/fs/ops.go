package fs

import (
	"encoding/base64"
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// Each filesystem call is an fsOp: it reads its arguments on the JavaScript thread and
// returns the work to do, which uses only Go values. The synchronous form (fs.mkdirSync)
// does that work at once; the asynchronous form (fs.__mkdirAsync, behind fs.mkdir) does it
// on another goroutine, as Node does on its threadpool, and calls back on the event loop.

// opResult is what a call's work produced: a value to build on the JavaScript thread, or a
// failure to describe as Node's error.
type opResult struct {
	value   func(ctx *v8.Context) *v8.Value // nil for undefined
	err     error
	syscall string
	path    string
	dest    string // set for calls on two paths, whose errors name both
}

// fsOp reads a call's arguments. It returns the work, and the descriptor the call uses, or
// -1: calls on one descriptor run one at a time and in the order they were made. A non-nil
// thrown is thrown at once instead.
type fsOp func(ctx *v8.Context, args []*v8.Value) (work func() opResult, fd int, thrown *v8.Value)

func failed(err error, syscallName, path string) opResult {
	return opResult{err: err, syscall: syscallName, path: path}
}

// describe builds the value of a finished call, or the fsFailure that fs_setup.js turns into
// Node's error.
func (f *FS) describe(ctx *v8.Context, r opResult) *v8.Value {
	if r.err != nil {
		if r.dest != "" {
			return fsFailureTo(ctx, r.err, r.syscall, r.path, r.dest)
		}
		return fsFailure(ctx, r.err, r.syscall, r.path)
	}
	if r.value == nil {
		return nil
	}
	return r.value(ctx)
}

// syncCall is the synchronous form of op.
func (f *FS) syncCall(op fsOp) v8.FunctionCallback {
	return func(info *v8.FunctionCallbackInfo) *v8.Value {
		ctx := info.Context()
		work, fd, thrown := op(ctx, info.Args())
		if thrown != nil {
			return thrown
		}
		var r opResult
		f.onDescriptor(fd, func() { r = work() })
		return f.describe(ctx, r)
	}
}

// asyncCall is the asynchronous form of op, which takes a callback after op's arguments and
// calls it with (failure, value): failure is null or an fsFailure.
func (f *FS) asyncCall(op fsOp) v8.FunctionCallback {
	return func(info *v8.FunctionCallbackInfo) *v8.Value {
		ctx := info.Context()
		args := info.Args()
		if len(args) == 0 || !args[len(args)-1].IsFunction() {
			return ctx.Throw("fs: a callback is required")
		}
		callback := args[len(args)-1]
		work, fd, thrown := op(ctx, args[:len(args)-1])
		if thrown != nil {
			return thrown
		}
		loop := f.rt.EventLoop()
		loop.AddPendingWork()
		f.schedule(fd, func() {
			defer loop.DonePendingWork()
			r := work()
			loop.EnqueueMicrotask(func() {
				value := f.describe(ctx, r)
				if value == nil {
					value = ctx.Undefined()
				}
				if r.err != nil {
					f.rt.MakeCallback(callback, nil, value)
					return
				}
				f.rt.MakeCallback(callback, nil, ctx.Null(), value)
			})
		})
		return nil
	}
}

// fdQueue runs the calls on one descriptor one at a time, in the order they were made, so
// writes land in order and a positioned read or write doesn't move another's offset.
type fdQueue struct {
	busy    sync.Mutex // held while a call on the descriptor runs
	mu      sync.Mutex
	pending []func()
	running bool
}

func (f *FS) queueFor(fd int) *fdQueue {
	f.queueMu.Lock()
	defer f.queueMu.Unlock()
	q := f.queues[fd]
	if q == nil {
		q = &fdQueue{}
		f.queues[fd] = q
	}
	return q
}

// onDescriptor runs work now, after any call on fd that is running; fd -1 means none.
func (f *FS) onDescriptor(fd int, work func()) {
	if fd < 0 {
		work()
		return
	}
	q := f.queueFor(fd)
	q.busy.Lock()
	defer q.busy.Unlock()
	work()
}

// schedule runs work on another goroutine, after the calls on fd made before it.
func (f *FS) schedule(fd int, work func()) {
	if fd < 0 {
		go work()
		return
	}
	q := f.queueFor(fd)
	q.mu.Lock()
	q.pending = append(q.pending, work)
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()
	go func() {
		for {
			q.mu.Lock()
			if len(q.pending) == 0 {
				q.running = false
				q.mu.Unlock()
				return
			}
			next := q.pending[0]
			// The slot is cleared, and an empty queue lets its array go, so a finished
			// call's data isn't kept alive by the queue.
			q.pending[0] = nil
			q.pending = q.pending[1:]
			if len(q.pending) == 0 {
				q.pending = nil
			}
			q.mu.Unlock()
			q.busy.Lock()
			next()
			q.busy.Unlock()
		}
	}()
}

// registerOps adds the sync and async forms of each call.
func (f *FS) registerOps(regFn func(string, v8.FunctionCallback) error) error {
	ops := map[string]fsOp{
		"mkdir":      f.mkdirOp,
		"rmdir":      f.rmdirOp,
		"unlink":     f.unlinkOp,
		"readdir":    f.readdirOp,
		"rename":     f.renameOp,
		"copyFile":   f.copyFileOp,
		"open":       f.openOp,
		"close":      f.closeOp,
		"write":      f.writeOp,
		"read":       f.readOp,
		"fstat":      f.fstatOp,
		"__rmTarget": f.rmTargetOp,
		"truncate":   f.truncateOp,
		"ftruncate":  f.ftruncateOp,
		"fsync":      f.fsyncOp("fsync"),
		"fdatasync":  f.fsyncOp("fdatasync"),
		"chmod":      f.chmodOp,
		"fchmod":     f.fchmodOp,
		"chown":      f.chownOp,
		"fchown":     f.fchownOp,
		"utimes":     f.utimesOp,
		"futimes":    f.futimesOp,
		"statfs":     f.statfsOp,
		"access":     f.accessOp,
		"readv":      f.readvOp,
		"writev":     f.writevOp,
		"symlink":    f.symlinkOp,
		"readlink":   f.readlinkOp,
		"link":       f.linkOp,
		"realpath":   f.realpathOp,
		"mkdtemp":    f.mkdtempOp,
	}
	for name, op := range ops {
		if err := regFn(name+"Sync", f.syncCall(op)); err != nil {
			return err
		}
		if err := regFn("__"+trimUnderscores(name)+"Async", f.asyncCall(op)); err != nil {
			return err
		}
	}
	// The asynchronous close of a pipe also waits for the reads queued on it before it.
	return regFn("__closeAsync", f.asyncCall(f.closeAfterReadsOp))
}

func trimUnderscores(name string) string {
	for len(name) > 0 && name[0] == '_' {
		name = name[1:]
	}
	return name
}

// stringArg reads a path or other string argument. A path whose bytes aren't UTF-8 comes as
// { __pathBytes: base64 }, since a string can't carry them (fs_setup.js, asPaths).
func stringArg(args []*v8.Value, i int) string {
	if i >= len(args) || args[i] == nil {
		return ""
	}
	if args[i].IsObject() {
		if b, err := args[i].Get("__pathBytes"); err == nil && b != nil && b.IsString() {
			if raw, err := base64.StdEncoding.DecodeString(b.String()); err == nil {
				return string(raw)
			}
		}
	}
	return args[i].String()
}

func intAt(args []*v8.Value, i int) int64 {
	if i < len(args) && args[i] != nil {
		return args[i].Integer()
	}
	return 0
}

func fdAt(args []*v8.Value) int {
	return int(intAt(args, 0))
}

// mkdirOp backs mkdir(path, { recursive }, mode); fs_setup.js passes the mode, already
// checked, as a number, and Node's default is 0o777.
func (f *FS) mkdirOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	recursive := false
	if len(args) >= 2 && args[1].IsObject() {
		if v, err := args[1].Get("recursive"); err == nil && v != nil {
			recursive = v.Boolean()
		}
	}
	perm := fs.FileMode(0o777)
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		perm = dirMode(args[2].Integer())
	}
	fsys := f.fs()
	return func() opResult {
		var err error
		if recursive {
			err = fsys.MkdirAll(path, perm)
		} else {
			err = fsys.Mkdir(path, perm)
		}
		return failed(err, "mkdir", path)
	}, -1, nil
}

// rmdirOp backs rmdir(path, { recursive }). Remove deletes files too, but rmdir only takes a
// directory: anything else, a symbolic link included, is ENOTDIR and kept, as in Node.
// Node's recursive form looks first with lstat, so a missing path is reported as lstat.
func (f *FS) rmdirOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	recursive := false
	if len(args) >= 2 && args[1].IsObject() {
		if v, err := args[1].Get("recursive"); err == nil && v != nil {
			recursive = v.Boolean()
		}
	}
	fsys := f.fs()
	return func() opResult {
		stat, err := runtime.Lstat(fsys, path)
		if err != nil {
			if recursive {
				return failed(err, "lstat", path)
			}
			return failed(err, "rmdir", path)
		}
		if !stat.IsDir {
			return failed(syscall.ENOTDIR, "rmdir", path)
		}
		if recursive {
			err = fsys.RemoveAll(path)
		} else {
			err = fsys.Remove(path)
		}
		return failed(err, "rmdir", path)
	}, -1, nil
}

func (f *FS) unlinkOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		return failed(fsys.Remove(path), "unlink", path)
	}, -1, nil
}

// readdirOp backs readdir(path[, { withFileTypes }]): names, or { name, type } objects that
// fs_setup.js makes into fs.Dirent objects.
func (f *FS) readdirOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	withFileTypes := false
	if len(args) >= 2 && args[1] != nil && args[1].IsObject() {
		if v, err := args[1].Get("withFileTypes"); err == nil && v != nil {
			withFileTypes = v.Boolean()
		}
	}
	fsys := f.fs()
	return func() opResult {
		// Reading a directory opens it while it runs.
		release, err := f.rt.OpenFile()
		if err != nil {
			return failed(err, "scandir", path)
		}
		entries, err := fsys.ReadDir(path)
		release()
		if err != nil {
			return failed(err, "scandir", path)
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value {
			arr, _ := ctx.NewArray(len(entries))
			for i, entry := range entries {
				name, _ := ctx.NewString(entry.Name)
				if !withFileTypes {
					arr.SetIndex(i, name)
					continue
				}
				obj, _ := ctx.NewObject()
				obj.Set("name", name)
				obj.Set("type", ctx.NewInteger(direntType(entry)))
				arr.SetIndex(i, obj)
			}
			return arr
		}}
	}, -1, nil
}

func (f *FS) renameOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	from, to := stringArg(args, 0), stringArg(args, 1)
	fsys := f.fs()
	return func() opResult {
		if err := fsys.Rename(from, to); err != nil {
			return opResult{err: err, syscall: "rename", path: from, dest: to}
		}
		return opResult{}
	}, -1, nil
}

// copyFileOp backs copyFile(src, dest[, mode]); COPYFILE_EXCL refuses to replace dest.
func (f *FS) copyFileOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	src, dest := stringArg(args, 0), stringArg(args, 1)
	excl := len(args) >= 3 && args[2].IsNumber() && args[2].Integer()&1 != 0
	fsys := f.fs()
	return func() opResult {
		if excl && fsys.Exists(dest) {
			return opResult{err: syscall.EEXIST, syscall: "copyfile", path: src, dest: dest}
		}
		// A copy has both files open while it runs.
		releaseSrc, err := f.rt.OpenFile()
		if err != nil {
			return opResult{err: err, syscall: "copyfile", path: src, dest: dest}
		}
		defer releaseSrc()
		releaseDest, err := f.rt.OpenFile()
		if err != nil {
			return opResult{err: err, syscall: "copyfile", path: src, dest: dest}
		}
		defer releaseDest()
		if err := fsys.Copy(src, dest); err != nil {
			return opResult{err: err, syscall: "copyfile", path: src, dest: dest}
		}
		return opResult{}
	}, -1, nil
}

// openOp backs open(path[, flags[, mode]]) and gives the new descriptor.
func (f *FS) openOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	flags, ok := parseOpenFlags(argAt(args, 1))
	if !ok {
		return nil, -1, ctx.Throw("openSync: invalid flags")
	}
	perm := fs.FileMode(0o666)
	if len(args) >= 3 && args[2] != nil && args[2].IsNumber() {
		perm = fs.FileMode(args[2].Integer())
	}
	fsys := f.fs()
	return func() opResult {
		// The descriptor holds one of Config.MaxOpenFiles until it's closed.
		release, err := f.rt.OpenFile()
		if err != nil {
			return failed(err, "open", path)
		}
		file, err := fsys.OpenFile(path, flags, perm)
		if err != nil {
			release()
			return failed(err, "open", path)
		}
		// A pipe, socket or device reads in a queue of its own (readLane).
		streamLike := false
		if info, err := file.Stat(); err == nil && !info.Mode.IsRegular() && !info.IsDir {
			streamLike = true
		}
		f.fdMu.Lock()
		if f.disposed {
			// The runtime was disposed while this open ran, and its cleanup has
			// already closed the other files: nothing will close this one.
			f.fdMu.Unlock()
			file.Close()
			release()
			return failed(syscall.EBADF, "open", path)
		}
		fd := f.nextFD
		f.nextFD++
		f.fds[fd] = file
		f.fdSlots[fd] = release
		if streamLike {
			f.streams[fd] = true
		}
		f.fdMu.Unlock()
		return opResult{value: func(ctx *v8.Context) *v8.Value { return ctx.NewInteger(int64(fd)) }}
	}, -1, nil
}

// closeAfterReadsOp is closeOp for fs.close and FileHandle.close: on a pipe, socket or
// device, whose reads queue apart (readLane), it first lets the reads made before it finish,
// as Node's close waits for them. closeSync doesn't wait, as Node's doesn't.
func (f *FS) closeAfterReadsOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	work, fd, thrown := f.closeOp(ctx, args)
	if thrown != nil {
		return nil, fd, thrown
	}
	return func() opResult {
		if lane := f.readLane(fd); lane != fd {
			reads := make(chan struct{})
			f.schedule(lane, func() { close(reads) })
			<-reads
		}
		return work()
	}, fd, nil
}

func (f *FS) closeOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	return func() opResult {
		f.fdMu.Lock()
		file, ok := f.fds[fd]
		release := f.fdSlots[fd]
		if ok {
			delete(f.fds, fd)
			delete(f.fdSlots, fd)
		}
		f.fdMu.Unlock()
		if !ok {
			return failed(syscall.EBADF, "close", "")
		}
		if release != nil {
			defer release()
		}
		f.queueMu.Lock()
		delete(f.queues, fd)
		delete(f.queues, fd+readLaneOffset)
		f.fdMu.Lock()
		delete(f.streams, fd)
		f.fdMu.Unlock()
		f.queueMu.Unlock()
		return failed(file.Close(), "close", "")
	}, fd, nil
}

// writeOp backs write(fd, base64Data, position) and gives the bytes written. Descriptors 1
// and 2 are the process's own stdout and stderr, as process.stdout uses.
func (f *FS) writeOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	data, err := decodeData(argAt(args, 1))
	if err != nil {
		return nil, -1, ctx.Throw(err.Error())
	}
	position := intAt(args, 2)
	return func() opResult {
		var n int
		var err error
		if file, ok := f.fileForFD(fd); ok {
			n, err = atPosition(file, position, func() (int, error) { return file.Write(data) })
		} else if std := map[int]*os.File{1: os.Stdout, 2: os.Stderr}[fd]; std != nil {
			n, err = std.Write(data)
		} else {
			err = syscall.EBADF
		}
		if err != nil {
			return failed(err, "write", "")
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value { return ctx.NewInteger(int64(n)) }}
	}, fd, nil
}

// readOp backs read(fd, length, position) and gives the bytes read as base64, empty at the
// end of the file.
func (f *FS) readOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	length := intAt(args, 1)
	position := intAt(args, 2)
	return func() opResult {
		file, ok := f.fileForFD(fd)
		if !ok {
			return failed(syscall.EBADF, "read", "")
		}
		buf := make([]byte, length)
		n, err := atPosition(file, position, func() (int, error) { return file.Read(buf) })
		if err != nil && err != io.EOF && n == 0 {
			return failed(err, "read", "")
		}
		contents := fileContents(buf[:n], false)
		return opResult{value: func(ctx *v8.Context) *v8.Value {
			v, _ := ctx.NewString(contents)
			return v
		}}
	}, f.readLane(fd), nil
}

func (f *FS) fstatOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	return func() opResult {
		file, ok := f.fileForFD(fd)
		if !ok {
			return failed(syscall.EBADF, "fstat", "")
		}
		stat, err := file.Stat()
		if err != nil {
			return failed(err, "fstat", "")
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value { return f.createStatObject(ctx, stat) }}
	}, fd, nil
}

// rmTargetOp describes the path rm is to remove, as Node's rm does with lstat: a symbolic
// link is described, and so removed, itself.
func (f *FS) rmTargetOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		stat, err := runtime.Lstat(fsys, path)
		if err != nil {
			return failed(err, "lstat", path)
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value { return f.createStatObject(ctx, stat) }}
	}, -1, nil
}

// vectorAt reads an array argument as its items.
func vectorAt(args []*v8.Value, i int) []*v8.Value {
	v := argAt(args, i)
	if v == nil || !v.IsArray() {
		return nil
	}
	items := make([]*v8.Value, v.ArrayLength())
	for j := range items {
		items[j], _ = v.GetIndex(j)
	}
	return items
}

// writevOp backs writev(fd, [base64Data...], position) and gives the bytes written. The
// buffers are written in one call on the descriptor, so a close made after it waits for all
// of them. A position of -1 means the file's offset.
func (f *FS) writevOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	// The buffers go out in one write, as writev(2) sends them, so another writer to a pipe
	// can't come between them.
	var all []byte
	for _, item := range vectorAt(args, 1) {
		data, err := decodeData(item)
		if err != nil {
			return nil, -1, ctx.Throw(err.Error())
		}
		all = append(all, data...)
	}
	position := intAt(args, 2)
	return func() opResult {
		if len(all) == 0 {
			return opResult{value: func(ctx *v8.Context) *v8.Value { return ctx.NewInteger(0) }}
		}
		// Descriptors 1 and 2 are the process's own stdout and stderr, as for write.
		var n int
		var err error
		if file, ok := f.fileForFD(fd); ok {
			n, err = atPosition(file, position, func() (int, error) { return file.Write(all) })
		} else if std := map[int]*os.File{1: os.Stdout, 2: os.Stderr}[fd]; std != nil {
			n, err = std.Write(all)
		} else {
			err = syscall.EBADF
		}
		if err != nil {
			return failed(err, "write", "")
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value { return ctx.NewInteger(int64(n)) }}
	}, fd, nil
}

// readvOp backs readv(fd, [length...], position) and gives the bytes read into each buffer,
// as base64. Like readv(2), it reads once, as much as is there up to the buffers' total, and
// splits that across them, so a pipe with less waiting doesn't hold it up. A position of -1
// means the file's offset.
func (f *FS) readvOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	var lengths []int64
	var total int64
	for _, item := range vectorAt(args, 1) {
		lengths = append(lengths, item.Integer())
		total += item.Integer()
	}
	position := intAt(args, 2)
	return func() opResult {
		file, ok := f.fileForFD(fd)
		if !ok {
			return failed(syscall.EBADF, "read", "")
		}
		buf := make([]byte, total)
		n, err := atPosition(file, position, func() (int, error) { return file.Read(buf) })
		if err != nil && err != io.EOF && n == 0 {
			return failed(err, "read", "")
		}
		var parts []string
		data := buf[:n]
		for _, length := range lengths {
			take := min(length, int64(len(data)))
			parts = append(parts, fileContents(data[:take], false))
			data = data[take:]
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value {
			arr, _ := ctx.NewArray(len(parts))
			for i, part := range parts {
				v, _ := ctx.NewString(part)
				arr.SetIndex(i, v)
			}
			return arr
		}}
	}, f.readLane(fd), nil
}

// readLaneOffset keys the queue of reads on a pipe, socket or device apart from its other
// calls.
const readLaneOffset = 1 << 30

// readLane is the queue a read on fd joins. On a regular file, reads and writes share one,
// so positioned calls don't move each other's offset. On a pipe, socket or device, a read
// may wait for data a later write supplies, so reads queue on their own and a write isn't
// held up behind them.
// The open that made the descriptor records which kind it is, off the thread for fs.open.
func (f *FS) readLane(fd int) int {
	f.fdMu.Lock()
	defer f.fdMu.Unlock()
	if f.streams[fd] {
		return fd + readLaneOffset
	}
	return fd
}
