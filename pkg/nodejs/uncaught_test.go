package nodejs

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runUncaught runs source and returns what it passed to note(), and Run's error.
func runUncaught(t *testing.T, source string) ([]string, error) {
	t.Helper()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	prelude := `globalThis.__notes = []; globalThis.note = (...a) => __notes.push(a.join(' '));`
	if _, err := inst.Runtime.RunScript(prelude, "prelude.js"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run(source, "test.js")
	if inst.Runtime.IsKilled() {
		t.Fatalf("killed: %s", inst.Runtime.KillReason())
	}
	val, err := inst.Runtime.RunScript(`JSON.stringify(__notes)`, "notes.js")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	if err := json.Unmarshal([]byte(val.String()), &notes); err != nil {
		t.Fatal(err)
	}
	return notes, runErr
}

func supportsRejections(t *testing.T) {
	t.Helper()
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if !inst.Runtime.TracksRejections() {
		t.Fatal("the linked V8 glue does not report promise rejections")
	}
}

const (
	netThrower = `
const net = require('net');
let client;
const server = net.createServer((socket) => {
  socket.on('data', () => { throw new Error('boom'); });
});
const cleanup = () => { server.close(); client.destroy(); };
server.listen(0, '127.0.0.1', () => {
  client = net.connect(server.address().port, '127.0.0.1', () => client.write('x'));
});
`
	httpThrower = `
const http = require('http');
const server = http.createServer(() => { throw new Error('boom'); });
const cleanup = () => { server.close(); server.closeAllConnections(); };
server.listen(0, '127.0.0.1', () => {
  http.get({ port: server.address().port, host: '127.0.0.1' }).on('error', () => {});
});
`
)

// The expectations in the tests below are what Node 20.18.1 does with the same script.

func TestUncaughtCallbackWithHandler(t *testing.T) {
	throwers := map[string]string{
		"setTimeout":     `const cleanup = () => {}; setTimeout(() => { throw new Error('boom'); }, 1);`,
		"setImmediate":   `const cleanup = () => {}; setImmediate(() => { throw new Error('boom'); });`,
		"nextTick":       `const cleanup = () => {}; process.nextTick(() => { throw new Error('boom'); });`,
		"queueMicrotask": `const cleanup = () => {}; queueMicrotask(() => { throw new Error('boom'); });`,
		"fs":             `const cleanup = () => {}; require('fs').stat('.', () => { throw new Error('boom'); });`,
		"socket":         netThrower,
		"http handler":   httpThrower,
	}
	for name, thrower := range throwers {
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaught(t, `
process.on('uncaughtExceptionMonitor', (e, origin) => note('monitor', e.message, origin));
process.on('uncaughtException', (e, origin) => {
  note('handler', e.message, origin);
  cleanup();
  setTimeout(() => note('still running'), 1);
});
`+thrower)
			if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			want := []string{"monitor boom uncaughtException", "handler boom uncaughtException", "still running"}
			if !reflect.DeepEqual(notes, want) {
				t.Errorf("notes = %q, want %q", notes, want)
			}
		})
	}
}

func TestUncaughtCallbackWithoutHandler(t *testing.T) {
	throwers := map[string]string{
		"setTimeout":   `setTimeout(() => { throw new Error('boom'); }, 1);`,
		"nextTick":     `process.nextTick(() => { throw new Error('boom'); });`,
		"socket":       netThrower,
		"http handler": httpThrower,
	}
	for name, thrower := range throwers {
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code, process.exitCode));
setTimeout(() => note('must not run'), 200);
`+thrower)
			var uncaught *runtime.UncaughtError
			if !errors.As(err, &uncaught) {
				t.Fatalf("Run returned %v, want an UncaughtError", err)
			}
			if uncaught.ExitCode != 1 || uncaught.Origin != "uncaughtException" {
				t.Errorf("got %+v", uncaught)
			}
			if !strings.Contains(uncaught.Message, "Error: boom") || !strings.Contains(uncaught.Message, "    at ") {
				t.Errorf("message is missing the stack: %q", uncaught.Message)
			}
			if want := []string{"exit 1 1"}; !reflect.DeepEqual(notes, want) {
				t.Errorf("notes = %q, want %q", notes, want)
			}
		})
	}
}

func TestUncaughtMainScript(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e, origin) => note(e.message, origin));
setTimeout(() => note('later'), 1);
throw new Error('main');
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"main uncaughtException", "later"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}

	notes, err = runUncaught(t, `process.on('exit', (code) => note('exit', code)); throw new TypeError('main');`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 1 || !strings.Contains(uncaught.Message, "TypeError: main") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtMainScriptPassesTheThrownValue(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note(e.code, e instanceof RangeError));
const e = new RangeError('main');
e.code = 'E_X';
throw e;
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"E_X true"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtPassesTheThrownValue(t *testing.T) {
	notes, err := runUncaught(t, `
