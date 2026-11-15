package v8

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// ModuleResolveCallback is called when V8 needs to resolve an import.
// It takes the specifier (import path) and referrer (importing module path),
// and returns the resolved module source code, resolved path, and any error.
// An error that implements ExceptionError is thrown (or rejects import()) as its
// own JavaScript value; any other error becomes a plain Error.
type ModuleResolveCallback func(specifier, referrer string, attrs []ImportAttribute) (source string, resolvedPath string, err error)

// ImportAttribute is one entry of an import's attributes (`with { type: 'json' }`),
// in source order.
type ImportAttribute struct {
	Key, Value string
}

// ExceptionError is an error carrying the JavaScript value to throw for it, such
// as a Node error object with a code.
type ExceptionError interface {
	error
	Exception() *Value
}

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

// moduleResolverRegistry stores module resolve callbacks indexed by a unique ID.
var moduleResolverRegistry = struct {
	sync.RWMutex
	resolvers map[int]ModuleResolveCallback
	nextID    int
}{
	resolvers: make(map[int]ModuleResolveCallback),
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

// registerModuleResolver stores a module resolver and returns its ID.
func registerModuleResolver(resolver ModuleResolveCallback) int {
	moduleResolverRegistry.Lock()
	defer moduleResolverRegistry.Unlock()
	id := moduleResolverRegistry.nextID
	moduleResolverRegistry.nextID++
	moduleResolverRegistry.resolvers[id] = resolver
	return id
}

// getModuleResolver retrieves a module resolver by ID.
func getModuleResolver(id int) ModuleResolveCallback {
	moduleResolverRegistry.RLock()
	defer moduleResolverRegistry.RUnlock()
	return moduleResolverRegistry.resolvers[id]
}

// unregisterModuleResolver removes a module resolver by ID.
func unregisterModuleResolver(id int) {
	moduleResolverRegistry.Lock()
	defer moduleResolverRegistry.Unlock()
	delete(moduleResolverRegistry.resolvers, id)
}

// nearHeapLimitRegistry stores NearHeapLimitCallbacks indexed by ID.
var nearHeapLimitRegistry = struct {
	sync.RWMutex
	callbacks map[int]NearHeapLimitCallback
	nextID    int
}{
	callbacks: make(map[int]NearHeapLimitCallback),
	nextID:    1,
}

func registerNearHeapLimitCallback(cb NearHeapLimitCallback) int {
	nearHeapLimitRegistry.Lock()
	defer nearHeapLimitRegistry.Unlock()
	id := nearHeapLimitRegistry.nextID
	nearHeapLimitRegistry.nextID++
	nearHeapLimitRegistry.callbacks[id] = cb
	return id
}

func getNearHeapLimitCallback(id int) NearHeapLimitCallback {
	nearHeapLimitRegistry.RLock()
	defer nearHeapLimitRegistry.RUnlock()
	return nearHeapLimitRegistry.callbacks[id]
}

func unregisterNearHeapLimitCallback(id int) {
	nearHeapLimitRegistry.Lock()
	defer nearHeapLimitRegistry.Unlock()
	delete(nearHeapLimitRegistry.callbacks, id)
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

	return newValue(ptr, ctx), nil
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

	return newValue(ptr, ctx), nil
}

//export goCallbackHandler
func goCallbackHandler(ctxPtr, isoPtr unsafe.Pointer, callbackID C.int, infoPtr unsafe.Pointer) unsafe.Pointer {
	// The Context the callback runs in, which takes over the wrapper made for this call and
	// frees it once nothing the callback kept uses it.
	ctx := adoptContext(ctxPtr, isoPtr)
	// On the isolate's thread, so wrappers earlier callbacks left can be freed safely.
	if ctx.iso != nil {
		ctx.iso.freeUnused()
	}

	cb := getCallback(int(callbackID))
	if cb == nil {
		return nil
	}

	// What the callback makes may outlive any release scope open around the call, as a timer
	// keeps the function it's given, so none of it falls in one.
	var result *Value
	keepValues(isoPtr, func() { result = runCallback(cb, ctx, infoPtr) })
	if result == nil {
		return nil
	}
	return result.ptr
}

// runCallback calls cb with the arguments infoPtr holds, in the context ctxPtr.
func runCallback(cb FunctionCallback, ctx *Context, infoPtr unsafe.Pointer) *Value {
	// Get callback info from C
	argc := int(C.v8go_callback_info_length(infoPtr))
	args := make([]*Value, argc)

	for i := 0; i < argc; i++ {
		argPtr := C.v8go_callback_info_arg(infoPtr, C.int(i))
		if argPtr != nil {
			args[i] = newValue(argPtr, ctx)
		}
	}

	thisPtr := C.v8go_callback_info_this(infoPtr)
	var this *Value
	if thisPtr != nil {
		this = newValue(thisPtr, ctx)
	}

	info := &FunctionCallbackInfo{
		ctx:  ctx,
		args: args,
		this: this,
	}

	return cb(info)
}

//export goModuleResolve
func goModuleResolve(resolverID C.int, specifier, referrer *C.char, attrs **C.char, attrCount C.int, sourceOut, nameOut **C.char, errorOut *unsafe.Pointer) C.int {
	resolver := getModuleResolver(int(resolverID))
	if resolver == nil {
		return C.int(-1)
	}

	specifierStr := C.GoString(specifier)
	referrerStr := C.GoString(referrer)

	var attributes []ImportAttribute
	if attrCount > 1 && attrs != nil {
		raw := unsafe.Slice(attrs, int(attrCount))
		for i := 0; i+1 < len(raw); i += 2 {
			attributes = append(attributes, ImportAttribute{Key: C.GoString(raw[i]), Value: C.GoString(raw[i+1])})
		}
	}

	source, resolvedPath, err := resolver(specifierStr, referrerStr, attributes)
	if err != nil {
		var exc ExceptionError
		if errors.As(err, &exc) {
			if v := exc.Exception(); v != nil && v.ptr != nil {
				*errorOut = v.ptr
			}
		}
		return C.int(-1)
	}

	*sourceOut = C.CString(source)
	*nameOut = C.CString(resolvedPath)

	return C.int(0)
}

//export goNearHeapLimitCallback
func goNearHeapLimitCallback(callbackID C.int, currentHeapLimit, initialHeapLimit C.size_t) C.size_t {
	cb := getNearHeapLimitCallback(int(callbackID))
	if cb == nil {
		// No Go callback — keep the current limit so V8 can proceed toward OOM
		// handling rather than inventing a bump.
		return currentHeapLimit
	}
	newLimit := cb(uint64(currentHeapLimit), uint64(initialHeapLimit))
	return C.size_t(newLimit)
}
