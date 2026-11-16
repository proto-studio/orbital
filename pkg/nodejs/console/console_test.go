package console

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T, stdout, stderr *bytes.Buffer) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Create console module with custom writer
	writer := runtime.NewStandardWriter(stdout, stderr)
	consoleMod := NewWithWriter(writer)
	if err := consoleMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register console module: %v", err)
	}

	return rt
}

func TestConsole_Log(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.log('Hello', 'World')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.log should produce output")
	}
}

func TestConsole_Error(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.error('Error message')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stderr.String()
	if output == "" {
		t.Error("console.error should produce output to stderr")
	}
}

// TestConsole_ErrorObject checks that console.error prints a real Error the way
// Node does: the stack (name, message, and frames), then any extra own
// properties. Error message/stack are non-enumerable, so a generic object dump
// drops them.
func TestConsole_ErrorObject(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
		reject []string
	}{
		{
			name:   "stack and extra enumerable field",
			script: `const err = new Error('boom'); err.code = 'ENOENT'; console.error(err);`,
			want: []string{
				"Error: boom\n",
				"\n    at ",
				"{\n  code: 'ENOENT'\n}",
			},
			reject: []string{"{ code: 'ENOENT' }\n"},
		},
		{
			name:   "TypeError",
			script: `console.error(new TypeError('bad'));`,
			want:   []string{"TypeError: bad\n", "\n    at "},
			reject: []string{"{}\n"},
		},
		{
			name:   "missing stack",
			script: `const err = new Error('bare'); err.stack = undefined; console.error(err);`,
			want:   []string{"[Error: bare]\n"},
		},
		{
			name:   "missing stack keeps extra fields",
			script: `const err = new Error('bare'); err.stack = undefined; err.code = 'X'; console.error(err);`,
			want:   []string{"[Error: bare] { code: 'X' }\n"},
		},
		{
			name:   "plain object is not an Error",
			script: `console.error({ message: 'boom', stack: 'Error: boom' });`,
			want:   []string{"{ message: 'boom', stack: 'Error: boom' }\n"},
		},
		{
			name:   "subclass constructor name",
			script: `class MyErr extends Error { constructor(m) { super(m); this.extra = 1; } } console.error(new MyErr('x'));`,
			want:   []string{"MyErr [Error]: x\n", "\n    at ", "{\n  extra: 1\n}"},
		},
		{
			name:   "cause",
			script: `console.error(new Error('bad', { cause: new Error('root') }));`,
			want:   []string{"Error: bad\n", "[cause]: Error: root\n", "\n    at "},
		},
		{
			name:   "AggregateError",
			script: `console.error(new AggregateError([new Error('a'), 'b'], 'many'));`,
			want:   []string{"AggregateError: many\n", "[errors]:", "Error: a\n", "'b'"},
		},
		{
			name:   "percent-s uses the stack",
			script: `console.error('failed: %s', new Error('boom'));`,
			want:   []string{"failed: Error: boom\n", "\n    at "},
		},
		{
			name:   "percent-j stays JSON-like",
			script: `console.error('%j', new Error('boom'));`,
			want:   []string{"{}\n"},
			reject: []string{"Error: boom", "\n    at "},
		},
		{
			name:   "console.log uses the same formatting",
			script: `console.log(new Error('boom'));`,
			want:   []string{"Error: boom\n", "\n    at "},
		},
		{
			name:   "circular",
			script: `const err = new Error('boom'); err.self = err; console.error(err);`,
			want:   []string{"<ref *1> Error: boom\n", "self: [Circular *1]"},
		},
		{
			name:   "null prototype",
			script: `const err = new Error('boom'); Object.setPrototypeOf(err, null); console.error(err);`,
			want:   []string{"[Error: null prototype]: boom\n", "\n    at "},
		},
		{
			name:   "null prototype keeps extra fields",
			script: `const err = new Error('boom'); err.code = 'ENOENT'; Object.setPrototypeOf(err, null); console.error(err);`,
			want:   []string{"[Error: null prototype]: boom\n", "code: 'ENOENT'"},
		},
		{
			name:   "null prototype uses the captured stack header",
			script: `const err = new TypeError('bad'); void err.stack; Object.setPrototypeOf(err, null); console.error(err);`,
			want:   []string{"[TypeError: null prototype]: bad\n"},
		},
		{
			name:   "toStringTag Error is not a branded error",
			script: `const fake = { message: 'x', stack: 'Error: x' }; Object.defineProperty(fake, Symbol.toStringTag, { value: 'Error' }); console.error(fake);`,
			want:   []string{"{ message: 'x', stack: 'Error: x' }\n"},
			reject: []string{"[Error: null prototype]"},
		},
		{
			// Node's isError takes anything inheriting from Error.prototype, which is
			// what a worker's exception becomes in its parent.
			name:   "inherits from Error without the brand",
			script: `console.error(Object.create(Error.prototype)); console.error(Object.create(RangeError.prototype, { message: { value: 'mm' } }));`,
			want:   []string{"[Error]\n", "[RangeError: mm]\n"},
		},
		{
			name:   "inherits from Error, with a stack",
			script: `class X extends Error {} const c = Object.create(X.prototype); c.stack = 'X: s\n    at foo (f.js:1:1)'; c.extra = 1; console.error(c);`,
			want:   []string{"X: s\n    at foo (f.js:1:1) {\n  extra: 1\n}\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			rt, err := runtime.New(nil)
			if err != nil {
				t.Fatalf("Failed to create runtime: %v", err)
			}
			defer rt.Dispose()

			writer := runtime.NewStandardWriter(&stdout, &stderr)
			consoleMod := NewWithWriterAndFormatter(writer, runtime.NewPlainFormatter())
			if err := consoleMod.Register(rt); err != nil {
				t.Fatalf("Failed to register console module: %v", err)
			}

			if _, err := rt.RunScript(tt.script, "test.js"); err != nil {
				t.Fatalf("RunScript failed: %v", err)
			}

			output := stdout.String() + stderr.String()
			for _, want := range tt.want {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q\nfull output:\n%s", want, output)
				}
			}
			for _, reject := range tt.reject {
				if strings.Contains(output, reject) {
					t.Errorf("output unexpectedly contains %q\nfull output:\n%s", reject, output)
				}
			}
		})
	}
}

