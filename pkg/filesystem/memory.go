package filesystem

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if !ok || f.isDir {
		return nil, fs.ErrNotExist
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
type memFileHandle struct {
	fs       *MemoryFilesystem
	path     string
	buf      *bytes.Buffer
	writable bool
	pos      int64
}

func (f *memFileHandle) Read(p []byte) (n int, err error) {
	f.fs.mu.RLock()
	defer f.fs.mu.RUnlock()

	mf, ok := f.fs.files[f.path]
	if !ok {
		return 0, fs.ErrNotExist
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
		return 0, fs.ErrPermission
	}
	return f.buf.Write(p)
}

func (f *memFileHandle) Close() error {
	if f.writable && f.buf != nil {
		f.fs.mu.Lock()
		f.fs.files[f.path] = &memFile{
			data:    f.buf.Bytes(),
			mode:    0644,
			modTime: time.Now(),
			isDir:   false,
		}
		f.fs.mu.Unlock()
	}
	return nil
}

func (f *memFileHandle) Seek(offset int64, whence int) (int64, error) {
	f.fs.mu.RLock()
	mf, ok := f.fs.files[f.path]
	f.fs.mu.RUnlock()

	if !ok {
		return 0, fs.ErrNotExist
	}

	switch whence {
	case io.SeekStart:
		f.pos = offset
	case io.SeekCurrent:
		f.pos += offset
	case io.SeekEnd:
		f.pos = int64(len(mf.data)) + offset
	}
	return f.pos, nil
}

func (f *memFileHandle) Stat() (*FileInfo, error) {
	return f.fs.Stat(f.path)
}

// Open opens a file for reading.
func (m *MemoryFilesystem) Open(path string) (File, error) {
	path = m.normalizePath(path)
	if !m.Exists(path) {
		return nil, fs.ErrNotExist
	}
	return &memFileHandle{fs: m, path: path}, nil
}

// Create creates a file for writing.
func (m *MemoryFilesystem) Create(path string) (File, error) {
	path = m.normalizePath(path)
	return &memFileHandle{
		fs:       m,
		path:     path,
		buf:      new(bytes.Buffer),
		writable: true,
	}, nil
}

// OpenFile opens a file with the given flags.
func (m *MemoryFilesystem) OpenFile(path string, flag int, perm fs.FileMode) (File, error) {
	path = m.normalizePath(path)
	writable := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0

	handle := &memFileHandle{
		fs:       m,
		path:     path,
		writable: writable,
	}

	if writable {
		handle.buf = new(bytes.Buffer)
		if flag&os.O_APPEND != 0 {
			if data, err := m.ReadFile(path); err == nil {
				handle.buf.Write(data)
			}
		}
	}

	return handle, nil
}
