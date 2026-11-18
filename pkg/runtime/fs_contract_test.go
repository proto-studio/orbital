package runtime

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A contractFS is a Filesystem under test, with dir an empty directory in it to work in.
type contractFS struct {
	fs  Filesystem
	dir string
}

func (c contractFS) path(name string) string { return c.dir + "/" + name }

// The Filesystem implementations that keep files, each made empty for every check.
var contractFilesystems = map[string]func(t *testing.T) contractFS{
	"memory": func(t *testing.T) contractFS {
		m := NewMemoryFilesystem()
		mustDo(t, "MkdirAll", m.MkdirAll("/work", 0o755))
		return contractFS{m, "/work"}
	},
	"local": func(t *testing.T) contractFS {
		return contractFS{NewLocalFilesystem(""), t.TempDir()}
	},
	"local rooted": func(t *testing.T) contractFS {
		l := NewLocalFilesystem(t.TempDir())
		mustDo(t, "MkdirAll", l.MkdirAll("/work", 0o755))
		return contractFS{l, "/work"}
	},
}

// runContract runs check against every implementation in contractFilesystems.
func runContract(t *testing.T, check func(t *testing.T, c contractFS)) {
	for name, newFS := range contractFilesystems {
		t.Run(name, func(t *testing.T) { check(t, newFS(t)) })
	}
}

// errnoIn is the errno err carries, as the fs module reads it: a syscall.Errno if there is
// one, and otherwise the errno for io/fs's ErrNotExist, ErrExist or ErrPermission.
func errnoIn(err error) syscall.Errno {
	var errno syscall.Errno
	switch {
	case errors.As(err, &errno):
		return errno
	case errors.Is(err, fs.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, fs.ErrExist):
		return syscall.EEXIST
	case errors.Is(err, fs.ErrPermission):
		return syscall.EACCES
	}
	return 0
}

// wantErrno fails t unless err carries errno.
func wantErrno(t *testing.T, what string, err error, errno syscall.Errno) {
	t.Helper()
	if errnoIn(err) != errno {
		t.Errorf("%s: err = %v, want %v", what, err, errno)
	}
}

func wantContents(t *testing.T, fsys Filesystem, p, want string) {
	t.Helper()
	data, err := fsys.ReadFile(p)
	if err != nil || string(data) != want {
		t.Errorf("ReadFile(%s) = %q, %v; want %q", p, data, err, want)
	}
}

func statOf(t *testing.T, fsys Filesystem, p string) *FileInfo {
	t.Helper()
	info, err := fsys.Stat(p)
	if err != nil {
		t.Fatalf("Stat(%s): %v", p, err)
	}
	return info
}

func TestFilesystemContract_ReadWrite(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		f := c.path("f")
		mustDo(t, "WriteFile", c.fs.WriteFile(f, []byte("hello"), 0o644))
		wantContents(t, c.fs, f, "hello")
		mustDo(t, "WriteFile again", c.fs.WriteFile(f, []byte("hi"), 0o644))
		wantContents(t, c.fs, f, "hi")
		mustDo(t, "AppendFile", c.fs.AppendFile(f, []byte("!")))
		wantContents(t, c.fs, f, "hi!")
		mustDo(t, "AppendFile new", c.fs.AppendFile(c.path("g"), []byte("new")))
		wantContents(t, c.fs, c.path("g"), "new")

		info := statOf(t, c.fs, f)
		if info.IsDir || info.Size != 3 || !info.Mode.IsRegular() || info.Name != "f" {
			t.Errorf("Stat(f) = %+v, want a regular file f of 3 bytes", info)
		}
		if !c.fs.Exists(f) || c.fs.Exists(c.path("missing")) {
			t.Errorf("Exists: f %v, missing %v", c.fs.Exists(f), c.fs.Exists(c.path("missing")))
		}

		_, err := c.fs.ReadFile(c.path("missing"))
		wantErrno(t, "ReadFile missing", err, syscall.ENOENT)
		_, err = c.fs.Stat(c.path("missing"))
		wantErrno(t, "Stat missing", err, syscall.ENOENT)
		_, err = c.fs.ReadFile(c.dir)
		wantErrno(t, "ReadFile dir", err, syscall.EISDIR)
		_, err = c.fs.Stat(c.path("f/x"))
		wantErrno(t, "Stat through a file", err, syscall.ENOTDIR)
		wantErrno(t, "WriteFile through a file", c.fs.WriteFile(c.path("f/x"), nil, 0o644), syscall.ENOTDIR)
		wantErrno(t, "WriteFile onto a dir", c.fs.WriteFile(c.dir, nil, 0o644), syscall.EISDIR)
		wantErrno(t, "AppendFile onto a dir", c.fs.AppendFile(c.dir, []byte("x")), syscall.EISDIR)
	})
}

