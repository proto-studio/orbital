// Package http2 implements the Node.js http2 module.
package http2

import (
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
	"golang.org/x/net/http2"
)

//go:embed http2.js
var http2JS string

// HTTP2 provides HTTP/2 functionality.
type HTTP2 struct {
	rt        *runtime.Runtime
	clients   map[int64]*http2Client
	servers   map[int64]*http2Server
	clientID  int64
	serverID  int64
	mu        sync.Mutex
}

type http2Client struct {
	client    *http.Client
	transport *http2.Transport
	host      string
	port      int
}

type http2Server struct {
	// Simplified - would need full HTTP/2 server implementation
}

// New creates a new HTTP2 module.
func New() *HTTP2 {
	return &HTTP2{
		clients: make(map[int64]*http2Client),
		servers: make(map[int64]*http2Server),
	}
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
	funcs := map[string]v8go.FunctionCallback{
		"connect":        h.connectFunc,
		"request":        h.requestFunc,
		"closeStream":    h.closeStreamFunc,
		"writeStream":    h.writeStreamFunc,
		"endStream":      h.endStreamFunc,
		"goaway":         h.goawayFunc,
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

func (h *HTTP2) connectFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Null()
	}

	host := args[0].String()
	port := int(args[1].Integer())
	callback := args[2]

	// Create HTTP/2 transport
	transport := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // For demo - should be configurable
		},
		AllowHTTP: true,
	}

	client := &http.Client{
		Transport: transport,
	}

	h.mu.Lock()
	id := atomic.AddInt64(&h.clientID, 1)
	h.clients[id] = &http2Client{
		client:    client,
		transport: transport,
		host:      host,
		port:      port,
	}
	h.mu.Unlock()

	// Simulate connection
	h.rt.EventLoop().AddPendingWork()
	go func() {
		defer h.rt.EventLoop().DonePendingWork()

		h.rt.EventLoop().EnqueueMicrotask(func() {
			h.callCallback(callback, "")
		})
	}()

	return ctx.NewNumber(float64(id))
}

func (h *HTTP2) requestFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}

	sessionID := int64(args[0].Integer())
	// streamID := int64(args[1].Integer())
	headersJSON := args[2].String()
	callback := args[3]

	h.mu.Lock()
	client, ok := h.clients[sessionID]
	h.mu.Unlock()

	if !ok {
		h.callRequestCallback(callback, "Session not found", "", "", false)
		return nil
	}

	// Parse headers
	var headers map[string]interface{}
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		h.callRequestCallback(callback, "Invalid headers", "", "", false)
		return nil
	}

	// Extract method, path, authority
	method := "GET"
	if m, ok := headers[":method"].(string); ok {
		method = m
	}
	path := "/"
	if p, ok := headers[":path"].(string); ok {
		path = p
	}
	scheme := "https"
	if s, ok := headers[":scheme"].(string); ok {
		scheme = s
	}

	url := scheme + "://" + client.host
	if client.port != 443 && client.port != 80 {
		url += ":" + string(rune('0'+client.port/10000%10)) +
			string(rune('0'+client.port/1000%10)) +
			string(rune('0'+client.port/100%10)) +
			string(rune('0'+client.port/10%10)) +
			string(rune('0'+client.port%10))
	}
	url += path

	h.rt.EventLoop().AddPendingWork()
	go func() {
		defer h.rt.EventLoop().DonePendingWork()

		req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
		if err != nil {
			h.rt.EventLoop().EnqueueMicrotask(func() {
				h.callRequestCallback(callback, err.Error(), "", "", false)
			})
			return
		}

		// Add headers
		for key, value := range headers {
			if !isPseudoHeader(key) {
				if strVal, ok := value.(string); ok {
					req.Header.Set(key, strVal)
				}
			}
		}

		resp, err := client.client.Do(req)
		if err != nil {
			h.rt.EventLoop().EnqueueMicrotask(func() {
				h.callRequestCallback(callback, err.Error(), "", "", false)
			})
			return
		}
		defer resp.Body.Close()

		// Read body
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			h.rt.EventLoop().EnqueueMicrotask(func() {
				h.callRequestCallback(callback, err.Error(), "", "", false)
			})
			return
		}

		// Build response headers
		respHeaders := map[string]interface{}{
			":status": resp.StatusCode,
		}
		for key, values := range resp.Header {
			if len(values) > 0 {
				respHeaders[key] = values[0]
			}
		}

		respHeadersJSON, _ := json.Marshal(respHeaders)

		h.rt.EventLoop().EnqueueMicrotask(func() {
			h.callRequestCallback(callback, "", string(respHeadersJSON), string(body), true)
		})
	}()

	return nil
}

func (h *HTTP2) closeStreamFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Simplified - would close HTTP/2 stream
	return nil
}

func (h *HTTP2) writeStreamFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Simplified - would write to HTTP/2 stream
	return nil
}

func (h *HTTP2) endStreamFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Simplified - would end HTTP/2 stream
	return nil
}

func (h *HTTP2) goawayFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// Simplified - would send GOAWAY frame
	return nil
}

func (h *HTTP2) destroySessionFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	sessionID := int64(args[0].Integer())

	h.mu.Lock()
	if client, ok := h.clients[sessionID]; ok {
		if client.transport != nil {
			client.transport.CloseIdleConnections()
		}
		delete(h.clients, sessionID)
	}
	h.mu.Unlock()

	return nil
}

func (h *HTTP2) createServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	ctx := info.Context()
	// HTTP/2 server creation is complex - return placeholder
	// Real implementation would create a proper HTTP/2 server
	return ctx.NewNumber(0)
}

func (h *HTTP2) closeServerFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return nil
}

func (h *HTTP2) respondFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	return nil
}

func (h *HTTP2) callCallback(callback *v8go.Value, errMsg string) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := h.rt.Context()
	var errVal *v8go.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	callback.Call(ctx.Undefined(), errVal)
}

func (h *HTTP2) callRequestCallback(callback *v8go.Value, errMsg string, headers string, data string, finished bool) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := h.rt.Context()
	var errVal *v8go.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	headersVal, _ := ctx.NewString(headers)
	dataVal, _ := ctx.NewString(data)
	finishedVal := ctx.False()
	if finished {
		finishedVal = ctx.True()
	}

	callback.Call(ctx.Undefined(), errVal, headersVal, dataVal, finishedVal)
}

func isPseudoHeader(name string) bool {
	return len(name) > 0 && name[0] == ':'
}
