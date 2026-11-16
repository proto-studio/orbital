package runtime

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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
	// No root: the path goes to the OS as given, which resolves each link before a ".." that
	// follows it, as Node leaves it to do. Cleaning the text first would apply the ".." to the
	// link's own name.
	if l.Root == "" {
		if p == "" {
			return filepath.Abs(p)
		}
		return p, nil
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
			Type:  e.Type(),
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
	if l.Root != "" {
		return l.rootedTruncate(path, size)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Truncate(resolved, size)
}

// Chmod sets the mode of a file.
func (l *LocalFilesystem) Chmod(path string, mode fs.FileMode) error {
	if l.Root != "" {
		return l.rootedChmod(path, mode)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Chmod(resolved, mode)
}

// Chown sets the owner and group of a file.
func (l *LocalFilesystem) Chown(path string, uid, gid int) error {
	if l.Root != "" {
		return l.rootedChown(path, uid, gid)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Chown(resolved, uid, gid)
}

// Chtimes sets the access and modification times of a file.
func (l *LocalFilesystem) Chtimes(path string, atime, mtime time.Time) error {
	if l.Root != "" {
		return l.rootedChtimes(path, atime, mtime)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, resolved, []unix.Timespec{timespec(atime), timespec(mtime)}, 0); err != nil {
		return &fs.PathError{Op: "chtimes", Path: path, Err: err}
	}
	return nil
}

// Access checks that a path exists and may be used as mode asks, as access(2) does.
func (l *LocalFilesystem) Access(path string, mode uint32) error {
	if l.Root != "" {
		return l.rootedAccess(path, mode)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return syscall.Access(resolved, mode)
}

// Statfs describes the filesystem a path is on.
func (l *LocalFilesystem) Statfs(path string) (*FsStats, error) {
	if l.Root != "" {
		return l.rootedStatfs(path)
	}
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
	return syscall.Futimes(int(f.Fd()), []syscall.Timeval{timeval(atime), timeval(mtime)})
}

// timeval gives t as seconds and microseconds, without going through nanoseconds since
// 1970, which overflow an int64 past 2262.
func timeval(t time.Time) syscall.Timeval {
	// The fraction is cut to whole microseconds, so it never rounds up into the next second.
	tv := syscall.NsecToTimeval(int64(t.Nanosecond() / 1000 * 1000))
	tv.Sec = t.Unix()
	return tv
}

// timespec gives t as seconds and nanoseconds, as timeval does.
func timespec(t time.Time) unix.Timespec {
	ts := unix.NsecToTimespec(int64(t.Nanosecond()))
	ts.Sec = t.Unix()
	return ts
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
	_ LinkFilesystem     = (*LocalFilesystem)(nil)
	_ MetadataFile       = (*localFile)(nil)
)

// hostTarget gives the target to store for a symbolic link the script makes: with a root,
// an absolute target names a path under the root, as every other absolute path does.
func (l *LocalFilesystem) hostTarget(target string) (string, error) {
	if l.Root == "" || !filepath.IsAbs(target) {
		return target, nil
	}
	absRoot, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	// A target already under the root is kept, as textualPath keeps such a path.
	clean := filepath.Clean(target)
	if clean == absRoot || strings.HasPrefix(clean, absRoot+string(filepath.Separator)) {
		return target, nil
	}
	return filepath.Join(absRoot, target), nil
}

// scriptPath gives a host path as the script names it: with a root, a path under the root
// is given from the root, as "/" followed by the rest. Any other path is given as it is.
func (l *LocalFilesystem) scriptPath(p string) string {
	if l.Root == "" || !filepath.IsAbs(p) {
		return p
	}
	absRoot, err := filepath.Abs(l.Root)
	if err != nil {
		return p
	}
	if rel, ok := l.underRoot(p, absRoot); ok {
		return filepath.Clean(string(filepath.Separator) + rel)
	}
	return p
}

// Symlink makes path a symbolic link to target. The link is checked like any other path
// when it is used.
func (l *LocalFilesystem) Symlink(target, path string) error {
	stored, err := l.hostTarget(target)
	if err != nil {
		return err
	}
	if l.Root != "" {
		return l.rootedSymlink(stored, path)
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Symlink(stored, resolved)
}

// Readlink gives the target of the symbolic link at path.
func (l *LocalFilesystem) Readlink(path string) (string, error) {
	if l.Root != "" {
		target, err := l.rootedReadlink(path)
		if err != nil {
			return "", err
		}
		return l.scriptPath(target), nil
	}
	resolved, err := l.resolvePath(path)
	if err != nil {
		return "", err
	}
	return os.Readlink(resolved)
}

// Link makes path another name for the file existing names. A symbolic link at existing is
// treated as the platform's link(2) treats it.
func (l *LocalFilesystem) Link(existing, path string) error {
	if l.Root != "" {
		return l.rootedLink(existing, path)
	}
	from, err := l.resolvePath(existing)
	if err != nil {
		return err
	}
	// link(2) itself decides what a link at existing means, as it does for Node: macOS
	// links the file it leads to, Linux the link itself.
	to, err := l.resolvePath(path)
	if err != nil {
		return err
	}
	return os.Link(from, to)
}

// Realpath gives the path p names with every symbolic link resolved, from the root if there
// is one.
func (l *LocalFilesystem) Realpath(p string) (string, error) {
	if l.Root != "" {
		return l.rootedRealpath(p)
	}
	resolved, err := l.resolvePath(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(resolved)
}