func TestFilesystemContract_Directories(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		mustDo(t, "Mkdir", c.fs.Mkdir(c.path("d"), 0o755))
		wantErrno(t, "Mkdir existing", c.fs.Mkdir(c.path("d"), 0o755), syscall.EEXIST)
		mustDo(t, "MkdirAll", c.fs.MkdirAll(c.path("d/e/f"), 0o755))
		mustDo(t, "MkdirAll existing", c.fs.MkdirAll(c.path("d/e"), 0o755))
		mustDo(t, "WriteFile", c.fs.WriteFile(c.path("d/file"), []byte("x"), 0o644))
		wantErrno(t, "MkdirAll through a file", c.fs.MkdirAll(c.path("d/file/x"), 0o755), syscall.ENOTDIR)
		if info := statOf(t, c.fs, c.path("d/e/f")); !info.IsDir || !info.Mode.IsDir() {
			t.Errorf("Stat(d/e/f) = %+v, want a directory", info)
		}

		entries, err := c.fs.ReadDir(c.path("d"))
		mustDo(t, "ReadDir", err)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name)
			if e.IsDir != (e.Name == "e") {
				t.Errorf("entry %s: IsDir %v", e.Name, e.IsDir)
			}
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"e", "file"}) {
			t.Errorf("ReadDir(d) = %v, want [e file]", names)
		}
		_, err = c.fs.ReadDir(c.path("d/file"))
		wantErrno(t, "ReadDir file", err, syscall.ENOTDIR)
		_, err = c.fs.ReadDir(c.path("missing"))
		wantErrno(t, "ReadDir missing", err, syscall.ENOENT)

		wantErrno(t, "Remove non-empty dir", c.fs.Remove(c.path("d")), syscall.ENOTEMPTY)
		mustDo(t, "Remove empty dir", c.fs.Remove(c.path("d/e/f")))
		mustDo(t, "Remove file", c.fs.Remove(c.path("d/file")))
		wantErrno(t, "Remove missing", c.fs.Remove(c.path("d/file")), syscall.ENOENT)
		mustDo(t, "RemoveAll", c.fs.RemoveAll(c.path("d")))
		mustDo(t, "RemoveAll missing", c.fs.RemoveAll(c.path("d")))
		if c.fs.Exists(c.path("d")) {
			t.Error("d exists after RemoveAll")
		}
	})
}

func TestFilesystemContract_RenameAndCopy(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		mustDo(t, "WriteFile a", c.fs.WriteFile(c.path("a"), []byte("A"), 0o644))
		mustDo(t, "WriteFile b", c.fs.WriteFile(c.path("b"), []byte("B"), 0o644))
		mustDo(t, "Rename onto b", c.fs.Rename(c.path("a"), c.path("b")))
		wantContents(t, c.fs, c.path("b"), "A")
		if c.fs.Exists(c.path("a")) {
			t.Error("a exists after Rename")
		}
		wantErrno(t, "Rename missing", c.fs.Rename(c.path("a"), c.path("c")), syscall.ENOENT)

		mustDo(t, "Copy", c.fs.Copy(c.path("b"), c.path("c")))
		mustDo(t, "WriteFile c", c.fs.WriteFile(c.path("c"), []byte("C"), 0o644))
		wantContents(t, c.fs, c.path("b"), "A")
		wantContents(t, c.fs, c.path("c"), "C")
		wantErrno(t, "Copy missing", c.fs.Copy(c.path("a"), c.path("d")), syscall.ENOENT)
	})
}

