package buffer

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	bufMod := New()
	if err := bufMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register buffer module: %v", err)
	}

	return rt
}

func TestBuffer_Alloc(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.alloc(10).length`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 10 {
		t.Errorf("Buffer.alloc(10).length should be 10, got %v", result.Number())
	}
}

func TestBuffer_From_String(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.from('hello').toString()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "hello" {
		t.Errorf("Buffer.from('hello').toString() should be 'hello', got %q", result.String())
	}
}

func TestBuffer_From_Array(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.from([72, 101, 108, 108, 111]).toString()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello" {
		t.Errorf("Expected 'Hello', got %q", result.String())
	}
}

func TestBuffer_IsBuffer(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	tests := []struct {
		input    string
		expected bool
	}{
		{`Buffer.isBuffer(Buffer.alloc(10))`, true},
		{`Buffer.isBuffer('string')`, false},
		{`Buffer.isBuffer([1, 2, 3])`, false},
		{`Buffer.isBuffer({})`, false},
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

func TestBuffer_Concat(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const buf1 = Buffer.from('Hello');
		const buf2 = Buffer.from(' ');
		const buf3 = Buffer.from('World');
		Buffer.concat([buf1, buf2, buf3]).toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", result.String())
	}
}

func TestBuffer_Slice(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.from('Hello World').slice(0, 5).toString()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello" {
		t.Errorf("Expected 'Hello', got %q", result.String())
	}
}

func TestBuffer_Fill(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const buf = Buffer.alloc(5, 'a');
		buf.toString();
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "aaaaa" {
		t.Errorf("Expected 'aaaaa', got %q", result.String())
	}
}

func TestBuffer_ToString_Encoding(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	// Test hex encoding
	result, err := rt.RunScript(`Buffer.from('Hello').toString('hex')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "48656c6c6f" {
		t.Errorf("Expected '48656c6c6f', got %q", result.String())
	}
}

func TestBuffer_From_Base64(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.from('SGVsbG8gV29ybGQ=', 'base64').toString()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", result.String())
	}
}

func TestBuffer_ByteLength(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.byteLength('Hello')`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 5 {
		t.Errorf("Expected 5, got %v", result.Number())
	}
}

// Node drops up to two trailing '=' for both base64 and base64url, so a
// padded base64url string reports its decoded length.
func TestBuffer_ByteLength_Base64Padding(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`JSON.stringify(['aGk=','aGk','aGVsbG8','aGVsbG8=','aGVsbA==','aGVsbA','','=','==','a','ab','abc','abcd','-_-_','-_8=','a==='].flatMap(s => ['base64','base64url'].map(e => Buffer.byteLength(s, e))))`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Captured from Node 20.18.1.
	want := "[2,2,2,2,5,5,5,5,4,4,4,4,0,0,0,0,0,0,0,0,1,1,2,2,3,3,3,3,2,2,1,1]"
	if result.String() != want {
		t.Errorf("Expected %s, got %s", want, result.String())
	}
}

func TestBuffer_Compare(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Buffer.compare(Buffer.from('abc'), Buffer.from('abc'))`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 0 {
		t.Errorf("Expected 0 for equal buffers, got %v", result.Number())
	}
}

func TestBuffer_Name(t *testing.T) {
	b := New()
	if b.Name() != "buffer" {
		t.Errorf("Name() should return 'buffer', got %q", b.Name())
	}
}
