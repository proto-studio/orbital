// Package child_process implements the Node.js child_process module.
package child_process

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/nodejs/internal/sysconst"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed child_process.js
var childProcessJS string

// ChildProcess provides child process spawning.
//
// All process I/O crosses into JavaScript as base64. Value.String() stops at the first
// NUL and NewString decodes UTF-8, so neither can carry arbitrary bytes directly.
type ChildProcess struct {
	rt        *runtime.Runtime
	processes map[int64]*childProc
	processID int64
	mu        sync.Mutex
}

// childProc wraps a spawned process with the pipes JavaScript can use, by descriptor.
type childProc struct {
	proc    runtime.ChildProcess
	writers map[int]io.WriteCloser
	readers map[int]io.ReadCloser

	// A process may exit before its output has been read, so its entry stays until
	// it has exited and JavaScript has closed every reader. Guarded by mu.
	exited bool
}

// New creates a new ChildProcess module.
func New() *ChildProcess {
	return &ChildProcess{
		processes: make(map[int64]*childProc),
	}
}

// Name returns the module name.
func (c *ChildProcess) Name() string {
	return "child_process"
}

// Register sets up the child_process module.
func (c *ChildProcess) Register(rt *runtime.Runtime) error {
	c.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create internal object
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"spawn":       c.spawnFunc,
		"spawnSync":   c.spawnSyncFunc,
		"kill":        c.killFunc,
		"wait":        c.waitFunc,
		"read":        c.readFunc,
		"closeOutput": c.closeOutputFunc,
		"write":       c.writeFunc,
		"closeInput":  c.closeInputFunc,
		"ref":         c.refFunc,
		"unref":       c.unrefFunc,
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
		if err := internal.Set(name, val); err != nil {
			return err
		}
	}

	// kill() sends real signals, so it needs the host's numbers for their names.
	signals, err := ctx.NewObject()
	if err != nil {
		return err
	}
	for _, sig := range sysconst.Signals() {
		signals.Set(sig.Name, ctx.NewInteger(int64(sig.Value)))
	}
	if err := internal.Set("signals", signals); err != nil {
		return err
	}

	if err := rt.SetGlobal("__child_process_internal", internal); err != nil {
		return err
	}

	if _, err := rt.RunScript(childProcessJS, "child_process.js"); err != nil {
		return err
	}

	return nil
}

// deliver runs fn on the JavaScript thread.
func (c *ChildProcess) deliver(fn func(ctx *v8.Context)) {
	c.rt.EventLoop().EnqueueMicrotask(func() {
		ctx := c.rt.Context()
		fn(ctx)
		ctx.PerformMicrotaskCheckpoint()
	})
}

func (c *ChildProcess) lookup(id int64) (*childProc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	proc, ok := c.processes[id]
	return proc, ok
}

// releaseIfDone forgets a process once nothing can use it. The caller holds mu.
func (c *ChildProcess) releaseIfDone(id int64, proc *childProc) {
	if proc.exited && len(proc.readers) == 0 {
		delete(c.processes, id)
	}
}

func jsString(ctx *v8.Context, s string) *v8.Value {
	v, err := ctx.NewString(s)
	if err != nil {
		return ctx.Null()
	}
	return v
}

func jsError(ctx *v8.Context, err error) *v8.Value {
	if err == nil {
		return ctx.Null()
	}
	return jsString(ctx, err.Error())
}

func jsBase64(ctx *v8.Context, data []byte) *v8.Value {
	return jsString(ctx, base64.StdEncoding.EncodeToString(data))
}

// stringArgs reads a JavaScript array of strings.
func stringArgs(array *v8.Value) []string {
	var out []string
	if array.IsArray() {
		length, _ := array.Get("length")
		if length != nil {
			l := int(length.Integer())
			for i := 0; i < l; i++ {
				elem, _ := array.GetIndex(i)
				if elem != nil {
					out = append(out, elem.String())
				}
			}
		}
	}
	return out
}