func TestFilesystemContract_Handles(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		p := c.path("h")
		f, err := c.fs.Create(p)
		mustDo(t, "Create", err)
		_, err = f.Write([]byte("abcdef"))
		mustDo(t, "Write", err)
		_, err = f.Seek(2, io.SeekStart)
		mustDo(t, "Seek", err)
		_, err = f.Write([]byte("XY"))
		mustDo(t, "Write at 2", err)
		info, err := f.Stat()
		mustDo(t, "File.Stat", err)
		if info.Size != 6 {
			t.Errorf("File.Stat size = %d, want 6", info.Size)
		}
		mustDo(t, "Close", f.Close())
		wantContents(t, c.fs, p, "abXYef")

		f, err = c.fs.Open(p)
		mustDo(t, "Open", err)
		buf := make([]byte, 3)
		_, err = f.Seek(3, io.SeekStart)
		mustDo(t, "Seek", err)
		if n, err := io.ReadFull(f, buf); err != nil || string(buf[:n]) != "Yef" {
			t.Errorf("Read at 3 = %q, %v", buf[:n], err)
		}
		if n, err := f.Read(buf); n != 0 || err != io.EOF {
			t.Errorf("Read at end = %d, %v; want EOF", n, err)
		}
		mustDo(t, "Close", f.Close())

		f, err = c.fs.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
		mustDo(t, "OpenFile append", err)
		_, err = f.Write([]byte("!"))
		mustDo(t, "Write append", err)
		mustDo(t, "Close", f.Close())
		wantContents(t, c.fs, p, "abXYef!")

		f, err = c.fs.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
		mustDo(t, "OpenFile trunc", err)
		mustDo(t, "Close", f.Close())
		wantContents(t, c.fs, p, "")

		// Each open file has its own offset.
		mustDo(t, "WriteFile", c.fs.WriteFile(p, []byte("abcdef"), 0o644))
		one, err := c.fs.Open(p)
		mustDo(t, "Open one", err)
		two, err := c.fs.Open(p)
		mustDo(t, "Open two", err)
		_, err = io.ReadFull(one, buf[:2])
		mustDo(t, "Read one", err)
		if n, err := io.ReadFull(two, buf); err != nil || string(buf[:n]) != "abc" {
			t.Errorf("Read two = %q, %v; want its own offset", buf[:n], err)
		}
		mustDo(t, "Close one", one.Close())
		mustDo(t, "Close two", two.Close())

		// A directory opens, and reading it fails.
		d, err := c.fs.Open(c.dir)
		mustDo(t, "Open dir", err)
		_, err = d.Read(buf)
		wantErrno(t, "Read dir", err, syscall.EISDIR)
		mustDo(t, "Close dir", d.Close())

		// A handle opened for reading refuses writes, and no offset comes before the start.
		r, err := c.fs.OpenFile(p, os.O_RDONLY, 0)
		mustDo(t, "OpenFile read-only", err)
		_, err = r.Write([]byte("x"))
		wantErrno(t, "Write read-only", err, syscall.EBADF)
		_, err = r.Seek(-1, io.SeekStart)
		wantErrno(t, "Seek before the start", err, syscall.EINVAL)
		mustDo(t, "Close read-only", r.Close())
		wantContents(t, c.fs, p, "abcdef")

		_, err = c.fs.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		wantErrno(t, "OpenFile excl on existing", err, syscall.EEXIST)
		_, err = c.fs.Open(c.path("missing"))
		wantErrno(t, "Open missing", err, syscall.ENOENT)
		_, err = c.fs.OpenFile(c.path("missing"), os.O_RDWR, 0)
		wantErrno(t, "OpenFile missing", err, syscall.ENOENT)
	})
}

func TestFilesystemContract_Metadata(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		m, ok := c.fs.(MetadataFilesystem)
		if !ok {
			t.Fatal("not a MetadataFilesystem")
		}
		p := c.path("m")
		mustDo(t, "WriteFile", c.fs.WriteFile(p, []byte("abc"), 0o644))
		mustDo(t, "Truncate grow", m.Truncate(p, 5))
		wantContents(t, c.fs, p, "abc\x00\x00")
		mustDo(t, "Truncate shrink", m.Truncate(p, 1))
		wantContents(t, c.fs, p, "a")
		mustDo(t, "Chmod", m.Chmod(p, 0o600))
		if got := statOf(t, c.fs, p).Mode.Perm(); got != 0o600 {
			t.Errorf("mode after Chmod = %o, want 600", got)
		}
		mtime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
		mustDo(t, "Chtimes", m.Chtimes(p, mtime, mtime))
		if got := statOf(t, c.fs, p).ModTime; !got.Equal(mtime) {
			t.Errorf("mtime after Chtimes = %v, want %v", got, mtime)
		}
		mustDo(t, "Access", m.Access(p, 4|2))
		wantErrno(t, "Access missing", m.Access(c.path("missing"), 0), syscall.ENOENT)
		wantErrno(t, "Truncate missing", m.Truncate(c.path("missing"), 0), syscall.ENOENT)
		wantErrno(t, "Truncate a dir", m.Truncate(c.dir, 0), syscall.EISDIR)
		wantErrno(t, "Truncate to a negative size", m.Truncate(p, -1), syscall.EINVAL)
		wantErrno(t, "Chmod missing", m.Chmod(c.path("missing"), 0o644), syscall.ENOENT)
	})
}

