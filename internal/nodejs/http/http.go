// Package http implements the Node.js http module.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/andrewcurioso/gnode/pkg/network"
	"github.com/andrewcurioso/gnode/pkg/runtime"
	"github.com/andrewcurioso/gnode/pkg/v8go"
)

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

	// Create http object
	httpObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8go.FunctionCallback{
		"request": h.requestFunc,
		"get":     h.getFunc,
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
		if err := httpObj.Set(name, val); err != nil {
			return err
		}
	}

	// Add constants
	httpObj.Set("METHODS", h.createMethodsArray(ctx))
	httpObj.Set("STATUS_CODES", h.createStatusCodesObject(ctx))

	// Set as global
	if err := rt.SetGlobal("__http_module", httpObj); err != nil {
		return err
	}

	// Set up IncomingMessage and ServerResponse classes via JS
	jsSetup := `
(function() {
	const http = __http_module;
	// EventEmitter is set as a global by the events module

	// IncomingMessage class (for responses)
	class IncomingMessage extends EventEmitter {
		constructor(response) {
			super();
			this.statusCode = response.statusCode;
			this.statusMessage = response.statusMessage || '';
			this.headers = response.headers || {};
			this.rawHeaders = [];
			this.httpVersion = '1.1';
			this.httpVersionMajor = 1;
			this.httpVersionMinor = 1;
			this.complete = false;
			this._body = response.body || '';
			this._bodyBuffer = null;
			this._consumed = false;

			// Build rawHeaders
			for (const [key, value] of Object.entries(this.headers)) {
				if (Array.isArray(value)) {
					value.forEach(v => {
						this.rawHeaders.push(key, v);
					});
				} else {
					this.rawHeaders.push(key, value);
				}
			}
		}

		setEncoding(encoding) {
			this._encoding = encoding;
			return this;
		}

		_emitData() {
			if (this._consumed) return;
			this._consumed = true;

			// Emit data event
			if (this._body) {
				if (typeof Buffer !== 'undefined' && !(this._body instanceof Buffer)) {
					this.emit('data', Buffer.from(this._body));
				} else {
					this.emit('data', this._body);
				}
			}
			this.complete = true;
			this.emit('end');
		}

		read() {
			if (this._consumed) return null;
			this._consumed = true;
			return this._body;
		}

		resume() {
			// Use setImmediate to emit data asynchronously
			setImmediate(() => this._emitData());
			return this;
		}

		pause() {
			return this;
		}

		pipe(destination) {
			this.on('data', chunk => destination.write(chunk));
			this.on('end', () => destination.end());
			this.resume();
			return destination;
		}

		destroy() {
			this.emit('close');
		}
	}

	// ClientRequest class
	class ClientRequest extends EventEmitter {
		constructor(options, callback) {
			super();
			this._options = options;
			this._callback = callback;
			this._headers = {};
			this._body = [];
			this._ended = false;
			this._aborted = false;
			this.socket = null;
			this.path = options.path || '/';
			this.method = options.method || 'GET';
			this.host = options.host || options.hostname || 'localhost';

			// Set default headers
			if (options.headers) {
				for (const [key, value] of Object.entries(options.headers)) {
					this._headers[key.toLowerCase()] = value;
				}
			}

			if (callback) {
				this.once('response', callback);
			}
		}

		setHeader(name, value) {
			this._headers[name.toLowerCase()] = value;
			return this;
		}

		getHeader(name) {
			return this._headers[name.toLowerCase()];
		}

		removeHeader(name) {
			delete this._headers[name.toLowerCase()];
			return this;
		}

		write(chunk, encoding, callback) {
			if (this._ended || this._aborted) return false;
			
			if (typeof chunk === 'string') {
				this._body.push(chunk);
			} else if (chunk instanceof Buffer || chunk instanceof Uint8Array) {
				this._body.push(String.fromCharCode.apply(null, chunk));
			}

			if (callback) callback();
			return true;
		}

		end(data, encoding, callback) {
			if (this._ended) return this;
			
			if (typeof data === 'function') {
				callback = data;
				data = null;
			} else if (typeof encoding === 'function') {
				callback = encoding;
				encoding = null;
			}

			if (data) {
				this.write(data, encoding);
			}

			this._ended = true;

			// Make the actual request
			this._sendRequest(callback);

			return this;
		}

		_sendRequest(callback) {
			const options = this._options;
			const protocol = options.protocol || 'http:';
			const host = options.host || options.hostname || 'localhost';
			const port = options.port || (protocol === 'https:' ? 443 : 80);
			const path = options.path || '/';

			let url = protocol + '//' + host;
			if ((protocol === 'http:' && port !== 80) || (protocol === 'https:' && port !== 443)) {
				url += ':' + port;
			}
			url += path;

			const reqOptions = {
				method: this.method,
				url: url,
				headers: this._headers,
				body: this._body.join(''),
				timeout: options.timeout || 0
			};

			// Use the internal _doRequest function
			http._doRequest(JSON.stringify(reqOptions), (err, responseJson) => {
				if (this._aborted) return;

				if (err) {
					this.emit('error', new Error(err));
					return;
				}

				try {
					const response = JSON.parse(responseJson);
					const incomingMessage = new IncomingMessage(response);
					this.emit('response', incomingMessage);
					
					// Auto-resume for callback style
					if (this._callback) {
						setImmediate(() => incomingMessage.resume());
					}
				} catch (e) {
					this.emit('error', e);
				}

				if (callback) callback();
			});
		}

		abort() {
			this._aborted = true;
			this.emit('abort');
			this.emit('close');
		}

		destroy(error) {
			this._aborted = true;
			if (error) {
				this.emit('error', error);
			}
			this.emit('close');
		}

		setTimeout(msecs, callback) {
			if (callback) {
				this.once('timeout', callback);
			}
			this._options.timeout = msecs;
			return this;
		}

		setNoDelay(noDelay) {
			return this;
		}

		setSocketKeepAlive(enable, initialDelay) {
			return this;
		}
	}

	// http.request function wrapper
	const originalRequest = http.request;
	http.request = function(urlOrOptions, optionsOrCallback, callback) {
		let options = {};
		
		if (typeof urlOrOptions === 'string') {
			// Parse URL string
			const parsed = new URL(urlOrOptions);
			options = {
				protocol: parsed.protocol,
				hostname: parsed.hostname,
				port: parsed.port || (parsed.protocol === 'https:' ? 443 : 80),
				path: parsed.pathname + parsed.search,
				method: 'GET'
			};
			
			if (typeof optionsOrCallback === 'object') {
				Object.assign(options, optionsOrCallback);
				callback = callback;
			} else if (typeof optionsOrCallback === 'function') {
				callback = optionsOrCallback;
			}
		} else if (typeof urlOrOptions === 'object') {
			if (urlOrOptions instanceof URL) {
				options = {
					protocol: urlOrOptions.protocol,
					hostname: urlOrOptions.hostname,
					port: urlOrOptions.port || (urlOrOptions.protocol === 'https:' ? 443 : 80),
					path: urlOrOptions.pathname + urlOrOptions.search,
					method: 'GET'
				};
			} else {
				options = urlOrOptions;
			}
			callback = optionsOrCallback;
		}

		return new ClientRequest(options, callback);
	};

	// http.get function wrapper
	const originalGet = http.get;
	http.get = function(urlOrOptions, optionsOrCallback, callback) {
		let options = {};
		
		if (typeof urlOrOptions === 'string') {
			const parsed = new URL(urlOrOptions);
			options = {
				protocol: parsed.protocol,
				hostname: parsed.hostname,
				port: parsed.port || (parsed.protocol === 'https:' ? 443 : 80),
				path: parsed.pathname + parsed.search,
				method: 'GET'
			};
			
			if (typeof optionsOrCallback === 'object') {
				Object.assign(options, optionsOrCallback);
			} else if (typeof optionsOrCallback === 'function') {
				callback = optionsOrCallback;
			}
		} else {
			options = urlOrOptions;
			callback = optionsOrCallback;
		}

		options.method = 'GET';
		const req = http.request(options, callback);
		req.end();
		return req;
	};

	// Agent class (simplified)
	class Agent {
		constructor(options) {
			this.options = options || {};
			this.maxSockets = this.options.maxSockets || Infinity;
			this.maxFreeSockets = this.options.maxFreeSockets || 256;
			this.keepAlive = this.options.keepAlive || false;
			this.keepAliveMsecs = this.options.keepAliveMsecs || 1000;
			this.sockets = {};
			this.freeSockets = {};
			this.requests = {};
		}

		createConnection(options, callback) {
			// Simplified - just return null
			return null;
		}

		destroy() {
			this.sockets = {};
			this.freeSockets = {};
		}
	}

	http.Agent = Agent;
	http.globalAgent = new Agent({ keepAlive: true });
	http.IncomingMessage = IncomingMessage;
	http.ClientRequest = ClientRequest;

	// createServer placeholder (would need actual server implementation)
	http.createServer = function(options, requestListener) {
		if (typeof options === 'function') {
			requestListener = options;
			options = {};
		}
		console.warn('http.createServer is not fully implemented in sandbox mode');
		return {
			listen: function(port, host, callback) {
				if (typeof host === 'function') {
					callback = host;
					host = 'localhost';
				}
				console.warn('Server.listen called but servers are not supported');
				if (callback) setImmediate(callback);
				return this;
			},
			close: function(callback) {
				if (callback) setImmediate(callback);
			},
			address: function() {
				return { port: 0, family: 'IPv4', address: '0.0.0.0' };
			},
			on: function() { return this; },
			once: function() { return this; }
		};
	};

	globalThis.__http_module = http;
})();
`
	if _, err := rt.RunScript(jsSetup, "http_setup.js"); err != nil {
		return err
	}

	// Add the internal _doRequest function
	doRequestFn, err := iso.NewFunctionTemplate(h.doRequestFunc)
	if err != nil {
		return err
	}
	doRequestVal, err := doRequestFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	httpObj.Set("_doRequest", doRequestVal)

	return nil
}