// errnoOf finds the error number behind a failed spawn. A spawner is free to return any
// error, so the second result is false for one that has no number.
func errnoOf(err error) (syscall.Errno, bool) {
	var errno syscall.Errno
	switch {
	case errors.As(err, &errno):
		return errno, true
	case errors.Is(err, exec.ErrNotFound):
		return syscall.ENOENT, true
	case errors.Is(err, runtime.ErrCommandNotAllowed), errors.Is(err, fs.ErrPermission):
		return syscall.EACCES, true
	case errors.Is(err, runtime.ErrSpawnBlocked):
		return syscall.EPERM, true
	case errors.Is(err, runtime.ErrTooManyProcesses):
		return syscall.EMFILE, true
	}
	return 0, false
}

// errnoName returns the name Node uses for an error number a spawn can report.
func errnoName(errno syscall.Errno) string {
	if name, ok := sysconst.ErrnoName(errno); ok {
		return name
	}
	return netdial.ErrnoName(errno)
}

// setErrno adds the fields JavaScript builds an error from: errno, code and, for an error
// with no number, detail.
func setErrno(ctx *v8.Context, result *v8.Value, err error) {
	errno, ok := errnoOf(err)
	if !ok {
		result.Set("errno", ctx.NewNumber(-4094)) // UV_UNKNOWN
		result.Set("code", jsString(ctx, "UNKNOWN"))
		result.Set("detail", jsError(ctx, err))
		return
	}
	result.Set("errno", ctx.NewNumber(-float64(errno)))
	result.Set("code", jsString(ctx, errnoName(errno)))
}

// stdioEntry is one element of the stdio array JavaScript has normalized.
type stdioEntry struct {
	Type string `json:"type"` // "pipe", "ignore" or "fd"
	Fd   int    `json:"fd"`
}

// spawnOptions contains the options of spawn and spawnSync.
type spawnOptions struct {
	Cwd      string       `json:"cwd"`
	Env      []string     `json:"env"`
	Shell    bool         `json:"shell"`
	Detached bool         `json:"detached"`
	Stdio    []stdioEntry `json:"stdio"`

	// spawnSync only. JavaScript watches the time and the buffers of an asynchronous
	// child itself.
	Input      string  `json:"input"`
	Timeout    float64 `json:"timeout"`
	MaxBuffer  float64 `json:"maxBuffer"` // 0 or negative means no limit
	KillSignal int     `json:"killSignal"`
}

// fileForFD finds the file a descriptor number in the stdio option refers to. 0-2 are this
// process's own stdio. Others are from fs.openSync, whose numbers do not name real
// descriptors, so script can only hand a child files its filesystem let it open. The second
// result says the caller owns the file and must close it.
func (c *ChildProcess) fileForFD(fd int) (*os.File, bool, error) {
	switch fd {
	case 0:
		return os.Stdin, false, nil
	case 1:
		return os.Stdout, false, nil
	case 2:
		return os.Stderr, false, nil
	}
	if mod, ok := c.rt.GetModule("fs"); ok {
		if table, ok := mod.(interface {
			FileForFD(int) (runtime.File, bool)
		}); ok {
			if file, ok := table.FileForFD(fd); ok {
				// A file from another Filesystem may have no descriptor to pass on.
				if real, ok := file.(interface{ Fd() uintptr }); ok {
					return dupFile(real.Fd())
				}
			}
		}
	}
	return nil, false, syscall.EBADF
}

// dupFile copies a descriptor the fs table owns, so closing the copy leaves it open.
func dupFile(fd uintptr) (*os.File, bool, error) {
	syscall.ForkLock.RLock()
	dup, err := syscall.Dup(int(fd))
	if err == nil {
		syscall.CloseOnExec(dup)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, false, err
	}
	return os.NewFile(uintptr(dup), "stdio"), true, nil
}

