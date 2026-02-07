// Package child_process implements the Node.js child_process module.
package child_process

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"syscall"

	"proto.zip/studio/orbital/pkg/process"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

//go:embed child_process.js
var childProcessJS string

// ChildProcess provides child process spawning.
type ChildProcess struct {
	rt         *runtime.Runtime
	processes  map[int64]*childProc
	processID  int64
	mu         sync.Mutex
}

// childProc wraps a spawned process with its stdio streams.
type childProc struct {
	proc   process.ChildProcess
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
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
	funcs := map[string]v8go.FunctionCallback{
		"spawn":        c.spawnFunc,
		"exec":         c.execFunc,
		"execFile":     c.execFileFunc,
		"execSync":     c.execSyncFunc,
		"execFileSync": c.execFileSyncFunc,
		"spawnSync":    c.spawnSyncFunc,
		"kill":         c.killFunc,
		"wait":         c.waitFunc,
		"read":         c.readFunc,
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

	if err := rt.SetGlobal("__child_process_internal", internal); err != nil {
		return err
	}

	if _, err := rt.RunScript(childProcessJS, "child_process.js"); err != nil {
		return err
	}

	return nil
}

// spawnOptions contains options for spawning a child process.
type spawnOptions struct {
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
	Shell    bool     `json:"shell"`
	Detached bool     `json:"detached"`
}

// spawnFunc handles spawn() calls from JavaScript.
func (c *ChildProcess) spawnFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.NewNumber(-1)
	}

	command := args[0].String()
	jsArgs := args[1]
	optionsJSON := args[2].String()
	callback := args[3]

	// Parse args array
	var cmdArgs []string
	if jsArgs.IsArray() {
		length, _ := jsArgs.Get("length")
		if length != nil {
			l := int(length.Integer())
			for i := 0; i < l; i++ {
				elem, _ := jsArgs.GetIndex(i)
				if elem != nil {
					cmdArgs = append(cmdArgs, elem.String())
				}
			}
		}
	}

	// Parse options
	var opts spawnOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd:      opts.Cwd,
		Env:      opts.Env,
		Shell:    opts.Shell,
		Detached: opts.Detached,
	}

	spawner := c.rt.ProcessSpawner()

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		proc, err := spawner.Spawn(context.Background(), command, cmdArgs, spawnOpts)

		c.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				errStr, _ := ctx.NewString(err.Error())
				callback.Call(ctx.Undefined(), errStr, ctx.NewNumber(0))
				return
			}

			c.mu.Lock()
			id := atomic.AddInt64(&c.processID, 1)
			c.processes[id] = &childProc{
				proc:   proc,
				stdin:  proc.Stdin(),
				stdout: proc.Stdout(),
				stderr: proc.Stderr(),
			}
			c.mu.Unlock()

			callback.Call(ctx.Undefined(), ctx.Null(), ctx.NewNumber(float64(proc.Pid())))
		})
	}()

	// Return a temporary ID that will be replaced
	c.mu.Lock()
	id := atomic.AddInt64(&c.processID, 1)
	c.mu.Unlock()
	return ctx.NewNumber(float64(id))
}

// execOptions contains options for exec/execFile operations.
type execOptions struct {
	Cwd       string   `json:"cwd"`
	Env       []string `json:"env"`
	Timeout   int      `json:"timeout"`
	MaxBuffer int      `json:"maxBuffer"`
	Encoding  string   `json:"encoding"`
}

// execFunc handles exec() calls from JavaScript.
func (c *ChildProcess) execFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	command := args[0].String()
	optionsJSON := args[1].String()
	callback := args[2]

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		stdout, stderr, exitCode, err := spawner.Exec(context.Background(), command, spawnOpts)

		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			var errVal *v8go.Value
			if err != nil {
				errVal, _ = ctx.NewString(err.Error())
			} else {
				errVal = ctx.Null()
			}

			stdoutVal, _ := ctx.NewString(string(stdout))
			stderrVal, _ := ctx.NewString(string(stderr))

			callback.Call(ctx.Undefined(), errVal, stdoutVal, stderrVal, ctx.NewNumber(float64(exitCode)))
		})
	}()

	return nil
}

