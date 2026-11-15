package runtime

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// linkLayout makes a root directory and a sibling directory beside it, with links in the root
// that point into the root and links that point to the sibling.
//
//	base/root/a.txt, base/root/in/x.txt
//	base/root/near -> in               base/root/nearfile -> a.txt
//	base/root/nearabs -> <abs>/root/in
//	base/root/far -> ../other          base/root/farfile -> <abs>/other/f.txt
//	base/root/dangling -> ../other/new.txt
//	base/root/loop -> loop
//	base/other/f.txt, base/other/d/
//	base/rootlink -> root
func linkLayout(t *testing.T) (base string) {
	t.Helper()
	base = t.TempDir()
	root := filepath.Join(base, "root")
	other := filepath.Join(base, "other")
	for _, d := range []string{filepath.Join(root, "in"), filepath.Join(other, "d")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "a.txt"):       "a",
		filepath.Join(root, "in", "x.txt"): "x",
		filepath.Join(other, "f.txt"):      "f",
	}
	for name, data := range files {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"root/near":     "in",
		"root/nearfile": "a.txt",
		"root/nearabs":  filepath.Join(root, "in"),
		"root/far":      "../other",
		"root/farfile":  filepath.Join(other, "f.txt"),
		"root/dangling": "../other/new.txt",
		"root/loop":     "loop",
		"rootlink":      "root",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func refused(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrOutsideSandbox) || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("%s: err = %v, want the path check's EACCES", what, err)
	}
}

// Paths through a link to the sibling directory are refused by every call, whether the root
// is given directly or through a link of its own.
func TestLocalFS_LinkTargetsAreChecked(t *testing.T) {
	base := linkLayout(t)
	other := filepath.Join(base, "other")
	for _, rootName := range []string{"root", "rootlink"} {
		t.Run(rootName, func(t *testing.T) {
			l := NewLocalFilesystem(filepath.Join(base, rootName))

			_, err := l.ReadFile("/far/f.txt")
			refused(t, "ReadFile far/f.txt", err)
			_, err = l.ReadFile("/farfile")
			refused(t, "ReadFile farfile", err)
			_, err = l.Stat("/far")
			refused(t, "Stat far", err)
			_, err = l.Stat("/far/d")
			refused(t, "Stat far/d", err)
			if l.Exists("/farfile") {
				t.Error("Exists farfile = true")
			}
			_, err = l.ReadDir("/far")
			refused(t, "ReadDir far", err)
			_, err = l.Open("/far/f.txt")
			refused(t, "Open far/f.txt", err)
			_, err = l.OpenFile("/farfile", os.O_RDWR, 0)
			refused(t, "OpenFile farfile", err)
			_, err = l.Lstat("/far/f.txt")
			refused(t, "Lstat far/f.txt", err)

			refused(t, "WriteFile farfile", l.WriteFile("/farfile", []byte("w"), 0o644))
			refused(t, "WriteFile far/new.txt", l.WriteFile("/far/new.txt", []byte("w"), 0o644))
			refused(t, "AppendFile farfile", l.AppendFile("/farfile", []byte("w")))
			refused(t, "WriteFile dangling", l.WriteFile("/dangling", []byte("w"), 0o644))
			_, err = l.Create("/dangling")
			refused(t, "Create dangling", err)
			_, err = l.OpenFile("/dangling", os.O_CREATE|os.O_WRONLY, 0o644)
			refused(t, "OpenFile dangling", err)
			refused(t, "Mkdir far/sub", l.Mkdir("/far/sub", 0o755))
			refused(t, "MkdirAll far/a/b", l.MkdirAll("/far/a/b", 0o755))
			refused(t, "MkdirAll dangling/a", l.MkdirAll("/dangling/a", 0o755))
			refused(t, "Copy from farfile", l.Copy("/farfile", "/copy.txt"))
			refused(t, "Copy to far", l.Copy("/a.txt", "/far/copy.txt"))
			refused(t, "Rename into far", l.Rename("/nearfile", "/far/moved"))
			refused(t, "Rename out of far", l.Rename("/far/f.txt", "/moved"))
			refused(t, "Remove far/f.txt", l.Remove("/far/f.txt"))
			refused(t, "RemoveAll far/d", l.RemoveAll("/far/d"))

			if data, err := os.ReadFile(filepath.Join(other, "f.txt")); err != nil || string(data) != "f" {
				t.Errorf("other/f.txt = %q, %v; want it unchanged", data, err)
			}
			for _, name := range []string{"new.txt", "sub", "a", "copy.txt", "moved"} {
				if _, err := os.Lstat(filepath.Join(other, name)); err == nil {
					t.Errorf("other/%s was created", name)
				}
			}
			if _, err := os.Stat(filepath.Join(other, "d")); err != nil {
				t.Errorf("other/d: %v; want it kept", err)
			}
		})
	}
}

