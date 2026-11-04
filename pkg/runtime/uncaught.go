package runtime

import (
	_ "embed"
	"errors"
	"fmt"

	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed uncaught.js
var uncaughtJS string

// UncaughtError is what ended a runtime when JavaScript threw, or left a promise rejected,
// and no handler took it. Node would print Message and exit with ExitCode. A library has no
// business doing either to its host, so the runtime stops and hands them to the embedder.
type UncaughtError struct {
	// Message is the text Node prints for the exception: its stack, for an Error.
	Message string
	// ExitCode is the code Node would exit with: 1, 7 if an 'uncaughtException' handler
	// threw, or whatever an 'exit' listener set process.exitCode to.
	ExitCode int
	// Origin is "uncaughtException" or "unhandledRejection".
	Origin string
}

// Error returns the text Node would print.
func (e *UncaughtError) Error() string {
	return e.Message
}

// uncaughtHandlers holds the functions uncaught.js returns.
type uncaughtHandlers struct {
	callback          *v8.Value
	trigger           *v8.Value
	rebuildThrown     *v8.Value
	processRejections *v8.Value
	bindProcess       *v8.Value
	markHandled       *v8.Value
	watchModule       *v8.Value
	runMain           *v8.Value
}

// installUncaught sets up exception handling for a new runtime.
func (rt *Runtime) installUncaught() error {
	factory, err := rt.context.RunScript(uncaughtJS, "node:internal/process/execution")
	if err != nil {
		return err
	}

	native, err := rt.context.NewObject()
	if err != nil {
		return err
	}
	if err := rt.setNative(native, "fatal", rt.fatalFunc); err != nil {
		return err
	}
	if err := rt.setNative(native, "rejectionsPending", func(*v8.FunctionCallbackInfo) *v8.Value {
		rt.rejectionsPending = true
		return nil
	}); err != nil {
		return err
	}

	api, err := factory.Call(nil, native)
	if err != nil {
		return err
	}
	for name, dst := range map[string]**v8.Value{
		"callback":          &rt.uncaught.callback,
		"trigger":           &rt.uncaught.trigger,
		"rebuildThrown":     &rt.uncaught.rebuildThrown,
		"processRejections": &rt.uncaught.processRejections,
		"bindProcess":       &rt.uncaught.bindProcess,
		"markHandled":       &rt.uncaught.markHandled,
		"watchModule":       &rt.uncaught.watchModule,
	} {
		if *dst, err = api.Get(name); err != nil {
			return err
		}
	}

	promiseReject, err := api.Get("promiseReject")
	if err != nil {
		return err
	}
	rt.tracksRejections = rt.context.SetPromiseRejectHandler(promiseReject)

	runMain, err := rt.isolate.NewFunctionTemplate(func(*v8.FunctionCallbackInfo) *v8.Value {
		body := rt.mainBody
		rt.mainBody = nil
		if body != nil {
			body()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if rt.uncaught.runMain, err = runMain.GetFunction(rt.context); err != nil {
		return err
	}

	rt.eventLoop.setDrainedHook(rt.reportRejections)
	return nil
}

func (rt *Runtime) setNative(obj *v8.Value, name string, cb v8.FunctionCallback) error {
	tmpl, err := rt.isolate.NewFunctionTemplate(cb)
	if err != nil {
		return err
	}
	fn, err := tmpl.GetFunction(rt.context)
	if err != nil {
		return err
	}
	return obj.Set(name, fn)
}

// fatalFunc implements native.fatal(message, exitCode, origin): no handler took an exception.
func (rt *Runtime) fatalFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	rt.fatal.Store(&UncaughtError{
		Message:  args[0].String(),
		ExitCode: int(args[1].Integer()),
		Origin:   args[2].String(),
	})
	rt.eventLoop.CancelAllTimers()
	rt.eventLoop.ClearAllMicrotasks()
	rt.eventLoop.Stop()
	// Clearing the loop's queues does not reach V8's promise queue, which V8 runs by itself
	// as soon as the stack empties, and JavaScript that called into Go may still be on the
	// stack. Terminating stops both before they run any more of the program.
	rt.isolate.TerminateExecution()
	return nil
}

// MakeCallback calls a JavaScript function from the event loop, the way node::MakeCallback
// does. Use it for any call Go starts with no JavaScript waiting on its result: if the
// function throws, the exception goes to process.on('uncaughtException'), or else ends the
// runtime. A plain Value.Call would hand the exception to Go, where it stops existing unless
// the caller does this same work. It returns nil if the function threw, or if the runtime
// has already ended. A nil recv means the global object, as it does for Value.Call.
func (rt *Runtime) MakeCallback(fn, recv *v8.Value, args ...*v8.Value) *v8.Value {
	if rt.fatal.Load() != nil || fn == nil || !fn.IsFunction() {
		return nil
	}
	if recv == nil {
		global, err := rt.context.Global()
		if err != nil {
			return nil
		}
		recv = global
	}
	callArgs := make([]*v8.Value, 0, len(args)+2)
	callArgs = append(callArgs, fn, recv)
	callArgs = append(callArgs, args...)
	// The only error left is a terminated isolate, which is reported through IsKilled.
	result, _ := rt.uncaught.callback.Call(nil, callArgs...)
	if rt.fatal.Load() != nil {
		rt.keepTerminating()
		return nil
	}
	return result
}

// keepTerminating asks for termination again. V8 consumes the request once the stack has
// unwound, and a runtime that has ended still has work in its promise queue until
// UncaughtError discards it.
func (rt *Runtime) keepTerminating() {
	rt.isolate.TerminateExecution()
}

// handleUncaught treats a value as an exception nothing caught, as if it had been thrown
// from a callback. It reports whether the runtime survived.
func (rt *Runtime) handleUncaught(thrown *v8.Value) bool {
	if rt.fatal.Load() == nil {
		// trigger reports a handler's exception itself, so this call cannot fail with one.
		_, _ = rt.uncaught.trigger.Call(nil, thrown, rt.context.NewBoolean(false))
	}
	if rt.fatal.Load() != nil {
		rt.keepTerminating()
		return false
	}
	return true
}

// WatchModuleEvaluation gives a rejection of a module's evaluation promise to the handlers
// as soon as it happens, however long the module's open handles keep the event loop running.
func (rt *Runtime) WatchModuleEvaluation(promise *v8.Value) {
	_, _ = rt.uncaught.watchModule.Call(nil, promise)
}

// MarkPromiseHandled stops a promise counting as unhandled, for Go code that reads the
// promise's state itself.
func (rt *Runtime) MarkPromiseHandled(promise *v8.Value) {
	_, _ = rt.uncaught.markHandled.Call(nil, promise)
}

// UncaughtError returns the exception that ended this runtime, or nil. Embedders who run the
// event loop themselves rather than through Run should check it afterwards. Call it with no
// JavaScript on the stack: the first call after the runtime ends throws away the promise
// reactions still queued, which leaves the isolate readable through RunScript and GetGlobal.
func (rt *Runtime) UncaughtError() *UncaughtError {
	uncaught := rt.fatal.Load()
	if uncaught != nil && !rt.fatalSettled {
		rt.fatalSettled = true
		// V8 empties its queue when execution terminates during a checkpoint, and the first
		// reaction's function is where termination lands, before any of its body runs.
		rt.isolate.TerminateExecution()
		rt.context.PerformMicrotaskCheckpoint()
		if !rt.IsKilled() {
			rt.isolate.CancelTerminateExecution()
		}
	}
	return uncaught
}

// TracksRejections reports whether this runtime's V8 glue reports promise rejections. See
// Context.SetPromiseRejectHandler.
func (rt *Runtime) TracksRejections() bool {
	return rt.tracksRejections
}

// BindProcess adds the methods only this package can implement to the process object.
func (rt *Runtime) BindProcess(process *v8.Value) error {
	_, err := rt.uncaught.bindProcess.Call(nil, process)
	return err
}

// reportRejections runs once the microtask queues are empty, which is the point where a
// promise rejected with no handler has had its chance to get one.
func (rt *Runtime) reportRejections() {
	for rt.rejectionsPending && rt.fatal.Load() == nil && !rt.execController.IsDone() {
		rt.rejectionsPending = false
		if _, err := rt.uncaught.processRejections.Call(nil); err != nil {
			return
		}
	}
}

// runMainScript runs the main script from inside a native call. V8 runs its promise queue
// when the stack empties, even if the stack is emptying because of an exception, so running
// the script directly would let queued reactions run ahead of the handlers, or instead of
// a fatal exit. It returns nil in place of an error if a handler took the exception.
func (rt *Runtime) runMainScript(source, origin string) (*v8.Value, error) {
	var result *v8.Value
	var scriptErr error
	rt.mainBody = func() {
		var err error
		if result, err = rt.RunScript(source, origin); err != nil {
			result = nil
			scriptErr = rt.reportScriptError(err)
		}
	}
	if _, err := rt.uncaught.runMain.Call(nil); err != nil && scriptErr == nil && rt.fatal.Load() == nil {
		scriptErr = err
	}
	return result, scriptErr
}

// reportScriptError gives an exception from the main script to the handlers. A script can
// register a handler and then throw, and in Node it keeps running after that. It returns nil
// if a handler took the exception, and otherwise the error Run should return.
func (rt *Runtime) reportScriptError(scriptErr error) error {
	if uncaught := rt.fatal.Load(); uncaught != nil {
		return uncaught
	}
	if errors.Is(scriptErr, v8.ErrExecutionTerminated) || rt.IsDone() {
		return scriptErr
	}
	thrown := rt.context.TakeLastScriptException()
	if thrown == nil {
		// The V8 glue predates TakeLastScriptException, so only the text survived. Node
		// would hand over the object: its name, message and stack come through here, and
		// its other properties do not.
		text, err := rt.context.NewString(scriptErr.Error())
		if err != nil {
			return scriptErr
		}
		if thrown, err = rt.uncaught.rebuildThrown.Call(nil, text); err != nil {
			return scriptErr
		}
	}
	if rt.handleUncaught(thrown) {
		return nil
	}
	if uncaught := rt.fatal.Load(); uncaught != nil {
		return uncaught
	}
	return fmt.Errorf("uncaught: %w", scriptErr)
}
