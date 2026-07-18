// Package v8go provides Go bindings to the V8 JavaScript engine.
package v8

/*
// The C++ implementation (pkg/v8/csrc/v8go.cc) is NOT compiled by cgo. V8 is
// built with Chromium's custom libc++ (the std::__Cr:: inline namespace), which
// is ABI-incompatible with the system libstdc++ that cgo's g++ would use. It is
// instead pre-compiled per platform into libv8go_glue.a with V8's own toolchain,
// so the std:: symbols match libv8_monolith.a. cgo here only compiles the pure-C
// boundary (v8go.h); it carries NO -L/-l flags.
//
// The V8 static libraries are NOT committed to this repository. They are fetched
// on demand into a project-local .v8/ directory by `go generate` (see cmd/v8setup),
// which also writes a per-target cgo file carrying the -L/-l LDFLAGS that link the
// glue, the V8 monolith, and Chromium's libc++. Run `go generate ./...` before
// `go build`. See README.md ("Installing the V8 runtime") for details.
#include <stdlib.h>
#include "v8go.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"
)

var (
	initOnce sync.Once
	initErr  error
)

// Initialize initializes the V8 engine. Must be called before any other V8 operations.
func Initialize() error {
	initOnce.Do(func() {
		// Initialize V8
		C.v8go_init()
	})
	return initErr
}

// Dispose shuts down the V8 engine.
func Dispose() {
	C.v8go_dispose()
}

// Isolate represents an isolated instance of the V8 engine.
// Each isolate has its own heap and is completely independent.
type Isolate struct {
	ptr unsafe.Pointer
	mu  sync.Mutex
}

// NewIsolate creates a new V8 isolate.
func NewIsolate() (*Isolate, error) {
	if err := Initialize(); err != nil {
		return nil, err
	}

	ptr := C.v8go_isolate_new()
	if ptr == nil {
		return nil, errors.New("failed to create V8 isolate")
	}

	iso := &Isolate{ptr: ptr}
	runtime.SetFinalizer(iso, (*Isolate).release)
	return iso, nil
}

// Dispose releases the isolate resources.
func (i *Isolate) Dispose() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.release()
}

func (i *Isolate) release() {
	if i.ptr != nil {
		C.v8go_isolate_dispose(i.ptr)
		i.ptr = nil
	}
}

// Context represents a V8 execution context.
type Context struct {
	ptr unsafe.Pointer
	iso *Isolate
}

// NewContext creates a new execution context for the isolate.
func (i *Isolate) NewContext() (*Context, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.ptr == nil {
		return nil, errors.New("isolate is disposed")
	}

	ptr := C.v8go_context_new(i.ptr)
	if ptr == nil {
		return nil, errors.New("failed to create V8 context")
	}

	ctx := &Context{ptr: ptr, iso: i}
	runtime.SetFinalizer(ctx, (*Context).release)
	return ctx, nil
}

// Dispose releases the context resources.
func (c *Context) Dispose() {
	c.release()
}

func (c *Context) release() {
	if c.ptr != nil {
		C.v8go_context_dispose(c.ptr)
		c.ptr = nil
	}
}

// RunScript compiles and runs a JavaScript script in this context.
func (c *Context) RunScript(source, origin string) (*Value, error) {
	if c.ptr == nil {
		return nil, errors.New("context is disposed")
	}

	cSource := C.CString(source)
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cSource))
	defer C.free(unsafe.Pointer(cOrigin))

	var errMsg *C.char
	result := C.v8go_context_run_script(c.ptr, cSource, cOrigin, &errMsg)

	if errMsg != nil {
		err := C.GoString(errMsg)
		C.free(unsafe.Pointer(errMsg))
		return nil, errors.New(err)
	}

	// An empty result with no captured exception means V8 returned an empty
	// MaybeLocal while TryCatch reported nothing caught — i.e. execution was
	// terminated/interrupted. Surface it instead of returning a silent nil so
	// callers don't mistake a stopped script for a successful one.
	if result == nil {
		return nil, ErrExecutionTerminated
	}

	return &Value{ptr: result, ctx: c}, nil
}

// Value represents a JavaScript value.
type Value struct {
	ptr unsafe.Pointer
	ctx *Context
}

// String returns the string representation of the value.
func (v *Value) String() string {
	if v.ptr == nil {
		return ""
	}
	cStr := C.v8go_value_to_string(v.ctx.ptr, v.ptr)
	if cStr == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(cStr))
	return C.GoString(cStr)
}

// IsUndefined returns true if the value is undefined.
func (v *Value) IsUndefined() bool {
	if v.ptr == nil {
		return true
	}
	return C.v8go_value_is_undefined(v.ptr) != 0
}

// IsNull returns true if the value is null.
func (v *Value) IsNull() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_null(v.ptr) != 0
}

// IsBoolean returns true if the value is a boolean.
func (v *Value) IsBoolean() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_boolean(v.ptr) != 0
}

// IsNumber returns true if the value is a number.
func (v *Value) IsNumber() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_number(v.ptr) != 0
}

// IsString returns true if the value is a string.
func (v *Value) IsString() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_is_string(v.ptr) != 0
}

// Boolean returns the boolean value.
func (v *Value) Boolean() bool {
	if v.ptr == nil {
		return false
	}
	return C.v8go_value_to_boolean(v.ptr) != 0
}

// Number returns the numeric value.
func (v *Value) Number() float64 {
	if v.ptr == nil {
		return 0
	}
	return float64(C.v8go_value_to_number(v.ctx.ptr, v.ptr))
}
