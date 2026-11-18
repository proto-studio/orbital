package runtime

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// MemoryFilesystem implements Filesystem using in-memory storage.
// Useful for testing or temporary file operations.
//
// A runtime given a MemoryFilesystem counts the memory it holds against the runtime's
// MemoryBudget, or, without one, the isolate's heap limit: a call that would take the
// filesystem over it fails with ENOSPC, as on a full disk.
type MemoryFilesystem struct {
	mu    sync.RWMutex
	files map[string]*memFile // by canonical path; hard links share a memFile

	lastIno uint64

	used   int64               // bytes held: each name, and each file's node, contents and link target
	limits map[memoryLimit]int // what used is charged to, with how many runtimes attached each
}

// memoryLimit limits how much memory a MemoryFilesystem may hold.
type memoryLimit interface {
	// charge takes delta more bytes for a filesystem holding used, or reports false, taking
	// nothing, when they don't fit.
	charge(used, delta int64) bool
	// release gives back n bytes taken with charge.
	release(n int64)
}

// What the filesystem counts besides contents: a name costs its entry in the map of files and
// its text, and a file, directory or link its node, however many names it has.
const (
	memNameOverhead = 64
	memFileOverhead = int64(unsafe.Sizeof(memFile{}))
)

// nameCost is what the name path costs.
func nameCost(path string) int64 {
	return memNameOverhead + int64(len(path))
}

// nodeCost is what a file with the one name path costs, not counting its contents.
func nodeCost(path string) int64 {
	return nameCost(path) + memFileOverhead
}

// fileCost is what mf itself holds, whatever its names: its node, contents and link target.
func fileCost(mf *memFile) int64 {
	return memFileOverhead + int64(cap(mf.data)) + int64(len(mf.target))
}

// limitMemory charges what the filesystem holds, and its growth, to limit too, until the
// returned function is called. A limit attached more than once, as when a runtime and its
// workers share a budget, is charged once, and given back when the last of them is removed.
// It reports false, attaching nothing, when what the filesystem holds doesn't fit.
func (m *MemoryFilesystem) limitMemory(limit memoryLimit) (remove func(), ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.limits[limit] == 0 {
		if m.used > 0 && !limit.charge(0, m.used) {
			return nil, false
		}
		if m.limits == nil {
			m.limits = make(map[memoryLimit]int)
		}
	}
	m.limits[limit]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.limits[limit]--; m.limits[limit] == 0 {
				delete(m.limits, limit)
				limit.release(m.used)
			}
		})
	}, true
}

// memoryInUse is how many bytes the filesystem holds.
func (m *MemoryFilesystem) memoryInUse() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.used
}

// account changes how much memory the filesystem holds by delta, charging growth to every
// limit, or reports false, changing nothing, when it doesn't fit one. Callers hold m.mu.
func (m *MemoryFilesystem) account(delta int64) bool {
	if delta <= 0 {
		m.release(-delta)
		return true
	}
	charged := make([]memoryLimit, 0, len(m.limits))
	for l := range m.limits {
		if !l.charge(m.used, delta) {
			for _, c := range charged {
				c.release(delta)
			}
			return false
		}
		charged = append(charged, l)
	}
	m.used += delta
	return true
}

// release gives back n bytes the filesystem no longer holds. Callers hold m.mu.
func (m *MemoryFilesystem) release(n int64) {
	if n == 0 {
		return
	}
	m.used -= n
	for l := range m.limits {
		l.release(n)
	}
}

// noSpace is the failure of op on path when the memory it needs is over the limit.
func noSpace(op, path string) error {
	return &fs.PathError{Op: op, Path: path, Err: syscall.ENOSPC}
}

// reserve makes room in mf.data for size bytes. Capacity grows geometrically, as append's
// does, so writing a file in chunks costs linear time; when that doesn't fit under the limit
// it grows to size alone. It reports false when even that doesn't fit. Callers hold m.mu.
func (m *MemoryFilesystem) reserve(mf *memFile, size int64) bool {
	old := int64(cap(mf.data))
	if size <= old {
		return true
	}
	newCap := max(size, 2*old)
	if !m.account(newCap - old) {
		newCap = size
		if !m.account(newCap - old) {
			return false
		}
	}
	grown := make([]byte, len(mf.data), newCap)
	copy(grown, mf.data)
	mf.data = grown
	return true
}

