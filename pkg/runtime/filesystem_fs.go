// Package filesystem defines the interface for filesystem operations.
package runtime

import (
	"io"
	"io/fs"
	"time"
)

// FileInfo contains information about a file.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool

	// Optional details, as the OS reports them. A Filesystem that doesn't track them leaves
	// them zero, and callers fall back on ModTime and plain defaults.
	AccessTime time.Time
	ChangeTime time.Time
	BirthTime  time.Time
	Uid, Gid   int
	Dev, Ino   uint64
	Nlink      uint64
	Rdev       uint64
	Blksize    int64
	Blocks     int64
	HasDetails bool
}

// FsStats describes a mounted filesystem, as statfs reports it.
type FsStats struct {
	Type   int64
	Bsize  int64
	Blocks uint64
	Bfree  uint64
	Bavail uint64
	Files  uint64
	Ffree  uint64
}

// MetadataFilesystem is implemented by a Filesystem that can change a file's size, mode,
// owner and times, and check access to it. It is optional: without it those calls fail with
// ENOSYS.
type MetadataFilesystem interface {
	Truncate(path string, size int64) error
	Chmod(path string, mode fs.FileMode) error
	Chown(path string, uid, gid int) error
	Chtimes(path string, atime, mtime time.Time) error
	// Access checks that path exists and, for mode's R_OK (4), W_OK (2) and X_OK (1) bits,
	// that it may be read, written or run.
	Access(path string, mode uint32) error
}

// StatfsFilesystem is implemented by a Filesystem that can describe the filesystem a path
// is on. It is optional: without it statfs fails with ENOSYS.
type StatfsFilesystem interface {
	Statfs(path string) (*FsStats, error)
}

// MetadataFile is implemented by a File that can change its size, mode, owner and times and
// flush its writes. It is optional: without it those calls fail with ENOSYS.
type MetadataFile interface {
	Truncate(size int64) error
	Sync() error
	Chmod(mode fs.FileMode) error
	Chown(uid, gid int) error
	Chtimes(atime, mtime time.Time) error
}

// DirEntry represents a directory entry.
type DirEntry struct {
	Name  string
	IsDir bool
}

// File represents an open file handle.
type File interface {
	io.Reader
	io.Writer
	io.Closer
	io.Seeker
	Stat() (*FileInfo, error)
}

// LstatFilesystem is implemented by a Filesystem that can describe a symbolic link itself
// rather than what it points to. It is optional: callers fall back to Stat without it.
type LstatFilesystem interface {
	Lstat(path string) (*FileInfo, error)
}

// Lstat describes path without following a final symbolic link, if fsys can, and otherwise
// as Stat does.
func Lstat(fsys Filesystem, path string) (*FileInfo, error) {
	if l, ok := fsys.(LstatFilesystem); ok {
		return l.Lstat(path)
	}
	return fsys.Stat(path)
}

// Filesystem defines the interface for filesystem operations.
type Filesystem interface {
	// Read operations
	ReadFile(path string) ([]byte, error)
	Stat(path string) (*FileInfo, error)
	Exists(path string) bool
	ReadDir(path string) ([]DirEntry, error)

	// Write operations
	WriteFile(path string, data []byte, perm fs.FileMode) error
	AppendFile(path string, data []byte) error
	Mkdir(path string, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Remove(path string) error
	RemoveAll(path string) error
	Rename(oldPath, newPath string) error
	Copy(src, dst string) error

	// File handle operations (for streaming)
	Open(path string) (File, error)
	Create(path string) (File, error)
	OpenFile(path string, flag int, perm fs.FileMode) (File, error)
}
