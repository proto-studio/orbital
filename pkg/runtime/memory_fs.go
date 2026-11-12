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
	files map[string]*memFile
}

type memFile struct {
	data    []byte
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

// NewMemoryFilesystem creates a new in-memory filesystem.
func NewMemoryFilesystem() *MemoryFilesystem {
	return &MemoryFilesystem{
		files: make(map[string]*memFile),
	}
}

func (m *MemoryFilesystem) normalizePath(path string) string {
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

// ReadFile reads the entire contents of a file.
func (m *MemoryFilesystem) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = m.normalizePath(path)
	f, ok := m.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	}
	if f.isDir {
		return nil, &fs.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
	}
	return append([]byte(nil), f.data...), nil
}

// Stat returns file information.
func (m *MemoryFilesystem) Stat(path string) (*FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = m.normalizePath(path)
	f, ok := m.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &FileInfo{
		Name:    filepath.Base(path),
		Size:    int64(len(f.data)),
		Mode:    f.mode,
		ModTime: f.modTime,
		IsDir:   f.isDir,
	}, nil
}

// Exists checks if a path exists.
func (m *MemoryFilesystem) Exists(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = m.normalizePath(path)
	_, ok := m.files[path]
	return ok
}

// ReadDir reads the contents of a directory.
func (m *MemoryFilesystem) ReadDir(path string) ([]DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	path = m.normalizePath(path)
	if path != "/" {
		f, ok := m.files[path]
		if !ok || !f.isDir {
			return nil, fs.ErrNotExist
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
		entries = append(entries, DirEntry{
			Name:  name,
			IsDir: len(parts) > 1 || f.isDir,
		})
	}

	return entries, nil
}

// WriteFile writes data to a file, creating it if necessary.
func (m *MemoryFilesystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = m.normalizePath(path)
	m.files[path] = &memFile{
		data:    append([]byte(nil), data...),
		mode:    perm,
		modTime: time.Now(),
		isDir:   false,
	}
	return nil
}

// AppendFile appends data to a file.
func (m *MemoryFilesystem) AppendFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = m.normalizePath(path)
	f, ok := m.files[path]
	if !ok {
		m.files[path] = &memFile{
			data:    append([]byte(nil), data...),
			mode:    0644,
			modTime: time.Now(),
			isDir:   false,
		}
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

	path = m.normalizePath(path)
	if _, ok := m.files[path]; ok {
		return fs.ErrExist
	}
	m.files[path] = &memFile{
		mode:    perm | fs.ModeDir,
		modTime: time.Now(),
		isDir:   true,
	}
	return nil
}

// MkdirAll creates a directory and all parent directories.
func (m *MemoryFilesystem) MkdirAll(path string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = m.normalizePath(path)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	current := ""
	for _, part := range parts {
		current += "/" + part
		if _, ok := m.files[current]; !ok {
			m.files[current] = &memFile{
				mode:    perm | fs.ModeDir,
				modTime: time.Now(),
				isDir:   true,
			}
		}
	}
	return nil
}

// Remove removes a file or empty directory.
func (m *MemoryFilesystem) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = m.normalizePath(path)
	if _, ok := m.files[path]; !ok {
		return fs.ErrNotExist
	}
	delete(m.files, path)
	return nil
}

// RemoveAll removes a path and all its children.
func (m *MemoryFilesystem) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path = m.normalizePath(path)
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for p := range m.files {
		if p == path || strings.HasPrefix(p, prefix) {
			delete(m.files, p)
		}
	}
	return nil
}

// Rename renames/moves a file.
func (m *MemoryFilesystem) Rename(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldPath = m.normalizePath(oldPath)
	newPath = m.normalizePath(newPath)

	f, ok := m.files[oldPath]
	if !ok {
		return fs.ErrNotExist
	}
	m.files[newPath] = f
	delete(m.files, oldPath)
	return nil
}

// Copy copies a file.
func (m *MemoryFilesystem) Copy(src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	src = m.normalizePath(src)
	dst = m.normalizePath(dst)

	f, ok := m.files[src]
	if !ok || f.isDir {
		return fs.ErrNotExist
	}
	m.files[dst] = &memFile{
		data:    append([]byte(nil), f.data...),
		mode:    f.mode,
		modTime: time.Now(),
		isDir:   false,
	}
	return nil
}

// memFileHandle implements File interface for memory filesystem.
// memFileHandle is an open file. Like a real descriptor it has its own offset, and its writes
// reach the file at once, so other handles and ReadFile see them.
type memFileHandle struct {
	fs       *MemoryFilesystem
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
	path = m.normalizePath(path)
	writable := flag&(os.O_WRONLY|os.O_RDWR) != 0

	m.mu.Lock()
	defer m.mu.Unlock()

	mf, exists := m.files[path]
	switch {
	case exists && flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EEXIST}
	case !exists && flag&os.O_CREATE == 0:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.ENOENT}
	case exists && mf.isDir && writable:
		return nil, &fs.PathError{Op: "open", Path: path, Err: syscall.EISDIR}
	case !exists:
		mf = &memFile{mode: perm & fs.ModePerm, modTime: time.Now()}
		m.files[path] = mf
	}
	if writable && flag&os.O_TRUNC != 0 {
		mf.data = nil
		mf.modTime = time.Now()
	}

	return &memFileHandle{
		fs:       m,
		path:     path,
		writable: writable,
		append:   flag&os.O_APPEND != 0,
	}, nil
}
