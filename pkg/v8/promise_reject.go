package v8

/*
#include <stdlib.h>
#include "v8go.h"
*/
import "C"

import "unsafe"

// SetPromiseRejectHandler makes V8 call fn as (event, promise, value) for each promise
// rejection event, where event is V8's PromiseRejectEvent value. It reports whether the
// handler was set.
func (c *Context) SetPromiseRejectHandler(fn *Value) bool {
	if c.ptr == nil || fn == nil || fn.ptr == nil {
		return false
	}
	return C.v8go_set_promise_reject_handler(c.ptr, fn.ptr) != 0
}

// TakeLastScriptException returns what the last failed RunScript threw and forgets it. It
// returns nil if there is no such value.
func (c *Context) TakeLastScriptException() *Value {
	if c.ptr == nil {
		return nil
	}
	result := C.v8go_context_take_last_script_exception(c.ptr)
	if result == nil {
		return nil
	}
	return &Value{ptr: result, ctx: c}
}

// ExceptionMessage is where V8's message for an exception places it: for a throw, the
// throw statement, and for a SyntaxError, the offending token. Node prints its fatal
// exception header from it.
type ExceptionMessage struct {
	// ResourceName is the script's name, as its stack frames give it.
	ResourceName string
	// Line is 1-based.
	Line int
	// SourceLine is the text of that line, as compiled.
	SourceLine string
	// StartColumn and EndColumn are 0-based UTF-16 offsets into SourceLine, or -1.
	StartColumn, EndColumn int
}

// TakeLastExceptionMessage returns where V8's message places what the last failed RunScript
// threw, and forgets it. It returns nil if there is none, as for a linked
// V8 glue that predates it.
func (c *Context) TakeLastExceptionMessage() *ExceptionMessage {
	if c.ptr == nil {
		return nil
	}
	var resource, sourceLine *C.char
	var line, start, end C.int
	if C.v8go_context_take_last_exception_message(c.ptr, &resource, &line, &sourceLine, &start, &end) == 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(resource))
	defer C.free(unsafe.Pointer(sourceLine))
	return &ExceptionMessage{
		ResourceName: C.GoString(resource),
		Line:         int(line),
		SourceLine:   C.GoString(sourceLine),
		StartColumn:  int(start),
		EndColumn:    int(end),
	}
}
