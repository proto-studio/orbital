package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// Paths, names and link targets past the OS's limits are ENAMETOOLONG, as on disk.
func TestMemoryFS_NameTooLong(t *testing.T) {
	m := NewMemoryFilesystem()
	long := "/" + strings.Repeat("./", pathMax/2)
	if _, err := m.Stat(long); !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("Stat of a %d-byte path: %v, want ENAMETOOLONG", len(long), err)
	}
	if _, err := m.Stat("/" + strings.Repeat("./", (pathMax-2)/2)); err == nil || errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("Stat of a path under the limit: %v, want the root's own result", err)
	}
	if err := m.WriteFile("/"+strings.Repeat("n", nameMax+1), nil, 0o644); !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("WriteFile of a %d-byte name: %v, want ENAMETOOLONG", nameMax+1, err)
	}
	mustDo(t, "WriteFile", m.WriteFile("/"+strings.Repeat("n", nameMax), nil, 0o644))
	if err := m.Symlink(strings.Repeat("a", pathMax), "/l"); !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Errorf("Symlink to a %d-byte target: %v, want ENAMETOOLONG", pathMax, err)
	}
}

// Resolving a path with as many names as the limit allows takes time linear in it, so a
// script can't stall a call with one.
func TestMemoryFS_LongPathsResolveQuickly(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "MkdirAll", m.MkdirAll("/a", 0o755))
	mustDo(t, "Symlink", m.Symlink("a/"+strings.Repeat("./", (pathMax-8)/2), "/l"))
	path := "/" + strings.Repeat("a/../", (pathMax-8)/5) + "l"
	start := time.Now()
	for range 1000 {
		if _, err := m.Stat(path); err != nil {
			t.Fatalf("Stat(%d-byte path): %v", len(path), err)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("1000 lookups of a %d-byte path took %v", len(path), elapsed)
	}
}

// unlink and rename take a "/" after a link to a file as Linux and current macOS do: the name
// isn't a directory, so the call fails and leaves both.
func TestMemoryFS_SlashAfterFileLinkIsKept(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/f", []byte("f"), 0o644))
	mustDo(t, "Symlink", m.Symlink("f", "/lf"))
	if err := m.Remove("/lf/"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Remove(lf/) = %v, want ENOTDIR", err)
	}
	if err := m.Rename("/lf/", "/moved"); !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Rename(lf/) = %v, want ENOTDIR", err)
	}
	if !m.Exists("/f") || !m.Exists("/lf") || m.Exists("/moved") {
		t.Errorf("f %v, lf %v, moved %v; want f and lf kept", m.Exists("/f"), m.Exists("/lf"), m.Exists("/moved"))
	}
}

// A new link's name with a "/" after it can't be made. If the name is a link already, macOS
// follows it, so one that leads nowhere is ENOENT and one to a file ENOTDIR; Linux takes the
// name as taken.
func TestMemoryFS_NewLinkNameWithSlash(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/f", []byte("f"), 0o644))
	mustDo(t, "Symlink", m.Symlink("f", "/lf"))
	mustDo(t, "Symlink", m.Symlink("nowhere", "/dangling"))
	if err := m.Symlink("x", "/new/"); !errors.Is(err, syscall.ENOENT) || m.Exists("/new") {
		t.Errorf("Symlink(new/) = %v, want ENOENT and nothing made", err)
	}
	if err := m.Link("/f", "/new/"); !errors.Is(err, syscall.ENOENT) || m.Exists("/new") {
		t.Errorf("Link(new/) = %v, want ENOENT and nothing made", err)
	}
	dangling, file := syscall.EEXIST, syscall.EEXIST
	if goruntime.GOOS == "darwin" {
		dangling, file = syscall.ENOENT, syscall.ENOTDIR
	}
	if err := m.Symlink("x", "/dangling/"); !errors.Is(err, dangling) || m.Exists("/nowhere") {
		t.Errorf("Symlink(dangling/) = %v, want %v and nothing made", err, dangling)
	}
	if err := m.Link("/f", "/lf/"); !errors.Is(err, file) {
		t.Errorf("Link(lf/) = %v, want %v", err, file)
	}
}

