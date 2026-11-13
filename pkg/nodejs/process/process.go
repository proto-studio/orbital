// Package process implements the Node.js process module.
package process

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"proto.zip/studio/orbital/internal/v8dist"
	goruntime "proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// Process provides the process global object.
type Process struct {
	rt        *goruntime.Runtime
	startTime time.Time
	exitCode  int
}

// New creates a new Process module.
func New() *Process {
	return &Process{
		startTime: time.Now(),
	}
}

// Name returns the module name.
func (p *Process) Name() string {
	return "process"
}

// Register sets up the process global object.
func (p *Process) Register(rt *goruntime.Runtime) error {
	p.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create process object
	processObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// runtime.version
	version, _ := ctx.NewString("v20.0.0") // Emulated Node version
	if err := processObj.Set("version", version); err != nil {
		return err
	}

	// runtime.versions
	versions, _ := ctx.NewObject()
	nodeVer, _ := ctx.NewString("20.0.0")
	v8Version := "unknown"
	if manifest, err := v8dist.LoadManifest(); err == nil {
		v8Version = manifest.V8Version
	}
	v8Ver, _ := ctx.NewString(v8Version)
	goVer, _ := ctx.NewString(runtime.Version())
	versions.Set("node", nodeVer)
	versions.Set("v8", v8Ver)
	versions.Set("gnode", goVer)
	if err := processObj.Set("versions", versions); err != nil {
		return err
	}

	// runtime.platform
	platform, _ := ctx.NewString(runtime.GOOS)
	if err := processObj.Set("platform", platform); err != nil {
		return err
	}

	// runtime.arch
	arch, _ := ctx.NewString(goArchToNode(runtime.GOARCH))
	if err := processObj.Set("arch", arch); err != nil {
		return err
	}

	// runtime.pid
	pid := ctx.NewInteger(int64(os.Getpid()))
	if err := processObj.Set("pid", pid); err != nil {
		return err
	}

	// runtime.ppid
	ppid := ctx.NewInteger(int64(os.Getppid()))
	if err := processObj.Set("ppid", ppid); err != nil {
		return err
	}

	// runtime.argv
	argv, err := p.createArgv(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("argv", argv); err != nil {
		return err
	}

	// runtime.env
	env, err := p.createEnv(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("env", env); err != nil {
		return err
	}

	// runtime.cwd()
	cwdFn, err := iso.NewFunctionTemplate(p.cwdFunc)
	if err != nil {
		return err
	}
	cwdVal, err := cwdFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("cwd", cwdVal); err != nil {
		return err
	}

	// runtime.chdir()
	chdirFn, err := iso.NewFunctionTemplate(p.chdirFunc)
	if err != nil {
		return err
	}
	chdirVal, err := chdirFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("chdir", chdirVal); err != nil {
		return err
	}

	// process.reallyExit(code), which process.exit calls once the 'exit' listeners have run.
	exitFn, err := iso.NewFunctionTemplate(p.reallyExitFunc)
	if err != nil {
		return err
	}
	exitVal, err := exitFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("reallyExit", exitVal); err != nil {
		return err
	}

	// runtime.hrtime()
	hrtimeFn, err := iso.NewFunctionTemplate(p.hrtimeFunc)
	if err != nil {
		return err
	}
	hrtimeVal, err := hrtimeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("hrtime", hrtimeVal); err != nil {
		return err
	}

	// runtime.hrtime.bigint()
	hrtimeBigintFn, err := iso.NewFunctionTemplate(p.hrtimeBigintFunc)
	if err != nil {
		return err
	}
	hrtimeBigintVal, err := hrtimeBigintFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := hrtimeVal.Set("bigint", hrtimeBigintVal); err != nil {
		return err
	}

	// runtime.uptime()
	uptimeFn, err := iso.NewFunctionTemplate(p.uptimeFunc)
	if err != nil {
		return err
	}
	uptimeVal, err := uptimeFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("uptime", uptimeVal); err != nil {
		return err
	}

	// runtime.memoryUsage()
	memoryUsageFn, err := iso.NewFunctionTemplate(p.memoryUsageFunc)
	if err != nil {
		return err
	}
	memoryUsageVal, err := memoryUsageFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("memoryUsage", memoryUsageVal); err != nil {
		return err
	}

	// runtime.nextTick()
	nextTickFn, err := iso.NewFunctionTemplate(p.nextTickFunc)
	if err != nil {
		return err
	}
	nextTickVal, err := nextTickFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("nextTick", nextTickVal); err != nil {
		return err
	}

	// process.stdout / process.stderr as real writable streams backed by the
	// OS file descriptors. Many npm packages write via process.stdout.write and
	// Node's own console is built on these, so they must be present.
	stdoutObj, err := p.buildWriteStream(ctx, iso, os.Stdout, 1)
	if err != nil {
		return err
	}
	if err := processObj.Set("stdout", stdoutObj); err != nil {
		return err
	}
	stderrObj, err := p.buildWriteStream(ctx, iso, os.Stderr, 2)
	if err != nil {
		return err
	}
	if err := processObj.Set("stderr", stderrObj); err != nil {
		return err
	}
	stdinObj, err := p.buildReadStream(ctx, iso, os.Stdin, 0)
	if err != nil {
		return err
	}
	if err := processObj.Set("stdin", stdinObj); err != nil {
		return err
	}

	if err := rt.BindProcess(processObj); err != nil {
		return err
	}

	// Set process as global
	if err := rt.SetGlobal("process", processObj); err != nil {
		return err
	}

	// In Node.js `process` is an EventEmitter (it emits 'exit', 'beforeExit',
	// etc.). The events module runs before process, so mix EventEmitter into
	// the process object here. Also expose a writable `exitCode` property,
	// which the CLI honors when the program finishes.
	_, err = rt.RunScript(`(function () {
		if (typeof EventEmitter === 'function' && process && typeof process.on !== 'function') {
			var emitter = new EventEmitter();
			var methods = ['on', 'once', 'off', 'addListener', 'removeListener',
				'removeAllListeners', 'emit', 'listeners', 'listenerCount',
				'eventNames', 'prependListener', 'prependOnceListener',
				'setMaxListeners', 'getMaxListeners'];
			methods.forEach(function (m) {
				if (typeof emitter[m] === 'function') {
					process[m] = function () {
						return emitter[m].apply(emitter, arguments);
					};
				}
			});
		}
		if (process && !('exitCode' in process)) {
			process.exitCode = undefined;
		}

		// process.exit as in Node: the 'exit' listeners run once, and can change the code
		// through process.exitCode or by calling process.exit again.
		if (process && typeof process.reallyExit === 'function') {
			process.exit = function exit(code) {
				if (code !== undefined) process.exitCode = code;
				if (!process._exiting) {
					process._exiting = true;
					// Without the events module there is no emit, and nothing to notify.
					if (typeof process.emit === 'function') {
						process.emit('exit', Number(process.exitCode) || 0);
					}
				}
				process.reallyExit(Number(process.exitCode) || 0);
				// reallyExit asked V8 to stop, but V8 only stops at its next interrupt check,
				// and returning from a native call is not one. A loop checks on every
				// iteration, so nothing after process.exit runs, not even a finally block.
				for (;;) {}
			};
		}

		// Turn the Go-backed std streams into proper Node stream-like objects:
		// mix in EventEmitter and add the writable/readable surface libraries
		// expect. The underlying write()/read() are provided in Go.
		var emMethods = ['on', 'once', 'off', 'addListener', 'removeListener',
			'removeAllListeners', 'emit', 'listeners', 'listenerCount',
			'eventNames', 'prependListener', 'prependOnceListener',
			'setMaxListeners', 'getMaxListeners'];
		function mixEmitter(s) {
			if (!s || typeof EventEmitter !== 'function') return;
			var em = new EventEmitter();
			emMethods.forEach(function (m) {
				if (typeof em[m] === 'function' && typeof s[m] !== 'function') {
					s[m] = function () { return em[m].apply(em, arguments); };
				}
			});
		}
		// Bytes reach Go as base64, because a string stops at its first NUL. Buffer registers
		// after process, and an embedder can register process without it, so without Buffer
		// strings are encoded as UTF-8 here.
		var B64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
		function utf8Bytes(str) {
			var out = [];
			for (var i = 0; i < str.length; i++) {
				var c = str.charCodeAt(i);
				// 0xD800-0xDBFF is a UTF-16 high surrogate, the first half of a pair.
				if (c >= 0xd800 && c <= 0xdbff && i + 1 < str.length) {
					var next = str.charCodeAt(i + 1);
					// 0xDC00-0xDFFF is a UTF-16 low surrogate, the second half of a pair.
					if (next >= 0xdc00 && next <= 0xdfff) {
						// Combine the pair into one code point. 0x10000 is the first code point past the Basic Multilingual Plane.
						c = 0x10000 + ((c - 0xd800) << 10) + (next - 0xdc00);
						i++;
					}
				}
				// A lone surrogate (0xD800-0xDFFF) becomes 0xFFFD, U+FFFD the replacement character.
				if (c >= 0xd800 && c <= 0xdfff) c = 0xfffd;
				// Below 0x80 is ASCII, written as one byte.
				if (c < 0x80) out.push(c);
				// Below 0x800 takes two bytes: 0xC0 marks a 2-byte lead (110xxxxx) and 0x80 a continuation byte (10xxxxxx).
				else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
				// Below 0x10000 takes three bytes: 0xE0 marks a 3-byte lead (1110xxxx), then two 0x80 continuation bytes.
				else if (c < 0x10000) out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
				// Anything else takes four bytes: 0xF0 marks a 4-byte lead (11110xxx), then three 0x80 continuation bytes.
				else out.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 63), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
			}
			return new Uint8Array(out);
		}
		function base64Of(bytes) {
			var out = '';
			for (var i = 0; i < bytes.length; i += 3) {
				var n = (bytes[i] << 16) | ((bytes[i + 1] || 0) << 8) | (bytes[i + 2] || 0);
				out += B64[n >> 18] + B64[(n >> 12) & 63] +
					(i + 1 < bytes.length ? B64[(n >> 6) & 63] : '=') + (i + 2 < bytes.length ? B64[n & 63] : '=');
			}
			return out;
		}
		function wrapWrite(s) {
			var writeBase64 = s.write;
			s.write = function (chunk, encoding, cb) {
				if (typeof encoding === 'function') {
					cb = encoding;
					encoding = undefined;
				}
				var hasBuffer = typeof Buffer === 'function';
				var bytes;
				if (typeof chunk === 'string') {
					bytes = hasBuffer ? Buffer.from(chunk, encoding || 'utf8') : utf8Bytes(chunk);
				} else if (ArrayBuffer.isView(chunk)) {
					bytes = hasBuffer
						? Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength)
						: new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength);
				} else {
					var err = chunk === null
						? new TypeError('May not write null values to stream')
						: new TypeError('The "chunk" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
					err.code = chunk === null ? 'ERR_STREAM_NULL_VALUES' : 'ERR_INVALID_ARG_TYPE';
					throw err;
				}
				writeBase64(hasBuffer ? bytes.toString('base64') : base64Of(bytes));
				if (typeof cb === 'function') process.nextTick(cb);
				return true;
			};
		}
		['stdout', 'stderr'].forEach(function (name) {
			var s = process[name];
			if (!s) return;
			mixEmitter(s);
			s.writable = true;
			s.readable = false;
			wrapWrite(s);
			if (typeof s.end !== 'function') {
				s.end = function (chunk, enc, cb) {
					if (typeof chunk === 'function') {
						cb = chunk;
						chunk = null;
					}
					if (typeof enc === 'function') {
						cb = enc;
						enc = undefined;
					}
					if (chunk != null) this.write(chunk, enc);
					if (typeof cb === 'function') cb();
					return this;
				};
			}
			if (typeof s.cork !== 'function') s.cork = function () {};
			if (typeof s.uncork !== 'function') s.uncork = function () {};
			if (typeof s.setDefaultEncoding !== 'function') s.setDefaultEncoding = function () { return this; };

			// TTY WriteStream surface. Node only exposes these on real TTY
			// streams; libraries (mocha, chalk, ora, ...) probe them to decide
			// column width and color support. We back them with sensible values
			// and honor COLUMNS/LINES / a default 80x24 when the size is unknown.
			if (s.isTTY) {
				if (!('columns' in s) || s.columns == null) {
					var envCols = parseInt((process.env && process.env.COLUMNS) || '', 10);
					s.columns = envCols > 0 ? envCols : 80;
				}
				if (!('rows' in s) || s.rows == null) {
					var envRows = parseInt((process.env && process.env.LINES) || '', 10);
					s.rows = envRows > 0 ? envRows : 24;
				}
				if (typeof s.getWindowSize !== 'function') {
					s.getWindowSize = function () { return [this.columns || 80, this.rows || 24]; };
				}
				if (typeof s.getColorDepth !== 'function') {
					s.getColorDepth = function () { return 8; };
				}
				if (typeof s.hasColors !== 'function') {
					s.hasColors = function (count) {
						var colors = 1 << this.getColorDepth();
						return typeof count === 'number' ? colors >= count : true;
					};
				}
				if (typeof s.clearLine !== 'function') s.clearLine = function () { return true; };
				if (typeof s.clearScreenDown !== 'function') s.clearScreenDown = function () { return true; };
				if (typeof s.cursorTo !== 'function') s.cursorTo = function () { return true; };
				if (typeof s.moveCursor !== 'function') s.moveCursor = function () { return true; };
			}
		});
		var stdin = process.stdin;
		if (stdin) {
			mixEmitter(stdin);
			stdin.readable = true;
			stdin.writable = false;
			if (typeof stdin.resume !== 'function') stdin.resume = function () { return this; };
			if (typeof stdin.pause !== 'function') stdin.pause = function () { return this; };
			if (typeof stdin.setEncoding !== 'function') stdin.setEncoding = function () { return this; };
			if (typeof stdin.read !== 'function') stdin.read = function () { return null; };
		}
	})();`, "process_events.js")
	return err
}

// buildWriteStream builds a minimal writable stream object bound to an OS file.
// The stream is enriched with EventEmitter + writable helpers in the bootstrap
// JS above; here we provide the native write() plus fd/isTTY.
func (p *Process) buildWriteStream(ctx *v8.Context, iso *v8.Isolate, f *os.File, fd int) (*v8.Value, error) {
	obj, err := ctx.NewObject()
	if err != nil {
		return nil, err
	}
	writeFn, err := iso.NewFunctionTemplate(p.writeStreamFunc(f))
	if err != nil {
		return nil, err
	}
	writeVal, err := writeFn.GetFunction(ctx)
	if err != nil {
		return nil, err
	}
	if err := obj.Set("write", writeVal); err != nil {
		return nil, err
	}
	if err := obj.Set("fd", ctx.NewInteger(int64(fd))); err != nil {
		return nil, err
	}
	if err := obj.Set("isTTY", ctx.NewBoolean(isTerminal(f))); err != nil {
		return nil, err
	}
	return obj, nil
}

// buildReadStream builds a minimal readable stream object bound to an OS file.
// Full stdin reading is not wired to the event loop yet; this provides the
// surface (fd/isTTY + JS-side resume/pause/read) so code that touches
// process.stdin does not crash.
func (p *Process) buildReadStream(ctx *v8.Context, iso *v8.Isolate, f *os.File, fd int) (*v8.Value, error) {
	obj, err := ctx.NewObject()
	if err != nil {
		return nil, err
	}
	if err := obj.Set("fd", ctx.NewInteger(int64(fd))); err != nil {
		return nil, err
	}
	if err := obj.Set("isTTY", ctx.NewBoolean(isTerminal(f))); err != nil {
		return nil, err
	}
	return obj, nil
}

// writeStreamFunc returns a write(base64Data) implementation that writes the bytes to the
// given OS file. The bootstrap JS above wraps it in Node's write(chunk[, encoding][, callback]).
func (p *Process) writeStreamFunc(f *os.File) v8.FunctionCallback {
	return func(info *v8.FunctionCallbackInfo) *v8.Value {
		args := info.Args()
		if len(args) < 1 {
			return nil
		}
		data, err := base64.StdEncoding.DecodeString(args[0].String())
		if err != nil {
			return info.Context().Throw(err.Error())
		}
		_, _ = f.Write(data)
		return nil
	}
}

// isTerminal reports whether the file is attached to a character device (TTY).
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (p *Process) createArgv(ctx *v8.Context) (*v8.Value, error) {
	args := os.Args
	argv, err := ctx.NewArray(len(args))
	if err != nil {
		return nil, err
	}
	for i, arg := range args {
		val, err := ctx.NewString(arg)
		if err != nil {
			return nil, err
		}
		if err := argv.SetIndex(i, val); err != nil {
			return nil, err
		}
	}
	return argv, nil
}

func (p *Process) createEnv(ctx *v8.Context) (*v8.Value, error) {
	env, err := ctx.NewObject()
	if err != nil {
		return nil, err
	}

	// Use the runtime's environment interface
	envProvider := p.rt.Environment()
	for key, value := range envProvider.All() {
		val, err := ctx.NewString(value)
		if err != nil {
			return nil, err
		}
		if err := env.Set(key, val); err != nil {
			return nil, err
		}
	}
	return env, nil
}

func (p *Process) cwdFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	// If document root is set, return path relative to it
	docRoot := p.rt.DocumentRoot()
	if docRoot != "" {
		absRoot, err := filepath.Abs(docRoot)
		if err == nil {
			if strings.HasPrefix(cwd+string(filepath.Separator), absRoot+string(filepath.Separator)) {
				relPath := strings.TrimPrefix(cwd, absRoot)
				if relPath == "" {
					relPath = "/"
				} else if !strings.HasPrefix(relPath, "/") {
					relPath = "/" + relPath
				}
				cwd = relPath
			} else {
				// CWD is outside sandbox, return root
				cwd = "/"
			}
		}
	}

	val, _ := info.Context().NewString(cwd)
	return val
}

func (p *Process) chdirFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	dir := args[0].String()
	if err := os.Chdir(dir); err != nil {
		// In a real implementation, we'd throw a proper error
		return nil
	}
	return nil
}

