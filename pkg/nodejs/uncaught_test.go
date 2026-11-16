package nodejs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"proto.zip/studio/orbital/internal/v8dist"
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
    'process.on("exit", () => { throw new TypeError("in exit"); })',
    'process.exitCode = 4; process.on("exit", () => { throw ""; })']) {
    const w = new Worker(src, { eval: true });
    w.on('error', (e) => note('error', String(e && e.message !== undefined ? e.message : e).includes('in exit')));
    note('throwing listener', await new Promise((resolve) => w.on('exit', resolve)));
  }
})();
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	want := []string{"set 3", "string 7", "rejection 1", "listener 6",
		"error true", "throwing listener 3", "error true", "throwing listener 0", "error false", "throwing listener 4"}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

// A rejection that belongs to a domain goes through the --unhandled-rejections mode too,
// with the domain's 'error' in place of 'unhandledRejection'. Expected values are what
// Node 20.18.1 does with each mode.
func TestUnhandledRejectionModesWithDomain(t *testing.T) {
	supportsRejections(t)
	const script = `
const domain = require('domain');
process.on('warning', (w) => note('warning', w.name));
process.on('uncaughtException', (e, o) => note('uncaught', e.message, o));
const d = domain.create();
d.on('error', (e) => note('domain error', e.message));
d.run(() => { Promise.reject(new Error('in domain')); });
setTimeout(() => note('still running'), 20);
`
	for mode, want := range map[string][]string{
		"throw":                {"domain error in domain", "still running"},
		"strict":               {"uncaught in domain unhandledRejection", "domain error in domain", "still running"},
		"warn":                 {"domain error in domain", "warning UnhandledPromiseRejectionWarning", "warning UnhandledPromiseRejectionWarning", "still running"},
		"none":                 {"domain error in domain", "still running"},
		"warn-with-error-code": {"domain error in domain", "still running"},
	} {
		t.Run(mode, func(t *testing.T) {
			notes, err := runUncaughtWith(t, &runtime.Config{UnhandledRejections: mode, NoWarnings: true}, script)
			if err != nil {
				t.Fatalf("Run returned %v", err)
			}
			if !reflect.DeepEqual(notes, want) {
				t.Errorf("notes = %q, want %q", notes, want)
			}
		})
	}
}

// An exception from a worker's 'exit' listener, once it ran out of work, reaches the
// parent as the value thrown, as in Node 20.18.1.
func TestWorkerExitListenerThrowKeepsTheValue(t *testing.T) {
	notes, err := runUncaught(t, `
const { Worker } = require('worker_threads');
const w = new Worker("process.on('exit', () => { const e = new TypeError('x'); e.code = 'E_X'; throw e; });", { eval: true });
w.on('error', (e) => note('error', e.constructor.name, e.message, e.code));
w.on('exit', (c) => note('exit', c));
`)
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if want := []string{"error TypeError x E_X", "exit 0"}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
}

