// Package filesystem defines the interface for filesystem operations.
package filesystem

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