// start spawns a process as the options describe, on the JavaScript thread.
func (c *ChildProcess) start(command string, cmdArgs []string, opts *spawnOptions) (runtime.ChildProcess, error) {
	spawnOpts := &runtime.SpawnOptions{
		Cwd:      opts.Cwd,
		Env:      opts.Env,
		Shell:    opts.Shell,
		Detached: opts.Detached,
	}
	for _, entry := range opts.Stdio {
		switch entry.Type {
		case "pipe":
			spawnOpts.Stdio = append(spawnOpts.Stdio, runtime.StdioOption{Mode: runtime.StdioPipe})
		case "fd":
			file, owned, err := c.fileForFD(entry.Fd)
			if err != nil {
				return nil, err
			}
			if owned {
				defer file.Close()
			}
			spawnOpts.Stdio = append(spawnOpts.Stdio, runtime.StdioOption{Mode: runtime.StdioFile, File: file})
		default:
			spawnOpts.Stdio = append(spawnOpts.Stdio, runtime.StdioOption{Mode: runtime.StdioIgnore})
		}
	}
	return c.rt.ProcessSpawner().Spawn(context.Background(), command, cmdArgs, spawnOpts)
}

// pipesOf lists the pipes a started process has, by descriptor.
func pipesOf(proc runtime.ChildProcess, stdio []stdioEntry) (map[int]io.WriteCloser, map[int]io.ReadCloser) {
	writers := make(map[int]io.WriteCloser)
	readers := make(map[int]io.ReadCloser)
	if stdin := proc.Stdin(); stdin != nil {
		writers[0] = stdin
	}
	if stdout := proc.Stdout(); stdout != nil {
		readers[1] = stdout
	}
	if stderr := proc.Stderr(); stderr != nil {
		readers[2] = stderr
	}
	for fd := 3; fd < len(stdio); fd++ {
		if pipe := proc.Pipe(fd); pipe != nil {
			writers[fd] = pipe
			readers[fd] = pipe
		}
	}
	return writers, readers
}

// spawnFunc handles spawn() calls from JavaScript. As in Node, the process has started
// by the time spawn() returns, so its pid and stdio are available immediately. It
// returns {id, pid}, or {errno, code} if the process could not be started.
func (c *ChildProcess) spawnFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	var opts spawnOptions
	if err := json.Unmarshal([]byte(args[2].String()), &opts); err != nil {
		return ctx.Throw("child_process: invalid spawn options")
	}

	result, _ := ctx.NewObject()
	proc, err := c.start(args[0].String(), stringArgs(args[1]), &opts)
	if err != nil {
		setErrno(ctx, result, err)
		return result
	}

	writers, readers := pipesOf(proc, opts.Stdio)
	id := atomic.AddInt64(&c.processID, 1)
	c.mu.Lock()
	c.processes[id] = &childProc{proc: proc, writers: writers, readers: readers}
	c.mu.Unlock()

	result.Set("id", ctx.NewNumber(float64(id)))
	result.Set("pid", ctx.NewNumber(float64(proc.Pid())))
	return result
}

// syncRun is one spawnSync call in progress. It runs on the goroutines serving the
// process, while JavaScript waits.
type syncRun struct {
	proc       runtime.ChildProcess
	readers    map[int]io.ReadCloser
	killSignal syscall.Signal
	maxBuffer  float64

	mu       sync.Mutex
	failure  syscall.Errno
	stopped  bool
	buffered float64 // bytes collected from every descriptor, guarded by mu
}

// fail ends the run early. As in Node it closes the pipes along with killing the process,
// since a process it started may still hold them open.
func (r *syncRun) fail(errno syscall.Errno) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.stopped = true
	r.failure = errno
	r.proc.Kill(r.killSignal)
	for _, reader := range r.readers {
		reader.Close()
	}
}

func (r *syncRun) collect(reader io.Reader) []byte {
	var out []byte
	buf := make([]byte, 65536)
	for {
		n, err := reader.Read(buf)
		out = append(out, buf[:n]...)
		// As in Node, the limit applies to all output together, and 0 means none.
		r.mu.Lock()
		r.buffered += float64(n)
		over := r.maxBuffer > 0 && r.buffered > r.maxBuffer
		r.mu.Unlock()
		if over {
			r.fail(syscall.ENOBUFS)
		}
		if err != nil {
			return out
		}
	}
}

