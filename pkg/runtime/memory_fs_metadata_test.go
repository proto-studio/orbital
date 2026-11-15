package runtime

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"
)

// The in-memory filesystem changes a file's size, mode, owner and times, and checks access
// against its owner bits, through a path or an open handle.
func TestMemoryFilesystem_Metadata(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Truncate("/f", 3); err != nil {
		t.Fatal(err)
	}
	if data, _ := m.ReadFile("/f"); string(data) != "abc" {
		t.Errorf("after Truncate(3) = %q", data)
	}
	if err := m.Truncate("/f", 5); err != nil {
		t.Fatal(err)
	}
	if data, _ := m.ReadFile("/f"); string(data) != "abc\x00\x00" {
		t.Errorf("after Truncate(5) = %q", data)
	}
	if err := m.Chmod("/f", 0o400); err != nil {
		t.Fatal(err)
	}
	if err := m.Access("/f", 4); err != nil {
		t.Errorf("Access(R_OK) = %v", err)
	}
	if err := m.Access("/f", 2); !errors.Is(err, syscall.EACCES) {
		t.Errorf("Access(W_OK) = %v, want EACCES", err)
	}
	if err := m.Chown("/f", 5, 6); err != nil {
		t.Fatal(err)
	}
	if err := m.Chown("/f", -1, 7); err != nil {
		t.Fatal(err)
	}
	atime, mtime := time.Unix(100, 0), time.Unix(200, 0)
	if err := m.Chtimes("/f", atime, mtime); err != nil {
		t.Fatal(err)
	}
	info, err := m.Stat("/f")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode.Perm() != 0o400 || info.Uid != 5 || info.Gid != 7 || !info.AccessTime.Equal(atime) || !info.ModTime.Equal(mtime) {
		t.Errorf("Stat = %+v", info)
	}
	for name, err := range map[string]error{
		"Truncate": m.Truncate("/missing", 0), "Chmod": m.Chmod("/missing", 0),
		"Chown": m.Chown("/missing", 0, 0), "Chtimes": m.Chtimes("/missing", atime, mtime),
		"Access": m.Access("/missing", 0),
	} {
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s(missing) = %v, want ENOENT", name, err)
		}
	}

	h, err := m.OpenFile("/f", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := h.(MetadataFile)
	if err := file.Truncate(1); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.Stat("/f"); info.Size != 1 || info.Mode.Perm() != 0o600 {
		t.Errorf("after handle changes: %+v", info)
	}
	r, _ := m.OpenFile("/f", os.O_RDONLY, 0)
	if err := r.(MetadataFile).Truncate(0); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("Truncate through a read-only handle = %v, want EINVAL", err)
	}
}
