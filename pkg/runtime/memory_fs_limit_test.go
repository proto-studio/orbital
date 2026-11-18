package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

// byteLimit lets a filesystem hold at most its value in bytes.
type byteLimit int64

func (l byteLimit) charge(used, delta int64) bool { return used+delta <= int64(l) }

func (byteLimit) release(int64) {}

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

// Copying over a directory replaces it, and gives back what it held.
func TestMemoryFilesystemCopyOverDirectory(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy("/f", "/d"); err != nil {
		t.Fatal(err)
	}
	if got, want := m.memoryInUse(), nodeCost("/f")+nodeCost("/d")+200; got != want {
		t.Fatalf("after copying over a directory: %d bytes, want %d", got, want)
	}
	for _, p := range []string{"/f", "/d"} {
		if err := m.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.memoryInUse(); got != 0 {
		t.Fatalf("with everything removed, the filesystem counts %d bytes", got)
	}

	// A copy that doesn't fit leaves the directory.
	if err := m.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/f", make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	m.limitMemory(byteLimit(m.memoryInUse() + 50))
	wantNoSpaceAt(t, "Copy over a directory", m.Copy("/f", "/d"), "copyfile", "/d")
	if info, err := m.Stat("/d"); err != nil || !info.IsDir {
		t.Fatalf("a copy that didn't fit replaced the directory: %v, %v", info, err)
	}
}

// Near the limit, where doubling a file doesn't fit, small writes still grow it by a share of
// the room left, not by only what each needs, which would copy the file for every write.
func TestMemoryFilesystemGrowsGeometricallyNearTheLimit(t *testing.T) {
	m := NewMemoryFilesystem()
	f, err := m.OpenFile("/f", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	m.limitMemory(byteLimit(nodeCost("/f") + 3<<19)) // 1.5 MiB
	grows := 0
	for range 1000 {
		before := cap(m.files["/f"].data)
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if cap(m.files["/f"].data) != before {
			grows++
		}
	}
	if grows > 2 {
		t.Fatalf("1000 one-byte writes near the limit grew the file %d times", grows)
	}
}

// A call that reaches a handle after it closed, as one in flight when a runtime is disposed
// can, fails with EBADF and charges nothing: the file it had open may have no names left, and
// its memory has been given back.
func TestMemoryFilesystemClosedHandleChangesNothing(t *testing.T) {
	m := NewMemoryFilesystem()
	f, err := m.OpenFile("/f", os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("/f"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.(*memFileHandle).Truncate(1 << 20); !errors.Is(err, syscall.EBADF) {
		t.Errorf("Truncate after Close: %v, want EBADF", err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, syscall.EBADF) {
		t.Errorf("Write after Close: %v, want EBADF", err)
	}
	if got := m.memoryInUse(); got != 0 {
		t.Fatalf("calls on a closed handle left %d bytes charged", got)
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

// A budget is charged what the filesystem holds when attached, then each change: growth is
// charged and what's freed, by removing or truncating, given back.
func TestMemoryFilesystemChargesBudget(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	budget := NewMemoryBudget(1 << 20)
	remove, ok := m.limitMemory(budgetLimit{budget})
	if !ok {
		t.Fatal("a small filesystem didn't fit a 1 MiB budget")
	}
	wantCharged := func(what string) {
		t.Helper()
		if got, want := budget.Used(), uint64(m.memoryInUse()); got != want {
			t.Fatalf("%s: the budget is charged %d bytes, the filesystem holds %d", what, got, want)
		}
	}
	wantCharged("attached")
	if err := m.Truncate("/f", 100<<10); err != nil {
		t.Fatal(err)
	}
	wantCharged("grown")
	if err := m.Truncate("/f", 0); err != nil {
		t.Fatal(err)
	}
	f, err := m.OpenFile("/f", os.O_RDWR|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	wantCharged("truncated")
	if err := m.Link("/f", "/g"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/g", "/a-longer-name"); err != nil {
		t.Fatal(err)
	}
	wantCharged("linked and renamed")

	// What doesn't fit fails with ENOSPC and takes nothing.
	before := budget.Used()
	wantNoSpaceAt(t, "Truncate past the budget", m.Truncate("/f", 2<<20), "truncate", "/f")
	if budget.Used() != before {
		t.Fatalf("a failed truncate changed the budget from %d to %d bytes", before, budget.Used())
	}

	// Attached twice, as by a runtime and its worker, the budget is charged once, and given
	// back when both are removed.
	removeAgain, ok := m.limitMemory(budgetLimit{budget})
	if !ok {
		t.Fatal("attaching the budget again failed")
	}
	wantCharged("attached twice")
	remove()
	remove() // removing twice gives nothing back twice
	wantCharged("one of two removed")
	removeAgain()
	if got := budget.Used(); got != 0 {
		t.Fatalf("with the filesystem detached, the budget is charged %d bytes", got)
	}
	if err := m.Remove("/f"); err != nil {
		t.Fatal(err)
	}
	if got := budget.Used(); got != 0 {
		t.Fatalf("a detached filesystem gave back to the budget: %d bytes charged", got)
	}
}

// A filesystem that already holds more than a budget doesn't attach to it.
func TestMemoryFilesystemOverBudgetDoesNotAttach(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 2<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	budget := NewMemoryBudget(1 << 10)
	if _, ok := m.limitMemory(budgetLimit{budget}); ok {
		t.Fatal("a filesystem holding 2 KiB attached to a 1 KiB budget")
	}
	if budget.Used() != 0 || len(m.limits) != 0 {
		t.Fatalf("a failed attach left %d bytes charged and %d limits", budget.Used(), len(m.limits))
	}
}

// Growth that one limit refuses is charged to none: what another limit took is given back.
func TestMemoryFilesystemChargesAllLimitsOrNone(t *testing.T) {
	m := NewMemoryFilesystem()
	budget := NewMemoryBudget(1 << 20)
	m.limitMemory(budgetLimit{budget})
	m.limitMemory(byteLimit(1 << 10))
	if err := m.WriteFile("/f", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before := budget.Used()
	for range 10 { // the limits are tried in no fixed order
		wantNoSpace(t, "Truncate past one limit", m.Truncate("/f", 4<<10))
		if budget.Used() != before {
			t.Fatalf("growth the other limit refused left the budget charged %d bytes, not %d", budget.Used(), before)
		}
	}
}

// A runtime with a MemoryBudget charges its memory filesystem to it, and gives that back when
// disposed; one whose filesystem doesn't fit the budget doesn't start.
func TestRuntimeChargesMemoryFilesystemToBudget(t *testing.T) {
	m := NewMemoryFilesystem()
	if err := m.WriteFile("/f", make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	budget := NewMemoryBudget(256 << 20)
	rt, err := New(&Config{Filesystem: m, MemoryBudget: budget})
	if err != nil {
		t.Fatal(err)
	}
	heapAndFS := budget.Used()
	if heapAndFS < uint64(m.memoryInUse()) {
		t.Fatalf("the budget is charged %d bytes, less than the filesystem's %d", heapAndFS, m.memoryInUse())
	}
	if err := m.Truncate("/f", 2<<20); err != nil {
		t.Fatal(err)
	}
	if got, want := budget.Used(), heapAndFS+1<<20; got != want {
		t.Fatalf("after growing the file 1 MiB, the budget is charged %d bytes, want %d", got, want)
	}
	rt.Dispose()
	if got := budget.Used(); got != 0 {
		t.Fatalf("a disposed runtime left %d bytes charged to its budget", got)
	}

	if _, err := New(&Config{Filesystem: m, MemoryBudget: NewMemoryBudget(1 << 20)}); !errors.Is(err, ErrOutOfMemory) {
		t.Fatalf("New with a filesystem bigger than its budget = %v, want ErrOutOfMemory", err)
	}
}

// Dispose keeps a memory filesystem limited until filesystem work the runtime started is done,
// so work still running after the script stops can't grow it past the limit.
func TestRuntimeLimitsFilesystemWorkUntilItEnds(t *testing.T) {
	m := NewMemoryFilesystem()
	rt, err := New(&Config{Filesystem: m, IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: 64 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteFile("/f", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	done := rt.FilesystemWork()
	disposed := make(chan struct{})
	go func() {
		rt.Dispose()
		close(disposed)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-disposed:
		t.Fatal("Dispose returned with filesystem work still running")
	default:
	}
	// The work, still running, is limited.
	wantNoSpace(t, "Truncate during Dispose", m.Truncate("/f", 64<<20))
	done()
	done() // calling done twice counts once
	select {
	case <-disposed:
	case <-time.After(10 * time.Second):
		t.Fatal("Dispose didn't return once the work was done")
	}
	if len(m.limits) != 0 {
		t.Fatalf("a disposed runtime left %d limits on the filesystem", len(m.limits))
	}
}
