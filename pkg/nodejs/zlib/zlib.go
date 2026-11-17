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
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"

	nodecrypto "proto.zip/studio/orbital/pkg/nodejs/crypto"
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
//
// Output is handed over one chunk at a time: after each chunk the codec waits
// for JS to acknowledge it (_streamAck), which JS does once the readable side
// has room. So a small input can't expand into unbounded memory however slowly
// the stream is read. While it waits, the codec doesn't keep the event loop
// alive, as a paused zlib stream doesn't in Node; the acknowledgement, made on
// the JS thread, takes the event loop's pending work back for it.
type zStream struct {
	z        *Zlib
	id       int
	ctx      *v8.Context
	format   string
	callback *v8.Value
	pr       *io.PipeReader
	inW      *io.PipeWriter

	// ack carries JS's acknowledgement of the chunk the codec is waiting on.
	ack chan struct{}
	// closed is closed when the stream is destroyed, so a codec waiting on an
	// acknowledgement stops.
	closed    chan struct{}
	closeOnce sync.Once
	// awaitingAck is whether the codec has handed over a chunk and released the
	// event loop's pending work until JS acknowledges it. The codec sets it
	// before it enqueues the chunk; the acknowledgement clears it, so a stray
	// _streamAck can't take pending work the codec won't give back.
	awaitingAck atomic.Bool

	startMu sync.Mutex
	started bool
}

// close stops the stream: its input ends with an error and a codec waiting on
// an acknowledgement gives up.
func (s *zStream) close() {
	s.closeOnce.Do(func() { close(s.closed) })
	s.inW.CloseWithError(io.ErrClosedPipe)
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
		// The codec holds the event loop's pending work except while it waits
		// for an acknowledgement; if it stops while waiting, it holds none.
		holding := true
		defer func() {
			if holding {
				s.z.rt.EventLoop().DonePendingWork()
			}
		}()

		err := s.z.runTransform(s, &holding)

		s.z.mu.Lock()
		delete(s.z.streams, s.id)
		s.z.mu.Unlock()

		if errors.Is(err, errStreamClosed) {
			// Destroyed, or the runtime stopped: nothing is listening.
			s.pr.CloseWithError(err)
			return
		}

		if err != nil && err != io.EOF {
			s.pr.CloseWithError(err)
			s.z.rt.EventLoop().EnqueueMicrotask(func() {
				errVal, _ := s.ctx.NewString(err.Error())
				s.z.rt.MakeCallback(s.callback, nil, errVal, s.ctx.Null())
			})
			return
		}
		s.pr.Close()
		s.z.rt.EventLoop().EnqueueMicrotask(func() {
			s.z.rt.MakeCallback(s.callback, nil, s.ctx.Null(), s.ctx.Null())
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
	if err := regFn("_streamAck", z.streamAckFunc); err != nil {
		return err
	}
	if err := zlibInternal.Set("maxOutput", ctx.NewNumber(maxOutput)); err != nil {
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

// maxOutput is the most bytes a sync call can hand back, since its result
// reaches JavaScript as one base64 string. Node allows up to
// buffer.constants.MAX_LENGTH.
const maxOutput = nodecrypto.MaxNativeBytes

// errTooLarge stops a codec whose output passes the call's limit.
var errTooLarge = errors.New("zlib: output is larger than the limit")

// outputLimit reads the output limit a sync call was given (zlib.js passes
// options.maxOutputLength), within what one result can carry. The natives check
// it themselves, since scripts can call them directly.
func outputLimit(args []*v8.Value) int64 {
	if len(args) < 2 || !args[1].IsNumber() {
		return maxOutput
	}
	f := args[1].Number()
	switch {
	case math.IsNaN(f) || f >= maxOutput:
		return maxOutput
	case f < 1:
		return 0
	}
	return int64(f)
}

// limitedBuffer collects a codec's output and fails with errTooLarge as soon as
// it would pass limit, so a small input can't expand without bound.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.limit {
		return 0, errTooLarge
	}
	return b.buf.Write(p)
}

// syncResult is a sync call's result: the output as base64, false when it was
// larger than the limit (zlib.js throws ERR_BUFFER_TOO_LARGE), or nil when the
// codec failed.
func syncResult(ctx *v8.Context, out *limitedBuffer, err error) *v8.Value {
	if errors.Is(err, errTooLarge) {
		return ctx.False()
	}
	if err != nil {
		return nil
	}
	return returnBytes(ctx, out.buf.Bytes())
}

// compressSync runs a compressor over the call's input, with its output limited.
func compressSync(info *v8.FunctionCallbackInfo, newWriter func(io.Writer) (io.WriteCloser, error)) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	data := getBytes(args[0])
	out := &limitedBuffer{limit: outputLimit(args)}
	w, err := newWriter(out)
	if err == nil {
		_, err = w.Write(data)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}
	return syncResult(info.Context(), out, err)
}

// decompressSync runs a decompressor over the call's input, reading no more
// output than the limit allows.
func decompressSync(info *v8.FunctionCallbackInfo, newReader func(io.Reader) (io.ReadCloser, error)) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	data := getBytes(args[0])
	out := &limitedBuffer{limit: outputLimit(args)}
	r, err := newReader(bytes.NewReader(data))
	if err == nil {
		_, err = io.Copy(out, r)
		r.Close()
	}
	return syncResult(info.Context(), out, err)
}