// RemoveAll on a link with a "/" after it does what Node's recursive rm does there: on Linux
// rmdir refuses it with ENOTDIR and nothing goes; macOS follows it and removes the tree it
// leads to, leaving the link.
func TestMemoryFS_RemoveAllSlashAfterLink(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "WriteFile", m.WriteFile("/target/x", []byte("x"), 0o644))
	mustDo(t, "Symlink", m.Symlink("target", "/link"))
	err := m.RemoveAll("/link/")
	_, linkErr := m.Lstat("/link")
	if goruntime.GOOS == "darwin" {
		if err != nil || m.Exists("/target/x") || linkErr != nil {
			t.Errorf("RemoveAll(link/) = %v; target/x kept %v, link err %v; want the tree gone and the link kept", err, m.Exists("/target/x"), linkErr)
		}
		return
	}
	if !errors.Is(err, syscall.ENOTDIR) || !m.Exists("/target/x") || linkErr != nil {
		t.Errorf("RemoveAll(link/) = %v; target/x kept %v, link err %v; want ENOTDIR and both kept", err, m.Exists("/target/x"), linkErr)
	}
	mustDo(t, "RemoveAll(link)", m.RemoveAll("/link"))
	if !m.Exists("/target/x") {
		t.Errorf("RemoveAll(link) removed what the link leads to")
	}
}

// A missing name with a "/" after it, given or from a link's target, can only be a directory,
// so making a file there fails and makes nothing: EISDIR on Linux, ENOENT on macOS.
func TestMemoryFS_FileWhereOnlyADirectoryFits(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "Symlink", m.Symlink("missing/", "/ds"))
	mustDo(t, "WriteFile", m.WriteFile("/src", []byte("s"), 0o644))
	want := syscall.EISDIR
	if goruntime.GOOS == "darwin" {
		want = syscall.ENOENT
	}
	for _, p := range []string{"/ds", "/new/"} {
		if err := m.WriteFile(p, []byte("x"), 0o644); !errors.Is(err, want) {
			t.Errorf("WriteFile(%s) = %v, want %v", p, err, want)
		}
		if err := m.AppendFile(p, []byte("x")); !errors.Is(err, want) {
			t.Errorf("AppendFile(%s) = %v, want %v", p, err, want)
		}
		if _, err := m.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o644); !errors.Is(err, want) {
			t.Errorf("OpenFile(%s, O_CREATE) = %v, want %v", p, err, want)
		}
		if err := m.Copy("/src", p); !errors.Is(err, want) {
			t.Errorf("Copy(src, %s) = %v, want %v", p, err, want)
		}
	}
	renameWant := syscall.ENOTDIR
	if goruntime.GOOS == "darwin" {
		renameWant = syscall.ENOENT
	}
	if err := m.Rename("/src", "/new/"); !errors.Is(err, renameWant) {
		t.Errorf("Rename(src, new/) = %v, want %v", err, renameWant)
	}
	mustDo(t, "MkdirAll", m.MkdirAll("/dir", 0o755))
	mustDo(t, "Rename dir", m.Rename("/dir", "/moved/"))
	if info, err := m.Stat("/moved"); err != nil || !info.IsDir {
		t.Errorf("Stat(moved) = %+v, %v; want the directory renamed", info, err)
	}
	if m.Exists("/missing") || m.Exists("/new") {
		t.Errorf("a file was made: missing %v, new %v", m.Exists("/missing"), m.Exists("/new"))
	}
}

// MkdirAll("/") makes the root, so it can be described and opened.
func TestMemoryFS_MkdirAllRoot(t *testing.T) {
	m := NewMemoryFilesystem()
	mustDo(t, "MkdirAll", m.MkdirAll("/", 0o755))
	if info, err := m.Stat("/"); err != nil || !info.IsDir {
		t.Errorf("Stat(/) = %+v, %v; want a directory", info, err)
	}
	f, err := m.Open("/")
	mustDo(t, "Open(/)", err)
	f.Close()
}
