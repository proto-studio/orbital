// Package fetch implements the Web Fetch API.
package fetch

import (
	"context"
	_ "embed"
	"encoding/json"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed fetch.js
var fetchJS string

// Fetch provides the fetch API.
type Fetch struct {
	rt *runtime.Runtime
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
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create __http_fetch function
	fetchFn, err := iso.NewFunctionTemplate(f.fetchFunc)
	if err != nil {
		return err
	}
	fetchVal, err := fetchFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := rt.SetGlobal("__http_fetch", fetchVal); err != nil {
		return err
	}

	// Initialize fetch JS
	if _, err := rt.RunScript(fetchJS, "fetch.js"); err != nil {
		return err
	}

	return nil
}

// fetchFunc implements the internal __http_fetch function
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

	jsCtx := info.Context()
	client := f.rt.HTTPClient()

	// Parse headers
	var headers map[string]string
	if headersJSON != "" {
		json.Unmarshal([]byte(headersJSON), &headers)
	}

	// Convert headers to map[string][]string
	reqHeaders := make(map[string][]string)
	for k, v := range headers {
		reqHeaders[k] = []string{v}
	}

	// Create request
	req := &runtime.Request{
		Method:  method,
		URL:     url,
		Headers: reqHeaders,
		Body:    []byte(body),
	}

	// Execute async
	f.rt.EventLoop().AddPendingWork()
	go func() {
		defer f.rt.EventLoop().DonePendingWork()

		// Make request using HTTP client interface
		resp, err := client.Do(context.Background(), req)

		f.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				errVal, _ := jsCtx.NewString(err.Error())
				callback.Call(nil, errVal, jsCtx.NewNumber(0), jsCtx.Null(), jsCtx.Null(), jsCtx.Null())
				return
			}

			// Convert headers to flat map for JSON
			respHeaders := make(map[string]string)
			for k, v := range resp.Headers {
				if len(v) > 0 {
					respHeaders[k] = v[0]
				}
			}

			// Convert headers to JSON
			respHeadersJSON, _ := json.Marshal(respHeaders)
			respHeadersVal, _ := jsCtx.NewString(string(respHeadersJSON))

			// Get status text
			statusText := getStatusText(resp.StatusCode)
			statusTextVal, _ := jsCtx.NewString(statusText)

			// Convert body
			respBodyVal, _ := jsCtx.NewString(string(resp.Body))

			callback.Call(nil, jsCtx.Null(), jsCtx.NewNumber(float64(resp.StatusCode)), statusTextVal, respHeadersVal, respBodyVal)
		})
	}()

	return nil
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