// setData replaces mf's contents with a copy of data, or reports false when it doesn't fit.
// Callers hold m.mu.
func (m *MemoryFilesystem) setData(mf *memFile, data []byte) bool {
	if !m.account(int64(len(data)) - int64(cap(mf.data))) {
		return false
	}
	mf.data = append(make([]byte, 0, len(data)), data...)
	return true
}

// add makes mf, a new file, and names it path, or reports false when it doesn't fit. Callers
// hold m.mu.
func (m *MemoryFilesystem) add(path string, mf *memFile) bool {
	if !m.account(nameCost(path) + fileCost(mf)) {
		return false
	}
	m.files[path] = m.newFile(mf)
	return true
}

// forget gives back what mf holds once nothing refers to it: no name and no open handle, as
// a disk keeps an unlinked file's blocks while it is open. Callers hold m.mu.
func (m *MemoryFilesystem) forget(mf *memFile) {
	if mf.nlink == 0 && mf.handles == 0 {
		m.release(fileCost(mf))
		mf.data = nil
	}
}

type memFile struct {
	data    []byte
	mode    fs.FileMode
	modTime time.Time
	isDir   bool

	// Set by Chown and Chtimes; zero until then.
	uid, gid   int
	accessTime time.Time

	target string // a symbolic link's target, as given; mode has fs.ModeSymlink
	ino    uint64
	nlink  uint64 // the names the file has

	// handles counts the handles open on the file. Its memory is given back once it has
	// neither names nor handles.
	handles int
}

// NewMemoryFilesystem creates a new in-memory filesystem.
func NewMemoryFilesystem() *MemoryFilesystem {
	return &MemoryFilesystem{
		files: make(map[string]*memFile),
	}
}

// ReadFile reads the entire contents of a file.
func (m *MemoryFilesystem) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path, f, err := m.resolve("open", path, true)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	}
	if f.isDir {
		return nil, &fs.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
	}
	return append([]byte(nil), f.data...), nil
}

// Stat returns file information, following symbolic links.
func (m *MemoryFilesystem) Stat(path string) (*FileInfo, error) {
	return m.stat(path, true)
}

// Lstat returns file information like Stat, but describes a final symbolic link itself.
func (m *MemoryFilesystem) Lstat(path string) (*FileInfo, error) {
	return m.stat(path, false)
}

func (m *MemoryFilesystem) stat(path string, follow bool) (*FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path, f, err := m.resolve("stat", path, follow)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fs.ErrNotExist
	}
	size := int64(len(f.data))
	if f.isLink() {
		// A link's size is its target's length, as lstat gives it.
		size = int64(len(f.target))
	}
	atime := f.accessTime
	if atime.IsZero() {
		atime = f.modTime
	}
	return &FileInfo{
		Name:       filepath.Base(path),
		Size:       size,
		Mode:       f.mode,
		ModTime:    f.modTime,
		IsDir:      f.isDir,
		AccessTime: atime,
		Uid:        f.uid,
		Gid:        f.gid,
		Ino:        f.ino,
		Nlink:      f.nlink,
	}, nil
}

// Exists checks if a path exists.
func (m *MemoryFilesystem) Exists(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, f, err := m.resolve("stat", path, true)
	return err == nil && f != nil
}