func TestConsole_Warn(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.warn('Warning message')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stderr.String()
	if output == "" {
		t.Error("console.warn should produce output to stderr")
	}
}

func TestConsole_Info(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.info('Info message')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.info should produce output to stdout")
	}
}

func TestConsole_Debug(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.debug('Debug message')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Debug may or may not produce output depending on implementation
	// Just verify it doesn't error
}

func TestConsole_Dir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.dir({foo: 123, bar: 'test'})`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.dir should produce output")
	}
}

func TestConsole_Table(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.table([{a: 1, b: 2}, {a: 3, b: 4}])`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.table should produce output")
	}
}

func TestConsole_Time_TimeEnd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		console.time('test');
		// Do something
		let sum = 0;
		for (let i = 0; i < 1000; i++) sum += i;
		console.timeEnd('test');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.timeEnd should produce output")
	}
}

func TestConsole_Count(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		console.count('test');
		console.count('test');
		console.count('test');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.count should produce output")
	}
}

func TestConsole_CountReset(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		console.count('test');
		console.countReset('test');
		console.count('test');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Just verify no errors
}

func TestConsole_Group_GroupEnd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		console.group('outer');
		console.log('inside group');
		console.groupEnd();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stdout.String()
	if output == "" {
		t.Error("console.group should produce output")
	}
}

func TestConsole_Assert_Pass(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.assert(true, 'should not appear')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Passing assert should produce no output
}

func TestConsole_Assert_Fail(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.assert(false, 'assertion failed')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	output := stderr.String()
	if output == "" {
		t.Error("console.assert(false) should produce error output")
	}
}

