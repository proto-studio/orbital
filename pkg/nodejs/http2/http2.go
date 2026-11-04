// Package http2 implements the client side of the Node.js http2 module.
//
// Every session is one HTTP/2 connection, as in Node. Connections are established
// only through the runtime's injected controls: Config.DNSResolver finds the
// address, Config.SocketFactory opens the socket to that address, and crypto/tls
// runs on top with normal certificate verification. There is no other path to the
// network; see dial.go.
package http2

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed http2.js
var http2JS string

// shutdownGrace bounds how long closing a session waits on a peer that has stopped
// reading before the socket is closed underneath it.
const shutdownGrace = 2 * time.Second

// HTTP2 provides HTTP/2 functionality.
type HTTP2 struct {
	rt        *runtime.Runtime
	sessions  map[int64]*session
	sessionID int64
	mu        sync.Mutex
}

// session is one client connection and the streams multiplexed on it.
type session struct {
	h      *HTTP2
	id     int64
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	cc         *http2.ClientConn
	conn       io.Closer // socket beneath TLS
	streams    map[int64]*clientStream
	resourceID uint64
	destroyed  bool
	holding    bool // holds the event loop open
}

// clientStream is one request/response exchange.
type clientStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	reqW   *io.PipeWriter // nil when the request was sent with END_STREAM

	mu   sync.Mutex
	body io.ReadCloser
	resp *http.Response
}

// New creates a new HTTP2 module.
func New() *HTTP2 {
	return &HTTP2{sessions: make(map[int64]*session)}
}

// Name returns the module name.
func (h *HTTP2) Name() string {
	return "http2"
}

// Register sets up the http2 module.
func (h *HTTP2) Register(rt *runtime.Runtime) error {
	h.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"connect":        h.connectFunc,
		"request":        h.requestFunc,
		"readStream":     h.readStreamFunc,
		"closeStream":    h.closeStreamFunc,
		"writeStream":    h.writeStreamFunc,
		"endStream":      h.endStreamFunc,
		"goaway":         h.goawayFunc,
		"ping":           h.pingFunc,
		"setRef":         h.setRefFunc,
		"destroySession": h.destroySessionFunc,
		"createServer":   h.createServerFunc,
		"closeServer":    h.closeServerFunc,
		"respond":        h.respondFunc,
	}

	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := internal.Set(name, val); err != nil {
			return err
		}
	}

	if err := rt.SetGlobal("__http2_internal", internal); err != nil {
		return err
	}

	if _, err := rt.RunScript(http2JS, "http2.js"); err != nil {
		return err
	}

	return nil
}

// deliver runs fn on the JavaScript thread.
func (h *HTTP2) deliver(fn func(ctx *v8.Context)) {
	h.rt.EventLoop().EnqueueMicrotask(func() {
		ctx := h.rt.Context()
		fn(ctx)
		ctx.PerformMicrotaskCheckpoint()
	})
}

// jsString makes a string value, or null for "".
func jsString(ctx *v8.Context, s string) *v8.Value {
	if s == "" {
		return ctx.Null()
	}
	v, err := ctx.NewString(s)
	if err != nil {
		return ctx.Null()
	}
	return v
}

// connect(host, port, optionsJSON, onConnect(errMsg, errCode), onClose(errMsg, errCode)) -> id
func (h *HTTP2) connectFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 5 {
		return ctx.Null()
	}

	opts := dialOptions{host: args[0].String(), port: int(args[1].Integer())}
	if err := json.Unmarshal([]byte(args[2].String()), &opts.dialJSON); err != nil {
		return ctx.Throw("http2: invalid connect options")
	}
	onConnect, onClose := args[3], args[4]

	sctx, cancel := context.WithCancel(h.rt.ExecutionContext())
	s := &session{h: h, ctx: sctx, cancel: cancel, streams: make(map[int64]*clientStream)}
	s.resourceID = h.rt.TrackResource(s)
	s.setRef(true)

	h.mu.Lock()
	h.sessionID++
	s.id = h.sessionID
	h.sessions[s.id] = s
	h.mu.Unlock()

	go func() {
		err := s.establish(opts, func(err error) {
			msg, code := "", ""
			if err != nil {
				msg, code = describeError(err, opts)
			}
			h.deliver(func(ctx *v8.Context) {
				h.rt.MakeCallback(onClose, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code))
			})
		})
		msg, code := "", ""
		if err != nil {
			msg, code = describeError(err, opts)
		}
		h.deliver(func(ctx *v8.Context) {
			h.rt.MakeCallback(onConnect, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code))
		})
	}()

	return ctx.NewNumber(float64(s.id))
}