// ReadDir reads the contents of a directory.
func (m *MemoryFilesystem) ReadDir(path string) ([]DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path, f, err := m.resolve("scandir", path, true)
	if err != nil {
		return nil, err
	}
	if path != "/" {
		if f == nil {
			return nil, fs.ErrNotExist
		}
		if !f.isDir {
			return nil, &fs.PathError{Op: "scandir", Path: path, Err: syscall.ENOTDIR}
		}
	}

	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	seen := make(map[string]bool)
	var entries []DirEntry

	for p, f := range m.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" {
			continue
		}
		// Get immediate child
		parts := strings.SplitN(rest, "/", 2)
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		entry := DirEntry{Name: name, IsDir: len(parts) > 1 || f.isDir}
		if len(parts) == 1 && f.isLink() {
			entry.Type = fs.ModeSymlink
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// WriteFile writes data to a file, creating it if necessary.
func (m *MemoryFilesystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.walk("open", path, followFinal)
	if err != nil {
		return err
	}
	path, mf := r.path, r.file
	if mf == nil {
		if err := fileAt("open", r); err != nil {
			return err
		}
	}
	// An existing file keeps its identity and mode, as a real one does, so handles open on it
	// see the new contents.
	if mf != nil {
		if mf.isDir {
			return &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
		}
		if !m.setData(mf, data) {
			return noSpace("write", path)
		}
		mf.modTime = time.Now()
		return nil
	}
	// Making the file fails at open; its contents, at write.
	mf = &memFile{mode: perm, modTime: time.Now()}
	if !m.add(path, mf) {
		return noSpace("open", path)
	}
	if !m.setData(mf, data) {
		m.unname(path)
		return noSpace("write", path)
	}
	return nil
}

// AppendFile appends data to a file.
func (m *MemoryFilesystem) AppendFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.walk("open", path, followFinal)
	if err != nil {
		return err
	}
	path, f := r.path, r.file
	if f == nil {
		if err := fileAt("open", r); err != nil {
			return err
		}
		f = &memFile{mode: 0644, modTime: time.Now()}
		if !m.add(path, f) {
			return noSpace("open", path)
		}
		if !m.setData(f, data) {
			m.unname(path)
			return noSpace("write", path)
		}
		return nil
	}
	if f.isDir {
		return fs.ErrInvalid
	}
	if !m.reserve(f, int64(len(f.data)+len(data))) {
		return noSpace("write", path)
	}
	f.data = append(f.data, data...)
	f.modTime = time.Now()
	return nil
}

// Mkdir creates a directory.
func (m *MemoryFilesystem) Mkdir(path string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.walk("mkdir", path, createFinal)
	if err != nil {
		return err
	}
	path, f := r.path, r.file
	if f != nil {
		return fs.ErrExist
	}
	if !m.add(path, &memFile{mode: perm | fs.ModeDir, modTime: time.Now(), isDir: true}) {
		return noSpace("mkdir", path)
	}
	return nil
}

// MkdirAll creates a directory and all parent directories.
func (m *MemoryFilesystem) MkdirAll(path string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// The canonical path names no link, so the directories to make are those along it. A
	// link that leads nowhere is not made to lead somewhere: mkdir fails on it, as on disk.
	r, err := m.walk("mkdir", path, followFinal)
	if err != nil {
		return err
	}
	if r.missingTarget {
		return &fs.PathError{Op: "mkdir", Path: path, Err: syscall.ENOENT}
	}
	path = r.path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := ""
	for _, part := range parts {
		current += "/" + part
		if _, ok := m.files[current]; !ok {
			if !m.add(current, &memFile{mode: perm | fs.ModeDir, modTime: time.Now(), isDir: true}) {
				return noSpace("mkdir", current)
			}
		}
	}
	return nil
}

// Remove removes a file or empty directory.
func (m *MemoryFilesystem) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A final link is removed itself, not what it leads to.
	r, err := m.walk("unlink", path, entryFinal)
	if err != nil {
		return err
	}
	if r.file == nil {
		return fs.ErrNotExist
	}
	if r.file.isDir && m.hasChildren(r.path) {
		return &fs.PathError{Op: "rmdir", Path: path, Err: syscall.ENOTEMPTY}
	}
	m.unname(r.path)
	return nil
}

// RemoveAll removes a path and all its children.
func (m *MemoryFilesystem) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A final link is removed itself; what it leads to is left alone. A "/" after it is taken
	// as rmdir takes it: Linux refuses it, macOS removes the tree it leads to.
	r, err := m.walk("rm", path, entryFinal)
	if err != nil {
		return err
	}
	path = r.path
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for p := range m.files {
		if p == path || strings.HasPrefix(p, prefix) {
			m.unname(p)
		}
	}
	return nil
}

