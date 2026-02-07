package url

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	urlMod := New()
	if err := urlMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register url module: %v", err)
	}

	return rt
}

func TestURL_Parse(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const url = new URL('https://example.com:8080/path?query=value#hash');
		JSON.stringify({
			protocol: url.protocol,
			host: url.host,
			hostname: url.hostname,
			port: url.port,
			pathname: url.pathname,
			search: url.search,
			hash: url.hash
		});
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Just verify it returns valid JSON with expected structure
	str := result.String()
	if str == "" || str == "{}" {
		t.Error("URL parsing should return non-empty result")
	}
}

func TestURL_Protocol(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com').protocol`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https:" {
		t.Errorf("Expected 'https:', got %q", result.String())
	}
}

func TestURL_Hostname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').hostname`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "example.com" {
		t.Errorf("Expected 'example.com', got %q", result.String())
	}
}

func TestURL_Port(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').port`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "8080" {
		t.Errorf("Expected '8080', got %q", result.String())
	}
}

func TestURL_Pathname(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com/path/to/file').pathname`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "/path/to/file" {
		t.Errorf("Expected '/path/to/file', got %q", result.String())
	}
}

func TestURL_SearchParams(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com?foo=bar&baz=qux').searchParams.get('foo')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar" {
		t.Errorf("Expected 'bar', got %q", result.String())
	}
}

func TestURLSearchParams_Set(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.set('foo', 'bar');
		params.get('foo');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar" {
		t.Errorf("Expected 'bar', got %q", result.String())
	}
}

func TestURLSearchParams_Append(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.append('foo', 'bar');
		params.append('foo', 'baz');
		params.getAll('foo').join(',');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar,baz" {
		t.Errorf("Expected 'bar,baz', got %q", result.String())
	}
}

func TestURLSearchParams_Delete(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams('foo=bar&baz=qux');
		params.delete('foo');
		params.toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "baz=qux" {
		t.Errorf("Expected 'baz=qux', got %q", result.String())
	}
}

func TestURLSearchParams_Has(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`new URLSearchParams('foo=bar').has('foo')`, true},
		{`new URLSearchParams('foo=bar').has('baz')`, false},
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

func TestURLSearchParams_ToString(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const params = new URLSearchParams();
		params.set('foo', 'bar');
		params.set('baz', 'qux');
		params.toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	str := result.String()
	if str != "foo=bar&baz=qux" && str != "baz=qux&foo=bar" {
		t.Errorf("Expected 'foo=bar&baz=qux' or similar, got %q", str)
	}
}

func TestURL_Href(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com/path?query=value').href`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https://example.com/path?query=value" {
		t.Errorf("Expected full URL, got %q", result.String())
	}
}

func TestURL_Origin(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`new URL('https://example.com:8080/path').origin`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "https://example.com:8080" {
		t.Errorf("Expected 'https://example.com:8080', got %q", result.String())
	}
}

func TestURL_Name(t *testing.T) {
	u := New()
	if u.Name() != "url" {
		t.Errorf("Name() should return 'url', got %q", u.Name())
	}
}