// spawnSyncFunc handles spawnSync() calls from JavaScript. It returns {errno, code} if the
// process could not be started. Otherwise it returns {pid, status, signal, output}, with
// {errno, code} added if the process had to be stopped.
func (c *ChildProcess) spawnSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	var opts spawnOptions
	if err := json.Unmarshal([]byte(args[2].String()), &opts); err != nil {
		return ctx.Throw("child_process: invalid spawn options")
	}
	input, err := base64.StdEncoding.DecodeString(opts.Input)
	if err != nil {
		return ctx.Throw("child_process: invalid input")
	}

	result, _ := ctx.NewObject()
	proc, err := c.start(args[0].String(), stringArgs(args[1]), &opts)
	if err != nil {
		setErrno(ctx, result, err)
		return result
	}

	writers, readers := pipesOf(proc, opts.Stdio)
	run := &syncRun{
		proc:       proc,
		readers:    readers,
		killSignal: syscall.Signal(opts.KillSignal),
		maxBuffer:  opts.MaxBuffer,
	}

	// Nothing waits for these. A child that never reads must not hold up its own exit,
	// and that exit closes the pipes they are using.
	if stdin, ok := writers[0]; ok {
		go func() {
			stdin.Write(input)
			stdin.Close()
		}()
	}
	for fd, writer := range writers {
		if fd != 0 {
			// Only the read side of these is exposed to spawnSync.
			if closer, ok := writer.(interface{ CloseWrite() error }); ok {
				closer.CloseWrite()
			}
		}
	}

	var wg sync.WaitGroup
	var outputMu sync.Mutex
	output := make(map[int][]byte, len(readers))
	for fd, reader := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := run.collect(reader)
			outputMu.Lock()
			output[fd] = data
			outputMu.Unlock()
		}()
	}

	if opts.Timeout > 0 {
		timer := time.AfterFunc(time.Duration(opts.Timeout*float64(time.Millisecond)), func() {
			run.fail(syscall.ETIMEDOUT)
		})
		defer timer.Stop()
	}

	code, _ := proc.Wait()
	wg.Wait()
	for _, reader := range readers {
		reader.Close()
	}

	run.mu.Lock()
	run.stopped = true
	failure := run.failure
	run.mu.Unlock()

	result.Set("pid", ctx.NewNumber(float64(proc.Pid())))
	if signal := proc.ExitSignal(); signal != 0 {
		result.Set("signal", ctx.NewNumber(float64(signal)))
		result.Set("status", ctx.Null())
	} else {
		result.Set("signal", ctx.Null())
		result.Set("status", ctx.NewNumber(float64(code)))
	}
	outputs, _ := ctx.NewObject()
	for fd, data := range output {
		outputs.SetIndex(fd, jsBase64(ctx, data))
	}
	result.Set("output", outputs)
	if failure != 0 {
		setErrno(ctx, result, failure)
	}
	return result
}

// killFunc sends a signal to a process. It returns 0, or the negative error number.
func (c *ChildProcess) killFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	esrch := ctx.NewNumber(-float64(syscall.ESRCH))
	if len(args) < 2 {
		return esrch
	}

	proc, ok := c.lookup(int64(args[0].Integer()))
	if !ok {
		return esrch
	}

	err := proc.proc.Kill(syscall.Signal(args[1].Integer()))
	if err == nil {
		return ctx.NewNumber(0)
	}
	if errors.Is(err, os.ErrProcessDone) {
		return esrch
	}
	if errno, ok := errnoOf(err); ok {
		return ctx.NewNumber(-float64(errno))
	}
	return ctx.NewNumber(-4094) // UV_UNKNOWN
}

