// Package fetch implements the Web Fetch API.
package fetch

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed fetch.js
var fetchJS string

// Fetch provides the fetch API.
type Fetch struct {
	rt     *runtime.Runtime
	mu     sync.Mutex
	ops    map[int64]*fetchOp
	nextID int64
}

// fetchOp is one in-flight fetch. The same id identifies the request before
// headers arrive and the response body stream afterward.
type fetchOp struct {
	id        int64
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	readMu    sync.Mutex
	body      io.ReadCloser
	closed    bool
	aborted   atomic.Bool
	userStop  atomic.Bool // reader.cancel / body.cancel, a clean close
	tracked   atomic.Bool // holds event-loop pending work for an open stream
	delivered atomic.Bool
}

// New creates a new Fetch module.
func New() *Fetch {
	return &Fetch{}
}

// Name returns the module name.
func (f *Fetch) Name() string {
	return "fetch"
}

// Register sets up the fetch API globals.
func (f *Fetch) Register(rt *runtime.Runtime) error {
	f.rt = rt
	f.mu.Lock()
	f.ops = make(map[int64]*fetchOp)
	f.mu.Unlock()

	iso := rt.Isolate()
	ctx := rt.Context()

	bindings := []struct {
		name string
		fn   v8.FunctionCallback
	}{
		{"__http_fetch", f.fetchFunc},
		{"__http_fetch_read", f.readFunc},
		{"__http_fetch_cancel", f.cancelFunc},
		{"__http_fetch_abort", f.abortFunc},
	}
	for _, binding := range bindings {
		fn, err := iso.NewFunctionTemplate(binding.fn)
		if err != nil {
			return err
		}
		val, err := fn.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := rt.SetGlobal(binding.name, val); err != nil {
			return err
		}
	}

	if _, err := rt.RunScript(fetchJS, "fetch.js"); err != nil {
		return err
	}
	return nil
}

// fetchFunc implements __http_fetch. It returns a request id and invokes the
// callback once status and headers are available, not when the body ends.
func (f *Fetch) fetchFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}

	url := args[0].String()
	method := args[1].String()
	headersJSON := args[2].String()
	body := args[3].String()
	callback := args[4]
	if !callback.IsFunction() {
		return nil
	}

	var headers map[string]string
	if headersJSON != "" {
		json.Unmarshal([]byte(headersJSON), &headers)
	}
	reqHeaders := make(map[string][]string, len(headers))
	for k, v := range headers {
		reqHeaders[k] = []string{v}
	}

	parent := f.rt.ExecutionContext()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	op := &fetchOp{
		id:     atomic.AddInt64(&f.nextID, 1),
		ctx:    ctx,
		cancel: cancel,
	}
	f.mu.Lock()
	f.ops[op.id] = op
	f.mu.Unlock()

	// When the isolate deadline (or an explicit cancel) fires, drop the body
	// even if JavaScript is not currently reading it.
	go func() {
		<-op.ctx.Done()
		f.closeBody(op)
	}()

	req := &runtime.Request{
		Method:  method,
		URL:     url,
		Headers: reqHeaders,
		Body:    []byte(body),
	}
	jsCtx := info.Context()
	client := f.rt.HTTPClient()

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()
		f.perform(op, client, req, jsCtx, callback)
	}()

	return jsCtx.NewNumber(float64(op.id))
}

func (f *Fetch) perform(op *fetchOp, client runtime.HTTPClient, req *runtime.Request, jsCtx *v8.Context, callback *v8.Value) {
	resp, err := client.Do(op.ctx, req)
	if err != nil || op.aborted.Load() || op.ctx.Err() != nil {
		if resp != nil && resp.BodyReader != nil {
			resp.BodyReader.Close()
		}
		msg := "fetch failed"
		switch {
		case op.aborted.Load():
			msg = "AbortError"
		case err != nil:
			msg = err.Error()
		case op.ctx.Err() != nil:
			msg = op.ctx.Err().Error()
		}
		f.finishEarly(op, jsCtx, callback, msg)
		return
	}

	if responseHasNullBody(req.Method, resp.StatusCode) {
		if resp.BodyReader != nil {
			resp.BodyReader.Close()
			resp.BodyReader = nil
		}
		resp.Body = nil
		f.enqueueResponse(op, jsCtx, callback, resp, true, false)
		f.closeBody(op)
		return
	}

	if resp.BodyReader != nil {
		if !f.attachBody(op, resp.BodyReader) {
			resp.BodyReader.Close()
			msg := "fetch failed"
			if op.aborted.Load() {
				msg = "AbortError"
			} else if op.ctx.Err() != nil {
				msg = op.ctx.Err().Error()
			}
			f.finishEarly(op, jsCtx, callback, msg)
			return
		}
		f.enqueueResponse(op, jsCtx, callback, resp, false, true)
		return
	}

	f.enqueueResponse(op, jsCtx, callback, resp, false, false)
	f.closeBody(op)
}