func TestUncaughtSourceLine(t *testing.T) {
	for name, tc := range map[string]struct {
		main, dep string
		dir       string // a subdirectory to put the files in
		file      string // the file the header names, "" for none
		line      int
		caret     string // what comes before the carets
		carets    int    // how many carets, 1 if 0
		inline    bool   // a thrown value that is not an error: Node prints its header inline
	}{
		"thrown in a function": {main: "function f(){ throw new Error('boom'); }\nf();\n", file: "main.js", line: 1, caret: strings.Repeat(" ", 14)},
		"made by V8":           {main: "const x = 1;\n  null.foo;\n", file: "main.js", line: 2, caret: strings.Repeat(" ", 7)},
		"in a timer":           {main: "setTimeout(() => {\n    throw new TypeError('in timer');\n}, 1);\n", file: "main.js", line: 2, caret: "    "},
		"tab indent":           {main: "if (true) {\n\tthrow new Error('tab');\n}\n", file: "main.js", line: 2, caret: "\t"},
		"rejection":            {main: "Promise.reject(new Error('rej'));\n", file: "main.js", line: 1, caret: strings.Repeat(" ", 15)},
		"async function":       {main: "async function f() {\n  await null;\n  throw new Error('async');\n}\nf();\n", file: "main.js", line: 3, caret: strings.Repeat(" ", 8)},
		"first line of a required module": {
			main: "const d = require(DEP);\nsetTimeout(d, 1);\n",
			dep:  "module.exports = () => { throw new RangeError('dep'); };\n",
			file: "dep.js", line: 1, caret: strings.Repeat(" ", 25),
		},
		// Expected values below are what Node 20.18.1 prints for the same files.
		"thrown by a built-in": {main: "setTimeout(() => {\n  decodeURIComponent('%');\n}, 1);\n", file: "main.js", line: 2, caret: "  "},
		"not an error":         {main: "throw 'str';\n", file: "main.js", line: 1, inline: true},
		"object thrown":        {main: "const o = 1;\n  throw {a: 1};\n", file: "main.js", line: 2, caret: "  ", inline: true},
		"null thrown":          {main: "throw null;\n", file: "main.js", line: 1, inline: true},
		"non-error after tab":  {main: "\tthrow 'tab';\n", file: "main.js", line: 1, caret: "\t", inline: true},
		"non-error in a required module": {
			main: "require(DEP);\n",
			dep:  "  throw 'dep';\n",
			file: "dep.js", line: 1, caret: "  ", inline: true,
		},
		"syntax error":              {main: "let x = ;\n", file: "main.js", line: 1, caret: strings.Repeat(" ", 8)},
		"syntax error over a token": {main: "let x = foo barbaz;\n", file: "main.js", line: 1, caret: strings.Repeat(" ", 12), carets: 6},
		"globals replaced before the throw": {
			main: "globalThis.String = () => ''; globalThis.require = null; globalThis.Error = null; throw 'boom';\n",
			file: "main.js", line: 1, caret: strings.Repeat(" ", 82), inline: true,
		},
		"syntax error on a later line": {main: "const a = 1;\nconst a = 2;\n", file: "main.js", line: 2, caret: strings.Repeat(" ", 6)},
		"module with a BOM": {
			main: "require(DEP);\n",
			dep:  "\uFEFFthrow new Error('bom');\n",
			file: "dep.js", line: 1, caret: " ",
		},
		"other line terminators": {main: "const a = 1;\rconst b = 2;\u2028const c = 3;\u2029  throw new Error('cr');\r\n", file: "main.js", line: 4, caret: "  "},
		"parentheses in the path": {
			dir:  "app (copy)",
			main: "function f() { throw new Error('paren'); }\nf();\n", file: "main.js", line: 1, caret: strings.Repeat(" ", 15),
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.dir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			mainPath := filepath.Join(dir, "main.js")
			main := strings.ReplaceAll(tc.main, "DEP", strconv.Quote(filepath.Join(dir, "dep.js")))
			files := map[string]string{"main.js": main}
			if tc.dep != "" {
				files["dep.js"] = tc.dep
			}
			for f, text := range files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Runtime.Dispose()
			_, runErr := inst.Runtime.Run(main, mainPath)
			var uncaught *runtime.UncaughtError
			if !errors.As(runErr, &uncaught) {
				t.Fatalf("Run returned %v", runErr)
			}
			want := ""
			if tc.file != "" {
				text := files[tc.file]
				line := regexp.MustCompile("\r\n|[\n\r\u2028\u2029]").Split(text, -1)[tc.line-1]
				carets := tc.carets
				if carets == 0 {
					carets = 1
				}
				want = fmt.Sprintf("%s:%d\n%s\n%s%s\n", filepath.Join(dir, tc.file), tc.line, line, tc.caret, strings.Repeat("^", carets))
			}
			if uncaught.Source != want {
				t.Errorf("Source = %q, want %q", uncaught.Source, want)
			}
			report := uncaught.Report()
			wantReport := uncaught.Message + "\n\nOrbital " + v8dist.ModuleVersion + "\n"
			if tc.inline {
				wantReport = "\n" + want + wantReport
			} else if want != "" {
				wantReport = want + "\n" + wantReport
			}
			if report != wantReport {
				t.Errorf("Report() = %q, want %q", report, wantReport)
			}
		})
	}
}

func TestUncaughtReportsWithBrokenProcess(t *testing.T) {
	_, err := runUncaught(t, `
process.on('exit', () => {
  Object.defineProperty(globalThis, 'process', { get() { throw new Error('no process'); }, configurable: true });
});
throw new Error('original');
`)
	var uncaught *runtime.UncaughtError
	if !errors.As(err, &uncaught) || !strings.Contains(uncaught.Message, "Error: original") {
		t.Fatalf("Run returned %v, want the original uncaught error", err)
	}
}

