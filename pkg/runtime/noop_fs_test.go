package runtime

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// Every NoOpFilesystem method fails with EACCES, and Exists reports nothing.
func TestNoOpFilesystemRefusesEverything(t *testing.T) {
	n := NewNoOpFilesystem()
	calls := map[string]func() error{
		"ReadFile":   func() error { _, err := n.ReadFile("/f"); return err },
		"Stat":       func() error { _, err := n.Stat("/f"); return err },
		"Lstat":      func() error { _, err := n.Lstat("/f"); return err },
		"ReadDir":    func() error { _, err := n.ReadDir("/"); return err },
		"WriteFile":  func() error { return n.WriteFile("/f", []byte("x"), 0o644) },
		"AppendFile": func() error { return n.AppendFile("/f", []byte("x")) },
		"Mkdir":      func() error { return n.Mkdir("/d", 0o755) },
		"MkdirAll":   func() error { return n.MkdirAll("/d/e", 0o755) },
		"Remove":     func() error { return n.Remove("/f") },
		"RemoveAll":  func() error { return n.RemoveAll("/d") },
		"Rename":     func() error { return n.Rename("/f", "/g") },
		"Copy":       func() error { return n.Copy("/f", "/g") },
		"Open":       func() error { _, err := n.Open("/f"); return err },
		"Create":     func() error { _, err := n.Create("/f"); return err },
		"OpenFile":   func() error { _, err := n.OpenFile("/f", os.O_RDWR|os.O_CREATE, 0o644); return err },
		"Truncate":   func() error { return n.Truncate("/f", 0) },
		"Chmod":      func() error { return n.Chmod("/f", 0o644) },
		"Chown":      func() error { return n.Chown("/f", 1, 1) },
		"Chtimes":    func() error { return n.Chtimes("/f", time.Now(), time.Now()) },
		"Access":     func() error { return n.Access("/f", 0) },
		"Symlink":    func() error { return n.Symlink("/t", "/f") },
		"Readlink":   func() error { _, err := n.Readlink("/f"); return err },
		"Link":       func() error { return n.Link("/f", "/g") },
		"Realpath":   func() error { _, err := n.Realpath("/f"); return err },
		"Statfs":     func() error { _, err := n.Statfs("/"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, syscall.EACCES) {
			t.Errorf("%s = %v, want EACCES", name, err)
		}
	}
	if n.Exists("/") {
		t.Error("Exists(/) = true")
	}
}
