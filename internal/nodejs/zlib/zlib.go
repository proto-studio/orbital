// Package zlib implements the Node.js zlib module.
package zlib

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	_ "embed"
	"encoding/base64"
	"io"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
)

//go:embed zlib.js
var zlibJS string

// Zlib provides compression functionality.
type Zlib struct {
	rt *runtime.Runtime
}

// New creates a new Zlib module.
func New() *Zlib {
	return &Zlib{}
}

// Name returns the module name.
func (z *Zlib) Name() string {
	return "zlib"
}

// Register sets up the zlib module.
func (z *Zlib) Register(rt *runtime.Runtime) error {
	z.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create zlib internal object for Go functions
	zlibInternal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// gzipSync
	gzipSyncFn, err := iso.NewFunctionTemplate(z.gzipSyncFunc)
	if err != nil {
		return err
	}
	gzipSyncVal, err := gzipSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("gzipSync", gzipSyncVal); err != nil {
		return err
	}

	// gunzipSync
	gunzipSyncFn, err := iso.NewFunctionTemplate(z.gunzipSyncFunc)
	if err != nil {
		return err
	}
	gunzipSyncVal, err := gunzipSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("gunzipSync", gunzipSyncVal); err != nil {
		return err
	}

	// deflateSync
	deflateSyncFn, err := iso.NewFunctionTemplate(z.deflateSyncFunc)
	if err != nil {
		return err
	}
	deflateSyncVal, err := deflateSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("deflateSync", deflateSyncVal); err != nil {
		return err
	}

	// inflateSync
	inflateSyncFn, err := iso.NewFunctionTemplate(z.inflateSyncFunc)
	if err != nil {
		return err
	}
	inflateSyncVal, err := inflateSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("inflateSync", inflateSyncVal); err != nil {
		return err
	}

	// deflateRawSync
	deflateRawSyncFn, err := iso.NewFunctionTemplate(z.deflateRawSyncFunc)
	if err != nil {
		return err
	}
	deflateRawSyncVal, err := deflateRawSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("deflateRawSync", deflateRawSyncVal); err != nil {
		return err
	}

	// inflateRawSync
	inflateRawSyncFn, err := iso.NewFunctionTemplate(z.inflateRawSyncFunc)
	if err != nil {
		return err
	}
	inflateRawSyncVal, err := inflateRawSyncFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := zlibInternal.Set("inflateRawSync", inflateRawSyncVal); err != nil {
		return err
	}

	// Set as global
	if err := rt.SetGlobal("__zlib_internal", zlibInternal); err != nil {
		return err
	}

	// Initialize JS wrapper
	if _, err := rt.RunScript(zlibJS, "zlib.js"); err != nil {
		return err
	}

	return nil
}

// Helper to get bytes from value (string or base64)
func getBytes(val *v8go.Value) []byte {
	str := val.String()
	// Try base64 decode first
	if decoded, err := base64.StdEncoding.DecodeString(str); err == nil {
		return decoded
	}
	return []byte(str)
}

// Helper to return bytes as base64 string
func returnBytes(ctx *v8go.Context, data []byte) *v8go.Value {
	encoded := base64.StdEncoding.EncodeToString(data)
	val, _ := ctx.NewString(encoded)
	return val
}

// gzipSyncFunc compresses data using gzip
func (z *Zlib) gzipSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return nil
	}

	return returnBytes(info.Context(), buf.Bytes())
}

// gunzipSyncFunc decompresses gzip data
func (z *Zlib) gunzipSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}

	return returnBytes(info.Context(), result)
}

// deflateSyncFunc compresses data using deflate (zlib format)
func (z *Zlib) deflateSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return nil
	}

	return returnBytes(info.Context(), buf.Bytes())
}

// inflateSyncFunc decompresses deflate data (zlib format)
func (z *Zlib) inflateSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}

	return returnBytes(info.Context(), result)
}

// deflateRawSyncFunc compresses data using raw deflate (no header)
func (z *Zlib) deflateRawSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	var buf bytes.Buffer
	writer, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil
	}
	if _, err := writer.Write(data); err != nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return nil
	}

	return returnBytes(info.Context(), buf.Bytes())
}

// inflateRawSyncFunc decompresses raw deflate data (no header)
func (z *Zlib) inflateRawSyncFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	data := getBytes(args[0])

	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}

	return returnBytes(info.Context(), result)
}