// establish dials and starts HTTP/2 on the connection. onClose runs once if the
// established connection later ends.
func (s *session) establish(opts dialOptions, onClose func(error)) error {
	conn, raw, err := s.h.dial(s.ctx, opts)
	if err != nil {
		return err
	}

	// Compression is left to the application, as Node does not negotiate it.
	transport := &http2.Transport{DisableCompression: true}
	cc, err := transport.NewClientConn(&closeNotifyConn{Conn: conn, onClose: onClose})
	if err != nil {
		raw.Close()
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		go func() {
			cc.Close()
			raw.Close()
		}()
		return context.Canceled
	}
	s.cc = cc
	s.conn = raw
	return nil
}

func (h *HTTP2) session(id int64) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

func (s *session) stream(id int64) *clientStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

// request(sessionID, streamID, headersJSON, endStream, cb(errMsg, errCode, rstCode, headersJSON))
func (h *HTTP2) requestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 5 {
		return nil
	}

	sessionID := int64(args[0].Integer())
	streamID := int64(args[1].Integer())
	headersJSON := args[2].String()
	endStream := args[3].Boolean()
	callback := args[4]

	reply := func(msg, code string, rst int, headers string) {
		h.deliver(func(ctx *v8.Context) {
			h.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code), ctx.NewInteger(int64(rst)), jsString(ctx, headers))
		})
	}

	s := h.session(sessionID)
	if s == nil {
		reply("The session has been destroyed", "ERR_HTTP2_INVALID_SESSION", 0, "")
		return nil
	}
	s.mu.Lock()
	cc := s.cc
	s.mu.Unlock()
	if cc == nil {
		reply("The session has been destroyed", "ERR_HTTP2_INVALID_SESSION", 0, "")
		return nil
	}

	var headers map[string]any
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		reply("Invalid headers", "ERR_HTTP2_INVALID_HEADER_VALUE", 0, "")
		return nil
	}

	cs := &clientStream{}
	cs.ctx, cs.cancel = context.WithCancel(s.ctx)

	var body io.ReadCloser = http.NoBody
	if !endStream {
		pr, pw := io.Pipe()
		body = pr
		cs.reqW = pw
	}

	req, err := buildRequest(cs.ctx, headers, body)
	if err != nil {
		cs.close()
		reply(err.Error(), "ERR_HTTP2_INVALID_HEADER_VALUE", 0, "")
		return nil
	}

	s.mu.Lock()
	s.streams[streamID] = cs
	s.mu.Unlock()

	go func() {
		// The ClientConn is bound to the session's dialed connection, so nothing in
		// the request, including :authority, can change where it is sent.
		resp, err := cc.RoundTrip(req)
		if err != nil {
			cs.close()
			msg, code, rst := describeStreamError(err)
			reply(msg, code, rst, "")
			return
		}
		if !cs.setResponse(resp) {
			resp.Body.Close()
			return
		}
		reply("", "", 0, responseHeadersJSON(resp))
	}()

	return nil
}

// buildRequest converts Node-style headers (pseudo-headers included) to a request.
func buildRequest(ctx context.Context, headers map[string]any, body io.ReadCloser) (*http.Request, error) {
	pseudo := func(name, fallback string) string {
		if v, ok := headers[name].(string); ok && v != "" {
			return v
		}
		return fallback
	}
	method := pseudo(":method", http.MethodGet)
	scheme := pseudo(":scheme", "https")
	authority := pseudo(":authority", "")
	path := pseudo(":path", "/")
	if method == http.MethodConnect {
		path = ""
	}

	req, err := http.NewRequestWithContext(ctx, method, scheme+"://"+authority+path, body)
	if err != nil {
		return nil, err
	}
	req.Host = authority

	req.ContentLength = 0
	if body != http.NoBody {
		req.ContentLength = -1
	}

	for key, value := range headers {
		if strings.HasPrefix(key, ":") {
			continue
		}
		name := http.CanonicalHeaderKey(key)
		switch v := value.(type) {
		case string:
			req.Header[name] = []string{v}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					req.Header[name] = append(req.Header[name], s)
				}
			}
		}
		if name == "Content-Length" && body != http.NoBody {
			if n, err := strconv.ParseInt(req.Header.Get(name), 10, 64); err == nil {
				req.ContentLength = n
			}
			req.Header.Del(name)
		}
	}
	return req, nil
}