func (f *Fetch) attachBody(op *fetchOp, body io.ReadCloser) bool {
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.closed || op.ctx.Err() != nil {
		return false
	}
	op.body = body
	// Hold the event loop open for the life of the connection, including the
	// gap between headers arriving and the first read. Tracked is set under
	// the same lock closeBody uses so the hold cannot be missed or double-freed.
	if op.tracked.CompareAndSwap(false, true) {
		f.rt.EventLoop().AddPendingWork()
	}
	return true
}

func (f *Fetch) enqueueResponse(op *fetchOp, jsCtx *v8.Context, callback *v8.Value, resp *runtime.Response, nullBody, stream bool) {
	if resp == nil || !op.delivered.CompareAndSwap(false, true) {
		return
	}
	flat := make(map[string]string, len(resp.Headers))
	for k, v := range resp.Headers {
		if len(v) > 0 {
			flat[k] = v[0]
		}
	}
	headersJSON, _ := json.Marshal(flat)
	statusText := getStatusText(resp.StatusCode)
	status := resp.StatusCode
	var buffered []byte
	if !stream && !nullBody {
		buffered = append([]byte(nil), resp.Body...)
	}
	streamID := int64(0)
	if stream {
		streamID = op.id
	}

	f.rt.EventLoop().EnqueueMicrotask(func() {
		// op.ctx is also cancelled when a buffered body is released, so that is
		// not a failure signal. Abort and the isolate deadline are.
		if op.aborted.Load() || f.isolateDone() {
			if stream {
				f.closeBody(op)
			}
			errMsg := "fetch failed"
			if op.aborted.Load() {
				errMsg = "AbortError"
			} else if ctxErr := f.rt.ExecutionContext(); ctxErr != nil && ctxErr.Err() != nil {
				errMsg = ctxErr.Err().Error()
			}
			errVal, _ := jsCtx.NewString(errMsg)
			callback.Call(nil, errVal, jsCtx.NewNumber(0), jsCtx.Null(), jsCtx.Null(), jsCtx.Null(), jsCtx.NewNumber(0))
			jsCtx.PerformMicrotaskCheckpoint()
			return
		}

		var bodyVal *v8.Value
		if nullBody || stream {
			bodyVal = jsCtx.Null()
		} else {
			bodyVal, _ = jsCtx.NewString(latin1String(buffered))
		}
		headersVal, _ := jsCtx.NewString(string(headersJSON))
		statusVal, _ := jsCtx.NewString(statusText)
		callback.Call(nil,
			jsCtx.Null(),
			jsCtx.NewNumber(float64(status)),
			statusVal,
			headersVal,
			bodyVal,
			jsCtx.NewNumber(float64(streamID)),
		)
		jsCtx.PerformMicrotaskCheckpoint()
	})
}

func (f *Fetch) finishEarly(op *fetchOp, jsCtx *v8.Context, callback *v8.Value, msg string) {
	f.closeBody(op)
	if !op.delivered.CompareAndSwap(false, true) {
		return
	}
	f.rt.EventLoop().EnqueueMicrotask(func() {
		errVal, _ := jsCtx.NewString(msg)
		callback.Call(nil, errVal, jsCtx.NewNumber(0), jsCtx.Null(), jsCtx.Null(), jsCtx.Null(), jsCtx.NewNumber(0))
		jsCtx.PerformMicrotaskCheckpoint()
	})
}

// readFunc implements __http_fetch_read(id, callback). callback(err, chunk, done).
func (f *Fetch) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 || !args[1].IsFunction() {
		return nil
	}
	id := args[0].Integer()
	callback := args[1]
	jsCtx := info.Context()

	f.mu.Lock()
	op := f.ops[id]
	f.mu.Unlock()
	if op == nil {
		f.enqueueRead(jsCtx, callback, "", nil, true)
		return nil
	}

	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		op.mu.Lock()
		body := op.body
		closed := op.closed
		op.mu.Unlock()
		if op.aborted.Load() {
			f.closeBody(op)
			f.enqueueRead(jsCtx, callback, "AbortError", nil, true)
			return
		}
		if closed || body == nil {
			f.closeBody(op)
			f.enqueueRead(jsCtx, callback, "", nil, true)
			return
		}

		buf := make([]byte, 32*1024)
		var n int
		var err error
		op.readMu.Lock()
		for {
			n, err = body.Read(buf)
			if n > 0 || err != nil {
				break
			}
		}
		op.readMu.Unlock()

		if op.aborted.Load() {
			f.closeBody(op)
			f.enqueueRead(jsCtx, callback, "AbortError", nil, true)
			return
		}
		if op.userStop.Load() {
			f.closeBody(op)
			f.enqueueRead(jsCtx, callback, "", nil, true)
			return
		}
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			f.enqueueRead(jsCtx, callback, "", chunk, false)
			return
		}
		f.closeBody(op)
		if errors.Is(err, io.EOF) {
			f.enqueueRead(jsCtx, callback, "", nil, true)
			return
		}
		msg := "unexpected EOF"
		if err != nil {
			msg = err.Error()
		} else if op.ctx.Err() != nil {
			msg = op.ctx.Err().Error()
		}
		f.enqueueRead(jsCtx, callback, msg, nil, false)
	}()
	return nil
}

