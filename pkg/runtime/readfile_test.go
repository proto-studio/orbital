package runtime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadFileLimited_RefusesAFileOverTheLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/ten", []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}

	data, err := ReadFileLimited(m, "/ten", 10)
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("at the limit: %q, %v", data, err)
	}

	_, err = ReadFileLimited(m, "/ten", 9)
	var tooLarge *FileTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Size != 10 || tooLarge.Limit != 9 {
		t.Fatalf("over the limit: %v", err)
	}

	var pathErr *fs.PathError
	if _, err := ReadFileLimited(m, "/dir", 10); !errors.As(err, &pathErr) || pathErr.Op != "read" || !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("directory: %v", err)
	}
	if _, err := ReadFileLimited(m, "/missing", 10); !errors.As(err, &pathErr) || pathErr.Op != "open" || !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("missing: %v", err)
	}
}

// A file that reports no size, such as a device, is cut off after limit+1 bytes rather
// than read to the end.
func TestReadFileLimited_StopsAFileThatNeverEnds(t *testing.T) {
	src := &endlessFile{}
	_, err := ReadFileLimited(endlessFS{NewMemoryFilesystem(), src}, "/endless", 1000)
	var tooLarge *FileTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Size != 1001 {
		t.Fatalf("err = %v", err)
	}
	if src.read > 1001 {
		t.Fatalf("read %d bytes, want at most 1001", src.read)
	}
}

// The size a file reports is checked before any of it is read.
func TestReadFileLimited_ChecksTheReportedSizeFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 1<<31+10); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFileLimited(NewLocalFilesystem(""), path, MaxReadFileSize)
	var tooLarge *FileTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Size != 1<<31+10 {
		t.Fatalf("err = %v", err)
	}
	if got, want := err.Error(), "File size (2147483658) is greater than 2 GiB"; got != want {
		t.Fatalf("message = %q, want Node's %q", got, want)
	}
}

func TestFileTooLargeError_NamesOrbitalsLimitBelowNodes(t *testing.T) {
	err := &FileTooLargeError{Size: 500 << 20, Limit: MaxReadFileSize}
	if got, want := err.Error(), "File size (524288000) is greater than 402653166 bytes"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// endlessFS serves one file that reports size 0 and never ends, as /dev/zero does.
type endlessFS struct {
	*MemoryFilesystem
	file *endlessFile
}

func (e endlessFS) Open(string) (File, error) { return e.file, nil }

type endlessFile struct{ read int }

func (f *endlessFile) Read(p []byte) (int, error) {
	clear(p)
	f.read += len(p)
	return len(p), nil
}
func (f *endlessFile) Write([]byte) (int, error)      { return 0, syscall.EBADF }
func (f *endlessFile) Seek(int64, int) (int64, error) { return 0, syscall.ESPIPE }
func (f *endlessFile) Close() error                   { return nil }
func (f *endlessFile) Stat() (*FileInfo, error)       { return &FileInfo{Name: "endless"}, nil }

// Reading a file of the size it reports allocates once, without doubling the buffer to
// read EOF.
func TestReadFileLimited_AllocatesOnce(t *testing.T) {
	m := NewMemoryFilesystem()
	const size = 8<<20 - 1
	if err := m.WriteFile("/f", make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := ReadFileLimited(m, "/f", MaxReadFileSize)
	if err != nil {
		t.Fatal(err)
	}
	if c := cap(data); c >= 2*size {
		t.Fatalf("buffer capacity %d for a %d-byte file", c, size)
	}
}
