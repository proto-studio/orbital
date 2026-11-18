package runtime

import (
	"io/fs"
	"syscall"
	"time"
)

// NoOpFilesystem refuses every operation, for scripts that should have no filesystem at all.
// Every call fails with EACCES, as the OS reports a path the process may not access, so
// scripts see Node's "EACCES: permission denied" errors. Nothing on the host or in memory is
// read or changed.
//
// Loading code from files needs a filesystem, so with this one require and import of files
// and new Worker(file) fail as Node does for a file it can't read, with MODULE_NOT_FOUND or
// ERR_MODULE_NOT_FOUND. Scripts must be passed as source instead.
type NoOpFilesystem struct{}

// NewNoOpFilesystem creates a filesystem that refuses every operation.
func NewNoOpFilesystem() *NoOpFilesystem {
	return &NoOpFilesystem{}
}

var (
	_ Filesystem         = (*NoOpFilesystem)(nil)
	_ LstatFilesystem    = (*NoOpFilesystem)(nil)
	_ MetadataFilesystem = (*NoOpFilesystem)(nil)
	_ LinkFilesystem     = (*NoOpFilesystem)(nil)
	_ StatfsFilesystem   = (*NoOpFilesystem)(nil)
)

// denied is the error every NoOpFilesystem call returns.
func denied(op, path string) error {
	return &fs.PathError{Op: op, Path: path, Err: syscall.EACCES}
}

// ReadFile always fails with EACCES.
func (n *NoOpFilesystem) ReadFile(path string) ([]byte, error) {
	return nil, denied("open", path)
}

// Stat always fails with EACCES.
func (n *NoOpFilesystem) Stat(path string) (*FileInfo, error) {
	return nil, denied("stat", path)
}

// Lstat always fails with EACCES.
func (n *NoOpFilesystem) Lstat(path string) (*FileInfo, error) {
	return nil, denied("lstat", path)
}

// Exists always reports false, as Node's existsSync does for a path it can't access.
func (n *NoOpFilesystem) Exists(path string) bool {
	return false
}

// ReadDir always fails with EACCES.
func (n *NoOpFilesystem) ReadDir(path string) ([]DirEntry, error) {
	return nil, denied("open", path)
}

// WriteFile always fails with EACCES.
func (n *NoOpFilesystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return denied("open", path)
}

// AppendFile always fails with EACCES.
func (n *NoOpFilesystem) AppendFile(path string, data []byte) error {
	return denied("open", path)
}

// Mkdir always fails with EACCES.
func (n *NoOpFilesystem) Mkdir(path string, perm fs.FileMode) error {
	return denied("mkdir", path)
}

// MkdirAll always fails with EACCES.
func (n *NoOpFilesystem) MkdirAll(path string, perm fs.FileMode) error {
	return denied("mkdir", path)
}

// Remove always fails with EACCES.
func (n *NoOpFilesystem) Remove(path string) error {
	return denied("remove", path)
}

// RemoveAll always fails with EACCES.
func (n *NoOpFilesystem) RemoveAll(path string) error {
	return denied("remove", path)
}

// Rename always fails with EACCES.
func (n *NoOpFilesystem) Rename(oldPath, newPath string) error {
	return denied("rename", oldPath)
}

// Copy always fails with EACCES.
func (n *NoOpFilesystem) Copy(src, dst string) error {
	return denied("copyfile", src)
}

// Open always fails with EACCES.
func (n *NoOpFilesystem) Open(path string) (File, error) {
	return nil, denied("open", path)
}

// Create always fails with EACCES.
func (n *NoOpFilesystem) Create(path string) (File, error) {
	return nil, denied("open", path)
}

// OpenFile always fails with EACCES.
func (n *NoOpFilesystem) OpenFile(path string, flag int, perm fs.FileMode) (File, error) {
	return nil, denied("open", path)
}

// Truncate always fails with EACCES.
func (n *NoOpFilesystem) Truncate(path string, size int64) error {
	return denied("truncate", path)
}

// Chmod always fails with EACCES.
func (n *NoOpFilesystem) Chmod(path string, mode fs.FileMode) error {
	return denied("chmod", path)
}

// Chown always fails with EACCES.
func (n *NoOpFilesystem) Chown(path string, uid, gid int) error {
	return denied("chown", path)
}

// Chtimes always fails with EACCES.
func (n *NoOpFilesystem) Chtimes(path string, atime, mtime time.Time) error {
	return denied("utime", path)
}

// Access always fails with EACCES.
func (n *NoOpFilesystem) Access(path string, mode uint32) error {
	return denied("access", path)
}

// Symlink always fails with EACCES.
func (n *NoOpFilesystem) Symlink(target, path string) error {
	return denied("symlink", path)
}

// Readlink always fails with EACCES.
func (n *NoOpFilesystem) Readlink(path string) (string, error) {
	return "", denied("readlink", path)
}

// Link always fails with EACCES.
func (n *NoOpFilesystem) Link(existing, path string) error {
	return denied("link", path)
}

// Realpath always fails with EACCES.
func (n *NoOpFilesystem) Realpath(path string) (string, error) {
	return "", denied("realpath", path)
}

// Statfs always fails with EACCES.
func (n *NoOpFilesystem) Statfs(path string) (*FsStats, error) {
	return nil, denied("statfs", path)
}
