package util

import (
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	utilMod := New()
	if err := utilMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register util module: %v", err)
	}

	return rt
}

func TestUtil_Format(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected string
	}{
		{`__util_module.format('Hello %s', 'World')`, "Hello World"},
		{`__util_module.format('Number: %d', 42)`, "Number: 42"},
		{`__util_module.format('%j', {foo: 123})`, `{"foo":123}`},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.String() != tt.expected {
			t.Errorf("For %q: got %q, want %q", tt.input, result.String(), tt.expected)
		}
	}
}

func TestUtil_Inspect(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__util_module.inspect({foo: 123})`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	str := result.String()
	if str == "" {
		t.Error("inspect should return non-empty string")
	}
}

func TestUtil_InspectError(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const err = new Error('boom');
		err.code = 'ENOENT';
		__util_module.inspect(err);
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	got := result.String()
	for _, want := range []string{"Error: boom\n", "\n    at ", "code: 'ENOENT'"} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect(error) missing %q\n%s", want, got)
		}
	}

	bare, err := rt.RunScript(`
		const bareErr = new Error('bare');
		bareErr.stack = undefined;
		__util_module.inspect(bareErr);
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if bare.String() != "[Error: bare]" {
		t.Errorf("inspect(error without stack) = %q", bare.String())
	}

	nullProto, err := rt.RunScript(`
		const nullErr = new Error('boom');
		nullErr.code = 'ENOENT';
		Object.setPrototypeOf(nullErr, null);
		__util_module.inspect(nullErr);
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	got = nullProto.String()
	for _, want := range []string{"[Error: null prototype]: boom\n", "code: 'ENOENT'"} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect(null-prototype error) missing %q\n%s", want, got)
		}
	}

	native, err := rt.RunScript(`
		const nullErr2 = new Error('boom');
		Object.setPrototypeOf(nullErr2, null);
		const fake = { message: 'x', stack: 'Error: x' };
		Object.defineProperty(fake, Symbol.toStringTag, { value: 'Error' });
		__util_module.types.isNativeError(nullErr2) === true &&
			__util_module.types.isNativeError(fake) === false &&
			__util_module.types.isNativeError(new Error('ok')) === true;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if !native.Boolean() {
		t.Error("isNativeError should follow the error brand, including null prototypes")
	}
}

func TestUtil_Types_IsArray(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isArray([1, 2, 3])`, true},
		{`__util_module.types.isArray({})`, false},
		{`__util_module.types.isArray('string')`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsDate(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isDate(new Date())`, true},
		{`__util_module.types.isDate({})`, false},
		{`__util_module.types.isDate('2021-01-01')`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsMap(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isMap(new Map())`, true},
		{`__util_module.types.isMap({})`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsSet(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isSet(new Set())`, true},
		{`__util_module.types.isSet([])`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsRegExp(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isRegExp(/test/)`, true},
		{`__util_module.types.isRegExp(new RegExp('test'))`, true},
		{`__util_module.types.isRegExp('test')`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsPromise(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isPromise(Promise.resolve())`, true},
		{`__util_module.types.isPromise({then: () => {}})`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Types_IsFunction(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__util_module.types.isFunction(function() {})`, true},
		{`__util_module.types.isFunction(() => {})`, true},
		{`__util_module.types.isFunction({})`, false},
	}

	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if result.Boolean() != tt.expected {
			t.Errorf("For %q: got %v, want %v", tt.input, result.Boolean(), tt.expected)
		}
	}
}

func TestUtil_Promisify(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	// Test promisify basic functionality
	result, err := rt.RunScript(`
		const callback = (value, cb) => { cb(null, value * 2); };
		const promisified = __util_module.promisify(callback);
		typeof promisified;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "function" {
		t.Errorf("promisify should return a function, got %q", result.String())
	}
}

func TestUtil_Inherits(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		function Parent() {}
		Parent.prototype.greet = function() { return 'hello'; };
		
		function Child() {}
		__util_module.inherits(Child, Parent);
		
		const child = new Child();
		child.greet();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "hello" {
		t.Errorf("Expected 'hello', got %q", result.String())
	}
}

func TestUtil_Name(t *testing.T) {
	u := New()
	if u.Name() != "util" {
		t.Errorf("Name() should return 'util', got %q", u.Name())
	}
}

// TestUtil_InspectDataView checks that a DataView prints its fields and buffer, on lines of
// their own since they don't fit on one.
func TestUtil_InspectDataView(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__util_module.inspect(new DataView(new ArrayBuffer(2)))`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	want := "DataView {\n  byteLength: 2,\n  byteOffset: 0,\n  buffer: ArrayBuffer { [Uint8Contents]: <00 00>, byteLength: 2 }\n}"
	if got := result.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUtil_InspectBinaryPastDepth checks that an ArrayBuffer past the depth limit is named
// without reading more of it than its constructor, so a toStringTag getter that throws doesn't
// break inspection.
func TestUtil_InspectBinaryPastDepth(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const ab = new ArrayBuffer(2);
		Object.defineProperty(ab, Symbol.toStringTag, { get() { throw new Error('tag'); } });
		__util_module.inspect({ x: ab }, { depth: 0 });
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := result.String(), "{ x: [ArrayBuffer] }"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUtil_InspectBreaksLines checks that output longer than breakLength goes across lines, and
// that an array of short numbers goes in right-aligned columns.
func TestUtil_InspectBreaksLines(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input, want string
	}{
		{`__util_module.inspect(Array.from({ length: 30 }, (_, i) => i))`,
			"[\n   0,  1,  2,  3,  4,  5,  6,  7,  8,\n   9, 10, 11, 12, 13, 14, 15, 16, 17,\n  18, 19, 20, 21, 22, 23, 24, 25, 26,\n  27, 28, 29\n]"},
		{`__util_module.inspect({ a: 'a'.repeat(20), b: 'b'.repeat(20), c: 'c'.repeat(20), d: 'd'.repeat(20) })`,
			"{\n  a: 'aaaaaaaaaaaaaaaaaaaa',\n  b: 'bbbbbbbbbbbbbbbbbbbb',\n  c: 'cccccccccccccccccccc',\n  d: 'dddddddddddddddddddd'\n}"},
		{`__util_module.inspect({ a: 1, b: [1, 2] })`, "{ a: 1, b: [ 1, 2 ] }"},
		{`__util_module.inspect({ a: 'a'.repeat(30), b: 'b'.repeat(30) }, { breakLength: Infinity })`,
			"{ a: '" + strings.Repeat("a", 30) + "', b: '" + strings.Repeat("b", 30) + "' }"},
	}
	for _, tt := range tests {
		result, err := rt.RunScript(tt.input, "test.js")
		if err != nil {
			t.Fatalf("RunScript failed for %q: %v", tt.input, err)
		}
		if got := result.String(); got != tt.want {
			t.Errorf("For %q:\ngot  %q\nwant %q", tt.input, got, tt.want)
		}
	}
}

// TestUtil_InspectReadsElementsOnce checks that laying an array out in columns doesn't read its
// elements again, which would run a getter twice.
func TestUtil_InspectReadsElementsOnce(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const arr = [0, 1, 2, 3, 4, 5, 6, 7];
		let reads = 0;
		Object.defineProperty(arr, 0, { get() { if (reads++) throw new Error('read twice'); return 5; }, enumerable: true });
		__util_module.inspect(arr) + ' ' + reads;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := result.String(), "[\n  5, 1, 2, 3,\n  4, 5, 6, 7\n] 1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUtil_InspectProxyPastDepth checks that a proxy whose getPrototypeOf trap throws can still
// be inspected past the depth limit.
func TestUtil_InspectProxyPastDepth(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const p = new Proxy({}, { getPrototypeOf() { throw new Error('trap'); } });
		__util_module.inspect({ x: p }, { depth: 0 });
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if got, want := result.String(), "{ x: [Object] }"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
