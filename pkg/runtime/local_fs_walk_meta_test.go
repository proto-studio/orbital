package runtime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The metadata calls walk paths as the other calls do: they work through links under the
// root and refuse a link to the directory beside it, leaving its files alone.
func TestLocalFS_MetadataThroughLinks(t *testing.T) {
	base := linkLayout(t)
	l := NewLocalFilesystem(filepath.Join(base, "root"))
	other := filepath.Join(base, "other", "f.txt")
	before, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Chmod("/nearfile", 0o600); err != nil {
		t.Errorf("Chmod nearfile: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(base, "root", "a.txt")); info.Mode().Perm() != 0o600 {
		t.Errorf("a.txt mode = %v, want 0600", info.Mode().Perm())
	}
	if err := l.Chown("/near/x.txt", os.Getuid(), os.Getgid()); err != nil {
		t.Errorf("Chown near/x.txt: %v", err)
	}
	when := time.Unix(1000, 0)
	if err := l.Chtimes("/nearabs/x.txt", when, when); err != nil {
		t.Errorf("Chtimes nearabs/x.txt: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(base, "root", "in", "x.txt")); !info.ModTime().Equal(when) {
		t.Errorf("in/x.txt mtime = %v", info.ModTime())
	}
	if err := l.Truncate("/near/x.txt", 0); err != nil {
		t.Errorf("Truncate near/x.txt: %v", err)
	}
	if err := l.Access("/near/x.txt", 4); err != nil {
		t.Errorf("Access near/x.txt: %v", err)
	}
	if st, err := l.Statfs("/near"); err != nil || st.Bsize <= 0 {
		t.Errorf("Statfs near = %+v, %v", st, err)
	}
	if info, err := l.Stat("/near/x.txt"); err != nil || !info.HasDetails || info.Ino == 0 {
		t.Errorf("Stat near/x.txt = %+v, %v; want the OS's details", info, err)
	}

	for name, err := range map[string]error{
		"Chmod":    l.Chmod("/farfile", 0o777),
		"Chown":    l.Chown("/far/f.txt", os.Getuid(), os.Getgid()),
		"Chtimes":  l.Chtimes("/farfile", when, when),
		"Truncate": l.Truncate("/far/f.txt", 0),
		"Access":   l.Access("/farfile", 0),
	} {
		if !errors.Is(err, ErrOutsideSandbox) || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s through far = %v, want the path check's EACCES", name, err)
		}
	}
	if _, err := l.Statfs("/far"); !errors.Is(err, ErrOutsideSandbox) {
		t.Errorf("Statfs far = %v, want the path check's EACCES", err)
	}
	after, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode() != before.Mode() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("other/f.txt changed: %v %d %v", after.Mode(), after.Size(), after.ModTime())
	}
}

// Truncate refuses a FIFO with EINVAL and a directory with EISDIR at once, without waiting
// for a reader.
func TestLocalFS_TruncateNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := NewLocalFilesystem(root)
	done := make(chan error, 1)
	go func() { done <- l.Truncate("/fifo", 0) }()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EINVAL) {
			t.Errorf("Truncate fifo = %v, want EINVAL", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Truncate of a FIFO waited for a reader")
	}
	if err := l.Truncate("/dir", 0); !errors.Is(err, syscall.EISDIR) {
		t.Errorf("Truncate dir = %v, want EISDIR", err)
	}
}
