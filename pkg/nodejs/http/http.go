// Package http implements the Node.js http module over node:net sockets.
package http

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed http.js
var httpJS string

// HTTP provides HTTP functionality.
type HTTP struct {
	rt *runtime.Runtime
}

// New creates a new HTTP module.
func New() *HTTP {
	return &HTTP{}
}

// Name returns the module name.
func (h *HTTP) Name() string {
	return "http"
}

// Register sets up the http module.
func (h *HTTP) Register(rt *runtime.Runtime) error {
	h.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	httpObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	httpObj.Set("METHODS", h.createMethodsArray(ctx))
	httpObj.Set("STATUS_CODES", h.createStatusCodesObject(ctx))
	httpObj.Set("_useSockets", ctx.NewBoolean(rt.HTTPOverSockets()))

	doRequestFn, err := iso.NewFunctionTemplate(h.doRequestFunc)
	if err != nil {
		return err
	}
	doRequestVal, err := doRequestFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	httpObj.Set("_doRequest", doRequestVal)

	if err := rt.SetGlobal("__http_module", httpObj); err != nil {
		return err
	}

	_, err = rt.RunScript(httpJS, "http.js")
	return err
}

// doRequestFunc sends one whole request through the runtime's HTTPClient. http.js uses it
// when an embedder has injected a client, which then decides what the request may do.
func (h *HTTP) doRequestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 || !args[1].IsFunction() {
		return nil
	}
	callback := args[1]

	var opts struct {
		Method  string      `json:"method"`
		URL     string      `json:"url"`
		Headers [][2]string `json:"headers"`
		Body    string      `json:"body"`
		Timeout float64     `json:"timeout"`
	}
	fail := func(message string) {
		h.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := h.rt.Context()
			errStr, _ := ctx.NewString(message)
			callback.Call(ctx.Undefined(), errStr, ctx.Null())
			ctx.PerformMicrotaskCheckpoint()
		})
	}
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		fail(err.Error())
		return nil
	}
	body, err := base64.StdEncoding.DecodeString(opts.Body)
	if err != nil {
		fail(err.Error())
		return nil
	}

	headers := make(map[string][]string, len(opts.Headers))
	for _, header := range opts.Headers {
		headers[header[0]] = append(headers[header[0]], header[1])
	}

	req := &runtime.Request{
		Method:  opts.Method,
		URL:     opts.URL,
		Headers: headers,
		Body:    body,
		// Node's http.ClientRequest never auto-follows redirects; it hands the
		// 3xx response back to the caller (superagent/axios/etc. decide whether
		// to follow). Go's http.Client would otherwise follow transparently.
		NoFollowRedirects: true,
	}
	if opts.Timeout > 0 {
		req.Timeout = time.Duration(opts.Timeout) * time.Millisecond
	}

	client := h.rt.HTTPClient()
	reqCtx := h.rt.ExecutionContext()
	if reqCtx == nil {
		reqCtx = context.Background()
	}

	h.rt.EventLoop().AddPendingWork()
	go func() {
		defer h.rt.EventLoop().DonePendingWork()

		resp, err := client.Do(reqCtx, req)
		var responseBody []byte
		if err == nil {
			responseBody, err = resp.Bytes()
		}
		if err != nil {
			fail(err.Error())
			return
		}

		responseHeaders := make([][2]string, 0, len(resp.Headers))
		for name, values := range resp.Headers {
			for _, value := range values {
				responseHeaders = append(responseHeaders, [2]string{name, value})
			}
		}
		encoded, err := json.Marshal(map[string]any{
			"statusCode":    resp.StatusCode,
			"statusMessage": strings.TrimPrefix(resp.Status, fmt.Sprintf("%d ", resp.StatusCode)),
			"headers":       responseHeaders,
			"body":          base64.StdEncoding.EncodeToString(responseBody),
		})
		if err != nil {
			fail(err.Error())
			return
		}
		h.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := h.rt.Context()
			respStr, _ := ctx.NewString(string(encoded))
			callback.Call(ctx.Undefined(), ctx.Null(), respStr)
			ctx.PerformMicrotaskCheckpoint()
		})
	}()

	return nil
}

func (h *HTTP) createMethodsArray(ctx *v8.Context) *v8.Value {
	methods := []string{
		"ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD",
		"LINK", "LOCK", "M-SEARCH", "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL",
		"MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST", "PROPFIND", "PROPPATCH",
		"PURGE", "PUT", "REBIND", "REPORT", "SEARCH", "SOURCE", "SUBSCRIBE",
		"TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE",
	}

	arr, _ := ctx.NewArray(len(methods))
	for i, m := range methods {
		val, _ := ctx.NewString(m)
		arr.SetIndex(i, val)
	}
	return arr
}

func (h *HTTP) createStatusCodesObject(ctx *v8.Context) *v8.Value {
	codes := map[int]string{
		100: "Continue",
		101: "Switching Protocols",
		102: "Processing",
		103: "Early Hints",
		200: "OK",
		201: "Created",
		202: "Accepted",
		203: "Non-Authoritative Information",
		204: "No Content",
		205: "Reset Content",
		206: "Partial Content",
		207: "Multi-Status",
		300: "Multiple Choices",
		301: "Moved Permanently",
		302: "Found",
		303: "See Other",
		304: "Not Modified",
		305: "Use Proxy",
		307: "Temporary Redirect",
		308: "Permanent Redirect",
		400: "Bad Request",
		401: "Unauthorized",
		402: "Payment Required",
		403: "Forbidden",
		404: "Not Found",
		405: "Method Not Allowed",
		406: "Not Acceptable",
		407: "Proxy Authentication Required",
		408: "Request Timeout",
		409: "Conflict",
		410: "Gone",
		411: "Length Required",
		412: "Precondition Failed",
		413: "Payload Too Large",
		414: "URI Too Long",
		415: "Unsupported Media Type",
		416: "Range Not Satisfiable",
		417: "Expectation Failed",
		418: "I'm a Teapot",
		421: "Misdirected Request",
		422: "Unprocessable Entity",
		423: "Locked",
		424: "Failed Dependency",
		425: "Too Early",
		426: "Upgrade Required",
		428: "Precondition Required",
		429: "Too Many Requests",
		431: "Request Header Fields Too Large",
		451: "Unavailable For Legal Reasons",
		500: "Internal Server Error",
		501: "Not Implemented",
		502: "Bad Gateway",
		503: "Service Unavailable",
		504: "Gateway Timeout",
		505: "HTTP Version Not Supported",
		506: "Variant Also Negotiates",
		507: "Insufficient Storage",
		508: "Loop Detected",
		509: "Bandwidth Limit Exceeded",
		510: "Not Extended",
		511: "Network Authentication Required",
	}

	obj, _ := ctx.NewObject()
	for code, message := range codes {
		msg, _ := ctx.NewString(message)
		obj.Set(fmt.Sprintf("%d", code), msg)
	}
	return obj
}
