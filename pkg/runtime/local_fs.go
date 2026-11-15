package runtime

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrOutsideSandbox is the error a call returns for a path it refuses. It is a permission
// error, so callers that report errno codes report EACCES, as its text says.
var ErrOutsideSandbox error = pathCheckError{}

type pathCheckError struct{}

func (pathCheckError) Error() string { return "EACCES: path is outside the sandbox" }

func (pathCheckError) Unwrap() error { return syscall.EACCES }

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

// resolvePath gives the path the OS opens for p when there is no root. With a root, each
// method walks the path itself instead (local_fs_walk.go).
func (l *LocalFilesystem) resolvePath(p string) (string, error) {
	return l.textualPath(p)
}

// textualPath gives the path p names, taken within the root if there is one, from its text
// alone.
func (l *LocalFilesystem) textualPath(p string) (string, error) {
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
	if l.Root != "" {
		return l.rootedReadFile(path)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

// Stat returns file information.
func (l *LocalFilesystem) Stat(path string) (*FileInfo, error) {
	if l.Root != "" {
		return l.rootedStat(path, true)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	return fileInfoOf(info), nil
}

// Lstat returns file information like Stat, but describes a final symbolic link itself.
func (l *LocalFilesystem) Lstat(path string) (*FileInfo, error) {
	if l.Root != "" {
		return l.rootedStat(path, false)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, err
	}
	return fileInfoOf(info), nil
}

// Exists checks if a path exists.
func (l *LocalFilesystem) Exists(path string) bool {
	if l.Root != "" {
		_, err := l.rootedStat(path, true)
		return err == nil
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return false
	}
	_, err = os.Stat(resolved)
	return err == nil
}

// ReadDir reads the contents of a directory.
func (l *LocalFilesystem) ReadDir(path string) ([]DirEntry, error) {
	if l.Root != "" {
		return l.rootedReadDir(path)
	}
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
	if l.Root != "" {
		return l.rootedWrite(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm, data)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.WriteFile(resolved, data, perm)
}

// AppendFile appends data to a file.
func (l *LocalFilesystem) AppendFile(path string, data []byte) error {
	if l.Root != "" {
		return l.rootedWrite(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644, data)
	}
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
	if l.Root != "" {
		return l.rootedMkdir(path, perm)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Mkdir(resolved, perm)
}

// MkdirAll creates a directory and all parent directories.
func (l *LocalFilesystem) MkdirAll(path string, perm fs.FileMode) error {
	if l.Root != "" {
		return l.rootedMkdirAll(path, perm)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(resolved, perm)
}

// Remove removes a file or empty directory.
func (l *LocalFilesystem) Remove(path string) error {
	if l.Root != "" {
		return l.rootedRemove(path)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Remove(resolved)
}

// RemoveAll removes a path and all its children.
func (l *LocalFilesystem) RemoveAll(path string) error {
	if l.Root != "" {
		return l.rootedRemoveAll(path)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(resolved)
}

// Rename renames/moves a file.
func (l *LocalFilesystem) Rename(oldPath, newPath string) error {
	if l.Root != "" {
		return l.rootedRename(oldPath, newPath)
	}
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
	if l.Root != "" {
		return l.rootedCopy(src, dst)
	}
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

// fileInfoOf describes info as a FileInfo, with the OS's details.
func fileInfoOf(info fs.FileInfo) *FileInfo {
	fi := &FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}
	addDetails(fi, info.Sys())
	return fi
}

// Truncate sets the size of a file, cutting it or padding it with zeros.
func (l *LocalFilesystem) Truncate(path string, size int64) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Truncate(resolved, size)
}

// Chmod sets the mode of a file.
func (l *LocalFilesystem) Chmod(path string, mode fs.FileMode) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Chmod(resolved, mode)
}

// Chown sets the owner and group of a file.
func (l *LocalFilesystem) Chown(path string, uid, gid int) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Chown(resolved, uid, gid)
}

// Chtimes sets the access and modification times of a file.
func (l *LocalFilesystem) Chtimes(path string, atime, mtime time.Time) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Chtimes(resolved, atime, mtime)
}

// Access checks that a path exists and may be used as mode asks, as access(2) does.
func (l *LocalFilesystem) Access(path string, mode uint32) error {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return syscall.Access(resolved, mode)
}

// Statfs describes the filesystem a path is on.
func (l *LocalFilesystem) Statfs(path string) (*FsStats, error) {
	resolved, err := l.resolvePath(path)
	if err != nil {
		return nil, err
	}
	return statfs(resolved)
}

// localFile wraps os.File to implement our File interface.
type localFile struct {
	*os.File
}

// Chtimes sets the access and modification times of the open file.
func (f *localFile) Chtimes(atime, mtime time.Time) error {
	return syscall.Futimes(int(f.Fd()), []syscall.Timeval{
		syscall.NsecToTimeval(atime.UnixNano()), syscall.NsecToTimeval(mtime.UnixNano()),
	})
}

// Stat returns file information for the open file.
func (f *localFile) Stat() (*FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfoOf(info), nil
}

// Open opens a file for reading.
func (l *LocalFilesystem) Open(path string) (File, error) {
	if l.Root != "" {
		return l.OpenFile(path, os.O_RDONLY, 0)
	}
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
	if l.Root != "" {
		return l.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	}
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
	if l.Root != "" {
		f, err := l.rootedOpenFile(path, flag, perm)
		if err != nil {
			return nil, err
		}
		return &localFile{f}, nil
	}
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

// FilesystemIsUnrestricted reports whether fs is the real filesystem with no sandbox root.
func FilesystemIsUnrestricted(fs Filesystem) bool {
	local, ok := fs.(*LocalFilesystem)
	return ok && local.Root == ""
}

var (
	_ MetadataFilesystem = (*LocalFilesystem)(nil)
	_ StatfsFilesystem   = (*LocalFilesystem)(nil)
	_ LstatFilesystem    = (*LocalFilesystem)(nil)
	_ MetadataFile       = (*localFile)(nil)
)
