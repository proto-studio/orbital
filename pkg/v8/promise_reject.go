package v8

/*
#include <stdlib.h>
#include "v8go.h"

// These are overridden by the real definitions in libv8go_glue.a, once the pinned V8 release
// has them. Until then a build linked against an older glue works without rejection
// tracking. Delete them with the first V8 release built from a v8go.cc that defines both.
__attribute__((weak)) int v8go_set_promise_reject_handler(void* context_ptr, void* function_ptr) {
	return 0;
}
__attribute__((weak)) void* v8go_context_take_last_script_exception(void* context_ptr) {
	return NULL;
}
*/
import "C"

// SetPromiseRejectHandler makes V8 call fn as (event, promise, value) for each promise
// rejection event, where event is V8's PromiseRejectEvent value. It reports whether the
// linked V8 glue supports this.
func (c *Context) SetPromiseRejectHandler(fn *Value) bool {
	if c.ptr == nil || fn == nil || fn.ptr == nil {
		return false
	}
	return C.v8go_set_promise_reject_handler(c.ptr, fn.ptr) != 0
}

// TakeLastScriptException returns what the last failed RunScript threw and forgets it. It
// returns nil if there is no such value, or the linked V8 glue does not support this.
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
