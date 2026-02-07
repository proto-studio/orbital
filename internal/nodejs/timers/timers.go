// Package timers implements the Node.js timers module.
package timers

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

// Timers provides timer functionality (setTimeout, setInterval, etc.).
type Timers struct {
	rt        *runtime.Runtime
	nextID    uint64
	timers    sync.Map // map[uint64]*timerHandle
}

type timerHandle struct {
	id        uint64
	task      *runtime.Task
	callback  *v8go.Value
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

	return nil
}

// setTimeoutFunc implements setTimeout.
func (t *Timers) setTimeoutFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
	var callArgs []*v8go.Value
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
func (t *Timers) clearTimeoutFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
func (t *Timers) setIntervalFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
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
	var callArgs []*v8go.Value
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

// setImmediateFunc implements setImmediate.
func (t *Timers) setImmediateFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	callback := args[0]
	if !callback.IsFunction() {
		return nil
	}

	// Collect additional arguments to pass to callback
	var callArgs []*v8go.Value
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