// execFileFunc handles execFile() calls from JavaScript.
func (c *ChildProcess) execFileFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	file := args[0].String()
	jsArgs := args[1]
	optionsJSON := args[2].String()
	callback := args[3]

	// Parse args array
	var cmdArgs []string
	if jsArgs.IsArray() {
		length, _ := jsArgs.Get("length")
		if length != nil {
			l := int(length.Integer())
			for i := 0; i < l; i++ {
				elem, _ := jsArgs.GetIndex(i)
				if elem != nil {
					cmdArgs = append(cmdArgs, elem.String())
				}
			}
		}
	}

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		stdout, stderr, exitCode, err := spawner.ExecFile(context.Background(), file, cmdArgs, spawnOpts)

		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			var errVal *v8go.Value
			if err != nil {
				errVal, _ = ctx.NewString(err.Error())
			} else {
				errVal = ctx.Null()
			}

			stdoutVal, _ := ctx.NewString(string(stdout))
			stderrVal, _ := ctx.NewString(string(stderr))

			callback.Call(ctx.Undefined(), errVal, stdoutVal, stderrVal, ctx.NewNumber(float64(exitCode)))
		})
	}()

	return nil
}

// execSyncFunc handles execSync() calls from JavaScript.
func (c *ChildProcess) execSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Null()
	}

	command := args[0].String()
	optionsJSON := args[1].String()

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()
	stdout, stderr, exitCode, err := spawner.Exec(context.Background(), command, spawnOpts)

	result, _ := ctx.NewObject()
	if err != nil {
		errStr, _ := ctx.NewString(err.Error())
		result.Set("error", errStr)
	}
	stdoutVal, _ := ctx.NewString(string(stdout))
	stderrVal, _ := ctx.NewString(string(stderr))
	result.Set("stdout", stdoutVal)
	result.Set("stderr", stderrVal)
	result.Set("exitCode", ctx.NewNumber(float64(exitCode)))

	return result
}

// execFileSyncFunc handles execFileSync() calls from JavaScript.
func (c *ChildProcess) execFileSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	file := args[0].String()
	jsArgs := args[1]
	optionsJSON := args[2].String()

	var cmdArgs []string
	if jsArgs.IsArray() {
		length, _ := jsArgs.Get("length")
		if length != nil {
			l := int(length.Integer())
			for i := 0; i < l; i++ {
				elem, _ := jsArgs.GetIndex(i)
				if elem != nil {
					cmdArgs = append(cmdArgs, elem.String())
				}
			}
		}
	}

	var opts execOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd: opts.Cwd,
		Env: opts.Env,
	}

	spawner := c.rt.ProcessSpawner()
	stdout, stderr, exitCode, err := spawner.ExecFile(context.Background(), file, cmdArgs, spawnOpts)

	result, _ := ctx.NewObject()
	if err != nil {
		errStr, _ := ctx.NewString(err.Error())
		result.Set("error", errStr)
	}
	stdoutVal, _ := ctx.NewString(string(stdout))
	stderrVal, _ := ctx.NewString(string(stderr))
	result.Set("stdout", stdoutVal)
	result.Set("stderr", stderrVal)
	result.Set("exitCode", ctx.NewNumber(float64(exitCode)))

	return result
}

// spawnSyncFunc handles spawnSync() calls from JavaScript.
func (c *ChildProcess) spawnSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	command := args[0].String()
	jsArgs := args[1]
	optionsJSON := args[2].String()

	var cmdArgs []string
	if jsArgs.IsArray() {
		length, _ := jsArgs.Get("length")
		if length != nil {
			l := int(length.Integer())
			for i := 0; i < l; i++ {
				elem, _ := jsArgs.GetIndex(i)
				if elem != nil {
					cmdArgs = append(cmdArgs, elem.String())
				}
			}
		}
	}

	var opts spawnOptions
	json.Unmarshal([]byte(optionsJSON), &opts)

	spawnOpts := &process.SpawnOptions{
		Cwd:   opts.Cwd,
		Env:   opts.Env,
		Shell: opts.Shell,
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

	result, _ := ctx.NewObject()
	if err != nil {
		errStr, _ := ctx.NewString(err.Error())
		result.Set("error", errStr)
	}
	stdoutVal, _ := ctx.NewString(string(stdout))
	stderrVal, _ := ctx.NewString(string(stderr))
	result.Set("stdout", stdoutVal)
	result.Set("stderr", stderrVal)
	result.Set("exitCode", ctx.NewNumber(float64(exitCode)))
	result.Set("pid", ctx.NewNumber(0))

	return result
}

