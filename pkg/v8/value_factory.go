package v8

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import "unsafe"

// Undefined returns the undefined value for this context.
func (c *Context) Undefined() *Value {
	ptr := C.v8go_undefined(c.ptr)
	return &Value{ptr: ptr, ctx: c}
}

// Null returns the null value for this context.
func (c *Context) Null() *Value {
	ptr := C.v8go_null(c.ptr)
	return &Value{ptr: ptr, ctx: c}
}

// True returns the boolean true value for this context.
func (c *Context) True() *Value {
	ptr := C.v8go_true(c.ptr)
	return &Value{ptr: ptr, ctx: c}
}

// False returns the boolean false value for this context.
func (c *Context) False() *Value {
	ptr := C.v8go_false(c.ptr)
	return &Value{ptr: ptr, ctx: c}
}

// NewBoolean creates a new boolean value.
func (c *Context) NewBoolean(val bool) *Value {
	var b C.int
	if val {
		b = 1
	}
	ptr := C.v8go_new_boolean(c.ptr, b)
	return &Value{ptr: ptr, ctx: c}
}

// NewNumber creates a new number value.
func (c *Context) NewNumber(val float64) *Value {
	ptr := C.v8go_new_number(c.ptr, C.double(val))
	return &Value{ptr: ptr, ctx: c}
}

// NewInteger creates a new integer value.
func (c *Context) NewInteger(val int64) *Value {
	ptr := C.v8go_new_integer(c.ptr, C.long(val))
	return &Value{ptr: ptr, ctx: c}
}

// NewString creates a new string value.
func (c *Context) NewString(val string) (*Value, error) {
	cVal := C.CString(val)
	defer C.free(unsafe.Pointer(cVal))
	ptr := C.v8go_new_string(c.ptr, cVal, C.int(len(val)))
	if ptr == nil {
		return nil, ErrFailedToCreate
	}
	return &Value{ptr: ptr, ctx: c}, nil
}

// NewObject creates a new empty object.
func (c *Context) NewObject() (*Value, error) {
	ptr := C.v8go_new_object(c.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}
	return &Value{ptr: ptr, ctx: c}, nil
}

// NewArray creates a new array with the given length.
func (c *Context) NewArray(length int) (*Value, error) {
	ptr := C.v8go_new_array(c.ptr, C.int(length))
	if ptr == nil {
		return nil, ErrFailedToCreate
	}
	return &Value{ptr: ptr, ctx: c}, nil
}

// Global returns the global object for this context.
func (c *Context) Global() (*Value, error) {
	if c.ptr == nil {
		return nil, ErrContextDisposed
	}
	ptr := C.v8go_context_global(c.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}
	return &Value{ptr: ptr, ctx: c}, nil
}