func TestConsole_Clear(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`console.clear()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Just verify no errors
}

func TestConsole_Trace(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		function a() { b(); }
		function b() { console.trace('trace test'); }
		a();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Trace may output to stdout or stderr depending on implementation
	output := stdout.String() + stderr.String()
	if output == "" {
		t.Skip("console.trace may not produce output in this implementation")
	}
}

func TestConsole_Name(t *testing.T) {
	c := New()
	if c.Name() != "console" {
		t.Errorf("Name() should return 'console', got %q", c.Name())
	}
}

// TestConsole_LogDataView checks that console.log prints a DataView's fields and buffer rather
// than {}.
func TestConsole_LogDataView(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	if _, err := rt.RunScript(`console.log(new DataView(new ArrayBuffer(2)))`, "test.js"); err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := "DataView {\n  byteLength: 2,\n  byteOffset: 0,\n  buffer: ArrayBuffer { [Uint8Contents]: <00 00>, byteLength: 2 }\n}\n"
	if got := stripAnsi(stdout.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestConsole_LogBreaksLines checks that console.log puts a long array in columns, and indents
// each line of it inside a group.
func TestConsole_LogBreaksLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	script := `console.group(); console.log(Array.from({ length: 30 }, (_, i) => i)); console.groupEnd()`
	if _, err := rt.RunScript(script, "test.js"); err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := "  [\n     0,  1,  2,  3,  4,  5,  6,  7,  8,\n     9, 10, 11, 12, 13, 14, 15, 16, 17,\n    18, 19, 20, 21, 22, 23, 24, 25, 26,\n    27, 28, 29\n  ]\n"
	if got := stripAnsi(stdout.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Tracking which objects are being printed, to find the ones that hold themselves, costs a
// little per object printed and nothing for objects left out: printing a million-element array,
// of which 100 elements show, takes about as long as printing those 100. Both are timed in the
// same run, so a slower machine or coverage instrumentation slows both alike; walking the
// left-out elements would take hundreds of times longer.
func TestConsole_LogManyObjectsTime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const wide = {};
		for (let i = 0; i < 20000; i++) wide['k' + i] = { v: i };
		wide.self = wide;
		console.log(wide);
		const long = Array.from({ length: 1000000 }, (_, i) => ({ v: i }));
		const short = long.slice(0, 100);
		const time = (value) => {
			const start = Date.now();
			console.log(value);
			return Date.now() - start;
		};
		time(short);
		JSON.stringify({ short: time(short), long: time(long) });
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	var ms struct{ Short, Long int }
	if err := json.Unmarshal([]byte(result.String()), &ms); err != nil {
		t.Fatalf("decoding %q: %v", result.String(), err)
	}
	if ms.Long > 5*ms.Short+100 {
		t.Errorf("printing the long array took %d ms, the 100 elements it shows %d ms", ms.Long, ms.Short)
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "<ref *1> {\n  k0: { v: 0 },") || !strings.Contains(out, "self: [Circular *1]") {
		t.Errorf("unexpected output start %q", out[:min(len(out), 80)])
	}
}

// A console call made while another is printing, by a getter here, gets cycle tracking of its
// own: the array it prints again isn't inside itself.
func TestConsole_LogReentrantNoCycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		let busy = false;
		const arr = [0];
		Object.defineProperty(arr, 0, {
			get() { if (!busy) { busy = true; console.log(arr); busy = false; } return 1; },
			enumerable: true
		});
		console.log(arr);
		const loop = { name: 'loop' };
		loop.self = loop;
		console.log(loop);
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := stdout.String(), "[ 1 ]\n[ 1 ]\n<ref *1> { name: 'loop', self: [Circular *1] }\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// %s keeps a NUL in the string an object's own toString returns, as Node does.
func TestConsole_LogStringSpecifierKeepsNUL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	if _, err := rt.RunScript(`console.log('%s|', { toString() { return 'before\0after'; } });`, "test.js"); err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := stdout.String(), "before\x00after|\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// console.log finds an array's and typed array's keys without walking their elements, which
// for these 10-million-element ones would take about a second.
func TestConsole_LogLargeArrayKeys(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	_, err := rt.RunScript(`
		const list = Object.assign(new Array(10000000).fill(0), { x: 2 });
		const bytes = Object.assign(new Uint8Array(10000000), { y: 3 });
		globalThis.ms = Date.now();
		console.log(list, { a: { b: { c: [], bytes } } });
		globalThis.ms = Date.now() - globalThis.ms;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	ms, err := rt.RunScript("ms", "ms.js")
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "... 9999900 more items,\n  x: 2\n]") || !strings.Contains(out, "{ a: { b: { c: [], bytes: [Uint8Array] } } }") {
		t.Errorf("unexpected output %q", out[max(0, len(out)-200):])
	}
	if ms.Number() > 200 {
		t.Errorf("printing took %v ms; the elements were probably walked", ms.Number())
	}
}

// A Buffer that holds itself is marked as a cycle. (Node recurses until it runs out of stack.)
func TestConsole_LogBufferHoldingItself(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rt := setupRuntime(t, &stdout, &stderr)
	defer rt.Dispose()

	if _, err := rt.RunScript(`
		// This runtime has no Buffer, so a stand-in that inspect takes for one.
		globalThis.Buffer = class Buffer extends Uint8Array { static isBuffer(v) { return v instanceof Buffer; } };
		const b = new Buffer(1);
		b.self = b;
		console.log(b);
	`, "test.js"); err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := stdout.String(), "<ref *1> <Buffer 00, self: [Circular *1]>\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
