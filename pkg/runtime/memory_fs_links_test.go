package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	goruntime "runtime"
	"strconv"
	"syscall"
	"testing"
)

func mustDo(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// No link leads out of the filesystem: ".." past the root stays at the root, and an absolute
// target names a path in the filesystem, never one on the host.
func TestMemoryFS_LinksStayInside(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "MkdirAll", m.MkdirAll("/d", 0o755))
	mustDo(t, "WriteFile", m.WriteFile("/etc/hosts", []byte("memory"), 0o644))
	mustDo(t, "Symlink up", m.Symlink("../../../../..", "/d/up"))
	mustDo(t, "Symlink abs", m.Symlink("/etc/hosts", "/d/hosts"))
	mustDo(t, "Symlink up hosts", m.Symlink("../../../../etc/hosts", "/d/uphosts"))

	for _, p := range []string{"/d/up", "/d/up/d/up/.."} {
		real, err := m.Realpath(p)
		if err != nil || real != "/" {
			t.Errorf("Realpath(%q) = %q, %v; want /", p, real, err)
		}
	}
	for _, p := range []string{"/d/hosts", "/d/uphosts", "/d/up/etc/hosts"} {
		data, err := m.ReadFile(p)
		if err != nil || string(data) != "memory" {
			t.Errorf("ReadFile(%q) = %q, %v; want the file in memory", p, data, err)
		}
	}
	// A write through a link to a path that doesn't exist makes it in memory.
	mustDo(t, "Symlink new", m.Symlink("../../new", "/d/new"))
	mustDo(t, "WriteFile new", m.WriteFile("/d/new", []byte("x"), 0o644))
	if !m.Exists("/new") {
		t.Errorf("writing through d/new didn't make /new")
	}
}

// Hard links share one file: its contents, mode and inode, with a count of its names that
// unlink, rename and RemoveAll keep.
func TestMemoryFS_HardLinks(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/a", []byte("one"), 0o644))
	mustDo(t, "Link", m.Link("/a", "/b"))
	mustDo(t, "MkdirAll", m.MkdirAll("/dir", 0o755))
	mustDo(t, "Link into dir", m.Link("/b", "/dir/c"))

	nlink := func(p string) uint64 {
		t.Helper()
		info, err := m.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%q): %v", p, err)
		}
		return info.Nlink
	}
	a, _ := m.Stat("/a")
	c, _ := m.Stat("/dir/c")
	if a.Ino == 0 || a.Ino != c.Ino || nlink("/a") != 3 {
		t.Errorf("a: ino %d, c: ino %d, nlink %d; want one inode with 3 names", a.Ino, c.Ino, nlink("/a"))
	}

	f, err := m.OpenFile("/dir/c", os.O_WRONLY|os.O_APPEND, 0)
	mustDo(t, "OpenFile", err)
	_, err = f.Write([]byte("+"))
	mustDo(t, "Write", err)
	if data, _ := m.ReadFile("/a"); string(data) != "one+" {
		t.Errorf("a = %q after writing through dir/c, want one+", data)
	}

	mustDo(t, "Rename onto another name", m.Rename("/a", "/b"))
	if !m.Exists("/a") || nlink("/b") != 3 {
		t.Errorf("renaming one name onto another changed them: a exists %v, nlink %d", m.Exists("/a"), nlink("/b"))
	}
	mustDo(t, "Remove", m.Remove("/a"))
	mustDo(t, "RemoveAll", m.RemoveAll("/dir"))
	if data, err := m.ReadFile("/b"); err != nil || string(data) != "one+" || nlink("/b") != 1 {
		t.Errorf("b = %q, %v with nlink %d; want one+ with one name", data, err, nlink("/b"))
	}
}

// Link treats a symbolic link as the platform's link(2) does: macOS links the file it leads
// to, Linux the link itself.
func TestMemoryFS_LinkOfASymlink(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/f", []byte("f"), 0o644))
	mustDo(t, "Symlink", m.Symlink("f", "/alias"))
	mustDo(t, "Link", m.Link("/alias", "/h"))
	info, err := m.Lstat("/h")
	mustDo(t, "Lstat", err)
	if got, want := info.Mode&fs.ModeSymlink != 0, goruntime.GOOS != "darwin"; got != want {
		t.Errorf("h is a symbolic link: %v, want %v on %s", got, want, goruntime.GOOS)
	}
	if data, err := m.ReadFile("/h"); err != nil || string(data) != "f" {
		t.Errorf("ReadFile(h) = %q, %v", data, err)
	}
}

// A path may pass through as many links as the OS allows, and one more is ELOOP.
func TestMemoryFS_LinkLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/l0", []byte("end"), 0o644))
	for i := 1; i <= maxSymlinks+1; i++ {
		mustDo(t, "Symlink", m.Symlink("l"+strconv.Itoa(i-1), "/l"+strconv.Itoa(i)))
	}
	if data, err := m.ReadFile("/l" + strconv.Itoa(maxSymlinks)); err != nil || string(data) != "end" {
		t.Errorf("through %d links: %q, %v", maxSymlinks, data, err)
	}
	if _, err := m.ReadFile("/l" + strconv.Itoa(maxSymlinks+1)); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("through %d links: %v, want ELOOP", maxSymlinks+1, err)
	}
}

// ReadDir reports a link as a link, and a handle opened through one reads and writes the file
// it leads to.
func TestMemoryFS_LinksInDirsAndHandles(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "MkdirAll", m.MkdirAll("/d/sub", 0o755))
	mustDo(t, "WriteFile", m.WriteFile("/d/f", []byte("abc"), 0o644))
	mustDo(t, "Symlink", m.Symlink("/d", "/top"))
	mustDo(t, "Symlink", m.Symlink("sub", "/d/subl"))

	entries, err := m.ReadDir("/top")
	mustDo(t, "ReadDir", err)
	kinds := map[string]string{}
	for _, e := range entries {
		switch {
		case e.Type&fs.ModeSymlink != 0:
			kinds[e.Name] = "link"
		case e.IsDir:
			kinds[e.Name] = "dir"
		default:
			kinds[e.Name] = "file"
		}
	}
	if kinds["f"] != "file" || kinds["sub"] != "dir" || kinds["subl"] != "link" || len(kinds) != 3 {
		t.Errorf("ReadDir(top) = %v", kinds)
	}

	f, err := m.OpenFile("/top/f", os.O_RDWR, 0)
	mustDo(t, "OpenFile", err)
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "abc" {
		t.Errorf("read through top/f: %q, %v", data, err)
	}
	info, err := f.Stat()
	if err != nil || info.Mode&fs.ModeSymlink != 0 || info.Size != 3 {
		t.Errorf("Stat of the handle: %+v, %v", info, err)
	}
}

// A "/" after a link to a file is ENOTDIR, as on Linux and current macOS, for stat and lstat
// alike; after a link to a directory it follows the link.
func TestMemoryFS_TrailingSlashAfterLink(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/f", []byte("f"), 0o644))
	mustDo(t, "MkdirAll", m.MkdirAll("/d", 0o755))
	mustDo(t, "Symlink", m.Symlink("f", "/lf"))
	mustDo(t, "Symlink", m.Symlink("d", "/ld"))
	if _, err := m.Stat("/lf/"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Stat(lf/) = %v, want ENOTDIR", err)
	}
	if _, err := m.Lstat("/lf/"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Lstat(lf/) = %v, want ENOTDIR", err)
	}
	if info, err := m.Lstat("/ld/"); err != nil || !info.IsDir {
		t.Errorf("Lstat(ld/) = %+v, %v; want the directory", info, err)
	}
}
