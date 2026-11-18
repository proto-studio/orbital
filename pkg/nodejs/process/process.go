// Package process implements the Node.js process module.
package process

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"proto.zip/studio/orbital/internal/v8dist"
	"proto.zip/studio/orbital/pkg/nodejs/internal/sysconst"
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
// NodeVersion is the Node.js release Orbital emulates: process.version is "v" + NodeVersion
// and process.versions.node is NodeVersion.
const NodeVersion = "20.18.1"

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

	// runtime.version: the Node release whose behavior Orbital matches, and which its
	// comparison tests capture against.
	version, _ := ctx.NewString("v" + NodeVersion)
	if err := processObj.Set("version", version); err != nil {
		return err
	}

	// runtime.versions
	versions, _ := ctx.NewObject()
	nodeVer, _ := ctx.NewString(NodeVersion)
	v8Version := "unknown"
	if manifest, err := v8dist.LoadManifest(); err == nil {
		v8Version = manifest.V8Version
	}
	v8Ver, _ := ctx.NewString(v8Version)
	goVer, _ := ctx.NewString(runtime.Version())
	versions.Set("node", nodeVer)
	versions.Set("v8", v8Ver)
	versions.Set("gnode", goVer)
	// Orbital's own version, without the "v" as Node writes the entries here.
	orbitalVer, _ := ctx.NewString(strings.TrimPrefix(v8dist.ModuleVersion, "v"))
	versions.Set("orbital", orbitalVer)
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

	// The native half of process.chdir. The setup script below takes it and removes it
	// from process.
	chdirFn, err := iso.NewFunctionTemplate(p.chdirFunc)
	if err != nil {
		return err
	}
	chdirVal, err := chdirFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("__orbitalChdir", chdirVal); err != nil {
		return err
	}

	// The native half of process.kill and the host's signal numbers. The setup script below
	// takes both and removes them from process.
	killFn, err := iso.NewFunctionTemplate(p.killFunc)
	if err != nil {
		return err
	}
	killVal, err := killFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := processObj.Set("__orbitalKill", killVal); err != nil {
		return err
	}
	signals, err := ctx.NewObject()
	if err != nil {
		return err
	}
	for _, sig := range sysconst.Signals() {
		signals.Set(sig.Name, ctx.NewInteger(int64(sig.Value)))
	}
	if err := processObj.Set("__orbitalSignals", signals); err != nil {
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

		// process.kill as in Node: it validates its arguments, then signals through the
		// runtime's ProcessSignaler and throws what that refuses as an errno error.
		if (process && typeof process.__orbitalKill === 'function') {
			var nativeKill = process.__orbitalKill;
			var signalNumbers = process.__orbitalSignals;
			delete process.__orbitalKill;
			delete process.__orbitalSignals;
			var describe = function (value) {
				if (value === null || value === undefined) return 'Received ' + value;
				if (typeof value === 'function') return 'Received function ' + value.name;
				if (typeof value === 'object') {
					return value.constructor && value.constructor.name
						? 'Received an instance of ' + value.constructor.name
						: 'Received ' + String(value);
				}
				var shown = typeof value === 'string' ? "'" + value + "'" : String(value);
				return 'Received type ' + typeof value + ' (' + shown + ')';
			};
			var codedError = function (code, message) {
				var err = new TypeError(message);
				err.code = code;
				return err;
			};
			process.kill = function kill(pid, sig) {
				// eslint-disable-next-line eqeqeq
				if (pid != (pid | 0)) {
					throw codedError('ERR_INVALID_ARG_TYPE',
						'The "pid" argument must be of type number. ' + describe(pid));
				}
				var number;
				if (sig === (sig | 0)) {
					number = sig;
				} else {
					sig = sig || 'SIGTERM';
					if (!Object.prototype.hasOwnProperty.call(signalNumbers, sig)) {
						throw codedError('ERR_UNKNOWN_SIGNAL', 'Unknown signal: ' + sig);
					}
					number = signalNumbers[sig];
				}
				var result = nativeKill(pid | 0, number);
				if (result !== 0) {
					var err = new Error('kill ' + result[1]);
					err.errno = result[0];
					err.code = result[1];
					err.syscall = 'kill';
					throw err;
				}
				return true;
			};
		}

		// process.chdir as in Node: it throws what the OS refuses as an errno error naming
		// the working directory and the one asked for.
		if (process && typeof process.__orbitalChdir === 'function') {
			var nativeChdir = process.__orbitalChdir;
			delete process.__orbitalChdir;
			var errnoMessages = { EACCES: 'permission denied', ENOENT: 'no such file or directory', ENOTDIR: 'not a directory' };
			process.chdir = function chdir(directory) {
				directory = String(directory);
				var result = nativeChdir(directory);
				if (result !== 0) {
					var cwd = process.cwd();
					var err = new Error(result[1] + ': ' + (errnoMessages[result[1]] || 'unknown error') +
						", chdir '" + cwd + "' -> '" + directory + "'");
					err.errno = result[0];
					err.code = result[1];
					err.syscall = 'chdir';
					err.path = cwd;
					err.dest = directory;
					throw err;
				}
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
	if err != nil {
		return err
	}
	if rt.NoWarnings() {
		if err := processObj.Set("noProcessWarnings", ctx.NewBoolean(true)); err != nil {
			return err
		}
	}
	if _, err := rt.RunScript(processWarningJS, "node:internal/process/warning"); err != nil {
		return err
	}
	return p.defineTitle(rt)
}

// defineTitle adds process.title. As in Node it starts as the name the program was run
// as ("orbital" for the CLI), and an assignment is kept as a string.
func (p *Process) defineTitle(rt *goruntime.Runtime) error {
	title := "node"
	if len(os.Args) > 0 {
		if base := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"); base != "" && base != "." && base != string(filepath.Separator) {
			title = base
		}
	}
	quoted, err := json.Marshal(title)
	if err != nil {
		return err
	}
	_, err = rt.RunScript(`(function (initial) {
		var title = initial;
		Object.defineProperty(process, 'title', {
			get: function () { return title; },
			set: function (value) { title = String(value); },
			enumerable: true,
			configurable: true
		});
	})(`+string(quoted)+`);`, "process_title.js")
	return err
}

// processWarningJS is process.emitWarning and the listener that prints warnings, following
// lib/internal/process/warning.js.
const processWarningJS = `(function () {
	'use strict';
	if (!process || typeof process.emit !== 'function' || typeof process.on !== 'function') return;

	function describe(value) {
		if (value === null || value === undefined) return String(value);
		if (typeof value === 'function') return 'function ' + (value.name || '<anonymous>');
		if (typeof value === 'object') {
			var ctor = value.constructor;
			return ctor && ctor.name ? 'an instance of ' + ctor.name : 'an instance of Object';
		}
		var shown = typeof value === 'string' ? "'" + value + "'" : String(value);
		if (shown.length > 28) shown = shown.slice(0, 25) + '...';
		return 'type ' + typeof value + ' (' + shown + ')';
	}

	function invalidArg(name, expected, value) {
		var err = new TypeError('The "' + name + '" argument must be ' + expected + '. Received ' + describe(value));
		err.code = 'ERR_INVALID_ARG_TYPE';
		return err;
	}

	// A program can freeze Error or make stackTraceLimit read-only.
	function stackTraceLimitWritable() {
		var desc = Object.getOwnPropertyDescriptor(Error, 'stackTraceLimit');
		if (desc === undefined) return Object.isExtensible(Error);
		return Object.prototype.hasOwnProperty.call(desc, 'writable') ? desc.writable : desc.set !== undefined;
	}

	function createWarningObject(message, type, code, ctor, detail) {
		var limit = Error.stackTraceLimit;
		var writable = stackTraceLimitWritable();
		if (writable) Error.stackTraceLimit = 0;
		var warning = new Error(message);
		if (writable) Error.stackTraceLimit = limit;
		warning.name = String(type || 'Warning');
		if (code !== undefined) warning.code = code;
		if (detail !== undefined) warning.detail = detail;
		if (typeof Error.captureStackTrace === 'function') {
			Error.captureStackTrace(warning, ctor || process.emitWarning);
		}
		return warning;
	}

	function doEmitWarning(warning) {
		process.emit('warning', warning);
	}

	process.emitWarning = function emitWarning(warning, type, code, ctor) {
		var detail;
		if (type !== null && typeof type === 'object' && !Array.isArray(type)) {
			ctor = type.ctor;
			code = type.code;
			if (typeof type.detail === 'string') detail = type.detail;
			type = type.type || 'Warning';
		} else if (typeof type === 'function') {
			ctor = type;
			code = undefined;
			type = 'Warning';
		}
		if (type !== undefined && typeof type !== 'string') throw invalidArg('type', 'of type string', type);
		if (typeof code === 'function') {
			ctor = code;
			code = undefined;
		} else if (code !== undefined && typeof code !== 'string') {
			throw invalidArg('code', 'of type string', code);
		}
		if (typeof warning === 'string') {
			warning = createWarningObject(warning, type, code, ctor, detail);
		} else if (!(warning instanceof Error)) {
			throw invalidArg('warning', 'of type string or an instance of Error', warning);
		}
		if (warning.name === 'DeprecationWarning') {
			if (process.noDeprecation) return;
			// Thrown a tick later, after the warnings already queued, as in Node.
			if (process.throwDeprecation) return process.nextTick(function () { throw warning; });
		}
		process.nextTick(doEmitWarning, warning);
	};

	var traceHintShown = false;

	function write(message) {
		if (process.stderr && typeof process.stderr.write === 'function') {
			process.stderr.write(message + '\n');
		} else if (typeof console !== 'undefined' && typeof console.error === 'function') {
			console.error(message);
		}
	}

	function onWarning(warning) {
		if (!(warning instanceof Error)) return;
		var isDeprecation = warning.name === 'DeprecationWarning';
		if (isDeprecation && process.noDeprecation) return;
		var trace = process.traceProcessWarnings || (isDeprecation && process.traceDeprecation);
		var release = process.release && process.release.name ? process.release.name : 'node';
		var message = '(' + release + ':' + process.pid + ') ';
		if (warning.code) message += '[' + warning.code + '] ';
		if (trace && warning.stack) {
			message += warning.stack;
		} else {
			message += typeof warning.toString === 'function' ? '' + warning.toString() : Error.prototype.toString.call(warning);
		}
		if (typeof warning.detail === 'string') message += '\n' + warning.detail;
		if (!trace && !traceHintShown) {
			var flag = isDeprecation ? '--trace-deprecation' : '--trace-warnings';
			var argv0 = String(process.argv0 || 'node').split(/[\\/]/).pop().replace(/\.exe$/, '');
			message += '\n(Use ` + "`" + `' + argv0 + ' ' + flag + ' ...` + "`" + ` to show where the warning was created)';
			traceHintShown = true;
		}
		write(message);
	}

	if (!process.noProcessWarnings) process.on('warning', onWarning);
})();`

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

// killFunc(pid, signal) sends a signal through the runtime's ProcessSignaler. It returns 0,
// or the negative errno and its code as [errno, code], as libuv reports them.
func (p *Process) killFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.NewInteger(0)
	}
	err := p.rt.ProcessSignaler().Kill(int(args[0].Integer()), syscall.Signal(args[1].Integer()))
	if err == nil {
		return ctx.NewInteger(0)
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		errno = syscall.EPERM
	}
	code, ok := sysconst.ErrnoName(errno)
	if !ok {
		code = "UNKNOWN"
	}
	result, _ := ctx.NewArray(2)
	result.SetIndex(0, ctx.NewInteger(-int64(errno)))
	codeVal, _ := ctx.NewString(code)
	result.SetIndex(1, codeVal)
	return result
}