// reallyExitFunc backs process.reallyExit(code): it ends the runtime, which hands the code to
// the embedder rather than ending the host process. Nothing more of the program runs.
func (p *Process) reallyExitFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	code := 0
	if args := info.Args(); len(args) >= 1 {
		code = int(args[0].Integer())
	}
	p.exitCode = code
	p.rt.Exit(code)
	return nil
}

func (p *Process) hrtimeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	now := time.Now().UnixNano()
	args := info.Args()

	if len(args) >= 1 && args[0].IsArray() {
		// Subtract the previous hrtime
		prevSec, _ := args[0].GetIndex(0)
		prevNano, _ := args[0].GetIndex(1)
		if prevSec != nil && prevNano != nil {
			prev := prevSec.Integer()*1e9 + prevNano.Integer()
			now -= prev
		}
	}

	sec := now / 1e9
	nano := now % 1e9

	arr, _ := ctx.NewArray(2)
	secVal := ctx.NewInteger(sec)
	nanoVal := ctx.NewInteger(nano)
	arr.SetIndex(0, secVal)
	arr.SetIndex(1, nanoVal)
	return arr
}

func (p *Process) hrtimeBigintFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	// Return as a string since we don't have BigInt support yet
	now := time.Now().UnixNano()
	val, _ := ctx.NewString(strconv.FormatInt(now, 10))
	return val
}

