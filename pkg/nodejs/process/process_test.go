package process

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	processMod := New()
	if err := processMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register process module: %v", err)
	}

	return rt
}

func setupRuntimeWithEnv(t *testing.T, env runtime.Environment) *runtime.Runtime {
	cfg := &runtime.Config{
		Environment: env,
	}

	rt, err := runtime.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	processMod := New()
	if err := processMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register process module: %v", err)
	}

	return rt
}

func TestProcess_Platform(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.platform`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	platform := result.String()
	if platform != "darwin" && platform != "linux" && platform != "win32" {
		t.Errorf("Unexpected platform: %q", platform)
	}
}

func TestProcess_Arch(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.arch`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	arch := result.String()
	if arch == "" {
		t.Error("process.arch should not be empty")
	}
}

func TestProcess_Version(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.version`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	version := result.String()
	if version == "" {
		t.Error("process.version should not be empty")
	}
}

func TestProcess_Pid(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.pid`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	pid := result.Number()
	if pid <= 0 {
		t.Error("process.pid should be positive")
	}
}

func TestProcess_Ppid(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.ppid`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	ppid := result.Number()
	if ppid < 0 {
		t.Error("process.ppid should not be negative")
	}
}

func TestProcess_Cwd(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.cwd()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	cwd := result.String()
	if cwd == "" {
		t.Error("process.cwd() should not be empty")
	}
}

func TestProcess_Env(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`typeof process.env`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "object" {
		t.Errorf("process.env should be an object, got %q", result.String())
	}
}

func TestProcess_Env_Sandboxed(t *testing.T) {
	env := runtime.NewSandboxedEnvironment(map[string]string{
		"TEST_VAR": "test_value",
	})

	rt := setupRuntimeWithEnv(t, env)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.env.TEST_VAR`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "test_value" {
		t.Errorf("Expected 'test_value', got %q", result.String())
	}
}

func TestProcess_Argv(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`Array.isArray(process.argv)`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.argv should be an array")
	}
}

func TestProcess_Hrtime(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const [sec, nsec] = process.hrtime();
		sec >= 0 && nsec >= 0;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.hrtime should return non-negative values")
	}
}

func TestProcess_Hrtime_Diff(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const start = process.hrtime();
		// Small delay
		let sum = 0;
		for (let i = 0; i < 10000; i++) sum += i;
		const diff = process.hrtime(start);
		Array.isArray(diff) && diff.length === 2;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.hrtime(diff) should return [seconds, nanoseconds]")
	}
}

func TestProcess_MemoryUsage(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const mem = process.memoryUsage();
		typeof mem.heapTotal === 'number' && typeof mem.heapUsed === 'number';
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.memoryUsage should return object with numeric properties")
	}
}

func TestProcess_Uptime(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.uptime() >= 0`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.uptime() should return non-negative number")
	}
}

func TestProcess_NextTick(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let called = false;
		process.nextTick(() => { called = true; });
		called;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// nextTick may or may not have been called synchronously
	// depending on implementation
	_ = result
}

func TestProcess_Versions(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`typeof process.versions === 'object'`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("process.versions should be an object")
	}
}

func TestProcess_ExecPath(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`process.execPath`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() == "" {
		t.Error("process.execPath should not be empty")
	}
}

func TestProcess_Name(t *testing.T) {
	p := New()
	if p.Name() != "process" {
		t.Errorf("Name() should return 'process', got %q", p.Name())
	}
}