// chdirFunc(dir) changes the working directory. It returns 0, or the negative errno and
// its code as [errno, code], as libuv reports them. Without filesystem access it refuses
// with EACCES, as the OS does for a directory it won't let the process enter.
func (p *Process) chdirFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.NewInteger(0)
	}
	var err error = syscall.EACCES
	if _, closed := p.rt.Filesystem().(*goruntime.NoOpFilesystem); !closed {
		err = os.Chdir(args[0].String())
	}
	if err == nil {
		return ctx.NewInteger(0)
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		errno = syscall.ENOENT
	}
	code, ok := sysconst.ErrnoName(errno)
	if !ok {
		code = "UNKNOWN"
	}
	result, _ := ctx.NewArray(2)
	result.SetIndex(0, ctx.NewInteger(-int64(errno)))
	codeVal, _ := ctx.NewString(code)
	result.SetIndex(1, codeVal)
	return result
}

// reallyExitFunc backs process.reallyExit(code): it ends the runtime, which hands the code to
// the embedder rather than ending the host process. Nothing more of the program runs.
func (p *Process) reallyExitFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	code := 0
	if args := info.Args(); len(args) >= 1 {
		code = int(args[0].Integer())
	}
	p.exitCode = code
	p.rt.RunProcessExitHooks()
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
