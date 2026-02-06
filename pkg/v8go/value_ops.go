package v8go

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import "unsafe"

// Set sets a property on this value (must be an object).
func (v *Value) Set(key string, val *Value) error {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return ErrDisposed
	}
	if val == nil || val.ptr == nil {
		return ErrInvalidValue
	}

	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	result := C.v8go_object_set(v.ctx.ptr, v.ptr, cKey, val.ptr)
	if result == 0 {
		return ErrFailedToCreate
	}
	return nil
}

// SetIndex sets an indexed property on this value (must be an array/object).
func (v *Value) SetIndex(idx int, val *Value) error {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return ErrDisposed
	}
	if val == nil || val.ptr == nil {
		return ErrInvalidValue
	}

	result := C.v8go_object_set_idx(v.ctx.ptr, v.ptr, C.int(idx), val.ptr)
	if result == 0 {
		return ErrFailedToCreate
	}
	return nil
}

// Get gets a property from this value (must be an object).
func (v *Value) Get(key string) (*Value, error) {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return nil, ErrDisposed
	}

	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	ptr := C.v8go_object_get(v.ctx.ptr, v.ptr, cKey)
	if ptr == nil {
		return nil, nil
	}
	return &Value{ptr: ptr, ctx: v.ctx}, nil
}

// GetIndex gets an indexed property from this value (must be an array/object).
func (v *Value) GetIndex(idx int) (*Value, error) {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return nil, ErrDisposed
	}

	ptr := C.v8go_object_get_idx(v.ctx.ptr, v.ptr, C.int(idx))
	if ptr == nil {
		return nil, nil
	}
	return &Value{ptr: ptr, ctx: v.ctx}, nil
}

// Has checks if a property exists on this value (must be an object).
func (v *Value) Has(key string) bool {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return false
	}

	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	return C.v8go_object_has(v.ctx.ptr, v.ptr, cKey) != 0
}

// Delete deletes a property from this value (must be an object).
func (v *Value) Delete(key string) bool {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return false
	}

	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	return C.v8go_object_delete(v.ctx.ptr, v.ptr, cKey) != 0
}

// IsObject returns true if the value is an object.
func (v *Value) IsObject() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_object(v.ptr) != 0
}

// IsArray returns true if the value is an array.
func (v *Value) IsArray() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_array(v.ptr) != 0
}

// IsFunction returns true if the value is a function.
func (v *Value) IsFunction() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_function(v.ptr) != 0
}

// ArrayLength returns the length of the array.
func (v *Value) ArrayLength() int {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return 0
	}
	return int(C.v8go_array_length(v.ctx.ptr, v.ptr))
}

// Integer returns the integer value.
func (v *Value) Integer() int64 {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return 0
	}
	return int64(C.v8go_value_to_integer(v.ctx.ptr, v.ptr))
}

// Call calls this value as a function.
func (v *Value) Call(recv *Value, args ...*Value) (*Value, error) {
	if v.ptr == nil || v.ctx == nil || v.ctx.ptr == nil {
		return nil, ErrDisposed
	}

	argc := len(args)
	var argvPtr unsafe.Pointer

	if argc > 0 {
		argvSlice := make([]unsafe.Pointer, argc)
		for i, arg := range args {
			if arg != nil {
				argvSlice[i] = arg.ptr
			}
		}
		argvPtr = unsafe.Pointer(&argvSlice[0])
	}

	var recvPtr unsafe.Pointer
	if recv != nil {
		recvPtr = recv.ptr
	}

	var errMsg *C.char
	result := C.v8go_function_call(v.ctx.ptr, v.ptr, recvPtr, C.int(argc), (*unsafe.Pointer)(argvPtr), &errMsg)

	if errMsg != nil {
		err := C.GoString(errMsg)
		C.free(unsafe.Pointer(errMsg))
		return nil, &JSError{Message: err}
	}

	if result == nil {
		return nil, nil
	}

	return &Value{ptr: result, ctx: v.ctx}, nil
}