// responseHeadersJSON returns response headers in Node's form: lower-case names, a
// numeric :status, set-cookie as an array, and other repeated headers joined.
func responseHeadersJSON(resp *http.Response) string {
	out := map[string]any{":status": resp.StatusCode}
	// net/http moves the Trailer header into resp.Trailer's keys.
	if len(resp.Trailer) > 0 {
		names := make([]string, 0, len(resp.Trailer))
		for name := range resp.Trailer {
			names = append(names, name)
		}
		sort.Strings(names)
		out["trailer"] = strings.Join(names, ", ")
	}
	for key, values := range resp.Header {
		name := strings.ToLower(key)
		switch name {
		case "set-cookie":
			out[name] = values
		case "cookie":
			out[name] = strings.Join(values, "; ")
		default:
			out[name] = strings.Join(values, ", ")
		}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func trailersJSON(resp *http.Response) string {
	if len(resp.Trailer) == 0 {
		return ""
	}
	out := make(map[string]string, len(resp.Trailer))
	for key, values := range resp.Trailer {
		if len(values) > 0 {
			out[strings.ToLower(key)] = strings.Join(values, ", ")
		}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func (cs *clientStream) setResponse(resp *http.Response) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.ctx.Err() != nil {
		return false
	}
	cs.resp = resp
	cs.body = resp.Body
	return true
}

// close cancels the exchange. If it is still open this sends RST_STREAM.
func (cs *clientStream) close() {
	cs.cancel()
	if cs.reqW != nil {
		cs.reqW.CloseWithError(context.Canceled)
	}
	cs.mu.Lock()
	body := cs.body
	cs.body = nil
	cs.mu.Unlock()
	if body != nil {
		body.Close()
	}
}

// readStream(sessionID, streamID, cb(errMsg, errCode, rstCode, chunk, done, trailersJSON))
//
// One call reads one chunk, so an application that stops reading stops consuming
// the stream's HTTP/2 flow-control window.
func (h *HTTP2) readStreamFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	callback := args[2]

	reply := func(msg, code string, rst int, chunk []byte, done bool, trailers string) {
		h.deliver(func(ctx *v8.Context) {
			chunkVal := ctx.Null()
			if len(chunk) > 0 {
				chunkVal = jsString(ctx, latin1String(chunk))
			}
			h.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code), ctx.NewInteger(int64(rst)),
				chunkVal, ctx.NewBoolean(done), jsString(ctx, trailers))
		})
	}

	s := h.session(int64(args[0].Integer()))
	if s == nil {
		return nil
	}
	cs := s.stream(int64(args[1].Integer()))
	if cs == nil {
		return nil
	}
	cs.mu.Lock()
	body, resp := cs.body, cs.resp
	cs.mu.Unlock()
	if body == nil {
		return nil
	}

	go func() {
		buf := make([]byte, 64*1024)
		n, err := body.Read(buf)
		switch {
		case err == nil:
			reply("", "", 0, buf[:n], false, "")
		case errors.Is(err, io.EOF):
			reply("", "", 0, buf[:n], true, trailersJSON(resp))
		default:
			msg, code, rst := describeStreamError(err)
			reply(msg, code, rst, nil, false, "")
		}
	}()
	return nil
}

// writeStream(sessionID, streamID, base64Data, cb(errMsg, errCode)). cb runs once
// HTTP/2 has taken the bytes.
func (h *HTTP2) writeStreamFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}
	callback := args[3]
	data, decodeErr := base64.StdEncoding.DecodeString(args[2].String())

	reply := func(msg, code string) {
		h.deliver(func(ctx *v8.Context) {
			h.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code))
		})
	}

	if decodeErr != nil {
		reply(decodeErr.Error(), "ERR_INVALID_ARG_VALUE")
		return nil
	}
	cs := h.findStream(args)
	if cs == nil || cs.reqW == nil {
		reply("", "")
		return nil
	}
	go func() {
		_, err := cs.reqW.Write(data)
		// The peer may finish the exchange before reading the whole request. That
		// is the end of the request body, not a failed write.
		if err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, context.Canceled) {
			msg, code, _ := describeStreamError(err)
			reply(msg, code)
			return
		}
		reply("", "")
	}()
	return nil
}

