package v8go

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ModuleStatus represents the status of a module.
type ModuleStatus int

const (
	ModuleStatusUninstantiated ModuleStatus = iota
	ModuleStatusInstantiating
	ModuleStatusInstantiated
	ModuleStatusEvaluating
	ModuleStatusEvaluated
	ModuleStatusErrored
)

// Module represents a V8 ES module.
type Module struct {
	ptr        unsafe.Pointer
	ctx        *Context
	resolverID int
}

// ErrModuleCompile is returned when module compilation fails.
var ErrModuleCompile = errors.New("module compilation failed")

// ErrModuleInstantiate is returned when module instantiation fails.
var ErrModuleInstantiate = errors.New("module instantiation failed")

// ErrModuleEvaluate is returned when module evaluation fails.
var ErrModuleEvaluate = errors.New("module evaluation failed")

// CompileModule compiles JavaScript source code as an ES module.
func (c *Context) CompileModule(source, name string) (*Module, error) {
	if c.ptr == nil {
		return nil, ErrContextDisposed
	}

	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var errMsg *C.char
	ptr := C.v8go_compile_module(c.ptr, cSource, cName, &errMsg)

	if ptr == nil {
		if errMsg != nil {
			err := C.GoString(errMsg)
			C.free(unsafe.Pointer(errMsg))
			return nil, &JSError{Message: err}
		}
		return nil, ErrModuleCompile
	}

	return &Module{ptr: ptr, ctx: c}, nil
}

// Instantiate links the module and its dependencies.
// The resolver callback is called for each import to load the imported module.
func (m *Module) Instantiate(resolver ModuleResolveCallback) error {
	if m.ptr == nil || m.ctx == nil || m.ctx.ptr == nil {
		return ErrDisposed
	}

	resolverID := registerModuleResolver(resolver)
	m.resolverID = resolverID

	var errMsg *C.char
	result := C.v8go_module_instantiate(m.ctx.ptr, m.ptr, C.int(resolverID), &errMsg)

	if result == 0 {
		unregisterModuleResolver(resolverID)
		if errMsg != nil {
			err := C.GoString(errMsg)
			C.free(unsafe.Pointer(errMsg))
			return &JSError{Message: err}
		}
		return ErrModuleInstantiate
	}

	return nil
}

// Evaluate runs the module and returns the completion value.
func (m *Module) Evaluate() (*Value, error) {
	if m.ptr == nil || m.ctx == nil || m.ctx.ptr == nil {
		return nil, ErrDisposed
	}

	var errMsg *C.char
	ptr := C.v8go_module_evaluate(m.ctx.ptr, m.ptr, &errMsg)

	// Clean up resolver after evaluation
	if m.resolverID != 0 {
		unregisterModuleResolver(m.resolverID)
		m.resolverID = 0
	}

	if ptr == nil {
		if errMsg != nil {
			err := C.GoString(errMsg)
			C.free(unsafe.Pointer(errMsg))
			return nil, &JSError{Message: err}
		}
		return nil, ErrModuleEvaluate
	}

	return &Value{ptr: ptr, ctx: m.ctx}, nil
}

// Status returns the current status of the module.
func (m *Module) Status() ModuleStatus {
	if m.ptr == nil {
		return ModuleStatusErrored
	}
	status := C.v8go_module_get_status(m.ptr)
	return ModuleStatus(status)
}

// GetNamespace returns the module namespace object.
func (m *Module) GetNamespace() (*Value, error) {
	if m.ptr == nil || m.ctx == nil || m.ctx.ptr == nil {
		return nil, ErrDisposed
	}

	ptr := C.v8go_module_get_namespace(m.ctx.ptr, m.ptr)
	if ptr == nil {
		return nil, ErrFailedToCreate
	}

	return &Value{ptr: ptr, ctx: m.ctx}, nil
}

// GetModuleRequests returns the list of import specifiers this module requests.
func (m *Module) GetModuleRequests() []string {
	if m.ptr == nil {
		return nil
	}

	length := int(C.v8go_module_get_requests_length(m.ptr))
	requests := make([]string, length)

	for i := 0; i < length; i++ {
		cStr := C.v8go_module_get_request(m.ptr, C.int(i))
		if cStr != nil {
			requests[i] = C.GoString(cStr)
			C.free(unsafe.Pointer(cStr))
		}
	}

	return requests
}
