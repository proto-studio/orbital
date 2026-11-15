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

// The walk goes through a directory that can be searched but not listed, ends at the
// directory a link to "." leads to, keeps few descriptors however deep the path, leaves an
// existing link alone for an exclusive create, and removes what it can around a directory it
// can't empty.
func TestLocalFS_WalkDetails(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)

	if err := os.MkdirAll(filepath.Join(root, "search", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "search", "inner", "f"), []byte("found"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "search"), 0o111); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "search"), 0o755)
	if data, err := l.ReadFile("/search/inner/f"); err != nil || string(data) != "found" {
		t.Errorf("ReadFile through a search-only directory = %q, %v", data, err)
	}
	os.Chmod(filepath.Join(root, "search"), 0o755)

	if err := os.Symlink(".", filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "top")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/self", "/top", "/in/../self"} {
		if info, err := l.Stat(p); err != nil || !info.IsDir {
			t.Errorf("Stat %s = %+v, %v; want the root directory", p, info, err)
		}
		if entries, err := l.ReadDir(p); err != nil || !hasEntry(entries, "a.txt") {
			t.Errorf("ReadDir %s = %v, %v", p, entries, err)
		}
	}

	deep := "/deep"
	for range 200 {
		deep += "/d"
	}
	if err := l.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteFile(deep+"/f", []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, err := l.ReadFile(deep + "/../d/f"); err != nil || string(data) != "deep" {
		t.Errorf("ReadFile deep = %q, %v", data, err)
	}

	if err := os.Symlink("missing-target", filepath.Join(root, "dangling-in")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.OpenFile("/dangling-in", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); !errors.Is(err, os.ErrExist) {
		t.Errorf("exclusive create over a link = %v, want EEXIST", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "missing-target")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing-target was made: %v", err)
	}

	if os.Geteuid() != 0 {
		locked := filepath.Join(root, "rm", "a-locked")
		if err := os.MkdirAll(filepath.Join(locked, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "rm", "b-file"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chmod(locked, 0o555)
		defer os.Chmod(locked, 0o755)
		if err := l.RemoveAll("/rm"); err == nil {
			t.Error("RemoveAll of a tree with a locked directory succeeded")
		}
		if _, err := os.Lstat(filepath.Join(root, "rm", "b-file")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("b-file beside the locked directory was kept: %v", err)
		}
	}
}

// A link whose target ends in "/" must lead to a directory, O_NOFOLLOW asked for by the
// caller refuses a final link, and RemoveAll removes an empty directory it can't open.
func TestLocalFS_WalkFlags(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	for name, target := range map[string]string{"slash-file": "a.txt/", "slash-missing": "missing/", "dot-file": "a.txt/."} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.ReadFile("/slash-file"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("ReadFile through a link to file/ = %v, want ENOTDIR", err)
	}
	if _, err := l.ReadFile("/dot-file"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("ReadFile through a link to file/. = %v, want ENOTDIR", err)
	}
	if err := l.WriteFile("/slash-missing", []byte("x"), 0o644); err == nil {
		t.Error("WriteFile through a link to missing/ succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing was made: %v", err)
	}
	if _, err := l.ReadDir("/near"); err != nil {
		t.Errorf("ReadDir near: %v", err)
	}

	if _, err := l.OpenFile("/nearfile", os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("O_NOFOLLOW open of a link = %v, want ELOOP", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "a" {
		t.Errorf("a.txt = %q, want it kept", data)
	}
	if f, err := l.OpenFile("/a.txt", os.O_RDONLY|syscall.O_NOFOLLOW, 0); err != nil {
		t.Errorf("O_NOFOLLOW open of a file: %v", err)
	} else {
		f.Close()
	}

	if os.Geteuid() != 0 {
		if err := os.MkdirAll(filepath.Join(root, "rm2", "closed"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Chmod(filepath.Join(root, "rm2", "closed"), 0)
		defer os.Chmod(filepath.Join(root, "rm2", "closed"), 0o755)
		if err := l.RemoveAll("/rm2"); err != nil {
			t.Errorf("RemoveAll with an empty mode-0 directory: %v", err)
		}
	}
}

// Stat follows a final link even when one replaces the file while the walk runs.
func TestLocalFS_StatWhileNameChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target"), []byte("t"), 0o644); err != nil {
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
			tmp := filepath.Join(root, "tmp")
			os.Remove(tmp)
			if i%2 == 0 {
				os.Symlink("target", tmp)
			} else {
				os.WriteFile(tmp, []byte("file"), 0o644)
			}
			os.Rename(tmp, filepath.Join(root, "name"))
		}
	}()
	l := NewLocalFilesystem(root)
	links := 0
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if info, err := l.Stat("/name"); err == nil && info.Mode&os.ModeSymlink != 0 {
			links++
		}
	}
	close(stop)
	wg.Wait()
	if links > 0 {
		t.Errorf("Stat described the link itself %d times", links)
	}
}

// MkdirAll doesn't make a missing directory a link names, and ReadDir lists a directory that
// can be read but not searched.
func TestLocalFS_WalkMakesOnlyNamedDirectories(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	if err := os.Symlink("missing-dir", filepath.Join(root, "to-missing")); err != nil {
		t.Fatal(err)
	}
	if err := l.MkdirAll("/to-missing", 0o755); err == nil {
		t.Error("MkdirAll of a link to a missing directory succeeded")
	}
	if err := l.MkdirAll("/to-missing/sub", 0o755); err == nil {
		t.Error("MkdirAll under a link to a missing directory succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "missing-dir")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing-dir was made: %v", err)
	}
	if err := l.MkdirAll("/near/through-link", 0o755); err != nil {
		t.Errorf("MkdirAll under a link to a directory: %v", err)
	}

	if os.Geteuid() != 0 {
		listed := filepath.Join(root, "listed")
		if err := os.MkdirAll(filepath.Join(listed, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(listed, "f"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chmod(listed, 0o444)
		defer os.Chmod(listed, 0o755)
		entries, err := l.ReadDir("/listed")
		if err != nil || len(entries) != 2 || !entries[1].IsDir || entries[0].IsDir {
			t.Errorf("ReadDir of a read-only directory = %+v, %v", entries, err)
		}
	}
}

// An absolute link target ending in "/" that reaches the root through a link outside it
// names the directory once, and closing an opened FIFO ends a read waiting on it.
func TestLocalFS_WalkTrailingSlashAndFIFO(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "f"), []byte("sub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "rootlink", "sub")+"/", filepath.Join(root, "via-alias")); err != nil {
		t.Fatal(err)
	}
	if data, err := l.ReadFile("/via-alias/f"); err != nil || string(data) != "sub" {
		t.Errorf("ReadFile via-alias/f = %q, %v", data, err)
	}

	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	f, err := l.OpenFile("/fifo", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.Read(make([]byte, 1))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	f.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Error("read returned data")
		}
	case <-time.After(2 * time.Second):
		t.Error("closing the FIFO didn't end the waiting read")
	}
}

// An absolute link target with ".." after a link resolves that link first, as the OS does,
// and Copy writes over a destination it may write but not read.
func TestLocalFS_WalkDotDotAfterLinkAndWriteOnlyCopy(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "f"), []byte("a/f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/b", filepath.Join(root, "jump")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base+"/rootlink/jump/../f", filepath.Join(root, "dotdot")); err != nil {
		t.Fatal(err)
	}
	if data, err := l.ReadFile("/dotdot"); err != nil || string(data) != "a/f" {
		t.Errorf("ReadFile dotdot = %q, %v; want a/f", data, err)
	}

	if os.Geteuid() != 0 {
		dst := filepath.Join(root, "write-only")
		if err := os.WriteFile(dst, []byte("old"), 0o200); err != nil {
			t.Fatal(err)
		}
		if err := l.Copy("/a.txt", "/write-only"); err != nil {
			t.Errorf("Copy onto a write-only file: %v", err)
		}
	}
}

// A missing name before ".." in an absolute link target is still looked up, MkdirAll makes
// a root that doesn't exist yet, and Mkdir keeps the sticky bit.
func TestLocalFS_WalkMissingNamesRootAndSticky(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	if err := os.Symlink(base+"/rootlink/missing/../a.txt", filepath.Join(root, "through-missing")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadFile("/through-missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile through a missing name and .. = %v, want ENOENT", err)
	}

	fresh := NewLocalFilesystem(filepath.Join(base, "new-root"))
	if err := fresh.MkdirAll("/sub", 0o755); err != nil {
		t.Errorf("MkdirAll with a missing root: %v", err)
	}
	if info, err := os.Stat(filepath.Join(base, "new-root", "sub")); err != nil || !info.IsDir() {
		t.Errorf("new-root/sub = %v, %v", info, err)
	}

	if err := l.Mkdir("/sticky", 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(root, "sticky")); info.Mode()&os.ModeSticky == 0 {
		t.Errorf("sticky mode = %v, want the sticky bit", info.Mode())
	}
}

// An absolute link target that is itself a link into the root is followed into it.
func TestLocalFS_WalkTargetThatIsAnAlias(t *testing.T) {
	base := linkLayout(t)
	root := filepath.Join(base, "root")
	l := NewLocalFilesystem(root)
	if err := os.Symlink(filepath.Join(base, "rootlink"), filepath.Join(root, "via-rootlink")); err != nil {
		t.Fatal(err)
	}
	if data, err := l.ReadFile("/via-rootlink/a.txt"); err != nil || string(data) != "a" {
		t.Errorf("ReadFile via-rootlink/a.txt = %q, %v", data, err)
	}
}