// killFunc handles kill() calls from JavaScript to send signals to a process.
func (c *ChildProcess) killFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.False()
	}

	id := int64(args[0].Integer())
	signal := syscall.Signal(args[1].Integer())

	c.mu.Lock()
	proc, ok := c.processes[id]
	c.mu.Unlock()

	if !ok || proc.proc == nil {
		return ctx.False()
	}

	err := proc.proc.Kill(signal)
	if err != nil {
		return ctx.False()
	}
	return ctx.True()
}

// waitFunc handles wait() calls from JavaScript to wait for process exit.
func (c *ChildProcess) waitFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	c.mu.Lock()
	proc, ok := c.processes[id]
	c.mu.Unlock()

	if !ok || proc.proc == nil {
		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			errStr, _ := ctx.NewString("Process not found")
			callback.Call(ctx.Undefined(), errStr, ctx.NewNumber(-1), ctx.Null())
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		exitCode, err := proc.proc.Wait()

		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			var errVal *v8go.Value
			if err != nil {
				errVal, _ = ctx.NewString(err.Error())
			} else {
				errVal = ctx.Null()
			}

			callback.Call(ctx.Undefined(), errVal, ctx.NewNumber(float64(exitCode)), ctx.Null())
		})

		// Clean up
		c.mu.Lock()
		delete(c.processes, id)
		c.mu.Unlock()
	}()

	return nil
}

// readFunc handles read() calls from JavaScript to read from stdout/stderr.
func (c *ChildProcess) readFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	streamType := args[1].String()
	callback := args[2]

	c.mu.Lock()
	proc, ok := c.processes[id]
	c.mu.Unlock()

	if !ok || proc.proc == nil {
		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			errStr, _ := ctx.NewString("Process not found")
			callback.Call(ctx.Undefined(), errStr, ctx.Null())
		})
		return nil
	}

	var reader io.ReadCloser
	if streamType == "stdout" {
		reader = proc.stdout
	} else {
		reader = proc.stderr
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		n, err := reader.Read(buf)

		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			if err != nil {
				callback.Call(ctx.Undefined(), ctx.Null(), ctx.Null())
				return
			}

			dataVal, _ := ctx.NewString(string(buf[:n]))
			callback.Call(ctx.Undefined(), ctx.Null(), dataVal)
		})
	}()

	return nil
}

// writeFunc handles write() calls from JavaScript to write to stdin.
func (c *ChildProcess) writeFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	data := args[1].String()
	callback := args[2]

	c.mu.Lock()
	proc, ok := c.processes[id]
	c.mu.Unlock()

	if !ok || proc.proc == nil {
		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			errStr, _ := ctx.NewString("Process not found")
			callback.Call(ctx.Undefined(), errStr)
		})
		return nil
	}

	c.rt.EventLoop().AddPendingWork()
	go func() {
		defer c.rt.EventLoop().DonePendingWork()

		_, err := proc.stdin.Write([]byte(data))

		c.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := info.Context()
			if err != nil {
				errStr, _ := ctx.NewString(err.Error())
				callback.Call(ctx.Undefined(), errStr)
			} else {
				callback.Call(ctx.Undefined(), ctx.Null())
			}
		})
	}()

	return nil
}

// closeStdinFunc handles closeStdin() calls from JavaScript.
func (c *ChildProcess) closeStdinFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	c.mu.Lock()
	proc, ok := c.processes[id]
	c.mu.Unlock()

	if ok && proc.stdin != nil {
		proc.stdin.Close()
	}

	return nil
}

// refFunc handles ref() calls from JavaScript (no-op for now).
func (c *ChildProcess) refFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// No-op for now
	return nil
}

// unrefFunc handles unref() calls from JavaScript (no-op for now).
func (c *ChildProcess) unrefFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// No-op for now
	return nil
}