// Rename renames/moves a file.
func (m *MemoryFilesystem) Rename(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// A link at either path is renamed or replaced itself.
	from, err := m.walk("rename", oldPath, entryFinal)
	if err != nil {
		return err
	}
	to, err := m.walk("rename", newPath, entryFinal)
	if err != nil {
		return err
	}
	oldPath, f, newPath, existing := from.path, from.file, to.path, to.file
	if f == nil {
		return fs.ErrNotExist
	}
	if existing == nil && to.dirOnly && !f.isDir {
		// Only a directory can take a name with a "/" after it: Linux refuses a file with
		// ENOTDIR, macOS with ENOENT.
		errno := syscall.ENOTDIR
		if goruntime.GOOS == "darwin" {
			errno = syscall.ENOENT
		}
		return &fs.PathError{Op: "rename", Path: newPath, Err: errno}
	}
	if existing == f {
		// Two names for one file: rename(2) leaves both.
		return nil
	}
	// The file takes the new name, replacing what had it. A longer name is charged first, so
	// a rename that doesn't fit leaves both names as they were.
	grow := nameCost(newPath) - nameCost(oldPath)
	if grow > 0 && !m.account(grow) {
		return noSpace("rename", oldPath)
	}
	m.unname(newPath)
	if grow < 0 {
		m.release(-grow)
	}
	m.files[newPath] = f
	delete(m.files, oldPath)
	return nil
}

// Copy copies a file.
func (m *MemoryFilesystem) Copy(src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src, f, err := m.resolve("copyfile", src, true)
	if err != nil {
		return err
	}
	to, err := m.walk("copyfile", dst, followFinal)
	if err != nil {
		return err
	}
	dst, existing := to.path, to.file
	if f == nil || f.isDir {
		return fs.ErrNotExist
	}
	// An existing destination keeps its identity, so handles open on it see the copy, and
	// takes the source's mode, as a real copy does.
	if existing != nil && !existing.isDir {
		if !m.setData(existing, f.data) {
			return noSpace("copyfile", dst)
		}
		existing.mode = f.mode
		existing.modTime = time.Now()
		return nil
	}
	if existing == nil {
		if err := fileAt("copyfile", to); err != nil {
			return err
		}
	}
	copied := &memFile{mode: f.mode, modTime: time.Now()}
	if !m.add(dst, copied) {
		return noSpace("copyfile", dst)
	}
	if !m.setData(copied, f.data) {
		m.unname(dst)
		return noSpace("copyfile", dst)
	}
	return nil
}

// memFileHandle implements File interface for memory filesystem.
// memFileHandle is an open file. Like a real descriptor it has its own offset, and its writes
// reach the file at once, so other handles and ReadFile see them.
type memFileHandle struct {
	fs       *MemoryFilesystem
	opened   *memFile // the file opened, which the metadata calls change even after a rename
	path     string
	writable bool
	append   bool
	pos      int64
	closed   bool
}

// file returns the file this handle refers to. The caller holds f.fs.mu.
func (f *memFileHandle) file() (*memFile, error) {
	mf, ok := f.fs.files[f.path]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrNotExist}
	}
	return mf, nil
}

func (f *memFileHandle) Read(p []byte) (n int, err error) {
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	mf, err := f.file()
	if err != nil {
		return 0, err
	}
	if mf.isDir {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: syscall.EISDIR}
	}
	if f.pos >= int64(len(mf.data)) {
		return 0, io.EOF
	}
	n = copy(p, mf.data[f.pos:])
	f.pos += int64(n)
	return n, nil
}

