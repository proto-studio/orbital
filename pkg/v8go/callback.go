package v8go

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import (
	"sync"
	"unsafe"
)

// FunctionCallback is the signature for Go functions callable from JavaScript.
type FunctionCallback func(info *FunctionCallbackInfo) *Value

// FunctionCallbackInfo provides information about the function call from JS.
type FunctionCallbackInfo struct {
	ctx    *Context
	args   []*Value
	this   *Value
	holder *Value
}

// Context returns the context in which the function was called.
func (i *FunctionCallbackInfo) Context() *Context {
	return i.ctx
}

// Args returns the arguments passed to the function.
func (i *FunctionCallbackInfo) Args() []*Value {
	return i.args
}

// This returns the receiver of the function call.
func (i *FunctionCallbackInfo) This() *Value {
	return i.this
}

// Length returns the number of arguments.
func (i *FunctionCallbackInfo) Length() int {
	return len(i.args)
}

// Arg returns the argument at index i, or undefined if out of bounds.
func (i *FunctionCallbackInfo) Arg(idx int) *Value {
	if idx < 0 || idx >= len(i.args) {
		return i.ctx.Undefined()
	}
	return i.args[idx]
}

// callbackRegistry stores Go callbacks indexed by a unique ID.
var callbackRegistry = struct {
	sync.RWMutex
	callbacks map[int]FunctionCallback
	nextID    int
}{
	callbacks: make(map[int]FunctionCallback),
	nextID:    1,
}

// registerCallback stores a callback and returns its ID.
func registerCallback(cb FunctionCallback) int {
	callbackRegistry.Lock()
	defer callbackRegistry.Unlock()
	id := callbackRegistry.nextID
	callbackRegistry.nextID++
	callbackRegistry.callbacks[id] = cb
	return id
}

// getCallback retrieves a callback by ID.
func getCallback(id int) FunctionCallback {
	callbackRegistry.RLock()
	defer callbackRegistry.RUnlock()
	return callbackRegistry.callbacks[id]
}

// unregisterCallback removes a callback by ID.
func unregisterCallback(id int) {
	callbackRegistry.Lock()
	defer callbackRegistry.Unlock()
	delete(callbackRegistry.callbacks, id)
}

// FunctionTemplate represents a V8 FunctionTemplate for creating functions.
type FunctionTemplate struct {
	ptr        unsafe.Pointer
	iso        *Isolate
	callbackID int
}

// NewFunctionTemplate creates a new function template with the given callback.
func (i *Isolate) NewFunctionTemplate(callback FunctionCallback) (*FunctionTemplate, error) {
	if i.ptr == nil {
		return nil, ErrIsolateDisposed
	}

	callbackID := registerCallback(callback)
	ptr := C.v8go_function_template_new_with_id(i.ptr, C.int(callbackID))
	if ptr == nil {
		unregisterCallback(callbackID)
		return nil, ErrFailedToCreate
	}

	return &FunctionTemplate{
		ptr:        ptr,
		iso:        i,
		callbackID: callbackID,
	}, nil
}

// GetFunction creates an instance of this function template in the given context.
func (ft *FunctionTemplate) GetFunction(ctx *Context) (*Value, error) {
	if ft.ptr == nil || ctx.ptr == nil {
		return nil, ErrDisposed
	}

	ptr := C.v8go_function_template_get_function(ctx.ptr, ft.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}

	return &Value{ptr: ptr, ctx: ctx}, nil
}

// ObjectTemplate represents a V8 ObjectTemplate for creating objects with predefined properties.
type ObjectTemplate struct {
	ptr unsafe.Pointer
	iso *Isolate
}

// NewObjectTemplate creates a new object template.
func (i *Isolate) NewObjectTemplate() (*ObjectTemplate, error) {
	if i.ptr == nil {
		return nil, ErrIsolateDisposed
	}

	ptr := C.v8go_object_template_new(i.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}

	return &ObjectTemplate{ptr: ptr, iso: i}, nil
}

// Set sets a property on the object template.
func (ot *ObjectTemplate) Set(key string, val interface{}) error {
	if ot.ptr == nil {
		return ErrDisposed
	}

	cKey := C.CString(key)
	defer C.free(unsafe.Pointer(cKey))

	switch v := val.(type) {
	case *FunctionTemplate:
		C.v8go_object_template_set_function(ot.ptr, cKey, v.ptr)
	default:
		return ErrUnsupportedType
	}

	return nil
}

// NewInstance creates a new instance of this object template in the given context.
func (ot *ObjectTemplate) NewInstance(ctx *Context) (*Value, error) {
	if ot.ptr == nil || ctx.ptr == nil {
		return nil, ErrDisposed
	}

	ptr := C.v8go_object_template_new_instance(ctx.ptr, ot.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}

	return &Value{ptr: ptr, ctx: ctx}, nil
}

//export goCallbackHandler
func goCallbackHandler(ctxPtr unsafe.Pointer, callbackID C.int, infoPtr unsafe.Pointer) unsafe.Pointer {
	cb := getCallback(int(callbackID))
	if cb == nil {
		return nil
	}

	// Get callback info from C
	argc := int(C.v8go_callback_info_length(infoPtr))
	args := make([]*Value, argc)

	// We need to find the context wrapper - for now we'll create a temporary one
	// In a real implementation, we'd maintain a context registry
	ctx := &Context{ptr: ctxPtr}

	for i := 0; i < argc; i++ {
		argPtr := C.v8go_callback_info_arg(infoPtr, C.int(i))
		if argPtr != nil {
			args[i] = &Value{ptr: argPtr, ctx: ctx}
		}
	}

	thisPtr := C.v8go_callback_info_this(infoPtr)
	var this *Value
	if thisPtr != nil {
		this = &Value{ptr: thisPtr, ctx: ctx}
	}

	info := &FunctionCallbackInfo{
		ctx:  ctx,
		args: args,
		this: this,
	}

	result := cb(info)
	if result == nil {
		return nil
	}
	return result.ptr
}
