package runtime

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MemoryFilesystem implements Filesystem using in-memory storage.
// Useful for testing or temporary file operations.
type MemoryFilesystem struct {
	mu    sync.RWMutex
	files map[string]*memFile // by canonical path; hard links share a memFile

	lastIno uint64
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

	path, mf, err := m.resolve("open", path, true)
	if err != nil {
		return err
	}
	// An existing file keeps its identity and mode, as a real one does, so handles open on it
	// see the new contents.
	if mf != nil {
		if mf.isDir {
			return &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
		}
		mf.data = append([]byte(nil), data...)
		mf.modTime = time.Now()
		return nil
	}
	m.files[path] = m.newFile(&memFile{
		data:    append([]byte(nil), data...),
		mode:    perm,
		modTime: time.Now(),
		isDir:   false,
	})
	return nil
}

// AppendFile appends data to a file.
func (m *MemoryFilesystem) AppendFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, f, err := m.resolve("open", path, true)
	if err != nil {
		return err
	}
	if f == nil {
		m.files[path] = m.newFile(&memFile{
			data:    append([]byte(nil), data...),
			mode:    0644,
			modTime: time.Now(),
			isDir:   false,
		})
		return nil
	}
	if f.isDir {
		return fs.ErrInvalid
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
	m.files[path] = m.newFile(&memFile{
		mode:    perm | fs.ModeDir,
		modTime: time.Now(),
		isDir:   true,
	})
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
	if path == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := ""
	for _, part := range parts {
		current += "/" + part
		if _, ok := m.files[current]; !ok {
			m.files[current] = m.newFile(&memFile{
				mode:    perm | fs.ModeDir,
				modTime: time.Now(),
				isDir:   true,
			})
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

	// A final link is removed itself; what it leads to is left alone.
	path, _, err := m.resolve("rm", path, false)
	if err != nil {
		return err
	}
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
	if existing == f {
		// Two names for one file: rename(2) leaves both.
		return nil
	}
	m.unname(newPath)
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
	dst, existing, err := m.resolve("copyfile", dst, true)
	if err != nil {
		return err
	}
	if f == nil || f.isDir {
		return fs.ErrNotExist
	}
	// An existing destination keeps its identity, so handles open on it see the copy, and
	// takes the source's mode, as a real copy does.
	if existing != nil && !existing.isDir {
		existing.data = append([]byte(nil), f.data...)
		existing.mode = f.mode
		existing.modTime = time.Now()
		return nil
	}
	m.files[dst] = m.newFile(&memFile{
		data:    append([]byte(nil), f.data...),
		mode:    f.mode,
		modTime: time.Now(),
		isDir:   false,
	})
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
		if end > int64(cap(mf.data)) {
			// Grow geometrically, as append does, so writing a file in chunks costs
			// linear time rather than copying the whole file on every write.
			grown := make([]byte, len(mf.data), max(end, 2*int64(cap(mf.data))))
			copy(grown, mf.data)
			mf.data = grown
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

func (f *memFileHandle) Close() error {
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

	// O_EXCL with O_CREATE fails on a final link, even one that leads nowhere, as open(2) does.
	excl := flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0
	path, mf, err := m.resolve("open", path, !excl)
	if err != nil {
		return nil, err
	}
	exists := mf != nil
	switch {
	case exists && flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EEXIST}
	case !exists && flag&os.O_CREATE == 0:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	case exists && mf.isDir && writable:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
	case !exists:
		mf = m.newFile(&memFile{mode: perm & fs.ModePerm, modTime: time.Now()})
		m.files[path] = mf
	}
	if writable && flag&os.O_TRUNC != 0 {
		mf.data = nil
		mf.modTime = time.Now()
	}

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

// resize sets the size of a file, cutting it or padding it with zeros.
func (mf *memFile) resize(op, path string, size int64) error {
	switch {
	case mf.isDir:
		return &fs.PathError{Op: op, Path: path, Err: syscall.EISDIR}
	case size < 0:
		return &fs.PathError{Op: op, Path: path, Err: syscall.EINVAL}
	case size <= int64(len(mf.data)):
		mf.data = mf.data[:size]
	default:
		mf.data = append(mf.data, make([]byte, size-int64(len(mf.data)))...)
	}
	mf.modTime = time.Now()
	return nil
}

// Truncate sets the size of a file, cutting it or padding it with zeros.
func (m *MemoryFilesystem) Truncate(path string, size int64) error {
	return m.change("truncate", path, func(mf *memFile) error { return mf.resize("truncate", path, size) })
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
	return f.onOpened(func(mf *memFile) error { return mf.resize("ftruncate", f.path, size) })
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
