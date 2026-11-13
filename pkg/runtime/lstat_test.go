package runtime

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Lstat describes a symbolic link itself, where Stat describes what it points to, and falls
// back to Stat for a Filesystem without Lstat.
func TestLstat(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "target"), link); err != nil {
		t.Fatal(err)
	}
	local := NewLocalFilesystem("")
	info, err := Lstat(local, link)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir || info.Mode&fs.ModeSymlink == 0 {
		t.Errorf("Lstat(link) = dir %v, mode %v; want the link itself", info.IsDir, info.Mode)
	}
	if info, err := local.Stat(link); err != nil || !info.IsDir {
		t.Errorf("Stat(link) = %v, %v; want the directory", info, err)
	}

	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := Lstat(local, dangling); err != nil {
		t.Errorf("Lstat(dangling) = %v; want the link", err)
	}

	memory := NewMemoryFilesystem()
	if err := memory.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	if info, err := Lstat(statOnly{memory}, "/d"); err != nil || !info.IsDir {
		t.Errorf("Lstat without Lstat = %v, %v; want Stat's answer", info, err)
	}
}

// statOnly hides MemoryFilesystem's Lstat.
type statOnly struct{ Filesystem }