// Links to places under the root work as before.
func TestLocalFS_NearbyLinksWork(t *testing.T) {
	base := linkLayout(t)
	for _, rootName := range []string{"root", "rootlink"} {
		t.Run(rootName, func(t *testing.T) {
			l := NewLocalFilesystem(filepath.Join(base, rootName))
			for _, p := range []string{"/near/x.txt", "/nearabs/x.txt", "/in/../near/x.txt"} {
				if data, err := l.ReadFile(p); err != nil || string(data) != "x" {
					t.Errorf("ReadFile %s = %q, %v", p, data, err)
				}
			}
			if data, err := l.ReadFile("/nearfile"); err != nil || string(data) != "a" {
				t.Errorf("ReadFile nearfile = %q, %v", data, err)
			}
			if info, err := l.Stat("/near"); err != nil || !info.IsDir {
				t.Errorf("Stat near = %v, %v", info, err)
			}
			if entries, err := l.ReadDir("/nearabs"); err != nil || !hasEntry(entries, "x.txt") {
				t.Errorf("ReadDir nearabs = %v, %v", entries, err)
			}
			if err := l.WriteFile("/near/"+rootName+".txt", []byte("w"), 0o644); err != nil {
				t.Errorf("WriteFile near: %v", err)
			}
			if err := l.MkdirAll("/near/"+rootName+"/sub", 0o755); err != nil {
				t.Errorf("MkdirAll near: %v", err)
			}
			if _, err := l.Stat("/"); err != nil {
				t.Errorf("Stat root: %v", err)
			}
			// A loop fails as the OS would fail it, not with the path check's error.
			if _, err := l.Stat("/loop"); err == nil || errors.Is(err, ErrOutsideSandbox) {
				t.Errorf("Stat loop = %v; want the OS's error", err)
			}
		})
	}
}

// Calls that work on a link itself, lstat, unlink and rename, still do so for a link to the
// sibling directory, and leave its target alone.
func TestLocalFS_LinkItselfCanBeHandled(t *testing.T) {
	base := linkLayout(t)
	l := NewLocalFilesystem(filepath.Join(base, "root"))

	for _, p := range []string{"/far", "/farfile", "/dangling", "/loop"} {
		info, err := l.Lstat(p)
		if err != nil || info.Mode&fs.ModeSymlink == 0 {
			t.Errorf("Lstat %s = %v, %v; want the link", p, info, err)
		}
	}
	if err := l.Rename("/farfile", "/renamed"); err != nil {
		t.Errorf("Rename farfile: %v", err)
	}
	if err := l.Remove("/renamed"); err != nil {
		t.Errorf("Remove renamed link: %v", err)
	}
	if err := l.RemoveAll("/far"); err != nil {
		t.Errorf("RemoveAll far: %v", err)
	}
	if err := l.Remove("/dangling"); err != nil {
		t.Errorf("Remove dangling: %v", err)
	}
	for _, p := range []string{"root/renamed", "root/far", "root/dangling"} {
		if _, err := os.Lstat(filepath.Join(base, p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
	for _, p := range []string{"other/f.txt", "other/d"} {
		if _, err := os.Stat(filepath.Join(base, p)); err != nil {
			t.Errorf("%s: %v; want it kept", p, err)
		}
	}
	// A new link can be made where one was removed, through the OS, and is checked again.
	if err := os.Symlink("../other", filepath.Join(base, "root", "far")); err != nil {
		t.Fatal(err)
	}
	_, err := l.ReadFile("/far/f.txt")
	refused(t, "ReadFile far/f.txt after relinking", err)
}

func hasEntry(entries []DirEntry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}
