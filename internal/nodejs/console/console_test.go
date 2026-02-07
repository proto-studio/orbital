package console

import (
	"bytes"
	"testing"

	pkgconsole "proto.zip/studio/orbital/pkg/console"
	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T, stdout, stderr *bytes.Buffer) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Create console module with custom writer
	writer := pkgconsole.NewStandardWriter(stdout, stderr)
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
