package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrOutsideSandbox is returned when a path escapes the document root
	ErrOutsideSandbox = errors.New("EACCES: path is outside the sandbox")
)

// LocalFilesystem implements Filesystem using the local OS filesystem.
type LocalFilesystem struct {
	// Root is the document root. If empty, no sandboxing is applied.
	Root string
}

// NewLocalFilesystem creates a new local filesystem.
// If root is empty, the filesystem is unrestricted.
func NewLocalFilesystem(root string) *LocalFilesystem {
	return &LocalFilesystem{Root: root}
}

// resolvePath resolves a path within the sandbox.
func (l *LocalFilesystem) resolvePath(p string) (string, error) {
	// No sandbox configured
	if l.Root == "" {
		if filepath.IsAbs(p) {
			return filepath.Clean(p), nil
		}
		return filepath.Abs(p)
	}

	// Ensure document root is absolute
	absRoot, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}

	// Resolve the path relative to the document root
	var resolvedPath string
	if filepath.IsAbs(p) {
		// Clean the absolute path first
		cleanPath := filepath.Clean(p)
		
		// Check if this absolute path is already within the sandbox
		if strings.HasPrefix(cleanPath+string(filepath.Separator), absRoot+string(filepath.Separator)) ||
			cleanPath == absRoot {
			// Path is already within sandbox, use it directly
			resolvedPath = cleanPath
		} else {
			// Absolute path outside sandbox - treat as relative to sandbox root
			// (strip leading slash and join with root)
			resolvedPath = filepath.Join(absRoot, cleanPath)
		}
	} else {
		// Relative path - join with sandbox root
		resolvedPath = filepath.Join(absRoot, p)
	}

	// Clean the path to resolve any ".." components
	resolvedPath = filepath.Clean(resolvedPath)

	// Verify the resolved path is still within the sandbox
	if !strings.HasPrefix(resolvedPath+string(filepath.Separator), absRoot+string(filepath.Separator)) &&
		resolvedPath != absRoot {
		return "", ErrOutsideSandbox
	}

	return resolvedPath, nil
}

// ReadFile reads the entire contents of a file.
func (l *LocalFilesystem) ReadFile(path string) ([]byte, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

// Stat returns file information.
func (l *LocalFilesystem) Stat(path string) (*FileInfo, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}, nil
}

// Exists checks if a path exists.
func (l *LocalFilesystem) Exists(path string) bool {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(resolved)
	return err == nil
}

// ReadDir reads the contents of a directory.
func (l *LocalFilesystem) ReadDir(path string) ([]DirEntry, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}
	result := make([]DirEntry, len(entries))
	for i, e := range entries {
		result[i] = DirEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
		}
	}
	return result, nil
}

// WriteFile writes data to a file, creating it if necessary.
func (l *LocalFilesystem) WriteFile(path string, data []byte, perm fs.FileMode) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.WriteFile(resolved, data, perm)
}

// AppendFile appends data to a file.
func (l *LocalFilesystem) AppendFile(path string, data []byte) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(resolved, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// Mkdir creates a directory.
func (l *LocalFilesystem) Mkdir(path string, perm fs.FileMode) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Mkdir(resolved, perm)
}

// MkdirAll creates a directory and all parent directories.
func (l *LocalFilesystem) MkdirAll(path string, perm fs.FileMode) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(resolved, perm)
}

// Remove removes a file or empty directory.
func (l *LocalFilesystem) Remove(path string) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Remove(resolved)
}

// RemoveAll removes a path and all its children.
func (l *LocalFilesystem) RemoveAll(path string) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(resolved)
}

// Rename renames/moves a file.
func (l *LocalFilesystem) Rename(oldPath, newPath string) error {
	resolvedOld, err := l.resolvePath(oldPath)
	if err != nil {
		return err
	}
	resolvedNew, err := l.resolvePath(newPath)
	if err != nil {
		return err
	}
	return os.Rename(resolvedOld, resolvedNew)
}

// Copy copies a file.
func (l *LocalFilesystem) Copy(src, dst string) error {
	resolvedSrc, err := l.resolvePath(src)
	if err != nil {
		return err
	}
	resolvedDst, err := l.resolvePath(dst)
	if err != nil {
		return err
	}

	srcFile, err := os.Open(resolvedSrc)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(resolvedDst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// localFile wraps os.File to implement our File interface.
type localFile struct {
	*os.File
}

// Stat returns file information for the open file.
func (f *localFile) Stat() (*FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}, nil
}

// Open opens a file for reading.
func (l *LocalFilesystem) Open(path string) (File, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	return &localFile{f}, nil
}

// Create creates a file for writing.
func (l *LocalFilesystem) Create(path string) (File, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(resolved)
	if err != nil {
		return nil, err
	}
	return &localFile{f}, nil
}

// OpenFile opens a file with the given flags.
func (l *LocalFilesystem) OpenFile(path string, flag int, perm fs.FileMode) (File, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(resolved, flag, perm)
	if err != nil {
		return nil, err
	}
	return &localFile{f}, nil
}
