package runtime

import (
	"testing"
	"time"
)

func TestRuntime_New(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	if rt.Isolate() == nil {
		t.Error("Isolate should not be nil")
	}
	if rt.Context() == nil {
		t.Error("Context should not be nil")
	}
	if rt.EventLoop() == nil {
		t.Error("EventLoop should not be nil")
	}
}

func TestRuntime_RunScript(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	// Test basic JavaScript execution
	val, err := rt.RunScript("1 + 2", "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if val.Number() != 3 {
		t.Errorf("Expected 3, got %v", val.Number())
	}
}

func TestRuntime_RunScript_String(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	val, err := rt.RunScript(`"hello" + " " + "world"`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if val.String() != "hello world" {
		t.Errorf("Expected 'hello world', got %q", val.String())
	}
}

func TestRuntime_RunScript_Object(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	val, err := rt.RunScript(`({foo: 123, bar: "test"})`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if !val.IsObject() {
		t.Error("Expected object")
	}
}

func TestRuntime_RunScript_Error(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	_, err = rt.RunScript(`throw new Error("test error")`, "test.js")
	if err == nil {
		t.Error("Expected error from throw")
	}
}

func TestRuntime_SetGlobal_GetGlobal(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	// Set a global value via script, then retrieve it
	_, err = rt.RunScript("globalThis.testValue = 42", "setup.js")
	if err != nil {
		t.Fatalf("Setup script failed: %v", err)
	}

	// Get it back via JavaScript
	result, err := rt.RunScript("testValue", "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}
	if result.Number() != 42 {
		t.Errorf("Expected 42, got %v", result.Number())
	}
}

func TestRuntime_WithConfig(t *testing.T) {
	cfg := &Config{
		EnableConsole: true,
		EnableTimers:  true,
		Filesystem:    NewMemoryFilesystem(),
		DocumentRoot:  "/sandbox",
		Timeout:       5 * time.Second,
	}

	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runtime with config: %v", err)
	}
	defer rt.Dispose()

	if rt.Filesystem() == nil {
		t.Error("Filesystem should not be nil")
	}
	if rt.DocumentRoot() != "/sandbox" {
		t.Errorf("DocumentRoot mismatch: got %q", rt.DocumentRoot())
	}
}

func TestRuntime_Dispose(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	rt.Dispose()

	// Multiple disposes should not panic
	rt.Dispose()
}

func TestRuntime_Kill(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	if rt.IsKilled() {
		t.Error("Runtime should not be killed initially")
	}

	rt.Kill("test reason")

	if !rt.IsKilled() {
		t.Error("Runtime should be killed after Kill()")
	}
	if rt.KillReason() != "test reason" {
		t.Errorf("KillReason mismatch: got %q", rt.KillReason())
	}
}

func TestRuntime_ResourceTracking(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	tracker := rt.ResourceTracker()
	if tracker == nil {
		t.Fatal("ResourceTracker should not be nil")
	}

	// Track a resource
	mockResource := &mockCloser{}
	id := rt.TrackResource(mockResource)
	if id == 0 {
		t.Error("TrackResource should return non-zero ID")
	}

	// Untrack it
	rt.UntrackResource(id)

	// Dispose should not close the untracked resource
	rt.Dispose()
	if mockResource.closed {
		t.Error("Untracked resource should not be closed")
	}
}

func TestRuntime_Timeout(t *testing.T) {
	cfg := &Config{
		Timeout: 100 * time.Millisecond,
	}

	rt, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}
	defer rt.Dispose()

	// Wait for timeout
	time.Sleep(200 * time.Millisecond)

	if !rt.IsDone() {
		t.Error("Runtime should be done after timeout")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.EnableConsole {
		t.Error("EnableConsole should be true by default")
	}
	if !cfg.EnableTimers {
		t.Error("EnableTimers should be true by default")
	}
	if cfg.Filesystem != nil {
		t.Error("Filesystem should be nil by default (uses local)")
	}
}