// links returns c's filesystem as a LinkFilesystem with, in c.dir:
//
//	f (file "hi"), d/ (dir), d/g (file "g")
//	rel -> f, abs -> <dir>/f, chain -> rel, dl -> d, up -> ../<base of dir>/f
//	dangling -> nowhere, loop_a -> loop_b -> loop_a
func links(t *testing.T, c contractFS) LinkFilesystem {
	t.Helper()
	l, ok := c.fs.(LinkFilesystem)
	if !ok {
		t.Fatal("not a LinkFilesystem")
	}
	mustDo(t, "WriteFile f", c.fs.WriteFile(c.path("f"), []byte("hi"), 0o644))
	mustDo(t, "Mkdir d", c.fs.Mkdir(c.path("d"), 0o755))
	mustDo(t, "WriteFile d/g", c.fs.WriteFile(c.path("d/g"), []byte("g"), 0o644))
	for name, target := range map[string]string{
		"rel": "f", "abs": c.path("f"), "chain": "rel", "dl": "d", "up": "../" + filepath.Base(c.dir) + "/f",
		"dangling": "nowhere", "loop_a": "loop_b", "loop_b": "loop_a",
	} {
		mustDo(t, "Symlink "+name, l.Symlink(target, c.path(name)))
	}
	return l
}

func TestFilesystemContract_SymlinksResolve(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		l := links(t, c)
		for _, name := range []string{"rel", "abs", "chain", "up", "dl/../f"} {
			wantContents(t, c.fs, c.path(name), "hi")
		}
		wantContents(t, c.fs, c.path("dl/g"), "g")
		if target, err := l.Readlink(c.path("chain")); err != nil || target != "rel" {
			t.Errorf("Readlink(chain) = %q, %v; want the target as written", target, err)
		}
		_, err := l.Readlink(c.path("f"))
		wantErrno(t, "Readlink of a file", err, syscall.EINVAL)
		_, err = l.Readlink(c.path("missing"))
		wantErrno(t, "Readlink missing", err, syscall.ENOENT)

		// Stat follows the link and Lstat describes it.
		if info := statOf(t, c.fs, c.path("chain")); !info.Mode.IsRegular() || info.Size != 2 {
			t.Errorf("Stat(chain) = %+v, want the file", info)
		}
		info, err := Lstat(c.fs, c.path("chain"))
		mustDo(t, "Lstat chain", err)
		if info.Mode&fs.ModeSymlink == 0 || info.Size != int64(len("rel")) || info.Name != "chain" {
			t.Errorf("Lstat(chain) = %+v, want the link itself", info)
		}
		if info := statOf(t, c.fs, c.path("dl")); !info.IsDir {
			t.Errorf("Stat(dl) = %+v, want the directory", info)
		}

		real, err := l.Realpath(c.path("chain"))
		mustDo(t, "Realpath", err)
		dirReal, err := l.Realpath(c.dir)
		mustDo(t, "Realpath dir", err)
		if real != dirReal+"/f" {
			t.Errorf("Realpath(chain) = %q, want %q", real, dirReal+"/f")
		}
		if real, err := l.Realpath(c.path("dl/g")); err != nil || real != dirReal+"/d/g" {
			t.Errorf("Realpath(dl/g) = %q, %v", real, err)
		}
		_, err = l.Realpath(c.path("missing"))
		wantErrno(t, "Realpath missing", err, syscall.ENOENT)

		entries, err := c.fs.ReadDir(c.dir)
		mustDo(t, "ReadDir", err)
		for _, e := range entries {
			if want := e.Name != "f" && e.Name != "d"; (e.Type&fs.ModeSymlink != 0) != want {
				t.Errorf("entry %s: type %v, want a link: %v", e.Name, e.Type, want)
			}
		}
		wantErrno(t, "Symlink onto a file", l.Symlink("x", c.path("f")), syscall.EEXIST)
		wantErrno(t, "Symlink onto a link", l.Symlink("x", c.path("dangling")), syscall.EEXIST)
		wantErrno(t, "Symlink with no parent", l.Symlink("x", c.path("nodir/l")), syscall.ENOENT)
		_, err = c.fs.OpenFile(c.path("rel"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		wantErrno(t, "OpenFile O_NOFOLLOW of a link", err, syscall.ELOOP)
		wantErrno(t, "Symlink in a loop", l.Symlink("x", c.path("loop_a/l")), syscall.ELOOP)
		wantErrno(t, "Symlink through a file", l.Symlink("x", c.path("f/l")), syscall.ENOTDIR)
	})
}