// endStream(sessionID, streamID) sends END_STREAM on the request.
func (h *HTTP2) endStreamFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	if cs := h.findStream(info.Args()); cs != nil && cs.reqW != nil {
		cs.reqW.Close()
	}
	return nil
}

// closeStream(sessionID, streamID) ends the exchange and forgets the stream.
func (h *HTTP2) closeStreamFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	s := h.session(int64(args[0].Integer()))
	if s == nil {
		return nil
	}
	id := int64(args[1].Integer())
	s.mu.Lock()
	cs := s.streams[id]
	delete(s.streams, id)
	s.mu.Unlock()
	if cs != nil {
		cs.close()
	}
	return nil
}

func (h *HTTP2) findStream(args []*v8.Value) *clientStream {
	if len(args) < 2 {
		return nil
	}
	s := h.session(int64(args[0].Integer()))
	if s == nil {
		return nil
	}
	return s.stream(int64(args[1].Integer()))
}

func (h *HTTP2) goawayFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// The HTTP/2 client only sends GOAWAY as part of closing; see destroySession.
	return nil
}

// ping(sessionID, cb(errMsg, errCode, durationMs))
func (h *HTTP2) pingFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	callback := args[1]
	reply := func(msg, code string, ms float64) {
		h.deliver(func(ctx *v8.Context) {
			h.rt.MakeCallback(callback, ctx.Undefined(), jsString(ctx, msg), jsString(ctx, code), ctx.NewNumber(ms))
		})
	}

	s := h.session(int64(args[0].Integer()))
	var cc *http2.ClientConn
	if s != nil {
		s.mu.Lock()
		cc = s.cc
		s.mu.Unlock()
	}
	if cc == nil {
		reply("The session has been destroyed", "ERR_HTTP2_INVALID_SESSION", 0)
		return nil
	}
	go func() {
		start := time.Now()
		if err := cc.Ping(s.ctx); err != nil {
			msg, code, _ := describeStreamError(err)
			reply(msg, code, 0)
			return
		}
		reply("", "", float64(time.Since(start).Microseconds())/1000)
	}()
	return nil
}

// setRef(sessionID, ref) controls whether the open session keeps the loop alive.
func (h *HTTP2) setRefFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}
	if s := h.session(int64(args[0].Integer())); s != nil {
		s.setRef(args[1].Boolean())
	}
	return nil
}

func (s *session) setRef(ref bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed || ref == s.holding {
		return
	}
	s.holding = ref
	if ref {
		s.h.rt.EventLoop().AddPendingWork()
	} else {
		s.h.rt.EventLoop().DonePendingWork()
	}
}

// destroySession(sessionID, graceful) ends the session and its connection.
func (h *HTTP2) destroySessionFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	if s := h.session(int64(args[0].Integer())); s != nil {
		s.destroy(len(args) > 1 && args[1].Boolean())
	}
	return nil
}

// Close implements runtime.CloseableResource so disposal closes open sessions.
func (s *session) Close() error {
	s.destroy(false)
	return nil
}

// destroy never touches V8, and never blocks the caller on the network.
func (s *session) destroy(graceful bool) {
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		return
	}
	s.destroyed = true
	cc, conn := s.cc, s.conn
	streams := s.streams
	s.streams = map[int64]*clientStream{}
	holding := s.holding
	s.holding = false
	s.mu.Unlock()

	s.h.mu.Lock()
	delete(s.h.sessions, s.id)
	s.h.mu.Unlock()
	s.h.rt.UntrackResource(s.resourceID)

	for _, cs := range streams {
		cs.close()
	}

	go func() {
		if cc != nil {
			if conn != nil {
				timer := time.AfterFunc(shutdownGrace, func() { conn.Close() })
				defer timer.Stop()
			}
			if graceful {
				shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownGrace/2)
				cc.Shutdown(shutdownCtx)
				cancelShutdown()
			}
			cc.Close()
		}
		if conn != nil {
			conn.Close()
		}
		s.cancel()
	}()

	if holding {
		s.h.rt.EventLoop().DonePendingWork()
	}
}

// The http2 server is not implemented: createServer never listens.

func (h *HTTP2) createServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return info.Context().NewNumber(0)
}

func (h *HTTP2) closeServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return nil
}

func (h *HTTP2) respondFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return nil
}

func latin1String(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}
