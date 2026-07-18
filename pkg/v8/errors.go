package v8

import "errors"

// Common errors returned by v8go functions.
var (
	ErrIsolateDisposed = errors.New("isolate is disposed")
	ErrContextDisposed = errors.New("context is disposed")
	ErrDisposed        = errors.New("object is disposed")
	ErrFailedToCreate  = errors.New("failed to create V8 object")
	ErrUnsupportedType = errors.New("unsupported type")
	ErrInvalidValue    = errors.New("invalid value")

	// ErrExecutionTerminated is returned when a V8 execution (RunScript / a
	// function Call) yields an empty result WITHOUT a pending JavaScript
	// exception. In V8 this is the signature of terminated/interrupted
	// execution (IsExecutionTerminating): Script::Run / Function::Call return
	// an empty MaybeLocal and TryCatch::HasCaught() is false. Historically the
	// bindings mapped this to a (nil, nil) return, so it surfaced as a script
	// that silently "succeeded" but stopped early. Returning a real error here
	// makes that failure observable instead of a silent early exit.
	ErrExecutionTerminated = errors.New("v8: execution terminated without a JavaScript exception (empty result, no pending exception)")
)

// JSError represents a JavaScript error with stack trace.
type JSError struct {
	Message    string
	Location   string
	StackTrace string
}

func (e *JSError) Error() string {
	if e.StackTrace != "" {
		return e.Message + "\n" + e.StackTrace
	}
	return e.Message
}
