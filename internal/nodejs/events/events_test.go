package events

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	eventsMod := New()
	if err := eventsMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register events module: %v", err)
	}

	return rt
}

func TestEventEmitter_On_Emit(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let result = '';
		const emitter = new EventEmitter();
		emitter.on('test', (msg) => { result = msg; });
		emitter.emit('test', 'hello');
		result;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "hello" {
		t.Errorf("Expected 'hello', got %q", result.String())
	}
}

func TestEventEmitter_Once(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let count = 0;
		const emitter = new EventEmitter();
		emitter.once('test', () => { count++; });
		emitter.emit('test');
		emitter.emit('test');
		count;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 1 {
		t.Errorf("Expected 1, got %v", result.Number())
	}
}

func TestEventEmitter_RemoveListener(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let count = 0;
		const emitter = new EventEmitter();
		const listener = () => { count++; };
		emitter.on('test', listener);
		emitter.emit('test');
		emitter.removeListener('test', listener);
		emitter.emit('test');
		count;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 1 {
		t.Errorf("Expected 1, got %v", result.Number())
	}
}

func TestEventEmitter_RemoveAllListeners(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let count = 0;
		const emitter = new EventEmitter();
		emitter.on('test', () => { count++; });
		emitter.on('test', () => { count++; });
		emitter.emit('test');
		emitter.removeAllListeners('test');
		emitter.emit('test');
		count;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 2 {
		t.Errorf("Expected 2, got %v", result.Number())
	}
}

func TestEventEmitter_ListenerCount(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const emitter = new EventEmitter();
		emitter.on('test', () => {});
		emitter.on('test', () => {});
		emitter.on('other', () => {});
		emitter.listenerCount('test');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 2 {
		t.Errorf("Expected 2, got %v", result.Number())
	}
}

func TestEventEmitter_EventNames(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const emitter = new EventEmitter();
		emitter.on('foo', () => {});
		emitter.on('bar', () => {});
		emitter.eventNames().sort().join(',');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "bar,foo" {
		t.Errorf("Expected 'bar,foo', got %q", result.String())
	}
}

func TestEventEmitter_Prepend(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let order = '';
		const emitter = new EventEmitter();
		emitter.on('test', () => { order += '1'; });
		emitter.prependListener('test', () => { order += '2'; });
		emitter.emit('test');
		order;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "21" {
		t.Errorf("Expected '21', got %q", result.String())
	}
}

func TestEventEmitter_Off(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let count = 0;
		const emitter = new EventEmitter();
		const fn = () => { count++; };
		emitter.on('test', fn);
		emitter.off('test', fn);
		emitter.emit('test');
		count;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 0 {
		t.Errorf("Expected 0, got %v", result.Number())
	}
}

func TestEventEmitter_AddListener(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	// addListener should be an alias for on
	result, err := rt.RunScript(`
		let result = '';
		const emitter = new EventEmitter();
		emitter.addListener('test', (msg) => { result = msg; });
		emitter.emit('test', 'works');
		result;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "works" {
		t.Errorf("Expected 'works', got %q", result.String())
	}
}

func TestEventEmitter_MultipleArgs(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		let sum = 0;
		const emitter = new EventEmitter();
		emitter.on('add', (a, b, c) => { sum = a + b + c; });
		emitter.emit('add', 1, 2, 3);
		sum;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Number() != 6 {
		t.Errorf("Expected 6, got %v", result.Number())
	}
}

func TestEvents_Name(t *testing.T) {
	e := New()
	if e.Name() != "events" {
		t.Errorf("Name() should return 'events', got %q", e.Name())
	}
}
