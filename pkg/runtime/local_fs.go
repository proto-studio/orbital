package runtime

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrOutsideSandbox is the error resolvePath returns for a path it refuses. It is a permission
// error, so callers that report errno codes report EACCES, as its text says.
var ErrOutsideSandbox error = pathCheckError{}

type pathCheckError struct{}

func (pathCheckError) Error() string { return "EACCES: path is outside the sandbox" }

func (pathCheckError) Unwrap() error { return syscall.EACCES }

// maxLinkHops is how many symbolic links resolving one path may follow, as the OS's own
// limit (ELOOP) does.
const maxLinkHops = 255

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

// resolvePath gives the path the OS opens for p. With a root, p is taken within it, and the
// check applies to the path with its symbolic links resolved, the final one included, so it
// covers what the OS will actually open.
func (l *LocalFilesystem) resolvePath(p string) (string, error) {
	return l.checkedPath(p, true)
}

// resolveLinkPath is resolvePath for calls that work on a final symbolic link itself, such
// as lstat, unlink and rename: the directories leading to it are resolved, the link isn't.
func (l *LocalFilesystem) resolveLinkPath(p string) (string, error) {
	return l.checkedPath(p, false)
}

func (l *LocalFilesystem) checkedPath(p string, followFinal bool) (string, error) {
	textual, err := l.textualPath(p)
	if err != nil || l.Root == "" {
		return textual, err
	}
	absRoot, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	// The root itself may be reached through links, such as /tmp on macOS.
	realRoot, err := resolveLinks(absRoot, true)
	if err != nil {
		return "", err
	}
	real, err := resolveLinks(textual, followFinal)
	if err != nil {
		return "", err
	}
	if !isUnder(real, realRoot) {
		return "", ErrOutsideSandbox
	}
	// The OS gets the path as named, so calls such as an exclusive create through a link
	// behave as they would without the check.
	return textual, nil
}

// isUnder reports whether p is root or below it.
func isUnder(p, root string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// resolveLinks resolves the symbolic links in the absolute, clean path p, as the OS does when
// it opens p, and leaves the part from the first missing component on unchanged. A link is
// followed even when what it names is missing, because creating a file through it creates
// that target. With followFinal false, a final link is left as it is.
func resolveLinks(p string, followFinal bool) (string, error) {
	if !followFinal {
		dir, base := filepath.Split(p)
		if base == "" {
			return p, nil
		}
		realDir, err := resolveLinks(filepath.Clean(dir), true)
		if err != nil {
			return "", err
		}
		return filepath.Join(realDir, base), nil
	}
	volume := filepath.VolumeName(p)
	resolved := volume + string(filepath.Separator)
	rest := splitPath(p[len(volume):])
	hops := 0
	for len(rest) > 0 {
		name := rest[0]
		rest = rest[1:]
		switch name {
		case ".":
			continue
		case "..":
			// resolved has no links left in it, so its parent is its textual parent.
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, name)
		info, err := os.Lstat(next)
		if err != nil {
			// Nothing to follow from here on: the OS fails or creates the rest as named.
			return filepath.Join(append([]string{next}, rest...)...), nil
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			resolved = next
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", &fs.PathError{Op: "resolve", Path: p, Err: syscall.ELOOP}
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			volume = filepath.VolumeName(target)
			resolved = volume + string(filepath.Separator)
			target = target[len(volume):]
		}
		rest = append(splitPath(target), rest...)
	}
	return resolved, nil
}

// splitPath splits p into its names, dropping empty ones.
func splitPath(p string) []string {
	var names []string
	for _, name := range strings.Split(p, string(filepath.Separator)) {
		if name != "" {
			names = append(names, name)
		}
	}
	return names
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

// Lstat returns file information like Stat, but describes a final symbolic link itself.
func (l *LocalFilesystem) Lstat(path string) (*FileInfo, error) {
	resolved, err := l.resolveLinkPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(resolved)
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
	resolved, err := l.resolveLinkPath(path)
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
	resolved, err := l.resolveLinkPath(path)
	if err != nil {
		return err
	}
	return os.Remove(resolved)
}

// RemoveAll removes a path and all its children.
func (l *LocalFilesystem) RemoveAll(path string) error {
	resolved, err := l.resolveLinkPath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(resolved)
}

// Rename renames/moves a file.
func (l *LocalFilesystem) Rename(oldPath, newPath string) error {
	resolvedOld, err := l.resolveLinkPath(oldPath)
	if err != nil {
		return err
	}
	resolvedNew, err := l.resolveLinkPath(newPath)
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

// FilesystemIsUnrestricted reports whether fs is the real filesystem with no sandbox root.
func FilesystemIsUnrestricted(fs Filesystem) bool {
	local, ok := fs.(*LocalFilesystem)
	return ok && local.Root == ""
}
