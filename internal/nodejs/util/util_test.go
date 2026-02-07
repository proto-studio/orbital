package util

import (
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