const thrown = { not: 'an error' };
process.on('uncaughtException', (e) => note(e === thrown));
setTimeout(() => { throw thrown; }, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"true"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtHandlerThrows(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('exit', () => note('must not run'));
process.on('uncaughtException', () => { note('handler'); throw new Error('second'); });
setTimeout(() => { throw new Error('first'); }, 1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 7 || !strings.Contains(uncaught.Message, "Error: second") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"handler"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtExitListenerSetsCode(t *testing.T) {
	_, err := runUncaught(t, `
process.on('exit', () => { process.exitCode = 9; });
setTimeout(() => { throw { a: 1 }; }, 1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 9 || !strings.Contains(uncaught.Message, "a: 1") {
		t.Fatalf("Run returned %#v", err)
	}
}

func TestUncaughtIntervalKeepsRunning(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note(e.message));
let n = 0;
const interval = setInterval(() => {
  if (++n === 2) clearInterval(interval);
  throw new Error('tick' + n);
}, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"tick1", "tick2"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtCaptureCallback(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', () => note('must not run'));
process.setUncaughtExceptionCaptureCallback((e) => note('captured', e.message));
note(process.hasUncaughtExceptionCaptureCallback());
try {
  process.setUncaughtExceptionCaptureCallback(() => {});
} catch (e) {
  note(e.code);
}
setTimeout(() => { throw new Error('boom'); }, 1);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"true", "ERR_UNCAUGHT_EXCEPTION_CAPTURE_ALREADY_SET", "captured boom"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionWithHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('unhandledRejection', (r, p) => note('unhandled', r.message || r, p instanceof Promise));
process.on('rejectionHandled', (p) => note('handled late', p instanceof Promise));
const late = Promise.reject(new Error('late'));
setTimeout(() => late.catch(() => {}), 1);
Promise.reject(5);
Promise.reject(new Error('caught')).catch(() => note('caught'));
(async () => { throw new Error('async'); })();
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"caught", "unhandled late true", "unhandled 5 true", "unhandled async true", "handled late true"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionWithoutHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code));
setTimeout(() => note('must not run'), 200);
(async () => { throw new Error('rejected'); })();
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("Run returned %v", err)
	}
	if uncaught.ExitCode != 1 || uncaught.Origin != "unhandledRejection" || !strings.Contains(uncaught.Message, "Error: rejected") {
		t.Errorf("got %+v", uncaught)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUnhandledRejectionReachesUncaughtHandler(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e, origin) => note(e.code, e.message, origin));
Promise.reject(new Error('x'));
Promise.reject(42);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if len(notes) != 2 || notes[0] != " x unhandledRejection" ||
		!strings.HasPrefix(notes[1], "ERR_UNHANDLED_REJECTION This error originated") ||
		!strings.HasSuffix(notes[1], `The promise rejected with the reason "42". unhandledRejection`) {
		t.Errorf("notes = %q", notes)
	}
}

func TestUnhandledRejectionHandlerThrows(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code));
process.on('unhandledRejection', () => { throw new Error('from handler'); });
Promise.reject(1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || uncaught.ExitCode != 1 || uncaught.Origin != "uncaughtException" ||
		!strings.Contains(uncaught.Message, "Error: from handler") {
		t.Fatalf("Run returned %#v", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestWorkerUncaughtReachesParent(t *testing.T) {
	notes, err := runUncaught(t, `
const { Worker } = require('worker_threads');
const worker = new Worker("setTimeout(() => { throw new Error('in worker'); }, 1)", { eval: true });
worker.on('error', (e) => note('error', String(e.message).includes('in worker')));
worker.on('exit', (code) => note('exit', code));
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"error true", "exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// The parent's 'error' event gets the worker's thrown value as Node's error_serdes sends it:
// an error keeps its built-in class and its properties, other values are cloned, and what
// cannot be cloned arrives as its inspected text.
func TestWorkerErrorKeepsThrownValue(t *testing.T) {
	notes, err := runUncaught(t, `
const { Worker } = require('worker_threads');
const cases = [
  'const e = new TypeError("boom"); e.extra = 1; throw e;',
  'const e = new Error("c"); e.code = "E_X"; e.nested = { a: [1, 2] }; throw e;',
  'class MyErr extends RangeError { constructor(m) { super(m); this.name = "MyErr"; } get g() { return 1; } } throw new MyErr("custom");',
  'setTimeout(() => { throw new URIError("later"); }, 1);',
  'throw new Error("outer", { cause: new TypeError("inner") });',
  'const e = new Error("f"); e.fn = () => 1; e.sym = Symbol(); e.d = new Date(0); e.m = new Map([[1, 2]]); e.n = NaN; e.b = 10n; throw e;',
  'const e = new Error("nf"); e.o = { f() {} }; throw e;',
  'throw new AggregateError([], "agg");',
  'throw "a string";',
  'throw undefined;',
  'throw { a: 1 };',
  'throw { f() {} };',
  'let x = ;',
  'throw new Uint8Array([1, 2]).buffer;',
  'const e = new Error("ab"); e.buf = new Uint8Array([1, 2]).buffer; throw e;',
  'const e = new Error("sab"); e.sab = new SharedArrayBuffer(2); e.x = 1; throw e;',
  'throw JSON.parse(\'{"__proto__":{"flag":true},"a":1}\');',
  'class C { [Symbol.for("nodejs.util.inspect.custom")]() { return "CUSTOM C"; } } throw new C();',
  'const e = new Error("d"); e.d = new Date(NaN); throw e;',
  'throw new DataView(new Uint8Array([1, 2, 3]).buffer, 1, 2);',
  'const e = new TypeError("boom"); e.view = new DataView(new ArrayBuffer(2)); e.code = "E_TEST"; throw e;',
  'throw Promise.resolve(1);',
  'const e = new Error("w"); e.wm = new WeakMap(); e.code = 1; throw e;',
  'const e = new Error("f"); e.samples = new Float64Array([-0, 1.5]); e.sub = new Uint16Array(new Uint8Array([1, 2, 3, 4, 5, 6]).buffer, 2, 2); e.buf = Buffer.from("hi"); throw e;',
  'const inner = new RangeError("in"); delete inner.stack; throw { detail: inner };',
  'throw { n: new Number(5), s: new String("x"), b: new Boolean(false), big: Object(3n) };',
  'throw { sym: Object(Symbol("q")) };',
  'throw new Proxy({ code: "E_PROXY" }, {});',
  'const e = new TypeError("px"); e.p = new Proxy({}, {}); e.code = 2; throw e;',
  'throw new Proxy([1, 2], {});',
  'const c = new TypeError("inner"); c.code = "E_IN"; throw new Error("outer", { cause: c });',
  'const c = new TypeError("inner"); c.fn = () => 1; c.p = Promise.resolve(); const e = new Error("outer", { cause: c }); e.code = 1; throw e;',
  'throw Symbol("x");',
  'throw { a: 1, [Symbol.for("nodejs.util.inspect.custom")]() { throw new Error("bad inspect"); } };',
  'throw { [Symbol.toStringTag]: "Error", code: "E" };',
  'const e = new Error("g"); Object.defineProperty(e, "ro", { get() { return 5; }, enumerable: true, configurable: true }); throw e;',
  'const m = new Map([[1, 2]]); m[Symbol.iterator] = function* () { yield [9, 9]; }; throw { m };',
  'JSON.stringify = () => "{}"; const e = new RangeError("json"); e.code = "J"; throw e;',
  'const b = Buffer.from("hi"); Object.setPrototypeOf(b, Object.create(Buffer.prototype)); const e = new Error("sub"); e.buf = b; throw e;',
  'globalThis.require = () => ({ inspect: () => "FAKE" }); throw { f() {} };',
  'const e = new Error("cg"); Object.defineProperty(e, "cause", { get() { throw new Error("no"); }, configurable: true }); throw e;',
  'Number.prototype[Symbol.for("nodejs.util.inspect.custom")] = () => "boxed"; throw 1;',
  'const u = new Uint8Array(4); if (typeof u.buffer.transfer === "function") u.buffer.transfer(); else { const c = new (require("worker_threads").MessageChannel)(); c.port1.postMessage(null, [u.buffer]); c.port1.close(); } const e = new Error("det"); e.u = u; e.code = 9; throw e;',
];
const describe = (e) => {
  if (e instanceof Error && e.message === 'cg') return 'Error cg hasCause=' + Object.prototype.hasOwnProperty.call(e, 'cause');
  if (typeof e === 'symbol') return 'symbol ' + String(e) + ' registered=' + (Symbol.keyFor(e) !== undefined);
  // Inspected text is cut at its first brace: util.inspect itself is not under test.
  if (e === null || typeof e !== 'object') return typeof e + ' ' + String(e).replace(/ ?\{[\s\S]*$/, ' {...}');
  if (e instanceof DataView) return 'DataView ' + new Uint8Array(e.buffer, e.byteOffset, e.byteLength).join(',');
  if (Object.getPrototypeOf(e) === null) return 'null prototype ' + require('util').inspect(e) + ' hasCause=' + Object.prototype.hasOwnProperty.call(e, 'cause');
  if (e instanceof ArrayBuffer) return 'ArrayBuffer ' + new Uint8Array(e).join(',');
  const props = Object.getOwnPropertyNames(e).filter((k) => k !== 'stack').sort()
    .map((k) => k + '=' + (typeof e[k] === 'object' && e[k] !== null ? Object.prototype.toString.call(e[k]) : String(e[k])) +
      (e[k] instanceof ArrayBuffer ? ':' + new Uint8Array(e[k]).join(',') : '') +
      (e[k] instanceof Date ? ':' + String(e[k].getTime()) : '') +
      (k === '__proto__' ? ':own flag=' + e.flag : '') +
      (ArrayBuffer.isView(e[k]) ? ':' + e[k].constructor.name + ':' + Array.from(new Uint8Array(e[k].buffer, e[k].byteOffset, e[k].byteLength)) : '') +
      (k === 'samples' ? ':negzero=' + Object.is(e[k][0], -0) : '') +
      (k === 'detail' ? ':stack=' + typeof e[k].stack + ',' + Object.prototype.hasOwnProperty.call(e[k], 'stack') : '') +
      (['n', 's', 'b', 'big'].includes(k) ? ':' + typeof e[k] + ',' + e[k].valueOf() : '') +
      (k === 'cause' && e[k] instanceof Error ? ':' + e[k].constructor.name + ',' + e[k].message + ',code=' + e[k].code + ',own=' + Object.getOwnPropertyNames(e[k]).filter((n) => n !== 'stack').sort() : '') +
      (k === 'ro' ? ':writable=' + Object.getOwnPropertyDescriptor(e, k).writable : '') +
      (k === 'm' ? ':' + JSON.stringify([...e[k]]) : '') +
      (k === 'buf' ? ':isBuffer=' + Buffer.isBuffer(e[k]) + ',read=' + (typeof e[k].readUInt16LE === 'function' ? e[k].readUInt16LE(0) : '-') : '') +
      (k === 'h' ? ':' + Object.prototype.toString.call(e[k]) + ',' + Array.from(e[k]).map((x) => Object.is(x, -0) ? '-0' : x) : ''));
  return [e.constructor && e.constructor.name, e instanceof Error, Object.prototype.toString.call(e),
    typeof e.stack, e.cause ? e.cause.constructor.name + ':' + e.cause.message : '-', ...props].join(' ');
};
const next = (i) => {
  if (i === cases.length) return;
  const w = new Worker(cases[i], { eval: true });
  w.on('error', (e) => note(describe(e)));
  w.on('exit', () => next(i + 1));
};
next(0);
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	// Captured from Node 20.18.1.
	want := []string{
		"TypeError true [object Error] string - extra=1 message=boom name=TypeError",
		"Error true [object Error] string - code=E_X message=c name=Error nested=[object Object]",
		"RangeError true [object Error] string - g=1 message=custom name=MyErr",
		"URIError true [object Error] string - message=later name=URIError",
		"Error true [object Error] string TypeError:inner cause=[object Error]:TypeError,inner,code=undefined,own=message,name message=outer name=Error",
		"Error true [object Error] string - b=10:bigint,10 d=[object Date]:0 m=[object Map]:[[1,2]] message=f n=NaN:number,NaN name=Error",
		"Error true [object Error] string - message=nf",
		"Error true [object Error] string - errors=[object Array] message=agg name=AggregateError",
		"string a string",
		"undefined undefined",
		"Object false [object Object] undefined - a=1",
		"string  {...}",
		"SyntaxError true [object Error] string - message=Unexpected token ';' name=SyntaxError",
		"ArrayBuffer 1,2",
		"Error true [object Error] string - buf=[object ArrayBuffer]:1,2:isBuffer=false,read=- message=ab name=Error",
		"Error true [object Error] string - message=sab",
		"Object false [object Object] undefined - __proto__=[object Object]:own flag=undefined a=1",
		"null prototype CUSTOM C hasCause=false",
		"Error true [object Error] string - d=[object Date]:NaN message=d name=Error",
		"DataView 2,3",
		"TypeError true [object Error] string - code=E_TEST message=boom name=TypeError view=[object DataView]:DataView:0,0",
		"string Promise {...}",
		"Error true [object Error] string - message=w",
		"Error true [object Error] string - buf=[object Uint8Array]:Buffer:104,105:isBuffer=true,read=26984 message=f name=Error samples=[object Float64Array]:Float64Array:0,0,0,0,0,0,0,128,0,0,0,0,0,0,248,63:negzero=true sub=[object Uint16Array]:Uint16Array:3,4,5,6",
		"Object false [object Object] undefined - detail=[object Error]:stack=undefined,true",
		"Object false [object Object] undefined - b=[object Boolean]:object,false big=[object BigInt]:object,3 n=[object Number]:object,5 s=[object String]:object,x",
		"string  {...}",
		"string  {...}",
		"TypeError true [object Error] string - message=px",
		"string [ 1, 2 ]",
		"Error true [object Error] string TypeError:inner cause=[object Error]:TypeError,inner,code=E_IN,own=code,message,name message=outer name=Error",
		"Error true [object Error] string TypeError:inner cause=[object Error]:TypeError,inner,code=undefined,own=message code=1 message=outer name=Error",
		"symbol Symbol(x) registered=true",
		"Object false [object Object] undefined - a=1",
		"Object false [object Object] undefined - code=E",
		"Error true [object Error] string - message=g name=Error ro=5:writable=false",
		"Object false [object Object] undefined - m=[object Map]:[[1,2]]",
		"RangeError true [object Error] string - code=J message=json name=RangeError",
		"Error true [object Error] string - buf=[object Uint8Array]:Buffer:104,105:isBuffer=true,read=26984 message=sub name=Error",
		"string  {...}",
		"Error cg hasCause=false",
		"number 1",
		"Error true [object Error] string - message=det",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes =\n%s\nwant\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
}

// A worker reports the code its 'exit' listeners chose. A throwing handler is an ordinary
// failure there: the 7 Node gives a whole process does not apply.
func TestWorkerExitCode(t *testing.T) {
	const thrower = "setTimeout(() => { throw new Error('w'); }, 1);"
	const nine = "process.on('exit', () => { process.exitCode = 9; });"
	for name, tc := range map[string]struct {
		source string
		want   []string
	}{
		"plain":                {thrower, []string{"error w", "exit 1"}},
		"main script":          {"throw new Error('w');", []string{"error w", "exit 1"}},
		"exit listener":        {nine + thrower, []string{"error w", "exit 9"}},
		"exit listener, main":  {nine + "throw new Error('w');", []string{"error w", "exit 9"}},
		"exit listener, zero":  {"process.on('exit', () => { process.exitCode = 0; });" + thrower, []string{"error w", "exit 0"}},
		"exit listener, seven": {"process.on('exit', () => { process.exitCode = 7; });" + thrower, []string{"error w", "exit 7"}},
		"handler throws":       {"process.on('uncaughtException', () => { throw new Error('h'); });" + thrower, []string{"error h", "exit 1"}},
		"handled":              {"process.on('uncaughtException', () => {});" + thrower, []string{"exit 0"}},
		"message listener": {nine + "require('worker_threads').parentPort.on('message', () => { throw new Error('w'); });",
			[]string{"error w", "exit 9"}},
	} {
		t.Run(name, func(t *testing.T) {
			source, _ := json.Marshal(tc.source)
			notes, err := runUncaught(t, fmt.Sprintf(`
const { Worker } = require('worker_threads');
const worker = new Worker(%s, { eval: true });
worker.postMessage(1);
worker.on('error', (e) => note('error', e.message.split('\n')[0].replace(/^Error: /, '')));
worker.on('exit', (code) => note('exit', code));
`, source))
			if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			if !reflect.DeepEqual(notes, tc.want) {
				t.Errorf("notes = %q, want %q", notes, tc.want)
			}
		})
	}
}

// A rejection made inside an 'unhandledRejection' listener gets its chance at a handler
// before it counts, as any other does. Reporting it at once also never ended if the listener
// kept doing this.
func TestUnhandledRejectionFromListener(t *testing.T) {
	supportsRejections(t)
	t.Run("handled by a queued reaction", func(t *testing.T) {
		notes, err := runUncaught(t, `
let count = 0;
process.on('unhandledRejection', (r) => {
  note('unhandled', r.message);
  if (++count > 5) throw new Error('runaway');
  const inner = Promise.reject(new Error('inner'));
  Promise.resolve().then(() => inner.catch(() => {}));
});
Promise.reject(new Error('outer'));
`)
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if want := []string{"unhandled outer"}; !reflect.DeepEqual(notes, want) {
			t.Errorf("notes = %q, want %q", notes, want)
		}
	})
	t.Run("handled by a queued tick", func(t *testing.T) {
		notes, err := runUncaught(t, `
let count = 0;
process.on('unhandledRejection', (r) => {
  note('unhandled', r.message);
  if (++count > 5) throw new Error('runaway');
  const inner = Promise.reject(new Error('inner'));
  process.nextTick(() => inner.catch(() => {}));
});
Promise.reject(new Error('outer'));
`)
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
		if want := []string{"unhandled outer"}; !reflect.DeepEqual(notes, want) {
			t.Errorf("notes = %q, want %q", notes, want)
		}
	})
	t.Run("reported after the batch it was made in", func(t *testing.T) {
		notes, err := runUncaught(t, `
process.on('unhandledRejection', (r) => {
  note('unhandled', r.message);
  if (r.message === 'a') Promise.reject(new Error('later'));
});
Promise.reject(new Error('a'));
Promise.reject(new Error('b'));
Promise.resolve().then(() => Promise.reject(new Error('c')));
`)
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
		want := []string{"unhandled a", "unhandled b", "unhandled c", "unhandled later"}
		if !reflect.DeepEqual(notes, want) {
			t.Errorf("notes = %q, want %q", notes, want)
		}
	})
}

// Node stops at a fatal exception: nothing already queued runs, whichever queue it is in.
func TestUncaughtStopsQueuedWork(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('exit', (code) => {
  note('exit', code);
  Promise.resolve().then(() => note('then in exit'));
});
setTimeout(() => {
  Promise.resolve().then(() => note('then'));
  queueMicrotask(() => note('queueMicrotask'));
  process.nextTick(() => note('nextTick'));
  setImmediate(() => note('setImmediate'));
  (async () => { await null; note('await'); })();
  throw new Error('boom');
}, 1);
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("Run returned %v, want an UncaughtError", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

// The same, for the promise work a main script leaves behind when it throws.
func TestUncaughtMainScriptStopsQueuedWork(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('exit', (code) => note('exit', code));
Promise.resolve().then(() => note('then'));
throw new Error('boom');
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("Run returned %v, want an UncaughtError", err)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

// A handler that takes the exception keeps the program going, promises included.
func TestUncaughtHandledKeepsQueuedWork(t *testing.T) {
	notes, err := runUncaught(t, `
process.on('uncaughtException', (e) => note('caught', e.message));
setTimeout(() => {
  Promise.resolve().then(() => note('then'));
  throw new Error('boom');
}, 1);
setTimeout(() => note('later'), 20);
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"caught boom", "then", "later"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

// runModuleUncaught runs source as an ES module, failing if it needs the whole timeout.
func runModuleUncaught(t *testing.T, source string) ([]string, error) {
	t.Helper()
	const timeout = 2 * time.Second
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	prelude := `globalThis.__notes = []; globalThis.note = (...a) => __notes.push(a.join(' '));`
	if _, err := inst.Runtime.RunScript(prelude, "prelude.js"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.ESM.RunModule(source, "/test.mjs")
	if inst.Runtime.IsDone() {
		t.Fatalf("ran out of time, so the exception waited for the interval (error: %v)", runErr)
	}
	val, err := inst.Runtime.RunScript(`JSON.stringify(__notes)`, "notes.js")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	if err := json.Unmarshal([]byte(val.String()), &notes); err != nil {
		t.Fatal(err)
	}
	return notes, runErr
}

// A module's exception reaches the handler while its handles are still open. Node sends it
// straight to 'uncaughtException', so 'unhandledRejection' does not see it.
func TestUncaughtModuleWithActiveHandle(t *testing.T) {
	notes, err := runModuleUncaught(t, `
process.on('uncaughtException', (e, origin) => {
  note('uncaught', e.message, origin);
  clearInterval(timer);
});
process.on('unhandledRejection', (e) => note('unhandledRejection', e.message));
const timer = setInterval(() => {}, 10);
throw new Error('boom');
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"uncaught boom unhandledRejection"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

func TestUncaughtModuleAfterTopLevelAwait(t *testing.T) {
	notes, err := runModuleUncaught(t, `
process.on('uncaughtException', (e, origin) => {
  note('uncaught', e.message, origin);
  clearInterval(timer);
});
const timer = setInterval(() => {}, 10);
await new Promise((resolve) => setTimeout(resolve, 5));
note('resumed');
throw new Error('late');
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"resumed", "uncaught late unhandledRejection"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

func TestUncaughtModuleWithoutHandler(t *testing.T) {
	notes, err := runModuleUncaught(t, `
process.on('exit', (code) => note('exit', code));
setInterval(() => {}, 10);
setTimeout(() => note('timer'), 1);
throw new Error('boom');
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) {
		t.Fatalf("RunModule returned %v, want an UncaughtError", err)
	}
	if uncaught.ExitCode != 1 || uncaught.Origin != "unhandledRejection" {
		t.Errorf("got %+v", uncaught)
	}
	if want := []string{"exit 1"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

// Work the module queued before it threw runs first, and work for later runs after the handler.
func TestUncaughtModuleOrder(t *testing.T) {
	notes, err := runModuleUncaught(t, `
process.on('uncaughtException', (e) => note('uncaught', e.message));
Promise.resolve().then(() => note('then'));
setTimeout(() => note('timer'), 1);
throw new Error('boom');
`)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"then", "uncaught boom", "timer"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("got %q, want %q", notes, want)
	}
}

// A runtime that has ended runs nothing more, whichever way it is asked to.
func TestRunModuleAfterUncaught(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, first := inst.Runtime.Run(`throw new Error('boom')`, "main.js")
	var uncaught *runtime.UncaughtError
	if !errors.As(first, &uncaught) {
		t.Fatalf("Run returned %v, want an UncaughtError", first)
	}
	_, second := inst.ESM.RunModule(`globalThis.after = true;`, "/after.mjs")
	if second != error(uncaught) {
		t.Errorf("RunModule returned %v, want the first error", second)
	}
	val, err := inst.Runtime.GetGlobal("after")
	if err != nil {
		t.Fatal(err)
	}
	if !val.IsUndefined() {
		t.Error("a module ran after the runtime ended")
	}
}

// Float16Array is newer than Node 20, so this is checked without a Node capture: the view
// keeps its type and values, -0 included, as the other typed arrays do.
func TestWorkerErrorKeepsFloat16Array(t *testing.T) {
	notes, err := runUncaught(t, `
if (typeof Float16Array !== 'function') {
  note('no Float16Array');
} else {
  const { Worker } = require('worker_threads');
  const w = new Worker('const e = new Error("f16"); e.h = new Float16Array([1.5, -0]); throw e;', { eval: true });
  w.on('error', (e) => note(Object.prototype.toString.call(e.h), Array.from(e.h).map((x) => Object.is(x, -0) ? '-0' : x).join(',')));
}
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if len(notes) == 1 && notes[0] == "no Float16Array" {
		t.Skip("this V8 has no Float16Array")
	}
	if want := []string{"[object Float16Array] 1.5,-0"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func runUncaughtWith(t *testing.T, cfg *runtime.Config, source string) ([]string, error) {
	t.Helper()
	cfg.EnableConsole, cfg.EnableTimers, cfg.Timeout = true, true, 20*time.Second
	inst, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	prelude := `globalThis.__notes = []; globalThis.note = (...a) => __notes.push(a.join(' '));`
	if _, err := inst.Runtime.RunScript(prelude, "prelude.js"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run(source, "test.js")
	if inst.Runtime.IsKilled() {
		t.Fatalf("killed: %s", inst.Runtime.KillReason())
	}
	val, err := inst.Runtime.RunScript(`JSON.stringify(__notes)`, "notes.js")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	if err := json.Unmarshal([]byte(val.String()), &notes); err != nil {
		t.Fatal(err)
	}
	return notes, runErr
}

func TestUnhandledRejectionModes(t *testing.T) {
	supportsRejections(t)
	const noListeners = `
process.on('warning', (w) => note('warning', w.name, w.message.split('\n')[0].slice(0, 40), w.id === undefined ? '-' : w.id));
const p = Promise.reject(new Error('x'));
const q = Promise.reject(42);
setTimeout(() => { p.catch(() => {}); note('late catch'); }, 10);
setTimeout(() => note('still running', process.exitCode), 20);
`
	const listeners = `
process.on('warning', (w) => note('warning', w.name));
process.on('unhandledRejection', (r) => note('unhandled', String(r)));
process.on('uncaughtException', (e, o) => note('uncaught', e.message, o));
process.on('rejectionHandled', () => note('rejectionHandled'));
const p = Promise.reject(new Error('x'));
setTimeout(() => p.catch(() => {}), 10);
setTimeout(() => note('still running', process.exitCode), 20);
`
	unhandled := "warning UnhandledPromiseRejectionWarning Unhandled promise rejection. This error  -"
	handledLate := "warning PromiseRejectionHandledWarning Promise rejection was handled asynchrono 1"
	for _, tc := range []struct {
		mode, script string
		fatal        bool
		want         []string
	}{
		{"", noListeners, true, []string{}},
		{"throw", noListeners, true, []string{}},
		{"strict", noListeners, true, []string{}},
		{"warn", noListeners, false, []string{"warning UnhandledPromiseRejectionWarning Error: x -", unhandled,
			"warning UnhandledPromiseRejectionWarning 42 -", unhandled, "late catch", handledLate, "still running "}},
		{"none", noListeners, false, []string{"late catch", handledLate, "still running "}},
		{"warn-with-error-code", noListeners, false, []string{"warning UnhandledPromiseRejectionWarning Error: x -", unhandled,
			"warning UnhandledPromiseRejectionWarning 42 -", unhandled, "late catch", handledLate, "still running 1"}},
		{"throw", listeners, false, []string{"unhandled Error: x", "rejectionHandled", "still running "}},
		{"strict", listeners, false, []string{"uncaught x unhandledRejection", "unhandled Error: x", "rejectionHandled", "still running "}},
		{"warn", listeners, false, []string{"unhandled Error: x", "warning UnhandledPromiseRejectionWarning",
			"warning UnhandledPromiseRejectionWarning", "rejectionHandled", "still running "}},
		{"none", listeners, false, []string{"unhandled Error: x", "rejectionHandled", "still running "}},
		{"warn-with-error-code", listeners, false, []string{"unhandled Error: x", "rejectionHandled", "still running "}},
	} {
		name := tc.mode
		if tc.script == listeners {
			name += " with listeners"
		}
		t.Run(name, func(t *testing.T) {
			notes, err := runUncaughtWith(t, &runtime.Config{UnhandledRejections: tc.mode, NoWarnings: true}, tc.script)
			var uncaught *runtime.UncaughtError
			if tc.fatal {
				if !errors.As(err, &uncaught) || uncaught.Origin != "unhandledRejection" || !strings.Contains(uncaught.Message, "Error: x") {
					t.Fatalf("Run returned %v, want the rejection as an uncaught error", err)
				}
			} else if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			if !reflect.DeepEqual(notes, tc.want) {
				t.Errorf("notes = %q\nwant %q", notes, tc.want)
			}
		})
	}
	if _, err := New(&runtime.Config{UnhandledRejections: "sometimes"}); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestProcessEmitWarning(t *testing.T) {
	notes, err := runUncaughtWith(t, &runtime.Config{NoWarnings: true}, `
process.on('warning', (w) => note(w instanceof Error, w.name, w.message, w.code, w.detail));
process.emitWarning('hello');
process.emitWarning(new RangeError('as error'));
process.emitWarning('dep', 'DeprecationWarning', 'DEP0001');
process.emitWarning('d', { type: 'Custom', code: 'C1', detail: 'more' });
for (const bad of [[5], ['x', 5], ['x', 'T', 5]]) {
  try { process.emitWarning(...bad); } catch (e) { note(e.code, e.message); }
}
note('sync done', process.listenerCount('warning'));
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{
		`ERR_INVALID_ARG_TYPE The "warning" argument must be of type string or an instance of Error. Received type number (5)`,
		`ERR_INVALID_ARG_TYPE The "type" argument must be of type string. Received type number (5)`,
		`ERR_INVALID_ARG_TYPE The "code" argument must be of type string. Received type number (5)`,
		"sync done 1",
		"true Warning hello  ",
		"true RangeError as error  ",
		"true DeprecationWarning dep DEP0001 ",
		"true Custom d C1 more",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q\nwant %q", notes, want)
	}
}

// Warnings survive a read-only Error.stackTraceLimit, a rejection with a proxy whose
// prototype chain never ends, and process.throwDeprecation, which throws a tick later as
// in Node. Expected values are Node 20.18.1's.
func TestWarningsWithHostileValues(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaughtWith(t, &runtime.Config{UnhandledRejections: "warn", NoWarnings: true}, `
process.on('warning', (w) => note('warning', w.name, w.message.split('\n')[0].slice(0, 30)));
process.on('uncaughtException', (e) => note('uncaught', e.name, e.message));
Object.defineProperty(Error, 'stackTraceLimit', { value: 10, writable: false, configurable: true });
process.emitWarning('frozen limit');
const p2 = new Proxy(Object.create(null), { getPrototypeOf() { return p2; } });
Promise.reject(p2);
process.throwDeprecation = true;
try { process.emitWarning('old', 'DeprecationWarning'); note('sync after deprecation'); } catch (e) { note('caught sync', e.name); }
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{
		"sync after deprecation",
		"warning Warning frozen limit",
		"uncaught DeprecationWarning old",
		"warning UnhandledPromiseRejectionWarning [object Object]",
		"warning UnhandledPromiseRejectionWarning Unhandled promise rejection. T",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q\nwant %q", notes, want)
	}
}

// A worker that runs out of work emits 'exit' and exits with process.exitCode, which
// --unhandled-rejections=warn-with-error-code sets. Expected values are Node 20.18.1's.
func TestWorkerExitCodeFromProcessExitCode(t *testing.T) {
	supportsRejections(t)
	notes, err := runUncaughtWith(t, &runtime.Config{UnhandledRejections: "warn-with-error-code", NoWarnings: true}, `
const { Worker } = require('worker_threads');
const run = (src) => new Promise((resolve) => new Worker(src, { eval: true }).on('exit', resolve));
(async () => {
  note('set', await run('process.exitCode = 3'));
  note('string', await run('process.exitCode = "7"'));
  note('rejection', await run('Promise.reject(1)'));
  note('listener', await run('process.on("exit", (c) => { process.exitCode = c + 5; }); process.exitCode = 1'));
  for (const src of ['process.exitCode = 3; process.on("exit", () => { throw new TypeError("in exit"); })',
    'process.on("exit", () => { throw new TypeError("in exit"); })']) {
    const w = new Worker(src, { eval: true });
    w.on('error', (e) => note('error', String(e.message).includes('in exit')));
    note('throwing listener', await new Promise((resolve) => w.on('exit', resolve)));
  }
})();
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"set 3", "string 7", "rejection 1", "listener 6",
		"error true", "throwing listener 3", "error true", "throwing listener 0"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}
