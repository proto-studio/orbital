package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// While a link under the root keeps changing between a directory under the root and one
// beside it, every call through the link reaches the directory under the root or is refused:
// the path the check resolves is the path the call uses.
func TestLocalFS_ConcurrentLinkChanges(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	for _, d := range []string{filepath.Join(root, "in"), filepath.Join(base, "other")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "in", "f"), []byte("in"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "other", "f"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(root, "cur")
	if err := os.Symlink("in", cur); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			target := "in"
			if i%2 == 1 {
				target = "../other"
			}
			tmp := filepath.Join(root, "tmp")
			os.Remove(tmp)
			if err := os.Symlink(target, tmp); err == nil {
				os.Rename(tmp, cur)
			}
		}
	}()

	l := NewLocalFilesystem(root)
	var reached atomic.Int64
	deadline := time.Now().Add(2 * time.Second)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for time.Now().Before(deadline) {
				if data, err := l.ReadFile("/cur/f"); err == nil && string(data) == "other" {
					reached.Add(1)
				}
				if f, err := l.OpenFile("/cur/new", os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
					f.Close()
				}
				if entries, err := l.ReadDir("/cur"); err == nil {
					for _, e := range entries {
						if e.Name == "new" {
							if _, err := os.Stat(filepath.Join(base, "other", "new")); err == nil {
								reached.Add(1)
							}
						}
					}
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	wg.Wait()

	if n := reached.Load(); n > 0 {
		t.Errorf("%d calls through the changing link reached the directory beside the root", n)
	}
	if _, err := os.Stat(filepath.Join(base, "other", "new")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("other/new: %v; want it never made", err)
	}
}

// RemoveAll removes links it finds inside a directory, not what they lead to, and MkdirAll
// and Create work through links that stay under the root.
func TestLocalFS_WalkMakesAndRemoves(t *testing.T) {
	base := linkLayout(t)
	l := NewLocalFilesystem(filepath.Join(base, "root"))
	if err := l.MkdirAll("/tree/near-sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../other", filepath.Join(base, "root", "tree", "far")); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveAll("/tree"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "other", "f.txt")); err != nil {
		t.Errorf("other/f.txt: %v; want it kept", err)
	}
	if err := l.MkdirAll("/near/made/deeper", 0o755); err != nil {
		t.Errorf("MkdirAll through near: %v", err)
	}
	if info, err := os.Stat(filepath.Join(base, "root", "in", "made", "deeper")); err != nil || !info.IsDir() {
		t.Errorf("in/made/deeper = %v, %v", info, err)
	}
	f, err := l.Create("/nearabs/created")
	if err != nil {
		t.Fatalf("Create through nearabs: %v", err)
	}
	f.Close()
	if _, err := os.Stat(filepath.Join(base, "root", "in", "created")); err != nil {
		t.Errorf("in/created: %v", err)
	}
	if err := l.MkdirAll("/a.txt/x", 0o755); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("MkdirAll under a file = %v, want ENOTDIR", err)
	}
	if err := l.Remove("/"); err == nil {
		t.Error("Remove of the root succeeded")
	}
}
