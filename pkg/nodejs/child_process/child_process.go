// Package child_process implements the Node.js child_process module.
package child_process

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"

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

// childProc wraps a spawned process with its stdio streams.
type childProc struct {
	proc   runtime.ChildProcess
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	// A process may exit before its output has been read, so its entry stays until
	// it has exited and JavaScript has closed both output streams. Guarded by mu.
	exited     bool
	stdoutDone bool
	stderrDone bool
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
		"spawn":        c.spawnFunc,
		"exec":         c.execFunc,
		"execFile":     c.execFileFunc,
		"execSync":     c.execSyncFunc,
		"execFileSync": c.execFileSyncFunc,
		"spawnSync":    c.spawnSyncFunc,
		"kill":         c.killFunc,
		"wait":         c.waitFunc,
		"read":         c.readFunc,
		"closeOutput":  c.closeOutputFunc,
		"write":        c.writeFunc,
		"closeStdin":   c.closeStdinFunc,
		"ref":          c.refFunc,
		"unref":        c.unrefFunc,
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
	return proc, ok && proc.proc != nil
}

// releaseIfDone forgets a process once nothing can use it. The caller holds mu.
func (c *ChildProcess) releaseIfDone(id int64, proc *childProc) {
	if proc.exited && proc.stdoutDone && proc.stderrDone {
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

// decodeInput reads the base64 stdin data of the buffered calls. JavaScript encodes it,
// so it is always valid.
func decodeInput(encoded string) []byte {
	input, _ := base64.StdEncoding.DecodeString(encoded)
	return input
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

// spawnOptions contains options for spawning a child runtime.
type spawnOptions struct {
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
	Shell    bool     `json:"shell"`
	Detached bool     `json:"detached"`
	Input    string   `json:"input"`
}

// spawnFunc handles spawn() calls from JavaScript. As in Node, the process has started
// by the time spawn() returns, so its pid and stdio are available immediately. It
// returns {id, pid}, or {error} if the process could not be started.
func (c *ChildProcess) spawnFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	command := args[0].String()
	cmdArgs := stringArgs(args[1])

	var opts spawnOptions
	json.Unmarshal([]byte(args[2].String()), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd:      opts.Cwd,
		Env:      opts.Env,
		Shell:    opts.Shell,
		Detached: opts.Detached,
	}

	result, _ := ctx.NewObject()
	proc, err := c.rt.ProcessSpawner().Spawn(context.Background(), command, cmdArgs, spawnOpts)
	if err != nil {
		result.Set("error", jsError(ctx, err))
		return result
	}

	id := atomic.AddInt64(&c.processID, 1)
	c.mu.Lock()
	c.processes[id] = &childProc{
		proc:   proc,
		stdin:  proc.Stdin(),
		stdout: proc.Stdout(),
		stderr: proc.Stderr(),
	}
	c.mu.Unlock()

	result.Set("id", ctx.NewNumber(float64(id)))
	result.Set("pid", ctx.NewNumber(float64(proc.Pid())))
	return result
}

// execOptions contains options for exec/execFile operations.
type execOptions struct {
	Cwd       string   `json:"cwd"`
	Env       []string `json:"env"`
	Timeout   int      `json:"timeout"`
	MaxBuffer int      `json:"maxBuffer"`
	Encoding  string   `json:"encoding"`
	Input     string   `json:"input"`
}

// finishBuffered delivers the result of an asynchronous exec or execFile.
func (c *ChildProcess) finishBuffered(callback *v8.Value, stdout, stderr []byte, exitCode int, err error) {
	c.deliver(func(ctx *v8.Context) {
		c.rt.MakeCallback(callback, ctx.Undefined(), jsError(ctx, err),
			jsBase64(ctx, stdout), jsBase64(ctx, stderr), ctx.NewNumber(float64(exitCode)))
	})
}

// bufferedResult builds the result object of a synchronous call.
func bufferedResult(ctx *v8.Context, stdout, stderr []byte, exitCode int, err error) *v8.Value {
	result, _ := ctx.NewObject()
	if err != nil {
		result.Set("error", jsError(ctx, err))
	}
	result.Set("stdout", jsBase64(ctx, stdout))
	result.Set("stderr", jsBase64(ctx, stderr))
	result.Set("exitCode", ctx.NewNumber(float64(exitCode)))
	return result
}

// execFunc handles exec() calls from JavaScript.
func (c *ChildProcess) execFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	command := args[0].String()
	optionsJSON := args[1].String()
	callback := args[2]

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		stdout, stderr, exitCode, err := spawner.Exec(context.Background(), command, spawnOpts)
		c.finishBuffered(callback, stdout, stderr, exitCode, err)
	}()

	return nil
}

// execFileFunc handles execFile() calls from JavaScript.
func (c *ChildProcess) execFileFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	file := args[0].String()
	cmdArgs := stringArgs(args[1])
	optionsJSON := args[2].String()
	callback := args[3]

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		stdout, stderr, exitCode, err := spawner.ExecFile(context.Background(), file, cmdArgs, spawnOpts)
		c.finishBuffered(callback, stdout, stderr, exitCode, err)
	}()

	return nil
}