// gzipSyncFunc compresses data using gzip
func (z *Zlib) gzipSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return compressSync(info, func(w io.Writer) (io.WriteCloser, error) { return gzip.NewWriter(w), nil })
}

// gunzipSyncFunc decompresses gzip data
func (z *Zlib) gunzipSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return decompressSync(info, func(r io.Reader) (io.ReadCloser, error) { return gzip.NewReader(r) })
}

// deflateSyncFunc compresses data using deflate (zlib format)
func (z *Zlib) deflateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return compressSync(info, func(w io.Writer) (io.WriteCloser, error) { return zlib.NewWriter(w), nil })
}

// inflateSyncFunc decompresses deflate data (zlib format)
func (z *Zlib) inflateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return decompressSync(info, func(r io.Reader) (io.ReadCloser, error) { return zlib.NewReader(r) })
}

// deflateRawSyncFunc compresses data using raw deflate (no header)
func (z *Zlib) deflateRawSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return compressSync(info, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
}

// inflateRawSyncFunc decompresses raw deflate data (no header)
func (z *Zlib) inflateRawSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return decompressSync(info, func(r io.Reader) (io.ReadCloser, error) { return flate.NewReader(r), nil })
}

// errStreamClosed stops a codec whose stream was destroyed, or whose runtime
// stopped, while it waited for JS to take a chunk.
var errStreamClosed = errors.New("zlib: stream closed")

// zSink is the output side of a streaming transform: every codec write is
// forwarded to the JS callback as a base64 chunk via the event loop, and the
// write returns once JS acknowledges it. base64 encoding copies the bytes
// synchronously, so the io.Copy buffer can be reused.
type zSink struct {
	s       *zStream
	holding *bool
}

func (k *zSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s := k.s
	el := s.z.rt.EventLoop()
	enc := base64.StdEncoding.EncodeToString(p)
	s.awaitingAck.Store(true)
	el.EnqueueMicrotask(func() {
		dataVal, _ := s.ctx.NewString(enc)
		s.z.rt.MakeCallback(s.callback, nil, s.ctx.Null(), dataVal)
	})
	// The chunk is queued, so the event loop has work until JS has seen it.
	*k.holding = false
	el.DonePendingWork()
	select {
	case <-s.ack:
		*k.holding = true
		return len(p), nil
	case <-s.closed:
	case <-s.z.rt.ExecutionContext().Done():
	}
	// An acknowledgement may have arrived just before the stream closed; it took
	// pending work for the codec, which the codec now gives back on its way out.
	select {
	case <-s.ack:
		*k.holding = true
	default:
	}
	return 0, errStreamClosed
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
		ack:      make(chan struct{}, 1),
		closed:   make(chan struct{}),
	}
	z.streams[id] = h
	z.mu.Unlock()

	return ctx.NewInteger(int64(id))
}

// runTransform drives the Go codec for the given Node stream format, copying the
// piped input through the (de)compressor into the JS-delivering sink.
func (z *Zlib) runTransform(s *zStream, holding *bool) error {
	out := &zSink{s: s, holding: holding}
	pr := s.pr

	switch s.format {
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
		return fmt.Errorf("zlib: unknown stream format %q", s.format)
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

// streamWriteFunc feeds one base64-encoded input chunk into a stream handle,
// and calls back once the codec has taken it. The write happens off the JS
// thread: the codec may be waiting for JS to acknowledge output before it reads
// more input.
func (z *Zlib) streamWriteFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 || !args[2].IsFunction() {
		return nil
	}
	id := int(args[0].Integer())
	done := args[2]

	z.mu.Lock()
	h, ok := z.streams[id]
	z.mu.Unlock()
	if !ok {
		return nil
	}

	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		errVal, _ := ctx.NewString(err.Error())
		z.rt.MakeCallback(done, nil, errVal)
		return nil
	}
	h.ensureStarted()
	el := z.rt.EventLoop()
	// The write doesn't keep the event loop alive: while the codec reads, it
	// holds the loop itself, and while it waits for a paused reader, nothing
	// should, as in Node.
	go func() {
		_, werr := h.inW.Write(data)
		el.EnqueueMicrotask(func() {
			if werr != nil {
				errVal, _ := ctx.NewString(werr.Error())
				z.rt.MakeCallback(done, nil, errVal)
				return
			}
			z.rt.MakeCallback(done, nil)
		})
	}()
	return nil
}

// streamAckFunc lets a codec waiting on its last chunk continue. It runs on the
// JS thread, and takes the event loop's pending work back for the codec before
// waking it, so the loop never sees the stream idle in between.
func (z *Zlib) streamAckFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	id := int(args[0].Integer())

	z.mu.Lock()
	h, ok := z.streams[id]
	z.mu.Unlock()
	if !ok || !h.awaitingAck.CompareAndSwap(true, false) {
		return nil
	}
	select {
	case <-h.closed:
		return nil
	default:
	}
	z.rt.EventLoop().AddPendingWork()
	h.ack <- struct{}{}
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
		h.close()
	}
	return nil
}