func (f *memFileHandle) Write(p []byte) (n int, err error) {
	if !f.writable {
		return 0, &fs.PathError{Op: "write", Path: f.path, Err: syscall.EBADF}
	}
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()

	mf, err := f.file()
	if err != nil {
		return 0, err
	}
	if len(p) == 0 {
		// An empty write changes nothing, even at an offset past the end.
		return 0, nil
	}
	if f.append {
		f.pos = int64(len(mf.data))
	}
	end := f.pos + int64(len(p))
	if end > int64(len(mf.data)) {
		if !f.fs.reserve(mf, end) {
			return 0, noSpace("write", f.path)
		}
		// Bytes between the old end and the offset read as zeros, as in a sparse file.
		old := int64(len(mf.data))
		mf.data = mf.data[:end]
		if old < f.pos {
			clear(mf.data[old:f.pos])
		}
	}
	copy(mf.data[f.pos:], p)
	f.pos = end
	mf.modTime = time.Now()
	return len(p), nil
}

// Close lets the file go: one with no names left gives its memory back when its last handle
// closes.
func (f *memFileHandle) Close() error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	f.opened.handles--
	f.fs.forget(f.opened)
	return nil
}

func (f *memFileHandle) Seek(offset int64, whence int) (int64, error) {
	f.fs.mu.RLock()
	mf, err := f.file()
	f.fs.mu.RUnlock()
	if err != nil {
		return 0, err
	}

	var pos int64
	switch whence {
	case io.SeekStart:
		pos = offset
	case io.SeekCurrent:
		pos = f.pos + offset
	case io.SeekEnd:
		pos = int64(len(mf.data)) + offset
	default:
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: syscall.EINVAL}
	}
	if pos < 0 {
		return 0, &fs.PathError{Op: "seek", Path: f.path, Err: syscall.EINVAL}
	}
	f.pos = pos
	return pos, nil
}

func (f *memFileHandle) Stat() (*FileInfo, error) {
	return f.fs.Stat(f.path)
}

// Open opens a file for reading.
func (m *MemoryFilesystem) Open(path string) (File, error) {
	return m.OpenFile(path, os.O_RDONLY, 0)
}

// Create creates or truncates a file for writing.
func (m *MemoryFilesystem) Create(path string) (File, error) {
	return m.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

// OpenFile opens a file with the given flags, as os.OpenFile does. Parent directories are not
// checked, because WriteFile has never required them here.
func (m *MemoryFilesystem) OpenFile(path string, flag int, perm fs.FileMode) (File, error) {
	writable := flag&(os.O_WRONLY|os.O_RDWR) != 0

	m.mu.Lock()
	defer m.mu.Unlock()

	// O_EXCL with O_CREATE fails on a final link, even one that leads nowhere, and O_NOFOLLOW
	// refuses one with ELOOP, as open(2) does.
	excl := flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0
	nofollow := flag&syscall.O_NOFOLLOW != 0
	final := followFinal
	if excl || nofollow {
		final = keepFinal
	}
	r, err := m.walk("open", path, final)
	if err != nil {
		return nil, err
	}
	path, mf := r.path, r.file
	exists := mf != nil
	switch {
	case exists && excl:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EEXIST}
	case exists && nofollow && mf.isLink():
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ELOOP}
	case !exists && flag&os.O_CREATE == 0:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	case exists && mf.isDir && writable:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
	case !exists:
		if err := fileAt("open", r); err != nil {
			return nil, err
		}
		mf = &memFile{mode: perm & fs.ModePerm, modTime: time.Now()}
		if !m.add(path, mf) {
			return nil, noSpace("open", path)
		}
	}
	if writable && flag&os.O_TRUNC != 0 {
		m.release(int64(cap(mf.data)))
		mf.data = nil
		mf.modTime = time.Now()
	}
	mf.handles++

	return &memFileHandle{
		fs:       m,
		opened:   mf,
		path:     path,
		writable: writable,
		append:   flag&os.O_APPEND != 0,
	}, nil
}

// change runs op on the file at path, or fails with ENOENT naming op.
func (m *MemoryFilesystem) change(op, path string, apply func(*memFile) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, mf, err := m.resolve(op, path, true)
	if err != nil {
		return err
	}
	if mf == nil {
		return &fs.PathError{Op: op, Path: path, Err: syscall.ENOENT}
	}
	return apply(mf)
}

