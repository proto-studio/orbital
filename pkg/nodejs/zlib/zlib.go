// Package zlib implements the Node.js zlib module.
package zlib

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"sync"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed zlib.js
var zlibJS string

// Zlib provides compression functionality.
type Zlib struct {
	rt *runtime.Runtime

	// Streaming transform handles backing zlib.create{Gunzip,Inflate,Unzip,...}.
	// Each is a Go (de)compressor fed by an io.Pipe on a goroutine, so bulk
	// (de)compression runs off the JS thread and streams chunks back through the
	// event loop.
	mu      sync.Mutex
	streams map[int]*zStream
	nextID  int
}

// zStream is one native streaming (de)compressor: JS writes source bytes into
// inW, a goroutine runs the codec and emits output chunks via the JS callback.
// The codec goroutine is started lazily on the first write/end so an unused
// stream (e.g. created for an instanceof check) does not block a pipe read
// forever and keep the event loop alive.
type zStream struct {
	z        *Zlib
	id       int
	ctx      *v8.Context
	format   string
	callback *v8.Value
	pr       *io.PipeReader
	inW      *io.PipeWriter

	startMu sync.Mutex
	started bool
}

// ensureStarted launches the codec goroutine exactly once.
func (s *zStream) ensureStarted() {
	s.startMu.Lock()
	if s.started {
		s.startMu.Unlock()
		return
	}
	s.started = true
	s.startMu.Unlock()

	s.z.rt.EventLoop().AddPendingWork()
	go func() {
		defer s.z.rt.EventLoop().DonePendingWork()

		err := s.z.runTransform(s.ctx, s.format, s.pr, s.callback)

		s.z.mu.Lock()
		delete(s.z.streams, s.id)
		s.z.mu.Unlock()

		if err != nil && err != io.EOF {
			s.pr.CloseWithError(err)
			s.z.rt.EventLoop().EnqueueMicrotask(func() {
				errVal, _ := s.ctx.NewString(err.Error())
				s.callback.Call(nil, errVal, s.ctx.Null())
			})
			return
		}
		s.pr.Close()
		s.z.rt.EventLoop().EnqueueMicrotask(func() {
			s.callback.Call(nil, s.ctx.Null(), s.ctx.Null())
		})
	}()
}

