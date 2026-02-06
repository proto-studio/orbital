package v8go

import "errors"

// Common errors returned by v8go functions.
var (
	ErrIsolateDisposed = errors.New("isolate is disposed")
	ErrContextDisposed = errors.New("context is disposed")
	ErrDisposed        = errors.New("object is disposed")
	ErrFailedToCreate  = errors.New("failed to create V8 object")
	ErrUnsupportedType = errors.New("unsupported type")
	ErrInvalidValue    = errors.New("invalid value")
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