// resize sets the size of a file, cutting it or padding it with zeros. Callers hold m.mu.
func (m *MemoryFilesystem) resize(mf *memFile, op, path string, size int64) error {
	switch {
	case mf.isDir:
		return &fs.PathError{Op: op, Path: path, Err: syscall.EISDIR}
	case size < 0:
		return &fs.PathError{Op: op, Path: path, Err: syscall.EINVAL}
	case size <= int64(len(mf.data)):
		mf.data = mf.data[:size]
	default:
		if !m.reserve(mf, size) {
			return noSpace(op, path)
		}
		old := len(mf.data)
		mf.data = mf.data[:size]
		clear(mf.data[old:])
	}
	mf.modTime = time.Now()
	return nil
}

// Truncate sets the size of a file, cutting it or padding it with zeros.
func (m *MemoryFilesystem) Truncate(path string, size int64) error {
	return m.change("truncate", path, func(mf *memFile) error { return m.resize(mf, "truncate", path, size) })
}

// Chmod sets the permission bits of a file, and its setuid, setgid and sticky bits.
func (m *MemoryFilesystem) Chmod(path string, mode fs.FileMode) error {
	return m.change("chmod", path, func(mf *memFile) error { mf.chmod(mode); return nil })
}

// chmod sets the permission bits, and the setuid, setgid and sticky bits.
func (mf *memFile) chmod(mode fs.FileMode) {
	const settable = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
	mf.mode = mf.mode&^settable | mode&settable
}

// chown records the owner and group; -1 leaves a value as it is, as in chown(2).
func (mf *memFile) chown(uid, gid int) {
	if uid != -1 {
		mf.uid = uid
	}
	if gid != -1 {
		mf.gid = gid
	}
}

// Chown records the owner and group of a file.
func (m *MemoryFilesystem) Chown(path string, uid, gid int) error {
	return m.change("chown", path, func(mf *memFile) error { mf.chown(uid, gid); return nil })
}

// Chtimes sets the access and modification times of a file.
func (m *MemoryFilesystem) Chtimes(path string, atime, mtime time.Time) error {
	return m.change("utime", path, func(mf *memFile) error {
		mf.accessTime, mf.modTime = atime, mtime
		return nil
	})
}

// Access checks that a path exists and that its owner permission bits allow mode: there is
// one user here, who owns everything.
func (m *MemoryFilesystem) Access(path string, mode uint32) error {
	return m.change("access", path, func(mf *memFile) error {
		owner := uint32(mf.mode.Perm()>>6) & 7
		if mode&7&^owner != 0 {
			return &fs.PathError{Op: "access", Path: path, Err: syscall.EACCES}
		}
		return nil
	})
}

// onOpened runs apply on the file the handle opened, under the filesystem's lock.
func (f *memFileHandle) onOpened(apply func(*memFile) error) error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	return apply(f.opened)
}

// Truncate sets the size of the open file.
func (f *memFileHandle) Truncate(size int64) error {
	if !f.writable {
		return &fs.PathError{Op: "ftruncate", Path: f.path, Err: syscall.EINVAL}
	}
	return f.onOpened(func(mf *memFile) error { return f.fs.resize(mf, "ftruncate", f.path, size) })
}

// Sync has nothing to flush: writes reach the file at once.
func (f *memFileHandle) Sync() error {
	return nil
}

// Chmod sets the permission bits of the open file.
func (f *memFileHandle) Chmod(mode fs.FileMode) error {
	return f.onOpened(func(mf *memFile) error { mf.chmod(mode); return nil })
}

// Chown records the owner and group of the open file.
func (f *memFileHandle) Chown(uid, gid int) error {
	return f.onOpened(func(mf *memFile) error { mf.chown(uid, gid); return nil })
}

// Chtimes sets the access and modification times of the open file.
func (f *memFileHandle) Chtimes(atime, mtime time.Time) error {
	return f.onOpened(func(mf *memFile) error { mf.accessTime, mf.modTime = atime, mtime; return nil })
}

var (
	_ MetadataFilesystem = (*MemoryFilesystem)(nil)
	_ MetadataFile       = (*memFileHandle)(nil)
)
