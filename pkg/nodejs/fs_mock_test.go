package nodejs

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// mockFS is a runtime.Filesystem that keeps no files. It records each call it gets, and
// answers with a chosen errno or with a fixed result: every path is a file holding "hello",
// except a name starting with "dir", which is a directory holding one file, "a".
//
// A script marks where each of its cases starts by calling fs.existsSync on
// "/@mark/<case>/<ERRNO>[/<Method>]". Calls after the mark fail with ERRNO (or, with a
// Method, only calls of that method do) until the next mark; "-" fails nothing.
type mockFS struct {
	mu       sync.Mutex
	calls    map[string][]string
	cases    []string
	current  string
	errno    syscall.Errno
	failOnly string
}

const mockMark = "/@mark/"

func newMockFS() *mockFS { return &mockFS{calls: map[string][]string{}} }

// newMockFSFailing returns a mockFS whose every call fails with errno, until a mark says
// otherwise.
func newMockFSFailing(errno syscall.Errno) *mockFS {
	m := newMockFS()
	m.errno = errno
	return m
}

// callsIn gives the calls recorded in the named case, in order.
func (m *mockFS) callsIn(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

var mockErrnos = map[string]syscall.Errno{
	"EACCES": syscall.EACCES, "EBADF": syscall.EBADF, "EEXIST": syscall.EEXIST, "EINVAL": syscall.EINVAL,
	"EISDIR": syscall.EISDIR, "ELOOP": syscall.ELOOP, "ENAMETOOLONG": syscall.ENAMETOOLONG,
	"ENOENT": syscall.ENOENT, "ENOSPC": syscall.ENOSPC, "ENOTDIR": syscall.ENOTDIR,
	"ENOTEMPTY": syscall.ENOTEMPTY, "EPERM": syscall.EPERM,
}

// call records a call and gives the error it should fail with, or nil.
func (m *mockFS) call(method string, args ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	m.calls[m.current] = append(m.calls[m.current], method+"("+strings.Join(parts, ", ")+")")
	if m.errno == 0 || (m.failOnly != "" && m.failOnly != method) {
		return nil
	}
	p, _ := args[0].(string)
	return &fs.PathError{Op: method, Path: p, Err: m.errno}
}

func (m *mockFS) mark(p string) {
	fields := strings.Split(strings.TrimPrefix(p, mockMark), "/")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current, m.errno, m.failOnly = fields[0], mockErrnos[fields[1]], ""
	m.cases = append(m.cases, m.current)
	if len(fields) > 2 {
		m.failOnly = fields[2]
	}
}

func mockIsDir(p string) bool { return strings.HasPrefix(path.Base(p), "dir") }

func mockInfo(p string) *runtime.FileInfo {
	info := &runtime.FileInfo{Name: path.Base(p), Size: 5, Mode: 0o644, ModTime: time.Unix(1e9, 0), Nlink: 1}
	if mockIsDir(p) {
		info.Mode, info.IsDir, info.Size = fs.ModeDir|0o755, true, 0
	}
	return info
}

func (m *mockFS) ReadFile(p string) ([]byte, error) {
	if err := m.call("ReadFile", p); err != nil {
		return nil, err
	}
	return []byte("hello"), nil
}

func (m *mockFS) Stat(p string) (*runtime.FileInfo, error) {
	if err := m.call("Stat", p); err != nil {
		return nil, err
	}
	return mockInfo(p), nil
}

func (m *mockFS) Lstat(p string) (*runtime.FileInfo, error) {
	if err := m.call("Lstat", p); err != nil {
		return nil, err
	}
	return mockInfo(p), nil
}

func (m *mockFS) Exists(p string) bool {
	if strings.HasPrefix(p, mockMark) {
		m.mark(p)
		return false
	}
	return m.call("Exists", p) == nil
}

func (m *mockFS) ReadDir(p string) ([]runtime.DirEntry, error) {
	if err := m.call("ReadDir", p); err != nil {
		return nil, err
	}
	return []runtime.DirEntry{{Name: "a"}}, nil
}

func (m *mockFS) WriteFile(p string, data []byte, perm fs.FileMode) error {
	return m.call("WriteFile", p, fmt.Sprintf("%q", data), fmt.Sprintf("%o", perm))
}

func (m *mockFS) AppendFile(p string, data []byte) error {
	return m.call("AppendFile", p, fmt.Sprintf("%q", data))
}

func (m *mockFS) Mkdir(p string, perm fs.FileMode) error {
	return m.call("Mkdir", p, fmt.Sprintf("%o", perm))
}

func (m *mockFS) MkdirAll(p string, perm fs.FileMode) error {
	return m.call("MkdirAll", p, fmt.Sprintf("%o", perm))
}

func (m *mockFS) Remove(p string) error                { return m.call("Remove", p) }
func (m *mockFS) RemoveAll(p string) error             { return m.call("RemoveAll", p) }
func (m *mockFS) Rename(oldPath, newPath string) error { return m.call("Rename", oldPath, newPath) }
func (m *mockFS) Copy(src, dst string) error           { return m.call("Copy", src, dst) }

func (m *mockFS) Open(p string) (runtime.File, error) {
	if err := m.call("Open", p); err != nil {
		return nil, err
	}
	return &mockFile{m: m, name: p, Reader: bytes.NewReader([]byte("hello"))}, nil
}

func (m *mockFS) Create(p string) (runtime.File, error) {
	if err := m.call("Create", p); err != nil {
		return nil, err
	}
	return &mockFile{m: m, name: p, Reader: bytes.NewReader(nil)}, nil
}

// openFlags names the flags of an OpenFile call, such as "O_WRONLY|O_CREAT|O_TRUNC".
func openFlags(flag int) string {
	names := []string{[]string{"O_RDONLY", "O_WRONLY", "O_RDWR"}[flag&3]}
	for _, f := range []struct {
		bit  int
		name string
	}{{os.O_CREATE, "O_CREAT"}, {os.O_EXCL, "O_EXCL"}, {os.O_TRUNC, "O_TRUNC"}, {os.O_APPEND, "O_APPEND"}} {
		if flag&f.bit != 0 {
			names = append(names, f.name)
		}
	}
	return strings.Join(names, "|")
}

func (m *mockFS) OpenFile(p string, flag int, perm fs.FileMode) (runtime.File, error) {
	if err := m.call("OpenFile", p, openFlags(flag), fmt.Sprintf("%o", perm)); err != nil {
		return nil, err
	}
	content := []byte("hello")
	if flag&os.O_TRUNC != 0 || flag&3 == os.O_WRONLY {
		content = nil
	}
	return &mockFile{m: m, name: p, Reader: bytes.NewReader(content)}, nil
}

func (m *mockFS) Truncate(p string, size int64) error { return m.call("Truncate", p, size) }
func (m *mockFS) Chmod(p string, mode fs.FileMode) error {
	return m.call("Chmod", p, fmt.Sprintf("%o", mode))
}
func (m *mockFS) Chown(p string, uid, gid int) error { return m.call("Chown", p, uid, gid) }
func (m *mockFS) Chtimes(p string, atime, mtime time.Time) error {
	return m.call("Chtimes", p, atime.Unix(), mtime.Unix())
}
func (m *mockFS) Access(p string, mode uint32) error { return m.call("Access", p, mode) }
func (m *mockFS) Symlink(target, p string) error     { return m.call("Symlink", target, p) }
func (m *mockFS) Readlink(p string) (string, error) {
	if err := m.call("Readlink", p); err != nil {
		return "", err
	}
	return "target", nil
}
func (m *mockFS) Link(existing, p string) error { return m.call("Link", existing, p) }
func (m *mockFS) Realpath(p string) (string, error) {
	if err := m.call("Realpath", p); err != nil {
		return "", err
	}
	return p, nil
}
func (m *mockFS) Statfs(p string) (*runtime.FsStats, error) {
	if err := m.call("Statfs", p); err != nil {
		return nil, err
	}
	return &runtime.FsStats{Bsize: 4096}, nil
}

// mockFile is an open file of a mockFS. Its reads come from a fixed buffer, and its calls
// are recorded as File.<Method>.
type mockFile struct {
	m    *mockFS
	name string
	*bytes.Reader
}

func (f *mockFile) Read(p []byte) (int, error) {
	if err := f.m.call("File.Read", f.name); err != nil {
		return 0, err
	}
	return f.Reader.Read(p)
}

func (f *mockFile) Write(p []byte) (int, error) {
	if err := f.m.call("File.Write", f.name, fmt.Sprintf("%q", p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *mockFile) Seek(offset int64, whence int) (int64, error) {
	if err := f.m.call("File.Seek", f.name, offset, whence); err != nil {
		return 0, err
	}
	if whence == io.SeekEnd {
		return 5 + offset, nil
	}
	return f.Reader.Seek(offset, whence)
}

func (f *mockFile) Close() error { return f.m.call("File.Close", f.name) }

func (f *mockFile) Stat() (*runtime.FileInfo, error) {
	if err := f.m.call("File.Stat", f.name); err != nil {
		return nil, err
	}
	return mockInfo(f.name), nil
}

var (
	_ runtime.Filesystem         = (*mockFS)(nil)
	_ runtime.LstatFilesystem    = (*mockFS)(nil)
	_ runtime.MetadataFilesystem = (*mockFS)(nil)
	_ runtime.LinkFilesystem     = (*mockFS)(nil)
	_ runtime.StatfsFilesystem   = (*mockFS)(nil)
)