func (f *Fetch) enqueueRead(jsCtx *v8.Context, callback *v8.Value, errMsg string, chunk []byte, done bool) {
	f.rt.EventLoop().EnqueueMicrotask(func() {
		var errVal *v8.Value
		if errMsg != "" {
			errVal, _ = jsCtx.NewString(errMsg)
		} else {
			errVal = jsCtx.Null()
		}
		var chunkVal *v8.Value
		if chunk != nil {
			chunkVal, _ = jsCtx.NewString(latin1String(chunk))
		} else {
			chunkVal = jsCtx.Null()
		}
		callback.Call(nil, errVal, chunkVal, jsCtx.NewBoolean(done))
		jsCtx.PerformMicrotaskCheckpoint()
	})
}

// cancelFunc implements __http_fetch_cancel(id): a clean stream cancel.
func (f *Fetch) cancelFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	f.stop(info, false)
	return nil
}

// abortFunc implements __http_fetch_abort(id): AbortSignal fired.
func (f *Fetch) abortFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	f.stop(info, true)
	return nil
}

func (f *Fetch) stop(info *v8.FunctionCallbackInfo, abort bool) {
	args := info.Args()
	if len(args) < 1 {
		return
	}
	f.mu.Lock()
	op := f.ops[args[0].Integer()]
	f.mu.Unlock()
	if op == nil {
		return
	}
	if abort {
		op.aborted.Store(true)
	} else {
		op.userStop.Store(true)
	}
	// Cancel before close so a blocked Do or Read observes the flag and the
	// request context ending together.
	if op.cancel != nil {
		op.cancel()
	}
	f.closeBody(op)
}

// closeBody releases the response body and the event-loop hold exactly once.
func (f *Fetch) closeBody(op *fetchOp) {
	if op == nil {
		return
	}
	op.mu.Lock()
	if op.closed {
		op.mu.Unlock()
		return
	}
	op.closed = true
	body := op.body
	op.body = nil
	release := op.tracked.Swap(false)
	op.mu.Unlock()

	if body != nil {
		body.Close()
	}
	if op.cancel != nil {
		op.cancel()
	}
	f.mu.Lock()
	if f.ops[op.id] == op {
		delete(f.ops, op.id)
	}
	f.mu.Unlock()
	if release {
		f.rt.EventLoop().DonePendingWork()
	}
}

func (f *Fetch) isolateDone() bool {
	if f == nil || f.rt == nil {
		return false
	}
	ctx := f.rt.ExecutionContext()
	return ctx != nil && ctx.Err() != nil
}

func responseHasNullBody(method string, status int) bool {
	if strings.EqualFold(method, "HEAD") {
		return true
	}
	switch status {
	case 101, 204, 205, 304:
		return true
	default:
		return false
	}
}

// latin1String maps each raw byte to the Unicode code point of the same value
// so the bytes survive the crossing into V8. JavaScript recovers them with
// charCodeAt. Invalid UTF-8 must not be passed through NewString directly.
func latin1String(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func getStatusText(code int) string {
	texts := map[int]string{
		100: "Continue",
		101: "Switching Protocols",
		200: "OK",
		201: "Created",
		202: "Accepted",
		204: "No Content",
		301: "Moved Permanently",
		302: "Found",
		303: "See Other",
		304: "Not Modified",
		307: "Temporary Redirect",
		308: "Permanent Redirect",
		400: "Bad Request",
		401: "Unauthorized",
		403: "Forbidden",
		404: "Not Found",
		405: "Method Not Allowed",
		408: "Request Timeout",
		409: "Conflict",
		410: "Gone",
		429: "Too Many Requests",
		500: "Internal Server Error",
		501: "Not Implemented",
		502: "Bad Gateway",
		503: "Service Unavailable",
		504: "Gateway Timeout",
	}
	if text, ok := texts[code]; ok {
		return text
	}
	return ""
}
