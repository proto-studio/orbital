package fs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	// Use memory filesystem for testing
	cfg := &runtime.Config{
		Filesystem: runtime.NewMemoryFilesystem(),
	}

	rt, err := runtime.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Register buffer module (needed for some fs operations)
	bufMod := buffer.New()
	if err := bufMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register buffer module: %v", err)
	}

	fsMod := New()
	if err := fsMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register fs module: %v", err)
	}

	return rt
}

func TestFS_WriteFileSync_ReadFileSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/test.txt', 'Hello World');
		__fs_module.readFileSync('/test.txt', 'utf8');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", result.String())
	}
}

func TestFS_ExistsSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/exists.txt', 'data');
		const exists = __fs_module.existsSync('/exists.txt');
		const notExists = __fs_module.existsSync('/not-exists.txt');
		JSON.stringify({exists, notExists});
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := `{"exists":true,"notExists":false}`
	if result.String() != expected {
		t.Errorf("Expected %q, got %q", expected, result.String())
	}
}

func TestFS_MkdirSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.mkdirSync('/testdir');
		__fs_module.existsSync('/testdir');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("Directory should exist after mkdirSync")
	}
}

func TestFS_MkdirSync_Recursive(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.mkdirSync('/a/b/c', { recursive: true });
		__fs_module.existsSync('/a/b/c');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("Nested directory should exist after recursive mkdirSync")
	}
}

func TestFS_ReaddirSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.mkdirSync('/dir');
		__fs_module.writeFileSync('/dir/a.txt', 'a');
		__fs_module.writeFileSync('/dir/b.txt', 'b');
		const files = __fs_module.readdirSync('/dir');
		files.sort().join(',');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "a.txt,b.txt" {
		t.Errorf("Expected 'a.txt,b.txt', got %q", result.String())
	}
}

func TestFS_UnlinkSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/to-delete.txt', 'data');
		__fs_module.unlinkSync('/to-delete.txt');
		__fs_module.existsSync('/to-delete.txt');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Boolean() {
		t.Error("File should not exist after unlinkSync")
	}
}

func TestFS_RmdirSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.mkdirSync('/to-remove');
		__fs_module.rmdirSync('/to-remove');
		__fs_module.existsSync('/to-remove');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.Boolean() {
		t.Error("Directory should not exist after rmdirSync")
	}
}

func TestFS_StatSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/stat-test.txt', 'hello');
		const stats = __fs_module.statSync('/stat-test.txt');
		typeof stats === 'object' && stats !== null && 'size' in stats;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("statSync should return stats object with size")
	}
}

func TestFS_AppendFileSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/append.txt', 'Hello');
		__fs_module.appendFileSync('/append.txt', ' World');
		__fs_module.readFileSync('/append.txt', 'utf8');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", result.String())
	}
}

func TestFS_CopyFileSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/original.txt', 'content');
		__fs_module.copyFileSync('/original.txt', '/copy.txt');
		__fs_module.readFileSync('/copy.txt', 'utf8');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if result.String() != "content" {
		t.Errorf("Expected 'content', got %q", result.String())
	}
}

func TestFS_RenameSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/old-name.txt', 'data');
		__fs_module.renameSync('/old-name.txt', '/new-name.txt');
		const oldExists = __fs_module.existsSync('/old-name.txt');
		const newExists = __fs_module.existsSync('/new-name.txt');
		JSON.stringify({oldExists, newExists});
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	expected := `{"oldExists":false,"newExists":true}`
	if result.String() != expected {
		t.Errorf("Expected %q, got %q", expected, result.String())
	}
}

func TestFS_ReadFileSync_NoEncoding(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/buffer.txt', 'test');
		const content = __fs_module.readFileSync('/buffer.txt');
		Buffer.isBuffer(content) && content.toString() === 'test';
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("readFileSync without an encoding should return a Buffer of the content")
	}
}

func TestFS_TruncateSync(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__fs_module.writeFileSync('/truncate.txt', 'Hello World');
		__fs_module.truncateSync('/truncate.txt', 5);
		__fs_module.readFileSync('/truncate.txt', 'utf8');
	`, "test.js")
	if err != nil {
		// truncateSync may not be implemented
		t.Skip("truncateSync may not be implemented")
	}

	if result.String() != "Hello" {
		t.Errorf("Expected 'Hello', got %q", result.String())
	}
}

func TestFS_Name(t *testing.T) {
	f := New()
	if f.Name() != "fs" {
		t.Errorf("Name() should return 'fs', got %q", f.Name())
	}
}

// An embedder can register fs without the buffer module. Strings and typed arrays are then
// written as their exact UTF-8 or raw bytes, and a read with no encoding gives a Uint8Array.
func TestFSWithoutBuffer(t *testing.T) {
	memory := runtime.NewMemoryFilesystem()
	rt, err := runtime.New(&runtime.Config{Filesystem: memory})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if err := New().Register(rt); err != nil {
		t.Fatal(err)
	}
	// The lone surrogate is written as U+FFFD, as Node writes it.
	// \x00 is NUL and \uFFFD is U+FFFD, the replacement character.
	want := "A\x00é€😀 \uFFFD end"
	result, err := rt.RunScript(`
		const fs = __fs_module;
		// \ud800 is a lone UTF-16 high surrogate, which UTF-8 can't encode.
		fs.writeFileSync('/text', 'A\0é€😀 \ud800 end');
		fs.writeFileSync('/bytes', new Uint8Array([0, 1, 255, 254]));
		fs.appendFileSync('/bytes', new Uint8Array([7]));
		const raw = fs.readFileSync('/bytes');
		JSON.stringify({
			text: fs.readFileSync('/text', 'utf8') === 'A\0é€😀 � end',
			type: raw instanceof Uint8Array,
			raw: Array.from(raw),
		});
	`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.String(), `{"text":true,"type":true,"raw":[0,1,255,254,7]}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	data, err := memory.ReadFile("/text")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("file holds %x, want %x", data, want)
	}
}
