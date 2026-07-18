// Package http implements the Node.js http module.
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
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
	funcs := map[string]v8.FunctionCallback{
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
			// Marks a real client-response instance. Express reassigns a server
			// request's prototype to chain through http.IncomingMessage.prototype
			// (this class), so the auto-flow below must NOT fire for server
			// requests — their body is delivered by the connection parser, and a
			// spurious _emitData() would emit a premature 'end' (with no _body),
			// truncating every body read (express.json/urlencoded/etc.).
			this._isClientResponse = true;

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

		// Node switches a readable stream into "flowing" mode as soon as a 'data'
		// listener is attached, without an explicit resume(). Real HTTP clients rely
		// on this: superagent (and therefore supertest) does res.on('data', ...) /
		// res.on('end', ...) and never calls resume(), so without auto-flow the
		// response body/'end' never arrive and the request hangs. Mirror Node by
		// scheduling the (buffered) body emit when the first 'data' listener lands,
		// unless the consumer has paused the stream. addListener is the base
		// EventEmitter primitive (on() delegates to it), so overriding it here also
		// covers res.on('data', ...) without recursion.
		addListener(event, listener) {
			super.addListener(event, listener);
			if (event === 'data' && this._isClientResponse && !this._consumed && !this._paused) {
				setImmediate(() => this._emitData());
			}
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
			this._paused = false;
			// Only a real client response auto-emits its buffered body here; a
			// server request (which borrows this prototype via express) has its
			// body driven by the connection parser instead.
			if (this._isClientResponse) {
				setImmediate(() => this._emitData());
			}
			return this;
		}

		pause() {
			this._paused = true;
			return this;
		}

		pipe(destination) {
			// Track the forwarding listeners per destination so unpipe() can
			// detach them. body-parser pipes the request into a zlib stream and,
			// on error/limit, calls req.unpipe(); without a working unpipe the
			// error path throws ("req.unpipe is not a function") and the response
			// never completes. This prototype is also what express-wrapped server
			// requests inherit, so it must support the full pipe/unpipe pair.
			if (!this._pipes) this._pipes = [];
			const onData = chunk => { if (destination.write(chunk) === false && typeof this.pause === 'function') this.pause(); };
			const onEnd = () => { try { destination.end(); } catch (e) {} };
			this._pipes.push({ destination, onData, onEnd });
			this.on('data', onData);
			this.on('end', onEnd);
			this.resume();
			return destination;
		}

		unpipe(destination) {
			if (!this._pipes) return this;
			const remaining = [];
			for (const p of this._pipes) {
				if (destination === undefined || p.destination === destination) {
					this.removeListener('data', p.onData);
					this.removeListener('end', p.onEnd);
				} else {
					remaining.push(p);
				}
			}
			this._pipes = remaining;
			return this;
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

			// Accumulate the outgoing body as a latin1 wire-string: one character
			// per wire byte. String chunks are first encoded with their declared
			// encoding (default utf8, matching Node) so multi-byte characters
			// become the correct byte sequence; Buffers pass through unchanged.
			// The native side recovers the exact bytes with latin1Bytes(). Storing
			// the raw JS string here instead would let non-ASCII characters get
			// re-encoded when crossing into Go and desync Content-Length.
			if (typeof chunk === 'string') {
				this._body.push(toLatin1(chunk, encoding));
			} else if (chunk instanceof Buffer || chunk instanceof Uint8Array) {
				this._body.push(Buffer.from(chunk).toString('latin1'));
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

			// HTTP header values are strings on the wire, but callers (e.g.
			// superagent sets Content-Length as a number) may store numbers or
			// arrays. The native client unmarshals headers into a Go
			// map[string]string, so coerce every value to a string here (joining
			// multi-value arrays like Node does) to avoid a JSON unmarshal error.
			const outHeaders = {};
			for (const k in this._headers) {
				const v = this._headers[k];
				if (v === undefined || v === null) continue;
				outHeaders[k] = Array.isArray(v) ? v.map(String).join(', ') : String(v);
			}

			const reqOptions = {
				method: this.method,
				url: url,
				headers: outHeaders,
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

	// --- Real HTTP/1.1 server, built on the net module's TCP server ---
	//
	// Data on the wire is handled as latin1 strings so raw bytes survive
	// round-trips (a latin1 string maps 1:1 to bytes; Buffer.from(s,'latin1')
	// reconstructs the exact bytes, which then decode correctly as utf8, etc.).

	const STATUS_CODES = http.STATUS_CODES || {};

	function toLatin1(chunk, encoding) {
		if (Buffer.isBuffer(chunk)) return chunk.toString('latin1');
		if (typeof chunk === 'string') return Buffer.from(chunk, encoding || 'utf8').toString('latin1');
		return Buffer.from(chunk).toString('latin1');
	}

	// Server-side request: a readable stream of the request body.
	class ServerIncomingMessage extends EventEmitter {
		constructor(socket) {
			super();
			this.socket = socket;
			this.connection = socket;
			this.method = 'GET';
			this.url = '/';
			this.headers = {};
			this.rawHeaders = [];
			this.httpVersion = '1.1';
			this.httpVersionMajor = 1;
			this.httpVersionMinor = 1;
			this.complete = false;
			this.aborted = false;
		}
		setEncoding(enc) { this._encoding = enc; return this; }
		pause() { this._paused = true; return this; }
		resume() { this._paused = false; return this; }
		destroy() { if (this.socket) this.socket.destroy(); return this; }
		// Pipe the request body to a writable/transform. body-parser pipes the
		// request through zlib.createGunzip()/createInflate() for compressed
		// bodies, so the request must be a real readable source. Body 'data'
		// events are emitted by the connection parser; forward them to dest.
		pipe(dest, options) {
			options = options || {};
			const src = this;
			const ondata = (chunk) => {
				if (dest && typeof dest.write === 'function') {
					if (dest.write(chunk) === false && typeof src.pause === 'function') src.pause();
				}
			};
			const onend = () => { if (options.end !== false && dest && typeof dest.end === 'function') dest.end(); };
			const onerror = (err) => { if (dest && typeof dest.emit === 'function') dest.emit('error', err); };
			src.on('data', ondata);
			src.on('end', onend);
			src.on('error', onerror);
			if (dest && typeof dest.emit === 'function') dest.emit('pipe', src);
			this._pipes = this._pipes || [];
			this._pipes.push({ dest, ondata, onend, onerror });
			return dest;
		}
		unpipe(dest) {
			if (!this._pipes) return this;
			this._pipes = this._pipes.filter((p) => {
				if (dest && p.dest !== dest) return true;
				this.removeListener('data', p.ondata);
				this.removeListener('end', p.onend);
				this.removeListener('error', p.onerror);
				return false;
			});
			return this;
		}
	}

	// Server-side response writer. Body is buffered and flushed on end() so a
	// correct Content-Length can be computed for finite responses.
	class ServerResponse extends EventEmitter {
		constructor(socket, req) {
			super();
			this.socket = socket;
			this.connection = socket;
			this._req = req;
			this.statusCode = 200;
			this.statusMessage = undefined;
			this.headersSent = false;
			this.finished = false;
			this.writableEnded = false;
			this.sendDate = true;
			this._headers = {}; // lowercased -> { name, value }
			this._bodyChunks = [];
		}
		setHeader(name, value) {
			this._headers[String(name).toLowerCase()] = { name: name, value: value };
			return this;
		}
		getHeader(name) {
			const h = this._headers[String(name).toLowerCase()];
			return h ? h.value : undefined;
		}
		removeHeader(name) { delete this._headers[String(name).toLowerCase()]; }
		hasHeader(name) {
			return Object.prototype.hasOwnProperty.call(this._headers, String(name).toLowerCase());
		}
		getHeaderNames() { return Object.keys(this._headers).map((k) => this._headers[k].name); }
		writeHead(statusCode, statusMessage, headers) {
			this.statusCode = statusCode;
			if (typeof statusMessage === 'string') {
				this.statusMessage = statusMessage;
			} else if (statusMessage && typeof statusMessage === 'object') {
				headers = statusMessage;
			}
			if (headers) {
				for (const k of Object.keys(headers)) this.setHeader(k, headers[k]);
			}
			return this;
		}
		write(chunk, encoding, callback) {
			if (typeof encoding === 'function') { callback = encoding; encoding = undefined; }
			if (chunk != null) this._bodyChunks.push(toLatin1(chunk, encoding));
			if (callback) callback();
			return true;
		}
		end(chunk, encoding, callback) {
			if (typeof chunk === 'function') { callback = chunk; chunk = undefined; }
			else if (typeof encoding === 'function') { callback = encoding; encoding = undefined; }
			if (this.finished) { if (callback) callback(); return this; }
			if (chunk != null) this._bodyChunks.push(toLatin1(chunk, encoding));
			if (callback) this.once('finish', callback);
			this._flush();
			this.finished = true;
			this.writableEnded = true;
			this.emit('finish');
			this.emit('close');
			return this;
		}
		_flush() {
			const body = this._bodyChunks.join('');
			const statusMessage = this.statusMessage || STATUS_CODES[this.statusCode] || 'OK';
			const reqHeaders = (this._req && this._req.headers) || {};
			const connHeader = (reqHeaders.connection || '').toLowerCase();
			const keepAlive = connHeader === 'keep-alive' ||
				(connHeader !== 'close' && this._req && this._req.httpVersionMajor === 1 && this._req.httpVersionMinor === 1);

			// 1xx, 204 and 304 responses carry no message body, so Node never emits
			// a Content-Length for them (and strips one if set). Skipping the
			// auto-computed header keeps res.send()'s 204/304 handling correct.
			const bodyAllowed = !(this.statusCode === 204 || this.statusCode === 304 ||
				(this.statusCode >= 100 && this.statusCode < 200));
			if (bodyAllowed && !this.hasHeader('content-length') && !this.hasHeader('transfer-encoding')) {
				this.setHeader('Content-Length', Buffer.byteLength(body, 'latin1'));
			}
			if (!this.hasHeader('connection')) {
				this.setHeader('Connection', keepAlive ? 'keep-alive' : 'close');
			}
			if (this.sendDate && !this.hasHeader('date')) {
				this.setHeader('Date', new Date().toUTCString());
			}

			let head = 'HTTP/1.1 ' + this.statusCode + ' ' + statusMessage + '\r\n';
			for (const k of Object.keys(this._headers)) {
				const h = this._headers[k];
				const v = h.value;
				if (Array.isArray(v)) {
					for (const item of v) head += h.name + ': ' + item + '\r\n';
				} else {
					head += h.name + ': ' + v + '\r\n';
				}
			}
			head += '\r\n';
			this.headersSent = true;
			this.socket.write(Buffer.from(head + body, 'latin1'));
			if (!keepAlive) this.socket.end();
		}
	}

	function handleHttpConnection(server, socket) {
		let buffer = '';
		let state = 'head';
		let req = null;
		let res = null;
		let expectedBody = 0;
		let bodyReceived = 0;
		let chunked = false;

		function reset() {
			state = 'head';
			req = null; res = null;
			expectedBody = 0; bodyReceived = 0; chunked = false;
		}

		const onServerClose = () => { try { socket.destroy(); } catch (e) {} };
		server.once('close', onServerClose);
		socket.on('close', () => { server.removeListener('close', onServerClose); });
		socket.on('error', () => {});
		socket.on('data', (chunk) => {
			buffer += chunk.toString('latin1');
			try { parse(); } catch (e) { server.emit('clientError', e, socket); socket.destroy(); }
		});

		function decodeChunkedBody() {
			while (true) {
				const nl = buffer.indexOf('\r\n');
				if (nl === -1) return false;
				const size = parseInt(buffer.slice(0, nl).trim(), 16);
				if (isNaN(size)) return true; // malformed: stop
				if (size === 0) {
					if (buffer.length < nl + 2 + 2) return false;
					buffer = buffer.slice(nl + 2 + 2);
					return true;
				}
				if (buffer.length < nl + 2 + size + 2) return false;
				const piece = buffer.slice(nl + 2, nl + 2 + size);
				buffer = buffer.slice(nl + 2 + size + 2);
				bodyReceived += size;
				req.emit('data', Buffer.from(piece, 'latin1'));
			}
		}

		function parse() {
			while (true) {
				if (state === 'head') {
					const idx = buffer.indexOf('\r\n\r\n');
					if (idx === -1) return;
					const headText = buffer.slice(0, idx);
					buffer = buffer.slice(idx + 4);
					const lines = headText.split('\r\n');
					const requestLine = lines.shift() || '';
					const parts = requestLine.split(' ');
					req = new ServerIncomingMessage(socket);
					req.method = (parts[0] || 'GET').toUpperCase();
					req.url = parts[1] || '/';
					const vm = /HTTP\/(\d)\.(\d)/.exec(parts[2] || 'HTTP/1.1');
					if (vm) {
						req.httpVersionMajor = +vm[1];
						req.httpVersionMinor = +vm[2];
						req.httpVersion = vm[1] + '.' + vm[2];
					}
					for (const line of lines) {
						const ci = line.indexOf(':');
						if (ci === -1) continue;
						const name = line.slice(0, ci).trim();
						const value = line.slice(ci + 1).trim();
						req.rawHeaders.push(name, value);
						const lower = name.toLowerCase();
						if (req.headers[lower] !== undefined) {
							req.headers[lower] += ', ' + value;
						} else {
							req.headers[lower] = value;
						}
					}
					const te = (req.headers['transfer-encoding'] || '').toLowerCase();
					if (te.indexOf('chunked') !== -1) {
						chunked = true;
						expectedBody = 0;
					} else {
						expectedBody = parseInt(req.headers['content-length'] || '0', 10) || 0;
					}
					bodyReceived = 0;
					res = new ServerResponse(socket, req);
					state = 'body';
					server.emit('request', req, res);
				}

				if (state === 'body') {
					if (chunked) {
						if (!decodeChunkedBody()) return;
					} else if (expectedBody > bodyReceived) {
						const take = Math.min(expectedBody - bodyReceived, buffer.length);
						if (take > 0) {
							const piece = buffer.slice(0, take);
							buffer = buffer.slice(take);
							bodyReceived += take;
							req.emit('data', Buffer.from(piece, 'latin1'));
						}
						if (bodyReceived < expectedBody) return;
					}
					req.complete = true;
					req.emit('end');
					reset();
					if (buffer.length === 0) return;
					continue;
				}
				return;
			}
		}
	}

	http.createServer = function(options, requestListener) {
		const net = globalThis.__net_module;
		if (!net || typeof net.createServer !== 'function') {
			throw new Error('http.createServer requires the net module');
		}
		if (typeof options === 'function') {
			requestListener = options;
			options = {};
		}
		options = options || {};

		const server = net.createServer();
		if (requestListener) server.on('request', requestListener);
		server.on('connection', (socket) => handleHttpConnection(server, socket));
		return server;
	};

	http.Server = function Server(options, requestListener) {
		return http.createServer(options, requestListener);
	};
	http.ServerResponse = ServerResponse;

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

func (h *HTTP) requestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is overridden by JS wrapper
	return nil
}

func (h *HTTP) getFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is overridden by JS wrapper
	return nil
}

// doRequestFunc performs the actual HTTP request using the runtime's HTTPClient.
func (h *HTTP) doRequestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
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
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
		Timeout int               `json:"timeout"`
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

	req := &runtime.Request{
		Method:  opts.Method,
		URL:     opts.URL,
		Headers: headers,
		// opts.Body is a latin1 wire-string built by ClientRequest.write (one
		// character per wire byte). Decode it back to raw bytes with latin1Bytes;
		// using []byte here would re-UTF-8-encode any non-ASCII byte and desync
		// the request body from its Content-Length.
		Body: latin1Bytes(opts.Body),
		// Node's http.ClientRequest never auto-follows redirects; it hands the
		// 3xx response back to the caller (superagent/axios/etc. decide whether
		// to follow). Go's http.Client would otherwise follow transparently.
		NoFollowRedirects: true,
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

			// Build response object. Node's http client collapses duplicate
			// header lines the same way for message.headers: set-cookie stays an
			// array, a fixed set of single-value headers keep only the first
			// occurrence, cookie joins with "; ", and everything else joins with
			// ", " (so res.set('X-Numbers', [1,2]) reads back as "1, 2").
			respHeaders := make(map[string]interface{})
			for k, v := range resp.Headers {
				respHeaders[k] = combineHeaderValues(k, v)
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

// singleValueHeaders are headers for which Node's http parser keeps only the
// first occurrence when populating message.headers (see _http_incoming.js).
var singleValueHeaders = map[string]bool{
	"age": true, "authorization": true, "content-length": true,
	"content-type": true, "etag": true, "expires": true, "from": true,
	"host": true, "if-modified-since": true, "if-none-match": true,
	"last-modified": true, "location": true, "max-forwards": true,
	"proxy-authorization": true, "referer": true, "retry-after": true,
	"server": true, "user-agent": true,
}

// combineHeaderValues collapses multiple header lines for the same field into
// the single JS value that Node exposes on message.headers. key must already be
// lowercased.
func combineHeaderValues(key string, values []string) interface{} {
	if len(values) == 0 {
		return ""
	}
	switch {
	case key == "set-cookie":
		return values
	case key == "cookie":
		return strings.Join(values, "; ")
	case singleValueHeaders[key]:
		return values[0]
	default:
		return strings.Join(values, ", ")
	}
}

// latin1Bytes recovers raw bytes from a latin1 wire-string that arrived from V8
// (decoded as UTF-8 runes) by taking the low byte of each code point. It is the
// inverse of the JS-side toLatin1()/Buffer.toString('latin1') convention used to
// carry arbitrary bytes across the Go/JS boundary without UTF-8 mangling.
func latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
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