func (p *Process) uptimeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	uptime := time.Since(p.startTime).Seconds()
	return info.Context().NewNumber(uptime)
}

func (p *Process) memoryUsageFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	obj, _ := ctx.NewObject()

	// rss remains process-wide OS memory (Go runtime view). Heap fields must
	// reflect this isolate's V8 heap so LRU / per-function budgeting is accurate.
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	obj.Set("rss", ctx.NewNumber(float64(m.Sys)))

	heapTotal, heapUsed, external := 0.0, 0.0, 0.0
	if iso := p.rt.Isolate(); iso != nil {
		if stats, err := iso.GetHeapStatistics(); err == nil {
			heapTotal = float64(stats.TotalHeapSize)
			heapUsed = float64(stats.UsedHeapSize)
			external = float64(stats.ExternalMemory)
		}
	}
	obj.Set("heapTotal", ctx.NewNumber(heapTotal))
	obj.Set("heapUsed", ctx.NewNumber(heapUsed))
	obj.Set("external", ctx.NewNumber(external))
	// arrayBuffers are a Node-specific subset of external; we do not track them
	// separately yet, so report 0 rather than double-count external.
	obj.Set("arrayBuffers", ctx.NewNumber(0))
	return obj
}

func (p *Process) nextTickFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 || !args[0].IsFunction() {
		return nil
	}

	callback := args[0]
	var callArgs []*v8.Value
	if len(args) > 1 {
		callArgs = args[1:]
	}

	p.rt.EventLoop().EnqueueTick(func() {
		p.rt.MakeCallback(callback, nil, callArgs...)
	})

	return nil
}

// goArchToNode converts Go architecture names to Node.js equivalents.
func goArchToNode(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}