// New creates a new Zlib module.
func New() *Zlib {
	return &Zlib{
		streams: make(map[int]*zStream),
		nextID:  1,
	}
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

	// Streaming transform API (create/write/end/destroy) backing the Node
	// zlib.create* stream classes. Registered via a small helper for brevity.
	regFn := func(name string, cb v8.FunctionCallback) error {
		tmpl, err := iso.NewFunctionTemplate(cb)
		if err != nil {
			return err
		}
		fn, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		return zlibInternal.Set(name, fn)
	}
	if err := regFn("_streamCreate", z.streamCreateFunc); err != nil {
		return err
	}
	if err := regFn("_streamWrite", z.streamWriteFunc); err != nil {
		return err
	}
	if err := regFn("_streamEnd", z.streamEndFunc); err != nil {
		return err
	}
	if err := regFn("_streamDestroy", z.streamDestroyFunc); err != nil {
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
func getBytes(val *v8.Value) []byte {
	str := val.String()
	// Try base64 decode first
	if decoded, err := base64.StdEncoding.DecodeString(str); err == nil {
		return decoded
	}
	return []byte(str)
}

// Helper to return bytes as base64 string
func returnBytes(ctx *v8.Context, data []byte) *v8.Value {
	encoded := base64.StdEncoding.EncodeToString(data)
	val, _ := ctx.NewString(encoded)
	return val
}

// gzipSyncFunc compresses data using gzip
func (z *Zlib) gzipSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (z *Zlib) gunzipSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (z *Zlib) deflateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (z *Zlib) inflateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (z *Zlib) deflateRawSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
func (z *Zlib) inflateRawSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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

// zSink is the output side of a streaming transform: every codec write is
// forwarded to the JS callback as a base64 chunk via the event loop. base64
// encoding copies the bytes synchronously, so the io.Copy buffer can be reused.
type zSink struct {
	z   *Zlib
	ctx *v8.Context
	cb  *v8.Value
}

func (s *zSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	enc := base64.StdEncoding.EncodeToString(p)
	s.z.rt.EventLoop().EnqueueMicrotask(func() {
		dataVal, _ := s.ctx.NewString(enc)
		s.cb.Call(nil, s.ctx.Null(), dataVal)
	})
	return len(p), nil
}

// streamCreateFunc starts a streaming (de)compressor and returns its handle id.
// The codec runs on a goroutine reading from an io.Pipe that JS feeds via
// _streamWrite; output chunks and the terminal end/error are delivered through
// the supplied callback (err, chunk) — a null chunk signals end.
func (z *Zlib) streamCreateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 || !args[1].IsFunction() {
		return nil
	}
	format := args[0].String()
	callback := args[1]

	pr, pw := io.Pipe()

	z.mu.Lock()
	id := z.nextID
	z.nextID++
	h := &zStream{
		z:        z,
		id:       id,
		ctx:      ctx,
		format:   format,
		callback: callback,
		pr:       pr,
		inW:      pw,
	}
	z.streams[id] = h
	z.mu.Unlock()

	return ctx.NewInteger(int64(id))
}

// runTransform drives the Go codec for the given Node stream format, copying the
// piped input through the (de)compressor into the JS-delivering sink.
func (z *Zlib) runTransform(ctx *v8.Context, format string, pr *io.PipeReader, callback *v8.Value) error {
	out := &zSink{z: z, ctx: ctx, cb: callback}

	switch format {
	case "gunzip":
		r, err := gzip.NewReader(pr)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, r)
		r.Close()
		return err
	case "inflate":
		r, err := zlib.NewReader(pr)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, r)
		r.Close()
		return err
	case "inflateraw":
		r := flate.NewReader(pr)
		_, err := io.Copy(out, r)
		r.Close()
		return err
	case "unzip":
		return z.runUnzip(out, pr)
	case "gzip":
		w := gzip.NewWriter(out)
		if _, err := io.Copy(w, pr); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	case "deflate":
		w := zlib.NewWriter(out)
		if _, err := io.Copy(w, pr); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	case "deflateraw":
		w, err := flate.NewWriter(out, flate.DefaultCompression)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, pr); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	default:
		return fmt.Errorf("zlib: unknown stream format %q", format)
	}
}

// runUnzip auto-detects gzip vs zlib vs raw-deflate from the first bytes, the
// way Node's Unzip does, then decompresses through the detected codec.
func (z *Zlib) runUnzip(out io.Writer, pr *io.PipeReader) error {
	br := bufio.NewReader(pr)
	hdr, err := br.Peek(2)
	if err != nil && err != io.EOF {
		return err
	}
	switch {
	case len(hdr) >= 2 && hdr[0] == 0x1f && hdr[1] == 0x8b:
		r, e := gzip.NewReader(br)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, r)
		r.Close()
		return e
	case len(hdr) >= 1 && hdr[0]&0x0f == 0x08:
		r, e := zlib.NewReader(br)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, r)
		r.Close()
		return e
	default:
		r := flate.NewReader(br)
		_, e := io.Copy(out, r)
		r.Close()
		return e
	}
}

// streamWriteFunc feeds one base64-encoded input chunk into a stream handle.
func (z *Zlib) streamWriteFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	id := int(args[0].Integer())

	z.mu.Lock()
	h, ok := z.streams[id]
	z.mu.Unlock()
	if !ok {
		return nil
	}

	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return nil
	}
	h.ensureStarted()
	// Blocks until the codec goroutine consumes the bytes (io.Pipe backpressure);
	// the sink never blocks, so the goroutine always drains and this returns.
	h.inW.Write(data)
	return nil
}

// streamEndFunc closes the input side so the codec sees EOF and finishes.
func (z *Zlib) streamEndFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int(args[0].Integer())

	z.mu.Lock()
	h, ok := z.streams[id]
	z.mu.Unlock()
	if ok {
		h.ensureStarted()
		h.inW.Close()
	}
	return nil
}

// streamDestroyFunc tears down a stream handle, unblocking the codec goroutine.
func (z *Zlib) streamDestroyFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int(args[0].Integer())

	z.mu.Lock()
	h, ok := z.streams[id]
	if ok {
		delete(z.streams, id)
	}
	z.mu.Unlock()
	if ok {
		h.inW.CloseWithError(io.ErrClosedPipe)
	}
	return nil
}
