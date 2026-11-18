package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// byteLimit lets a filesystem hold at most its value in bytes.
type byteLimit int64

func (l byteLimit) fits(total int64) bool { return total <= int64(l) }

func wantNoSpace(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Errorf("%s: got %v, want ENOSPC", what, err)
	}
}

// wantNoSpaceAt checks err is ENOSPC from op on path, which the fs module turns into the
// error Node gives for that call.
func wantNoSpaceAt(t *testing.T, what string, err error, op, path string) {
	t.Helper()
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Err != syscall.ENOSPC || pathErr.Op != op || pathErr.Path != path {
		t.Errorf("%s: got %#v, want ENOSPC from %s %s", what, err, op, path)
	}
}

// Everything the filesystem stores counts: contents, names and each node, and space reserved
// past the end. Removing it all gives every byte back.
func TestMemoryFilesystemCountsWhatItHolds(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/dir"); got != want {
		t.Fatalf("after mkdir: %d bytes, want %d", got, want)
	}
	if err := m.WriteFile("/dir/f", make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/dir")+nodeCost("/dir/f")+1000; got != want {
		t.Fatalf("after writeFile: %d bytes, want %d", got, want)
	}
	if err := m.Truncate("/dir/f", 5000); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/dir")+nodeCost("/dir/f")+5000; got != want {
		t.Fatalf("after truncate: %d bytes, want %d", got, want)
	}
	if err := m.Rename("/dir/f", "/dir/longer-name"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/dir")+nodeCost("/dir/longer-name")+5000; got != want {
		t.Fatalf("after rename: %d bytes, want %d", got, want)
	}

	f, err := m.OpenFile("/g", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(1<<16, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendFile("/g", []byte("more")); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy("/g", "/h"); err != nil {
		t.Fatal(err)
	}
	if err := m.MkdirAll("/a/b/c", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if m.memoryInUse() <= 2<<16 {
		t.Fatalf("holding two files of 64 KiB, the filesystem counts %d bytes", m.memoryInUse())
	}

	if err := m.RemoveAll("/dir"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveAll("/a"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/g", "/h"} {
		if err := m.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.memoryInUse(); got != 0 {
		t.Fatalf("with everything removed, the filesystem counts %d bytes", got)
	}
}

// Each call that would grow the filesystem past its limit fails with ENOSPC and changes
// nothing; calls that don't grow it still work.
func TestMemoryFilesystemOverLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	m.limitMemory(byteLimit(m.memoryInUse() + 50))
	before := m.memoryInUse()

	// Each names the call that ran out of room: making a file fails at open, its contents
	// at write.
	wantNoSpaceAt(t, "WriteFile", m.WriteFile("/f", make([]byte, 200), 0o644), "write", "/f")
	wantNoSpaceAt(t, "WriteFile new", m.WriteFile("/new", nil, 0o644), "open", "/new")
	wantNoSpaceAt(t, "AppendFile", m.AppendFile("/f", make([]byte, 100)), "write", "/f")
	wantNoSpaceAt(t, "Mkdir", m.Mkdir("/dir2", 0o755), "mkdir", "/dir2")
	wantNoSpaceAt(t, "MkdirAll", m.MkdirAll("/dir/a/b", 0o755), "mkdir", "/dir/a")
	wantNoSpaceAt(t, "Rename", m.Rename("/f", "/"+string(make([]byte, 100))), "rename", "/f")
	wantNoSpaceAt(t, "Copy", m.Copy("/f", "/copy"), "copyfile", "/copy")
	wantNoSpaceAt(t, "Truncate", m.Truncate("/f", 1000), "truncate", "/f")
	_, err := m.OpenFile("/new", os.O_WRONLY|os.O_CREATE, 0o644)
	wantNoSpaceAt(t, "OpenFile", err, "open", "/new")

	f, err := m.OpenFile("/f", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(1<<20, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	n, err := f.Write([]byte("x"))
	wantNoSpaceAt(t, "Write past the end", err, "write", "/f")
	if n != 0 {
		t.Errorf("a write that doesn't fit wrote %d bytes", n)
	}
	wantNoSpaceAt(t, "File.Truncate", f.(*memFileHandle).Truncate(1<<20), "ftruncate", "/f")
	if got := m.memoryInUse(); got != before {
		t.Fatalf("failed calls changed the count from %d to %d bytes", before, got)
	}
	if data, _ := m.ReadFile("/f"); len(data) != 100 {
		t.Fatalf("failed calls changed the file to %d bytes", len(data))
	}
	if m.Exists("/new") || m.Exists("/dir2") || m.Exists("/dir/a") || m.Exists("/copy") {
		t.Fatal("a failed call left something behind")
	}

	// Within the limit, and shrinking, still work.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("overwritten")); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendFile("/f", make([]byte, 40)); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/f", "/g"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("/dir"); err != nil {
		t.Fatal(err)
	}
	if err := m.Mkdir("/d", 0o755); err != nil {
		t.Fatalf("mkdir after freeing space: %v", err)
	}
}

// A hard link costs only its name: the contents are counted once, and given back with the
// last name. A symbolic link costs its node and target.
func TestMemoryFilesystemCountsLinks(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Link("/f", "/g"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/f")+nameCost("/g")+1000; got != want {
		t.Fatalf("after link: %d bytes, want %d", got, want)
	}
	if err := m.Symlink("/target", "/s"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/f")+nameCost("/g")+1000+nodeCost("/s")+int64(len("/target")); got != want {
		t.Fatalf("after symlink: %d bytes, want %d", got, want)
	}
	// Renaming another file over one name of a linked file leaves its contents with the other.
	if err := m.WriteFile("/h", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/h", "/g"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/f")+1000+nodeCost("/g")+nodeCost("/s")+int64(len("/target")); got != want {
		t.Fatalf("after renaming over a link: %d bytes, want %d", got, want)
	}
	if err := m.Link("/f", "/f2"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/f", "/g", "/s"} {
		if err := m.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := m.memoryInUse(), nodeCost("/f2")+1000; got != want {
		t.Fatalf("with one name left: %d bytes, want %d", got, want)
	}
	if err := m.Remove("/f2"); err != nil {
		t.Fatal(err)
	}
	if got := m.memoryInUse(); got != 0 {
		t.Fatalf("with everything removed, the filesystem counts %d bytes", got)
	}

	// Past the limit, neither kind of link is made.
	m.limitMemory(byteLimit(m.memoryInUse()))
	if err := m.WriteFile("/f", nil, 0o644); err == nil {
		t.Fatal("a file fit in a full filesystem")
	}
	m.limits = nil
	if err := m.WriteFile("/f", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m.limitMemory(byteLimit(m.memoryInUse()))
	wantNoSpaceAt(t, "Link", m.Link("/f", "/g"), "link", "/f")
	wantNoSpaceAt(t, "Symlink", m.Symlink("/f", "/s"), "symlink", "/s")
	if m.Exists("/g") || m.Exists("/s") {
		t.Fatal("a link that didn't fit was made")
	}
}

// A file unlinked or replaced while a handle has it open keeps its contents charged until the
// last handle closes, as a disk keeps an open file's blocks, so open handles can't hold memory
// past the limit.
func TestMemoryFilesystemChargesOpenUnlinkedFiles(t *testing.T) {
	m := NewMemoryFilesystem()
	m.limitMemory(byteLimit(10 << 10))
	var open []File
	grow := func(path string) error {
		f, err := m.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		open = append(open, f)
		return f.(*memFileHandle).Truncate(4 << 10)
	}
	if err := grow("/a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("/a"); err != nil {
		t.Fatal(err)
	}
	if err := grow("/b"); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/c", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/c", "/b"); err != nil {
		t.Fatal(err)
	}
	wantNoSpace(t, "Truncate with two unlinked 4 KiB files open", grow("/d"))
	if got := m.memoryInUse(); got < 8<<10 {
		t.Fatalf("with two unlinked 4 KiB files open, the filesystem counts %d bytes", got)
	}

	for _, f := range open {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	open[0].Close() // closing twice gives nothing back twice
	for _, p := range []string{"/b", "/d"} {
		if err := m.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.memoryInUse(); got != 0 {
		t.Fatalf("with every handle closed and every file removed, the filesystem counts %d bytes", got)
	}
}

// A file written in chunks still fills the filesystem to its limit: when doubling its
// capacity doesn't fit, it grows by what the write needs.
func TestMemoryFilesystemGrowsToTheLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	m.limitMemory(byteLimit(nodeCost("/f") + 1000))
	f, err := m.OpenFile("/f", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for {
		n, err := f.Write(make([]byte, 100))
		written += n
		if err != nil {
			wantNoSpace(t, "Write", err)
			break
		}
	}
	if written != 1000 {
		t.Fatalf("wrote %d bytes, want 1000", written)
	}
}

// A runtime given a memory filesystem limits it while it runs, and stops when disposed.
func TestRuntimeLimitsMemoryFilesystem(t *testing.T) {
	m := NewMemoryFilesystem()
	rt, err := New(&Config{Filesystem: m, IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: 64 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/f", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wantNoSpace(t, "Truncate to the limit", m.Truncate("/f", 64<<20))
	if err := m.Truncate("/f", 16<<20); err != nil {
		t.Fatalf("truncate within the limit: %v", err)
	}
	rt.Dispose()
	if len(m.limits) != 0 {
		t.Fatalf("a disposed runtime left %d limits on the filesystem", len(m.limits))
	}
}

// With no heap limit set, the filesystem is held to the one V8 applies.
func TestRuntimeLimitsMemoryFilesystemToV8sLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	rt, err := New(&Config{Filesystem: m})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	stats, err := rt.Isolate().GetHeapStatistics()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/f", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wantNoSpace(t, "Truncate to V8's limit", m.Truncate("/f", int64(stats.HeapSizeLimit)))
}

// The heap's usage counts too: what fits beside a small heap doesn't fit beside a large one.
func TestRuntimeLimitsMemoryFilesystemByHeapUsage(t *testing.T) {
	for _, tc := range []struct {
		script string
		fits   bool
	}{
		{"globalThis.kept = null", true},
		{"globalThis.kept = new Array(3e6).fill(1.5)", false}, // 24 MB of doubles
	} {
		m := NewMemoryFilesystem()
		rt, err := New(&Config{Filesystem: m, IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: 64 << 20}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.RunScript(tc.script, "test.js"); err != nil {
			t.Fatal(err)
		}
		rt.SampleHeapUsage()
		if err := m.WriteFile("/f", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		err = m.Truncate("/f", 45<<20)
		if tc.fits && err != nil {
			t.Errorf("%s: a 45 MiB file doesn't fit: %v", tc.script, err)
		}
		if !tc.fits {
			wantNoSpace(t, tc.script, err)
		}
		rt.Dispose()
	}
}