func TestFilesystemContract_DanglingAndLoops(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		l := links(t, c)
		_, err := c.fs.Stat(c.path("dangling"))
		wantErrno(t, "Stat dangling", err, syscall.ENOENT)
		_, err = c.fs.ReadFile(c.path("dangling"))
		wantErrno(t, "ReadFile dangling", err, syscall.ENOENT)
		_, err = l.Realpath(c.path("dangling"))
		wantErrno(t, "Realpath dangling", err, syscall.ENOENT)
		if c.fs.Exists(c.path("dangling")) {
			t.Error("Exists(dangling) = true")
		}
		if info, err := Lstat(c.fs, c.path("dangling")); err != nil || info.Mode&fs.ModeSymlink == 0 {
			t.Errorf("Lstat(dangling) = %+v, %v; want the link", info, err)
		}
		// Writing through a dangling link makes the file it names.
		mustDo(t, "WriteFile dangling", c.fs.WriteFile(c.path("dangling"), []byte("made"), 0o644))
		wantContents(t, c.fs, c.path("nowhere"), "made")

		for what, err := range map[string]error{
			"Stat":      func() error { _, err := c.fs.Stat(c.path("loop_a")); return err }(),
			"Stat in":   func() error { _, err := c.fs.Stat(c.path("loop_a/x")); return err }(),
			"ReadFile":  func() error { _, err := c.fs.ReadFile(c.path("loop_a")); return err }(),
			"Open":      func() error { _, err := c.fs.Open(c.path("loop_b")); return err }(),
			"ReadDir":   func() error { _, err := c.fs.ReadDir(c.path("loop_a")); return err }(),
			"Realpath":  func() error { _, err := l.Realpath(c.path("loop_a")); return err }(),
			"WriteFile": c.fs.WriteFile(c.path("loop_a"), nil, 0o644),
			"Copy onto": c.fs.Copy(c.path("f"), c.path("loop_a")),
			"Chmod":     c.fs.(MetadataFilesystem).Chmod(c.path("loop_a"), 0o644),
			"Mkdir in":  c.fs.Mkdir(c.path("loop_a/x"), 0o755),
		} {
			wantErrno(t, what+" in a loop", err, syscall.ELOOP)
		}
		if c.fs.Exists(c.path("loop_a")) {
			t.Error("Exists(loop_a) = true")
		}
		if info, err := Lstat(c.fs, c.path("loop_a")); err != nil || info.Mode&fs.ModeSymlink == 0 {
			t.Errorf("Lstat(loop_a) = %+v, %v; want the link", info, err)
		}
	})
}

// Removing, renaming and copying a symbolic link act on the link, or for Copy, on what it
// leads to.
func TestFilesystemContract_ChangingSymlinks(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		l := links(t, c)
		mustDo(t, "Remove link", c.fs.Remove(c.path("rel")))
		if _, err := Lstat(c.fs, c.path("rel")); errnoIn(err) != syscall.ENOENT {
			t.Errorf("Lstat(rel) after Remove: %v", err)
		}
		wantContents(t, c.fs, c.path("f"), "hi")
		mustDo(t, "Remove link to dir", c.fs.Remove(c.path("dl")))
		wantContents(t, c.fs, c.path("d/g"), "g")

		mustDo(t, "Rename link", c.fs.Rename(c.path("abs"), c.path("moved")))
		if target, err := l.Readlink(c.path("moved")); err != nil || target != c.path("f") {
			t.Errorf("Readlink(moved) = %q, %v; want the link moved as it was", target, err)
		}
		mustDo(t, "Copy through link", c.fs.Copy(c.path("moved"), c.path("copy")))
		if info, err := Lstat(c.fs, c.path("copy")); err != nil || !info.Mode.IsRegular() {
			t.Errorf("Lstat(copy) = %+v, %v; want a file", info, err)
		}
		wantContents(t, c.fs, c.path("copy"), "hi")
		mustDo(t, "Remove loop", c.fs.Remove(c.path("loop_a")))
		mustDo(t, "RemoveAll dangling", c.fs.RemoveAll(c.path("dangling")))
	})
}

