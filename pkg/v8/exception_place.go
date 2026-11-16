package v8

/*
#include <stdlib.h>
#include "v8go.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// ExceptionPlace is where V8's message places an exception: for a throw, the throw
// statement; for a SyntaxError, the offending token; for an error nothing threw, where it
// was made. Node prints its fatal exception header from it.
type ExceptionPlace struct {
	// ResourceName is the script's name, as its stack frames give it.
	ResourceName string
	// Line is 1-based.
	Line int
	// SourceLine is the text of that line, as compiled. It can hold NUL characters.
	SourceLine string
	// HasSourceLine reports whether there is a SourceLine: there is none when V8 gives no
	// line or the line is too long to copy.
	HasSourceLine bool
	// StartColumn and EndColumn are 0-based UTF-16 offsets into SourceLine, or -1.
	StartColumn, EndColumn int
}

type placeFunc func(resource **C.char, line *C.int, sourceLine **C.char, length, start, end *C.int) C.int

func takePlace(get placeFunc) *ExceptionPlace {
	var resource, sourceLine *C.char
	var line, length, start, end C.int
	if get(&resource, &line, &sourceLine, &length, &start, &end) == 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(resource))
	defer C.free(unsafe.Pointer(sourceLine))
	return &ExceptionPlace{
		ResourceName:  C.GoString(resource),
		Line:          int(line),
		SourceLine:    C.GoStringN(sourceLine, length),
		HasSourceLine: sourceLine != nil,
		StartColumn:   int(start),
		EndColumn:     int(end),
	}
}

// TakeExceptionPlace returns where V8's message placed the exception that RunScript, or a
// function from CallRecordingFunction, last caught, and forgets it. It returns nil if there
// is none.
func (c *Context) TakeExceptionPlace() *ExceptionPlace {
	if c.ptr == nil {
		return nil
	}
	return takePlace(func(resource **C.char, line *C.int, sourceLine **C.char, length, start, end *C.int) C.int {
		return C.v8go_context_take_exception_place(c.ptr, resource, line, sourceLine, length, start, end)
	})
}

// PlaceOf returns where V8 places value when nothing has thrown it: for an error, where it
// was made. Otherwise it is where JavaScript is running now. It returns nil if there is
// no place.
func (c *Context) PlaceOf(value *Value) *ExceptionPlace {
	if c.ptr == nil || value == nil || value.ptr == nil {
		return nil
	}
	return takePlace(func(resource **C.char, line *C.int, sourceLine **C.char, length, start, end *C.int) C.int {
		return C.v8go_context_place_of(c.ptr, value.ptr, resource, line, sourceLine, length, start, end)
	})
}

// CallRecordingFunction returns a JavaScript function callRecording(fn, recv, ...args) that
// calls fn with recv and args. If fn throws, it keeps where V8's message places the throw
// for TakeExceptionPlace, then throws the exception on: V8 keeps no message for an
// exception JavaScript catches, so a catch around callRecording is where the place can
// still be had. The call runs no Go code.
func (c *Context) CallRecordingFunction() (*Value, error) {
	if c.ptr == nil {
		return nil, ErrContextDisposed
	}
	ptr := C.v8go_context_call_recording_function(c.ptr)
	if ptr == nil {
		return nil, errors.New("v8: could not create callRecording")
	}
	return &Value{ptr: ptr, ctx: c}, nil
}

// RunScriptRethrowing runs a script from inside a native function. If the script throws,
// it returns nil and leaves the exception pending for the JavaScript that called the
// function, as if that JavaScript had run the script itself: V8's message for it, which
// places a SyntaxError at its token, stays as it was.
func (c *Context) RunScriptRethrowing(source, origin string) *Value {
	if c.ptr == nil {
		return nil
	}
	cSource := C.CString(source)
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cSource))
	defer C.free(unsafe.Pointer(cOrigin))
	result := C.v8go_context_run_script_rethrowing(c.ptr, cSource, cOrigin)
	if result == nil {
		return nil
	}
	return &Value{ptr: result, ctx: c}
}