// execSyncFunc handles execSync() calls from JavaScript.
func (c *ChildProcess) execSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Null()
	}

	command := args[0].String()
	optionsJSON := args[1].String()

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd:   opts.Cwd,
		Env:   opts.Env,
		Input: decodeInput(opts.Input),
	}

	spawner := c.rt.ProcessSpawner()
	stdout, stderr, exitCode, err := spawner.Exec(context.Background(), command, spawnOpts)
	return bufferedResult(ctx, stdout, stderr, exitCode, err)
}

// execFileSyncFunc handles execFileSync() calls from JavaScript.
func (c *ChildProcess) execFileSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	file := args[0].String()
	cmdArgs := stringArgs(args[1])
	optionsJSON := args[2].String()

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd:   opts.Cwd,
		Env:   opts.Env,
		Input: decodeInput(opts.Input),
	}

	spawner := c.rt.ProcessSpawner()
	stdout, stderr, exitCode, err := spawner.ExecFile(context.Background(), file, cmdArgs, spawnOpts)
	return bufferedResult(ctx, stdout, stderr, exitCode, err)
}

// spawnSyncFunc handles spawnSync() calls from JavaScript.
func (c *ChildProcess) spawnSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	command := args[0].String()
	cmdArgs := stringArgs(args[1])
	optionsJSON := args[2].String()

	var opts spawnOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &runtime.SpawnOptions{
		Cwd:   opts.Cwd,
		Env:   opts.Env,
		Shell: opts.Shell,
		Input: decodeInput(opts.Input),
	}

	spawner := c.rt.ProcessSpawner()

	// For sync, we use Exec or ExecFile depending on shell option
	var stdout, stderr []byte
	var exitCode int
	var err error

	if opts.Shell {
		fullCmd := command
		for _, arg := range cmdArgs {
			fullCmd += " " + arg
		}
		stdout, stderr, exitCode, err = spawner.Exec(context.Background(), fullCmd, spawnOpts)
	} else {
		stdout, stderr, exitCode, err = spawner.ExecFile(context.Background(), command, cmdArgs, spawnOpts)
	}

	result := bufferedResult(ctx, stdout, stderr, exitCode, err)
	result.Set("pid", ctx.NewNumber(0))
	return result
}

// killFunc handles kill() calls from JavaScript to send signals to a runtime.
func (c *ChildProcess) killFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.False()
	}

	id := int64(args[0].Integer())
	signal := syscall.Signal(args[1].Integer())

	proc, ok := c.lookup(id)
	if !ok {
		return ctx.False()
	}

	err := proc.proc.Kill(signal)
	if err != nil {
		return ctx.False()
	}
	return ctx.True()
}

// waitFunc handles wait() calls from JavaScript to wait for process exit.
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
			c.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, "Process not found"), ctx.NewNumber(-1), ctx.Null())
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		exitCode, err := proc.proc.Wait()

		c.mu.Lock()
		proc.exited = true
		c.releaseIfDone(id, proc)
		c.mu.Unlock()

		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), jsError(ctx, err), ctx.NewNumber(float64(exitCode)), ctx.Null())
		})
	}()

	return nil
}

// readFunc reads one chunk from stdout or stderr when JavaScript asks for more, which
// is how a paused stream applies backpressure to the child. The callback receives the
// chunk as base64, or null at the end of the stream.
func (c *ChildProcess) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	streamType := args[1].String()
	callback := args[2]

	proc, ok := c.lookup(id)
	if !ok {
		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), ctx.Null())
		})
		return nil
	}

	reader := proc.stderr
	if streamType == "stdout" {
		reader = proc.stdout
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

// closeOutputFunc closes stdout or stderr when its stream is finished or destroyed.
func (c *ChildProcess) closeOutputFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	isStdout := args[1].String() == "stdout"

	c.mu.Lock()
	proc, ok := c.processes[id]
	if !ok {
		c.mu.Unlock()
		return nil
	}
	reader := proc.stderr
	if isStdout {
		reader = proc.stdout
		proc.stdoutDone = true
	} else {
		proc.stderrDone = true
	}
	c.releaseIfDone(id, proc)
	c.mu.Unlock()

	if reader != nil {
		reader.Close()
	}
	return nil
}

// writeFunc writes one base64 chunk to stdin. The callback receives (null) or
// (message, code).
func (c *ChildProcess) writeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[2]

	data, decodeErr := base64.StdEncoding.DecodeString(args[1].String())
	if decodeErr != nil {
		return info.Context().Throw("child_process: invalid write data")
	}

	proc, ok := c.lookup(id)
	if !ok {
		c.deliver(func(ctx *v8.Context) {
			c.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, "Process not found"), ctx.Null())
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		_, err := proc.stdin.Write(data)

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

// closeStdinFunc handles closeStdin() calls from JavaScript.
func (c *ChildProcess) closeStdinFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	proc, ok := c.lookup(int64(args[0].Integer()))
	if ok && proc.stdin != nil {
		proc.stdin.Close()
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