// Hard links are one file under two names: the same contents and inode, with a count of
// names that removing one lowers.
func TestFilesystemContract_HardLinks(t *testing.T) {
	runContract(t, func(t *testing.T, c contractFS) {
		l := links(t, c)
		mustDo(t, "Link", l.Link(c.path("f"), c.path("h")))
		f, h := statOf(t, c.fs, c.path("f")), statOf(t, c.fs, c.path("h"))
		if f.Ino == 0 || f.Ino != h.Ino || f.Nlink != 2 || h.Nlink != 2 {
			t.Errorf("f ino %d nlink %d, h ino %d nlink %d; want one inode with 2 names", f.Ino, f.Nlink, h.Ino, h.Nlink)
		}
		file, err := c.fs.OpenFile(c.path("h"), os.O_WRONLY|os.O_APPEND, 0)
		mustDo(t, "OpenFile h", err)
		_, err = file.Write([]byte("+"))
		mustDo(t, "Write h", err)
		mustDo(t, "Close", file.Close())
		wantContents(t, c.fs, c.path("f"), "hi+")

		mustDo(t, "Remove f", c.fs.Remove(c.path("f")))
		wantContents(t, c.fs, c.path("h"), "hi+")
		if n := statOf(t, c.fs, c.path("h")).Nlink; n != 1 {
			t.Errorf("nlink after Remove = %d, want 1", n)
		}

		wantErrno(t, "Link onto a name", l.Link(c.path("h"), c.path("d/g")), syscall.EEXIST)
		wantErrno(t, "Link missing", l.Link(c.path("f"), c.path("h2")), syscall.ENOENT)
		wantErrno(t, "Link a dir", l.Link(c.path("d"), c.path("dh")), syscall.EPERM)
	})
}

// No path leads out of the filesystem: ".." past its root stays at the root, and a link's
// target is taken inside it.
func TestFilesystemContract_StaysInsideRoot(t *testing.T) {
	for name, fsys := range map[string]func(t *testing.T) (Filesystem, func(string) bool){
		"memory": func(t *testing.T) (Filesystem, func(string) bool) {
			return NewMemoryFilesystem(), func(string) bool { return false }
		},
		"local rooted": func(t *testing.T) (Filesystem, func(string) bool) {
			base := t.TempDir()
			root := filepath.Join(base, "root")
			mustDo(t, "MkdirAll", os.MkdirAll(root, 0o755))
			mustDo(t, "WriteFile", os.WriteFile(filepath.Join(base, "secret"), []byte("host"), 0o644))
			return NewLocalFilesystem(root), func(name string) bool {
				_, err := os.Lstat(filepath.Join(base, name))
				return err == nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fsys, onHost := fsys(t)
			mustDo(t, "WriteFile", fsys.WriteFile("/inside", []byte("in"), 0o644))
			for _, p := range []string{"/../inside", "/../../../inside", "../../inside"} {
				data, err := fsys.ReadFile(p)
				if err == nil && !bytes.Equal(data, []byte("in")) {
					t.Errorf("ReadFile(%q) = %q, want the file in the root", p, data)
				} else if err != nil && errnoIn(err) != syscall.EACCES {
					t.Errorf("ReadFile(%q): %v, want the file in the root or EACCES", p, err)
				}
			}
			if data, err := fsys.ReadFile("/../secret"); err == nil {
				t.Errorf("ReadFile(/../secret) = %q, want it refused or missing", data)
			}
			l := fsys.(LinkFilesystem)
			mustDo(t, "Symlink", l.Symlink("../escaped", "/out"))
			if err := fsys.WriteFile("/out", []byte("x"), 0o644); err == nil {
				if !fsys.Exists("/escaped") {
					t.Error("writing through /out made nothing in the root")
				}
			} else if errnoIn(err) != syscall.EACCES {
				t.Errorf("WriteFile through /out: %v, want EACCES or a file in the root", err)
			}
			if onHost("escaped") {
				t.Error("writing through /out made a file outside the root")
			}
		})
	}
}