func (h *HTTP) requestFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// This is overridden by JS wrapper
	return nil
}

func (h *HTTP) getFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	// This is overridden by JS wrapper
	return nil
}

// doRequestFunc performs the actual HTTP request using the runtime's HTTPClient.
func (h *HTTP) doRequestFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	optionsJSON := args[0].String()
	callback := args[1]

	if !callback.IsFunction() {
		return nil
	}

	// Parse options
	var opts struct {
		Method  string              `json:"method"`
		URL     string              `json:"url"`
		Headers map[string]string   `json:"headers"`
		Body    string              `json:"body"`
		Timeout int                 `json:"timeout"`
	}

	if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
		h.rt.EventLoop().EnqueueMicrotask(func() {
			errStr, _ := info.Context().NewString(err.Error())
			callback.Call(nil, errStr, info.Context().Null())
		})
		return nil
	}

	// Build request
	headers := make(map[string][]string)
	for k, v := range opts.Headers {
		headers[k] = []string{v}
	}

	req := &network.Request{
		Method:  opts.Method,
		URL:     opts.URL,
		Headers: headers,
		Body:    []byte(opts.Body),
	}

	if opts.Timeout > 0 {
		req.Timeout = time.Duration(opts.Timeout) * time.Millisecond
	}

	client := h.rt.HTTPClient()
	ctx := info.Context()

	// Execute async
	h.rt.EventLoop().AddPendingWork()
	go func() {
		defer h.rt.EventLoop().DonePendingWork()

		resp, err := client.Do(context.Background(), req)

		h.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				errStr, _ := ctx.NewString(err.Error())
				callback.Call(nil, errStr, ctx.Null())
				return
			}

			// Build response object
			respHeaders := make(map[string]interface{})
			for k, v := range resp.Headers {
				if len(v) == 1 {
					respHeaders[k] = v[0]
				} else {
					respHeaders[k] = v
				}
			}

			respObj := map[string]interface{}{
				"statusCode":    resp.StatusCode,
				"statusMessage": resp.Status,
				"headers":       respHeaders,
				"body":          string(resp.Body),
			}

			respJSON, _ := json.Marshal(respObj)
			respStr, _ := ctx.NewString(string(respJSON))
			callback.Call(nil, ctx.Null(), respStr)
		})
	}()

	return nil
}

func (h *HTTP) createMethodsArray(ctx *v8go.Context) *v8go.Value {
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

func (h *HTTP) createStatusCodesObject(ctx *v8go.Context) *v8go.Value {
	codes := map[int]string{
		100: "Continue",
		101: "Switching Protocols",
		102: "Processing",
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

// Helper to parse URL for request options
func parseURL(urlStr string) (protocol, hostname string, port int, path string) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "http:", "localhost", 80, "/"
	}

	protocol = parsed.Scheme + ":"
	hostname = parsed.Hostname()
	path = parsed.RequestURI()

	if parsed.Port() != "" {
		fmt.Sscanf(parsed.Port(), "%d", &port)
	} else if parsed.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}

	return
}

// buildURL constructs a URL from options.
func buildURL(protocol, hostname string, port int, path string) string {
	var sb strings.Builder
	sb.WriteString(protocol)
	sb.WriteString("//")
	sb.WriteString(hostname)

	defaultPort := 80
	if strings.HasPrefix(protocol, "https") {
		defaultPort = 443
	}

	if port != defaultPort {
		sb.WriteString(fmt.Sprintf(":%d", port))
	}

	sb.WriteString(path)
	return sb.String()
}
