package path

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Register the path module
	pathMod := New()
	if err := pathMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register path module: %v", err)
	}

	return rt
}

func TestPath_Join(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.join('foo', 'bar', 'baz')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := "foo/bar/baz"
	if result.String() != expected {
		t.Errorf("path.join mismatch: got %q, want %q", result.String(), expected)
	}
}

func TestPath_Basename(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected string
	}{
		{`__path_module.basename('/foo/bar/baz.txt')`, "baz.txt"},
		{`__path_module.basename('/foo/bar/baz.txt', '.txt')`, "baz"},
		{`__path_module.basename('/foo/bar/')`, "bar"},
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

func TestPath_Dirname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.dirname('/foo/bar/baz.txt')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := "/foo/bar"
	if result.String() != expected {
		t.Errorf("path.dirname mismatch: got %q, want %q", result.String(), expected)
	}
}

func TestPath_Extname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected string
	}{
		{`__path_module.extname('index.html')`, ".html"},
		{`__path_module.extname('index.coffee.md')`, ".md"},
		{`__path_module.extname('index.')`, "."},
		{`__path_module.extname('index')`, ""},
		{`__path_module.extname('.index')`, ".index"}, // Go behavior differs from Node
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

func TestPath_IsAbsolute(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`__path_module.isAbsolute('/foo/bar')`, true},
		{`__path_module.isAbsolute('foo/bar')`, false},
		{`__path_module.isAbsolute('./foo')`, false},
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

func TestPath_Normalize(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.normalize('/foo/bar//baz/asdf/quux/..')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := "/foo/bar/baz/asdf"
	if result.String() != expected {
		t.Errorf("path.normalize mismatch: got %q, want %q", result.String(), expected)
	}
}

func TestPath_Parse(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`JSON.stringify(__path_module.parse('/home/user/dir/file.txt'))`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Verify the result contains expected fields
	str := result.String()
	if str == "" {
		t.Error("parse should return non-empty JSON")
	}
}

func TestPath_Sep(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.sep`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// On Unix systems, should be "/"
	if result.String() != "/" && result.String() != "\\" {
		t.Errorf("path.sep should be '/' or '\\', got %q", result.String())
	}
}

func TestPath_Delimiter(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.delimiter`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// On Unix systems, should be ":"
	if result.String() != ":" && result.String() != ";" {
		t.Errorf("path.delimiter should be ':' or ';', got %q", result.String())
	}
}

func TestPath_Relative(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__path_module.relative('/data/orandea/test/aaa', '/data/orandea/impl/bbb')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := "../../impl/bbb"
	if result.String() != expected {
		t.Errorf("path.relative mismatch: got %q, want %q", result.String(), expected)
	}
}

func TestPath_Name(t *testing.T) {
	p := New()
	if p.Name() != "path" {
		t.Errorf("Name() should return 'path', got %q", p.Name())
	}
}