// Where Linux and macOS differ, the memory filesystem does what the disk under it does: a
// "/" after a link makes removing, renaming and making names act on where the link leads on
// macOS, and Linux refuses it. Each case runs on a fresh layout in both, and the errors and
// the names left must agree. Lstat runs first on the same path, as the fs module's unlink and
// rmdir do to check what they remove.
func TestFilesystemContract_PlatformCasesMatchDisk(t *testing.T) {
	cases := map[string]struct {
		path string
		op   func(fsys Filesystem, p string) error
	}{
		"remove_dir":              {"empty", Filesystem.Remove},
		"remove_dir_slash":        {"empty/", Filesystem.Remove},
		"remove_file_slash":       {"f/", Filesystem.Remove},
		"remove_link":             {"elink", Filesystem.Remove},
		"remove_link_slash":       {"link/", Filesystem.Remove},
		"remove_empty_link_slash": {"elink/", Filesystem.Remove},
		"remove_dangling_slash":   {"dangling/", Filesystem.Remove},
		"remove_full":             {"target", Filesystem.Remove},
		"rename_link_slash": {"elink/", func(fsys Filesystem, p string) error {
			return fsys.Rename(p, path.Dir(strings.TrimSuffix(p, "/"))+"/moved")
		}},
		"mkdir_dangling_slash": {"dangling/", func(fsys Filesystem, p string) error { return fsys.Mkdir(p, 0o755) }},
		"mkdir_dir_link_slash": {"link/", func(fsys Filesystem, p string) error { return fsys.Mkdir(p, 0o755) }},
		"symlink_dir_link_slash": {"link/", func(fsys Filesystem, p string) error {
			return fsys.(LinkFilesystem).Symlink("x", p)
		}},
		"mkdirall_file_link_child": {"flink/a", func(fsys Filesystem, p string) error { return fsys.MkdirAll(p, 0o755) }},
		"mkdirall_link":            {"link", func(fsys Filesystem, p string) error { return fsys.MkdirAll(p, 0o755) }},
		"mkdirall_through_link":    {"link/a/b", func(fsys Filesystem, p string) error { return fsys.MkdirAll(p, 0o755) }},
	}
	// layout makes target/x, empty/, f, and links to each of them and to nothing.
	layout := func(t *testing.T, c contractFS) {
		mustDo(t, "MkdirAll", c.fs.MkdirAll(c.path("target"), 0o755))
		mustDo(t, "WriteFile", c.fs.WriteFile(c.path("target/x"), []byte("x"), 0o644))
		mustDo(t, "Mkdir", c.fs.Mkdir(c.path("empty"), 0o755))
		mustDo(t, "WriteFile", c.fs.WriteFile(c.path("f"), []byte("f"), 0o644))
		for name, target := range map[string]string{"link": "target", "elink": "empty", "flink": "f", "dangling": "missing"} {
			mustDo(t, "Symlink", c.fs.(LinkFilesystem).Symlink(target, c.path(name)))
		}
	}
	// outcome is what a case did: the errnos of Lstat and the operation, and the names left.
	outcome := func(t *testing.T, c contractFS, p string, op func(Filesystem, string) error) []string {
		_, lstatErr := Lstat(c.fs, c.path(p))
		opErr := op(c.fs, c.path(p))
		out := []string{"lstat " + errnoIn(lstatErr).Error(), "op " + errnoIn(opErr).Error()}
		if errnoIn(lstatErr) == 0 {
			out[0] = "lstat ok"
		}
		if errnoIn(opErr) == 0 {
			out[1] = "op ok"
		}
		for _, name := range []string{"target/x", "empty", "f", "link", "elink", "flink", "missing", "moved", "link/a/b", "x"} {
			if _, err := Lstat(c.fs, c.path(name)); err == nil {
				out = append(out, name)
			}
		}
		return out
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mem, disk := contractFilesystems["memory"](t), contractFilesystems["local"](t)
			layout(t, mem)
			layout(t, disk)
			got, want := outcome(t, mem, tc.path, tc.op), outcome(t, disk, tc.path, tc.op)
			if !slices.Equal(got, want) {
				t.Errorf("memory: %v\n  disk: %v", got, want)
			}
		})
	}
}
