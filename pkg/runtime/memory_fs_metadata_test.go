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
	if err := m.Chmod("/f", 0o777|fs.ModeSticky|fs.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.Stat("/f"); info.Mode&(fs.ModeSticky|fs.ModeSetuid) != fs.ModeSticky|fs.ModeSetuid {
		t.Errorf("after chmod with special bits: %v", info.Mode)
	}
	if err := m.Chmod("/f", 0o644); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.Stat("/f"); info.Mode&(fs.ModeSticky|fs.ModeSetuid) != 0 {
		t.Errorf("after chmod clearing special bits: %v", info.Mode)
	}
	r, _ := m.OpenFile("/f", os.O_RDONLY, 0)
	if err := r.(MetadataFile).Truncate(0); !errors.Is(err, syscall.EINVAL) {
		t.Errorf("Truncate through a read-only handle = %v, want EINVAL", err)
	}
}

// A handle's metadata calls change the file it opened, even after that file is renamed and
// another takes its name.
func TestMemoryFilesystem_HandleKeepsItsFile(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/a", []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := m.OpenFile("/a", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/a", "/b"); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/a", []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := h.(MetadataFile)
	if err := file.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if info, _ := m.Stat("/b"); info.Mode.Perm() != 0o600 || info.Size != 2 {
		t.Errorf("/b = %v, %d; want 0600, 2 bytes", info.Mode.Perm(), info.Size)
	}
	if info, _ := m.Stat("/a"); info.Mode.Perm() != 0o644 || info.Size != 6 {
		t.Errorf("/a = %v, %d; want it unchanged", info.Mode.Perm(), info.Size)
	}
}

// WriteFile over an open file keeps that file, so the handle's calls change what the path
// names.
func TestMemoryFilesystem_WriteFileKeepsTheFile(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := m.OpenFile("/f", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/f", []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.(MetadataFile).Truncate(1); err != nil {
		t.Fatal(err)
	}
	if data, _ := m.ReadFile("/f"); string(data) != "r" {
		t.Errorf("/f = %q, want %q", data, "r")
	}
	if info, _ := m.Stat("/f"); info.Mode.Perm() != 0o644 {
		t.Errorf("/f mode = %v, want the original 0644", info.Mode.Perm())
	}
}

// Copy over an open file keeps that file, as WriteFile does.
func TestMemoryFilesystem_CopyKeepsTheFile(t *testing.T) {
	m := NewMemoryFilesystem()
	m.WriteFile("/src", []byte("abcdef"), 0o644)
	m.WriteFile("/dest", []byte("old"), 0o644)
	h, err := m.OpenFile("/dest", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Copy("/src", "/dest"); err != nil {
		t.Fatal(err)
	}
	if err := h.(MetadataFile).Truncate(1); err != nil {
		t.Fatal(err)
	}
	if data, _ := m.ReadFile("/dest"); string(data) != "a" {
		t.Errorf("/dest = %q, want %q", data, "a")
	}
}
