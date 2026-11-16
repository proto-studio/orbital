package v8

// #include <stdlib.h>
// #include "v8go.h"
import "C"

import "unsafe"

// SerializeValue writes value with V8's ValueSerializer, as Node's v8.serialize does.
// Array buffer views are written the way Node's serializer writes them, as their type and
// own bytes. bufferProto is Buffer.prototype, or nil: a Uint8Array with that prototype is
// sent as a Buffer. Values V8 cannot clone, such as functions, promises and proxies, fail
// with the DataCloneError V8 throws.
func (c *Context) SerializeValue(value, bufferProto *Value) ([]byte, error) {
	if c == nil || c.ptr == nil || value == nil || value.ptr == nil {
		return nil, ErrDisposed
	}
	var protoPtr unsafe.Pointer
	if bufferProto != nil {
		protoPtr = bufferProto.ptr
	}
	var size C.size_t
	var errMsg *C.char
	data := C.v8go_value_serialize(c.ptr, value.ptr, protoPtr, &size, &errMsg)
	if data == nil {
		return nil, takeError(errMsg)
	}
	defer C.free(unsafe.Pointer(data))
	return C.GoBytes(unsafe.Pointer(data), C.int(size)), nil
}

// DeserializeValue reads what SerializeValue wrote, possibly in another isolate. A view
// gets a new ArrayBuffer of its own, and a Buffer gets bufferProto (Buffer.prototype in
// this context) as its prototype.
func (c *Context) DeserializeValue(data []byte, bufferProto *Value) (*Value, error) {
	if c == nil || c.ptr == nil {
		return nil, ErrDisposed
	}
	var protoPtr unsafe.Pointer
	if bufferProto != nil {
		protoPtr = bufferProto.ptr
	}
	var ptr *C.uchar
	if len(data) > 0 {
		ptr = (*C.uchar)(C.CBytes(data))
		defer C.free(unsafe.Pointer(ptr))
	}
	var errMsg *C.char
	result := C.v8go_value_deserialize(c.ptr, ptr, C.size_t(len(data)), protoPtr, &errMsg)
	if result == nil {
		return nil, takeError(errMsg)
	}
	return newValue(result, c), nil
}

// takeError turns an error string from the glue into an error and frees it. An empty or
// missing one means execution was terminated.
func takeError(errMsg *C.char) error {
	if errMsg == nil {
		return ErrExecutionTerminated
	}
	msg := C.GoString(errMsg)
	C.free(unsafe.Pointer(errMsg))
	if msg == "" {
		return ErrExecutionTerminated
	}
	return &JSError{Message: msg}
}