func TestUncaughtKeepsRecentScriptSources(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	for i := 0; i < 40; i++ {
		src := fmt.Sprintf("globalThis.f%d = () => { throw new Error('from %d'); };\n", i, i)
		if _, err := inst.Runtime.Run(src, fmt.Sprintf("[script %d]", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => f39(), 1);\n", "[last]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := "[script 39]:1\nglobalThis.f39 = () => { throw new Error('from 39'); };\n" + strings.Repeat(" ", 25) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}

	inst2, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst2.Runtime.Dispose()
	for i := 0; i < 40; i++ {
		src := fmt.Sprintf("globalThis.f%d = () => { throw new Error('from %d'); };\n", i, i)
		if _, err := inst2.Runtime.Run(src, fmt.Sprintf("[script %d]", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, runErr = inst2.Runtime.Run("setTimeout(() => f0(), 1);\n", "[last]")
	uncaught = nil
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if uncaught.Source != "" {
		t.Errorf("the oldest script's header = %q, want none", uncaught.Source)
	}
}

func TestUncaughtSourceIgnoresFilesAStackNames(t *testing.T) {
	other := filepath.Join(t.TempDir(), "other.txt")
	if err := os.WriteFile(other, []byte("not a script\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, runErr := inst.Runtime.Run(fmt.Sprintf("Promise.reject({ stack: 'x\\n    at ' + %q + ':1:1' });\n", other), "main.js")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if uncaught.Source != "" {
		t.Errorf("Source = %q, want none", uncaught.Source)
	}
}

func TestUncaughtSourceForModuleAsCompiled(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.mjs")
	// The module rewrites its own file once it has been compiled.
	src := fmt.Sprintf("setTimeout(() => {\n  throw new Error('compiled');\n}, 1);\nimport fs from 'fs'; fs.writeFileSync(%q, '// changed\\n// on disk\\n');\n", entry)
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, runErr := inst.ESM.RunModuleFile(entry)
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("RunModuleFile returned %v", runErr)
	}
	if !strings.Contains(uncaught.Source, "\n  throw new Error('compiled');\n  ^\n") {
		t.Errorf("Source = %q, want the compiled line", uncaught.Source)
	}
}

func TestUncaughtSourceForReusedName(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run("globalThis.first = () => { throw new Error('first'); };\n", "[same]"); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Runtime.Run("globalThis.second = 2;\n", "[same]"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => first(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if uncaught.Source != "" {
		t.Errorf("Source = %q, want none", uncaught.Source)
	}
}

// A module that throws is dropped from the cache even if it set module.loaded itself, so
// the next require runs it again, as in Node; a JSON module stays cached.
func TestRequire_RetriesAModuleThatThrew(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m.js"), []byte("globalThis.n = (globalThis.n || 0) + 1; module.loaded = true;\nif (globalThis.n === 1) throw new Error('first');\nmodule.exports = 'ok';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "j.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const m = %q, j = %q;
let first;
try { require(m); } catch (e) { first = e.message; }
done({ first, second: require(m), runs: globalThis.n, jsonCached: require(j) === require(j) });`,
		filepath.Join(dir, "m.js"), filepath.Join(dir, "j.json")))
	wantJSON(t, got, map[string]any{"first": "first", "second": "ok", "runs": float64(2), "jsonCached": true})
}

// An ES module run from memory, under a name with no file, still gets its header. As in
// Node, an ES module's top-level exception is marked where the error was made.
func TestUncaughtSourceForInMemoryModule(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	name := filepath.Join(t.TempDir(), "virtual.mjs")
	_, runErr := inst.ESM.RunModule("const a = 1;\nthrow new Error('virtual');\n", name)
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("RunModule returned %v", runErr)
	}
	if want := name + ":2\nthrow new Error('virtual');\n      ^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// A script run under a name a CommonJS module was required by isn't shifted by the
// module's wrapper.
func TestUncaughtSourceAfterRequireUnderTheSameName(t *testing.T) {
	dir := t.TempDir()
	dep := filepath.Join(dir, "dep.js")
	const text = "globalThis.boom = () => { throw new Error('same name'); };\n"
	if err := os.WriteFile(dep, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run(fmt.Sprintf("require(%q);\n", dep), filepath.Join(dir, "main.js")); err != nil {
		t.Fatal(err)
	}
	// The same text, run unwrapped under the module's name: its columns have no offset.
	if _, err := inst.Runtime.Run(text, dep); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => boom(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := dep + ":1\n" + strings.TrimSuffix(text, "\n") + "\n" + strings.Repeat(" ", 26) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// A module compiled by eval, because its source holds a NUL, has its wrapper's opening
// taken off its first line too, and the line ends at the NUL, as Node 20.18.1 prints it.
func TestUncaughtSourceForModuleWithNUL(t *testing.T) {
	dir := t.TempDir()
	dep := filepath.Join(dir, "nul.js")
	if err := os.WriteFile(dep, []byte("/*\x00*/ throw new Error('boom');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	_, runErr := inst.Runtime.Run(fmt.Sprintf("require(%q);\n", dep), filepath.Join(dir, "main.js"))
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := dep + ":1\n/*\n  ^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

func TestUncaughtSourceForModuleChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	dep := filepath.Join(dir, "dep.js")
	if err := os.WriteFile(dep, []byte("module.exports = () => { throw new Error('compiled'); };\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	script := fmt.Sprintf("const f = require(%q);\nrequire('fs').writeFileSync(%q, '// changed\\n');\nsetTimeout(f, 1);\n", dep, dep)
	_, runErr := inst.Runtime.Run(script, filepath.Join(dir, "main.js"))
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := dep + ":1\nmodule.exports = () => { throw new Error('compiled'); };\n" + strings.Repeat(" ", 25) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// A module compiled through GetModuleNamespace keeps its text as RunModule's does.
func TestUncaughtSourceForModuleNamespace(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	name := filepath.Join(t.TempDir(), "ns.mjs")
	_, nsErr := inst.ESM.GetModuleNamespace("setTimeout(() => {\n  throw new Error('ns');\n}, 1);\nexport const a = 1;\n", name)
	var uncaught *runtime.UncaughtError
	if !errors.As(nsErr, &uncaught) {
		t.Fatalf("GetModuleNamespace returned %v", nsErr)
	}
	if want := name + ":2\n  throw new Error('ns');\n  ^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// RunModule run twice under one name with different text keeps none: functions from the
// first module can still throw, and the header is left out rather than misplaced.
func TestUncaughtSourceForReusedModuleName(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	name := filepath.Join(t.TempDir(), "same.mjs")
	if _, err := inst.ESM.RunModule("globalThis.first = () => { throw new Error('first'); };\n", name); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.ESM.RunModule("setTimeout(() => first(), 1);\n", name)
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("RunModule returned %v", runErr)
	}
	if uncaught.Source != "" {
		t.Errorf("Source = %q, want none", uncaught.Source)
	}
}

func TestUncaughtSourceForReusedNameAfterEviction(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run("globalThis.kept = () => { throw new Error('first'); };\n", "reused.js"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := inst.Runtime.Run("void 0;\n", fmt.Sprintf("[filler %d]", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := inst.Runtime.Run("// only a comment\n", "reused.js"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => kept(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if uncaught.Source != "" {
		t.Errorf("Source = %q, want none", uncaught.Source)
	}
}

func TestUncaughtSourceForSourceURLScript(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run("globalThis.later = () => { throw new Error('x'); };\n//# sourceURL=virtual.js\n", "[prelude]"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => later(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := "virtual.js:1\nglobalThis.later = () => { throw new Error('x'); };\n" + strings.Repeat(" ", 27) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

func TestUncaughtSourceForDirectModules(t *testing.T) {
	for name, run := range map[string]func(inst *Instance, src, file string) error{
		"RunModule": func(inst *Instance, src, file string) error {
			_, err := inst.ESM.RunModule(src, file)
			return err
		},
		"GetModuleNamespace": func(inst *Instance, src, file string) error {
			_, err := inst.ESM.GetModuleNamespace(src, file)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Runtime.Dispose()
			file := filepath.Join(t.TempDir(), "virtual.mjs")
			runErr := run(inst, "setTimeout(() => {\n  throw new Error('direct');\n}, 1);\nexport const a = 1;\n", file)
			var uncaught *runtime.UncaughtError
			if !errors.As(runErr, &uncaught) {
				t.Fatalf("returned %v", runErr)
			}
			if want := file + ":2\n  throw new Error('direct');\n  ^\n"; uncaught.Source != want {
				t.Errorf("Source = %q, want %q", uncaught.Source, want)
			}
		})
	}
}

func TestModuleLoaderNativesAreNotGlobals(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `done({ compile: typeof __compileModuleWrapper, keep: typeof __moduleWrapperCompiled, req: typeof require('path').join });`)
	wantJSON(t, got, map[string]any{"compile": "undefined", "keep": "undefined", "req": "function"})
}

func TestUncaughtSourceAfterFailedCompile(t *testing.T) {
	t.Run("require", func(t *testing.T) {
		dir := t.TempDir()
		dep := filepath.Join(dir, "dep.js")
		if err := os.WriteFile(dep, []byte("let x = ;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		script := fmt.Sprintf(`try { require(%q); } catch (e) {}
require('fs').writeFileSync(%q, "module.exports = () => { throw new Error('fixed'); };\n");
setTimeout(require(%q), 1);
`, dep, dep, dep)
		_, runErr := inst.Runtime.Run(script, filepath.Join(dir, "main.js"))
		var uncaught *runtime.UncaughtError
		if !errors.As(runErr, &uncaught) {
			t.Fatalf("Run returned %v", runErr)
		}
		if want := dep + ":1\nmodule.exports = () => { throw new Error('fixed'); };\n" + strings.Repeat(" ", 25) + "^\n"; uncaught.Source != want {
			t.Errorf("Source = %q, want %q", uncaught.Source, want)
		}
	})
	t.Run("RunModule", func(t *testing.T) {
		inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		name := filepath.Join(t.TempDir(), "mod.mjs")
		if _, err := inst.ESM.RunModule("let x = ;\n", name); err == nil {
			t.Fatal("the invalid module compiled")
		}
		_, runErr := inst.ESM.RunModule("setTimeout(() => {\n  throw new Error('fixed');\n}, 1);\n", name)
		var uncaught *runtime.UncaughtError
		if !errors.As(runErr, &uncaught) {
			t.Fatalf("RunModule returned %v", runErr)
		}
		if want := name + ":2\n  throw new Error('fixed');\n  ^\n"; uncaught.Source != want {
			t.Errorf("Source = %q, want %q", uncaught.Source, want)
		}
	})
}

func TestUncaughtSourceForSourceURLForms(t *testing.T) {
	t.Run("tab", func(t *testing.T) {
		inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		if _, err := inst.Runtime.Run("globalThis.later = () => { throw new Error('x'); };\n//#\tsourceURL=tabbed.js\n", "[prelude]"); err != nil {
			t.Fatal(err)
		}
		_, runErr := inst.Runtime.Run("setTimeout(() => later(), 1);\n", "[main]")
		var uncaught *runtime.UncaughtError
		if !errors.As(runErr, &uncaught) {
			t.Fatalf("Run returned %v", runErr)
		}
		if want := "tabbed.js:1\nglobalThis.later = () => { throw new Error('x'); };\n" + strings.Repeat(" ", 27) + "^\n"; uncaught.Source != want {
			t.Errorf("Source = %q, want %q", uncaught.Source, want)
		}
	})
	t.Run("module", func(t *testing.T) {
		inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		name := filepath.Join(t.TempDir(), "mod.mjs")
		_, runErr := inst.ESM.RunModule("setTimeout(() => {\n  throw new Error('url');\n}, 1);\n//# sourceURL=virtual.mjs\n", name)
		var uncaught *runtime.UncaughtError
		if !errors.As(runErr, &uncaught) {
			t.Fatalf("RunModule returned %v", runErr)
		}
		if want := "virtual.mjs:2\n  throw new Error('url');\n  ^\n"; uncaught.Source != want {
			t.Errorf("Source = %q, want %q", uncaught.Source, want)
		}
	})
}

func TestUncaughtSourceWithReplacedStringMethods(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	script := "for (const m of ['split', 'slice', 'indexOf', 'startsWith', 'endsWith', 'search', 'charCodeAt']) String.prototype[m] = () => { throw new Error('replaced'); };\n" +
		"RegExp.prototype.exec = () => null;\n" +
		"setTimeout(() => {\n  throw new Error('late');\n}, 1);\n"
	_, runErr := inst.Runtime.Run(script, "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := "[main]:4\n  throw new Error('late');\n  ^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

func TestUncaughtSourceForSourceURLFollowedByText(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	if _, err := inst.Runtime.Run("globalThis.later = () => { throw new Error('x'); };\n//# sourceURL=named.js\n// sourceURL=ignored\n", "[prelude]"); err != nil {
		t.Fatal(err)
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => later(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := "named.js:1\nglobalThis.later = () => { throw new Error('x'); };\n" + strings.Repeat(" ", 27) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

func TestUncaughtSourceWithLiteralFFFF(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.js")
	withNul := filepath.Join(dir, "nul.js")
	files := map[string]string{
		plain:   "/*￿*/ module.exports = () => { throw new Error('plain'); };\n",
		withNul: "/*￿*/ /*\x00*/ module.exports = () => { throw new Error('nul'); };\n",
	}
	for f, text := range files {
		if err := os.WriteFile(f, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct{ file, want string }{
		"no NUL": {plain, plain + ":1\n/*￿*/ module.exports = () => { throw new Error('plain'); };\n" + strings.Repeat(" ", 31) + "^\n"},
		// Node's caret after a NUL counts UTF-8 bytes; only the line is compared.
		"NUL too": {withNul, withNul + ":1\n/*\uFFFF*/ /*\n"},
	} {
		t.Run(name, func(t *testing.T) {
			inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Runtime.Dispose()
			_, runErr := inst.Runtime.Run(fmt.Sprintf("setTimeout(require(%q), 1);\n", tc.file), filepath.Join(dir, "main.js"))
			var uncaught *runtime.UncaughtError
			if !errors.As(runErr, &uncaught) {
				t.Fatalf("Run returned %v", runErr)
			}
			if !strings.HasPrefix(uncaught.Source, tc.want) {
				t.Errorf("Source = %q, want it to start %q", uncaught.Source, tc.want)
			}
		})
	}
}

func TestUncaughtSourceForScriptThenModule(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	name := filepath.Join(t.TempDir(), "both.mjs")
	const text = "globalThis.later = () => { throw new Error('both'); };\n"
	if _, err := inst.Runtime.Run(text, name); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.ESM.RunModule(text, name); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := inst.Runtime.Run("void 0;\n", fmt.Sprintf("[filler %d]", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => later(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := name + ":1\n" + strings.TrimSuffix(text, "\n") + "\n" + strings.Repeat(" ", 27) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

func TestUncaughtSourceURLAgesWithItsScript(t *testing.T) {
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	for i := 0; i < 12; i++ {
		src := fmt.Sprintf("globalThis.f%d = () => { throw new Error('x'); };\n//# sourceURL=url%d.js\n", i, i)
		if _, err := inst.Runtime.Run(src, fmt.Sprintf("[script %d]", i)); err != nil {
			t.Fatal(err)
		}
	}
	_, runErr := inst.Runtime.Run("setTimeout(() => f0(), 1);\n", "[main]")
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := "url0.js:1\nglobalThis.f0 = () => { throw new Error('x'); };\n" + strings.Repeat(" ", 24) + "^\n"; uncaught.Source != want {
		t.Errorf("Source = %q, want %q", uncaught.Source, want)
	}
}

// The text kept for a module with a NUL character is built with the string methods as
// they were before any script ran.
func TestUncaughtSourceForNulModuleWithReplacedStringMethods(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "nul.js")
	if err := os.WriteFile(file, []byte("/*\x00*/ module.exports = () => { throw new Error('nul'); };\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, err := New(&runtime.Config{EnableConsole: true, EnableTimers: true, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	script := "String.fromCharCode = () => 'mark';\n" +
		"String.prototype.replace = () => 'x';\n" +
		"String.prototype.indexOf = () => -1;\n" +
		fmt.Sprintf("setTimeout(require(%q), 1);\n", file)
	_, runErr := inst.Runtime.Run(script, filepath.Join(dir, "main.js"))
	var uncaught *runtime.UncaughtError
	if !errors.As(runErr, &uncaught) {
		t.Fatalf("Run returned %v", runErr)
	}
	if want := file + ":1\n/*\n"; !strings.HasPrefix(uncaught.Source, want) {
		t.Errorf("Source = %q, want it to start %q", uncaught.Source, want)
	}
}
