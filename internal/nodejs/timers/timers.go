// Package timers implements the Node.js timers module.
package timers

import (
	_ "embed"
	"sync"
	"sync/atomic"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed promises.js
var promisesJS string

// Timers provides timer functionality (setTimeout, setInterval, etc.).
type Timers struct {
	rt     *runtime.Runtime
	nextID uint64
	timers sync.Map // map[uint64]*timerHandle
}

type timerHandle struct {
	id        uint64
	task      *runtime.Task
	callback  *v8.Value
	cancelled bool
}

// New creates a new Timers module.
func New() *Timers {
	return &Timers{}
}

// Name returns the module name.
func (t *Timers) Name() string {
	return "timers"
}

// Register sets up the timer globals.
func (t *Timers) Register(rt *runtime.Runtime) error {
	t.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// setTimeout
	setTimeoutFn, err := iso.NewFunctionTemplate(t.setTimeoutFunc)
	if err != nil {
		return err
	}
	setTimeoutVal, err := setTimeoutFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("setTimeout", setTimeoutVal); err != nil {
		return err
	}

	// clearTimeout
	clearTimeoutFn, err := iso.NewFunctionTemplate(t.clearTimeoutFunc)
	if err != nil {
		return err
	}
	clearTimeoutVal, err := clearTimeoutFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("clearTimeout", clearTimeoutVal); err != nil {
		return err
	}

	// setInterval
	setIntervalFn, err := iso.NewFunctionTemplate(t.setIntervalFunc)
	if err != nil {
		return err
	}
	setIntervalVal, err := setIntervalFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("setInterval", setIntervalVal); err != nil {
		return err
	}

	// clearInterval (same as clearTimeout)
	if err := rt.SetGlobal("clearInterval", clearTimeoutVal); err != nil {
		return err
	}

	// setImmediate
	setImmediateFn, err := iso.NewFunctionTemplate(t.setImmediateFunc)
	if err != nil {
		return err
	}
	setImmediateVal, err := setImmediateFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("setImmediate", setImmediateVal); err != nil {
		return err
	}

	// clearImmediate (same as clearTimeout)
	if err := rt.SetGlobal("clearImmediate", clearTimeoutVal); err != nil {
		return err
	}

	// queueMicrotask (WHATWG global; used by React's SSR renderer and many
	// modern libraries to defer work onto the microtask queue).
	queueMicrotaskFn, err := iso.NewFunctionTemplate(t.queueMicrotaskFunc)
	if err != nil {
		return err
	}
	queueMicrotaskVal, err := queueMicrotaskFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("queueMicrotask", queueMicrotaskVal); err != nil {
		return err
	}

	// Initialize timers/promises
	if _, err := rt.RunScript(promisesJS, "timers/promises.js"); err != nil {
		return err
	}

	return nil
}

// setTimeoutFunc implements setTimeout.
func (t *Timers) setTimeoutFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	callback := args[0]
	if !callback.IsFunction() {
		return nil
	}

	delay := int64(0)
	if len(args) >= 2 {
		delay = args[1].Integer()
	}
	if delay < 0 {
		delay = 0
	}

	// Collect additional arguments to pass to callback
	var callArgs []*v8.Value
	if len(args) > 2 {
		callArgs = args[2:]
	}

	id := atomic.AddUint64(&t.nextID, 1)
	handle := &timerHandle{
		id:       id,
		callback: callback,
	}

	ctx := info.Context()
	task := t.rt.EventLoop().SetTimeout(func() {
		if handle.cancelled {
			return
		}
		t.timers.Delete(id)
		callback.Call(nil, callArgs...)
	}, time.Duration(delay)*time.Millisecond)

	handle.task = task
	t.timers.Store(id, handle)

	// Return the timer ID
	return ctx.NewNumber(float64(id))
}

// clearTimeoutFunc implements clearTimeout/clearInterval/clearImmediate.
func (t *Timers) clearTimeoutFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := uint64(args[0].Integer())
	if val, ok := t.timers.Load(id); ok {
		handle := val.(*timerHandle)
		handle.cancelled = true
		if handle.task != nil {
			handle.task.Cancel()
		}
		t.timers.Delete(id)
	}

	return nil
}

// setIntervalFunc implements setInterval.
func (t *Timers) setIntervalFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	callback := args[0]
	if !callback.IsFunction() {
		return nil
	}

	interval := int64(0)
	if len(args) >= 2 {
		interval = args[1].Integer()
	}
	if interval < 0 {
		interval = 0
	}

	// Collect additional arguments to pass to callback
	var callArgs []*v8.Value
	if len(args) > 2 {
		callArgs = args[2:]
	}

	id := atomic.AddUint64(&t.nextID, 1)
	handle := &timerHandle{
		id:       id,
		callback: callback,
	}

	ctx := info.Context()
	task := t.rt.EventLoop().SetInterval(func() {
		if handle.cancelled {
			return
		}
		callback.Call(nil, callArgs...)
	}, time.Duration(interval)*time.Millisecond)

	handle.task = task
	t.timers.Store(id, handle)

	return ctx.NewNumber(float64(id))
}

// queueMicrotaskFunc implements the global queueMicrotask(callback): it defers
// callback onto the event loop's microtask queue so it runs after the current
// operation completes but before the next macrotask/I/O.
func (t *Timers) queueMicrotaskFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 || !args[0].IsFunction() {
		return ctx.Throw("The \"callback\" argument must be of type function.")
	}

	callback := args[0]
	t.rt.EventLoop().EnqueueMicrotask(func() {
		callback.Call(nil)
	})
	return nil
}

// setImmediateFunc implements setImmediate.
func (t *Timers) setImmediateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	callback := args[0]
	if !callback.IsFunction() {
		return nil
	}

	// Collect additional arguments to pass to callback
	var callArgs []*v8.Value
	if len(args) > 1 {
		callArgs = args[1:]
	}

	id := atomic.AddUint64(&t.nextID, 1)
	handle := &timerHandle{
		id:       id,
		callback: callback,
	}

	ctx := info.Context()
	task := t.rt.EventLoop().SetImmediate(func() {
		if handle.cancelled {
			return
		}
		t.timers.Delete(id)
		callback.Call(nil, callArgs...)
	})

	handle.task = task
	t.timers.Store(id, handle)

	return ctx.NewNumber(float64(id))
}