// waitFunc handles wait() calls from JavaScript to wait for process exit. The callback
// receives (error, exitCode, signalNumber); the signal number is 0 if no signal ended it.
func (c *ChildProcess) waitFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	proc, ok := c.lookup(id)
	if !ok {
		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, "Process not found"), ctx.NewNumber(-1), ctx.NewNumber(0))
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		exitCode, err := proc.proc.Wait()
		signal := proc.proc.ExitSignal()

		c.mu.Lock()
		proc.exited = true
		c.releaseIfDone(id, proc)
		c.mu.Unlock()

		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), jsError(ctx, err), ctx.NewNumber(float64(exitCode)), ctx.NewNumber(float64(signal)))
		})
	}()

	return nil
}

// readFunc reads one chunk from a descriptor when JavaScript asks for more, which is how a
// paused stream applies backpressure to the child. The callback receives the chunk as
// base64, or null at the end of the stream.
func (c *ChildProcess) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	fd := int(args[1].Integer())
	callback := args[2]

	var reader io.Reader
	c.mu.Lock()
	if proc, ok := c.processes[id]; ok {
		if r, ok := proc.readers[fd]; ok {
			reader = r
		}
	}
	c.mu.Unlock()

	if reader == nil {
		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null())
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		n, _ := reader.Read(buf)

		// A read error other than the end of the stream can only be a closed pipe,
		// and a later read reports it if data came with it.
		c.deliver(func(ctx *v8.Context) {
			if n == 0 {
				c.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null())
				return
			}
			c.rt.MakeCallback(callback, ctx.Undefined(), jsBase64(ctx, buf[:n]))
		})
	}()

	return nil
}

// closeOutputFunc closes a descriptor's read side when its stream is finished or destroyed.
func (c *ChildProcess) closeOutputFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	fd := int(args[1].Integer())

	c.mu.Lock()
	proc, ok := c.processes[id]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	reader, open := proc.readers[fd]
	delete(proc.readers, fd)
	c.releaseIfDone(id, proc)
	c.mu.Unlock()

	if open {
		reader.Close()
	}
	return nil
}

// writeFunc writes one base64 chunk to a descriptor. The callback receives (null) or
// (message, code).
func (c *ChildProcess) writeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	id := int64(args[0].Integer())
	fd := int(args[1].Integer())
	callback := args[3]

	data, decodeErr := base64.StdEncoding.DecodeString(args[2].String())
	if decodeErr != nil {
		return info.Context().Throw("child_process: invalid write data")
	}

	var writer io.Writer
	c.mu.Lock()
	if proc, ok := c.processes[id]; ok {
		if w, ok := proc.writers[fd]; ok {
			writer = w
		}
	}
	c.mu.Unlock()

	if writer == nil {
		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, "Process not found"), ctx.Null())
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		_, err := writer.Write(data)

		c.deliver(func(ctx *v8.Context) {
			switch {
			case err == nil:
				c.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null())
			case errors.Is(err, syscall.EPIPE) || errors.Is(err, os.ErrClosed):
				c.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, "write EPIPE"), jsString(ctx, "EPIPE"))
			default:
				c.rt.MakeCallback(callback, ctx.Undefined(), jsError(ctx, err), ctx.Null())
			}
		})
	}()

	return nil
}

// closeInputFunc ends what JavaScript writes to a descriptor. A pipe above stdio stays
// open for reading.
func (c *ChildProcess) closeInputFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	fd := int(args[1].Integer())

	c.mu.Lock()
	var writer io.WriteCloser
	if proc, ok := c.processes[id]; ok {
		writer = proc.writers[fd]
		delete(proc.writers, fd)
	}
	c.mu.Unlock()

	if writer == nil {
		return nil
	}
	if half, ok := writer.(interface{ CloseWrite() error }); ok {
		half.CloseWrite()
	} else {
		writer.Close()
	}
	return nil
}

// refFunc handles ref() calls from JavaScript (no-op for now).
func (c *ChildProcess) refFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// No-op for now
	return nil
}

// unrefFunc handles unref() calls from JavaScript (no-op for now).
func (c *ChildProcess) unrefFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// No-op for now
	return nil
}
