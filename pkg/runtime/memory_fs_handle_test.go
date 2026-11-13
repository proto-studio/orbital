package runtime

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// Opening an existing file without O_TRUNC and closing it leaves its contents alone.
func TestMemoryFilesystemOpenKeepsContents(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []int{os.O_RDWR, os.O_WRONLY, os.O_RDWR | os.O_APPEND, os.O_RDONLY} {
		f, err := m.OpenFile("/f", flag, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if data, _ := m.ReadFile("/f"); string(data) != "keep" {
			t.Fatalf("flag %#x left %q", flag, data)
		}
	}
}

// A directory can't be opened for writing, so appending to one fails and leaves it a directory.
func TestMemoryFilesystemWriteToDirectory(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OpenFile("/d", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
		t.Fatal("opened a directory for writing")
	}
	if info, err := m.Stat("/d"); err != nil || !info.IsDir {
		t.Fatalf("/d is no longer a directory: %v %v", info, err)
	}
}

// An empty write past the end changes nothing; a non-empty one fills the gap with zeros.
func TestMemoryFilesystemWritePastEnd(t *testing.T) {
	m := NewMemoryFilesystem()
	f, err := m.OpenFile("/f", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(4096, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := m.ReadFile("/f"); len(data) != 0 {
		t.Fatalf("empty write left %d bytes", len(data))
	}
	f.Seek(3, io.SeekStart)
	f.Write([]byte("x"))
	if data, _ := m.ReadFile("/f"); !bytes.Equal(data, []byte{0, 0, 0, 'x'}) {
		t.Fatalf("got %q", data)
	}
	// Overwriting inside the file and running past its end keeps what came before.
	f.Seek(2, io.SeekStart)
	f.Write([]byte("abc"))
	if data, _ := m.ReadFile("/f"); !bytes.Equal(data, []byte{0, 0, 'a', 'b', 'c'}) {
		t.Fatalf("got %q", data)
	}
}

// Writing a file in chunks allocates in proportion to its size, not its size squared.
func TestMemoryFilesystemSequentialWritesGrowGeometrically(t *testing.T) {
	m := NewMemoryFilesystem()
	f, err := m.OpenFile("/big", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 64<<10)
	const size = 16 << 20
	allocs := testing.AllocsPerRun(1, func() {
		f.Seek(0, io.SeekStart)
		m.files["/big"].data = nil
		for written := 0; written < size; written += len(chunk) {
			f.Write(chunk)
		}
	})
	if allocs > 64 {
		t.Fatalf("%v allocations writing %d bytes in %d-byte chunks", allocs, size, len(chunk))
	}
}
