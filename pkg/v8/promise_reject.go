package v8

/*
#include <stdlib.h>
#include "v8go.h"
*/
import "C"

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
