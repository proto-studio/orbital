package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFilesystem_NoSandbox(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orbital-fs-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lfs := NewLocalFilesystem("")

	// Test WriteFile and ReadFile
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")
	if err := lfs.WriteFile(testFile, content, 0644); err != nil {
		t.Errorf("WriteFile failed: %v", err)
	}

	read, err := lfs.ReadFile(testFile)
	if err != nil {
		t.Errorf("ReadFile failed: %v", err)
	}
	if string(read) != string(content) {
		t.Errorf("ReadFile content mismatch: got %q, want %q", read, content)
	}

	// Test Exists
	if !lfs.Exists(testFile) {
		t.Error("Exists returned false for existing file")
	}
	if lfs.Exists(filepath.Join(tmpDir, "nonexistent.txt")) {
		t.Error("Exists returned true for non-existing file")
	}

	// Test Stat
	info, err := lfs.Stat(testFile)
	if err != nil {
		t.Errorf("Stat failed: %v", err)
	}
	if info.Name != "test.txt" {
		t.Errorf("Stat name mismatch: got %q, want %q", info.Name, "test.txt")
	}
	if info.Size != int64(len(content)) {
		t.Errorf("Stat size mismatch: got %d, want %d", info.Size, len(content))
	}

	// Test Mkdir and ReadDir
	subDir := filepath.Join(tmpDir, "subdir")
	if err := lfs.Mkdir(subDir, 0755); err != nil {
		t.Errorf("Mkdir failed: %v", err)
	}

	entries, err := lfs.ReadDir(tmpDir)
	if err != nil {
		t.Errorf("ReadDir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadDir count mismatch: got %d, want 2", len(entries))
	}

	// Test Remove
	if err := lfs.Remove(testFile); err != nil {
		t.Errorf("Remove failed: %v", err)
	}
	if lfs.Exists(testFile) {
		t.Error("File still exists after Remove")
	}
}

func TestLocalFilesystem_Sandboxed(t *testing.T) {
	sandboxRoot, err := os.MkdirTemp("", "orbital-sandbox-test-*")
	if err != nil {
		t.Fatalf("Failed to create sandbox root: %v", err)
	}
	defer os.RemoveAll(sandboxRoot)

	lfs := NewLocalFilesystem(sandboxRoot)

	// Test that relative paths work
	testFile := "test.txt"
	content := []byte("Sandboxed content")
	if err := lfs.WriteFile(testFile, content, 0644); err != nil {
		t.Errorf("WriteFile in sandbox failed: %v", err)
	}

	// Verify the file is actually in the sandbox
	actualPath := filepath.Join(sandboxRoot, testFile)
	if _, err := os.Stat(actualPath); err != nil {
		t.Errorf("File not created in sandbox: %v", err)
	}

	// Test that path traversal is blocked
	traversalPath := "../../../etc/passwd"
	_, err = lfs.ReadFile(traversalPath)
	if err != ErrOutsideSandbox {
		t.Errorf("Path traversal should return ErrOutsideSandbox, got: %v", err)
	}

	// Test subdirectory creation
	if err := lfs.MkdirAll("a/b/c", 0755); err != nil {
		t.Errorf("MkdirAll failed: %v", err)
	}
	if !lfs.Exists("a/b/c") {
		t.Error("Subdirectory not created")
	}
}

func TestLocalFilesystem_AppendFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orbital-append-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lfs := NewLocalFilesystem(tmpDir)

	if err := lfs.WriteFile("append.txt", []byte("Hello"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := lfs.AppendFile("append.txt", []byte(" World")); err != nil {
		t.Errorf("AppendFile failed: %v", err)
	}

	content, err := lfs.ReadFile("append.txt")
	if err != nil {
		t.Errorf("ReadFile failed: %v", err)
	}
	if string(content) != "Hello World" {
		t.Errorf("Append content mismatch: got %q, want %q", content, "Hello World")
	}
}

func TestLocalFilesystem_FileOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "orbital-file-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lfs := NewLocalFilesystem(tmpDir)

	// Test Create and Open
	f, err := lfs.Create("created.txt")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	f.Write([]byte("created content"))
	f.Close()

	f, err = lfs.Open("created.txt")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	buf := make([]byte, 100)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "created content" {
		t.Errorf("Read content mismatch: got %q", buf[:n])
	}
	f.Close()
}

func TestMemoryFilesystem(t *testing.T) {
	mfs := NewMemoryFilesystem()

	// Test WriteFile and ReadFile
	if err := mfs.WriteFile("/test.txt", []byte("memory content"), 0644); err != nil {
		t.Errorf("WriteFile failed: %v", err)
	}

	content, err := mfs.ReadFile("/test.txt")
	if err != nil {
		t.Errorf("ReadFile failed: %v", err)
	}
	if string(content) != "memory content" {
		t.Errorf("Content mismatch: got %q", content)
	}

	// Test Exists
	if !mfs.Exists("/test.txt") {
		t.Error("Exists returned false for existing file")
	}

	// Test Stat
	info, err := mfs.Stat("/test.txt")
	if err != nil {
		t.Errorf("Stat failed: %v", err)
	}
	if info.Size != 14 {
		t.Errorf("Size mismatch: got %d", info.Size)
	}

	// Test Remove
	if err := mfs.Remove("/test.txt"); err != nil {
		t.Errorf("Remove failed: %v", err)
	}
	if mfs.Exists("/test.txt") {
		t.Error("File exists after Remove")
	}
}

func TestFileInfo(t *testing.T) {
	info := &FileInfo{
		Name:  "test.txt",
		Size:  100,
		IsDir: false,
	}

	if info.Name != "test.txt" {
		t.Errorf("Name mismatch: got %q", info.Name)
	}
	if info.Size != 100 {
		t.Errorf("Size mismatch: got %d", info.Size)
	}
	if info.IsDir {
		t.Error("IsDir should be false")
	}
}
