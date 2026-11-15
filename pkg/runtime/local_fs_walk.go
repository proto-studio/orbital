//go:build unix

package runtime

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// With a root, LocalFilesystem resolves each path itself: it opens the root, walks the path
// one name at a time with openat, never letting the OS follow a symbolic link, and follows
// links by reading them and continuing the walk. The call is then made on the last name,
// relative to the directory the walk reached. So the path the walk checks is the path the
// call uses.

// maxLinkHops is how many symbolic links one walk may follow before it fails with ELOOP.
const maxLinkHops = 40

// walk is where a walk ended: the directory holding the final name, open, and that name,
// which is "." when the path names a directory reached by the walk itself.
type walk struct {
	dirs  []int    // open directories from the root down; the last holds name
	names []string // the names of dirs[1:] from the root
	name  string
	path  string // the path as the caller gave it, for errors
}

func (w *walk) dir() int { return w.dirs[len(w.dirs)-1] }

func (w *walk) close() {
	for _, fd := range w.dirs {
		unix.Close(fd)
	}
	w.dirs = nil
}

// rel is the path the walk reached, from the root.
func (w *walk) rel() string {
	parts := append(slices.Clone(w.names), w.name)
	return filepath.Join(parts...)
}

func (w *walk) fail(op string, err error) error {
	if err == nil || errors.Is(err, ErrOutsideSandbox) {
		return err
	}
	return &fs.PathError{Op: op, Path: w.path, Err: err}
}

// walkOptions says how to treat the final name: follow a link there, and make missing
// directories on the way, MkdirAll's way, with mode perm.
type walkOptions struct {
	followFinal bool
	makeDirs    bool
	perm        uint32
}

// splitNames splits p into its names, dropping empty ones and ".".
func splitNames(p string) []string {
	var names []string
	for _, name := range strings.Split(p, string(filepath.Separator)) {
		if name != "" && name != "." {
			names = append(names, name)
		}
	}
	return names
}

// walkTo walks p, as textualPath takes it, from the root.
func (l *LocalFilesystem) walkTo(p string, opts walkOptions) (*walk, error) {
	textual, err := l.textualPath(p)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(l.Root)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(absRoot, textual)
	if err != nil {
		return nil, err
	}
	// The root itself may be reached through links, such as /tmp on macOS: it is the
	// embedder's choice, so the OS follows those.
	rootFd, err := unix.Open(absRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: p, Err: err}
	}
	w := &walk{dirs: []int{rootFd}, name: ".", path: p}
	if err := l.walkNames(w, splitNames(rel), opts, absRoot); err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}

func (l *LocalFilesystem) walkNames(w *walk, queue []string, opts walkOptions, absRoot string) error {
	hops := 0
	// follow continues the walk at a link's target, in place of the link.
	follow := func(name string, rest []string) ([]string, error) {
		hops++
		if hops > maxLinkHops {
			return nil, w.fail("open", syscall.ELOOP)
		}
		target, err := readlinkat(w.dir(), name)
		if err != nil {
			return nil, w.fail("readlink", err)
		}
		if filepath.IsAbs(target) {
			// An absolute target is taken from the root when it names a path under it.
			under, ok := l.underRoot(target, absRoot)
			if !ok {
				return nil, ErrOutsideSandbox
			}
			for len(w.dirs) > 1 {
				unix.Close(w.dirs[len(w.dirs)-1])
				w.dirs = w.dirs[:len(w.dirs)-1]
			}
			w.names = w.names[:0]
			target = under
		}
		return append(splitNames(target), rest...), nil
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		last := len(queue) == 0
		w.name = "."
		if name == ".." {
			if len(w.dirs) == 1 {
				return ErrOutsideSandbox
			}
			unix.Close(w.dirs[len(w.dirs)-1])
			w.dirs = w.dirs[:len(w.dirs)-1]
			w.names = w.names[:len(w.names)-1]
			continue
		}
		if last && !opts.makeDirs {
			w.name = name
			if !opts.followFinal {
				return nil
			}
			var st unix.Stat_t
			err := unix.Fstatat(w.dir(), name, &st, unix.AT_SYMLINK_NOFOLLOW)
			if err != nil || st.Mode&unix.S_IFMT != unix.S_IFLNK {
				// A missing name is left for the call, which may make it.
				return nil
			}
			next, err := follow(name, queue)
			if err != nil {
				return err
			}
			queue = next
			continue
		}
		fd, err := unix.Openat(w.dir(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			w.dirs = append(w.dirs, fd)
			w.names = append(w.names, name)
			continue
		}
		if errors.Is(err, unix.ENOENT) && opts.makeDirs {
			if err := unix.Mkdirat(w.dir(), name, opts.perm); err != nil && !errors.Is(err, unix.EEXIST) {
				return w.fail("mkdir", err)
			}
			queue = append([]string{name}, queue...)
			continue
		}
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EMLINK) {
			var st unix.Stat_t
			if unix.Fstatat(w.dir(), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
				next, ferr := follow(name, queue)
				if ferr != nil {
					return ferr
				}
				queue = next
				continue
			}
		}
		return w.fail("open", err)
	}
	return nil
}

// underRoot gives an absolute host path as a path from the root, if it is under the root as
// configured or as resolved.
func (l *LocalFilesystem) underRoot(p, absRoot string) (string, bool) {
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		realRoot = absRoot
	}
	// The target is compared as written, then with the links in its directories resolved,
	// since either side may name the root through a link such as /var on macOS. Only the
	// name is mapped here: the walk then goes on from the open root.
	for _, target := range []string{p, resolvedPrefix(p)} {
		for _, root := range []string{absRoot, realRoot} {
			if target == root {
				return ".", true
			}
			if rest, ok := strings.CutPrefix(target, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator)); ok {
				return rest, true
			}
		}
	}
	return "", false
}

// resolvedPrefix gives p with the links in its longest existing directory resolved.
func resolvedPrefix(p string) string {
	dir, rest := filepath.Dir(p), filepath.Base(p)
	for {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func readlinkat(dir int, name string) (string, error) {
	for size := 256; ; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dir, name, buf)
		if err != nil {
			return "", err
		}
		if n < size {
			return string(buf[:n]), nil
		}
	}
}

// syscallMode turns a Go FileMode into the mode bits the OS takes.
func syscallMode(perm fs.FileMode) uint32 {
	m := uint32(perm.Perm())
	if perm&fs.ModeSetuid != 0 {
		m |= unix.S_ISUID
	}
	if perm&fs.ModeSetgid != 0 {
		m |= unix.S_ISGID
	}
	if perm&fs.ModeSticky != 0 {
		m |= unix.S_ISVTX
	}
	return m
}

// statInfo describes what fstatat reported, as os.Lstat would.
func statInfo(name string, st *unix.Stat_t) *FileInfo {
	mode := fs.FileMode(st.Mode & 0o777)
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= fs.ModeDir
	case unix.S_IFLNK:
		mode |= fs.ModeSymlink
	case unix.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= fs.ModeSocket
	case unix.S_IFCHR:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case unix.S_IFBLK:
		mode |= fs.ModeDevice
	}
	if st.Mode&unix.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if st.Mode&unix.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if st.Mode&unix.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}
	return &FileInfo{
		Name:    name,
		Size:    st.Size,
		Mode:    mode,
		ModTime: time.Unix(st.Mtim.Unix()),
		IsDir:   mode.IsDir(),
	}
}

// statAt describes the walk's final name, without following a link there.
func (w *walk) stat() (*FileInfo, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(w.dir(), w.name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, w.fail("stat", err)
	}
	return statInfo(filepath.Base(w.path), &st), nil
}

func (l *LocalFilesystem) rootedStat(p string, follow bool) (*FileInfo, error) {
	w, err := l.walkTo(p, walkOptions{followFinal: follow})
	if err != nil {
		return nil, err
	}
	defer w.close()
	return w.stat()
}

// rootedOpenFile opens the file p names. A link that appears at the final name after the walk
// read it is not followed by the OS: the open fails, and the walk runs again.
func (l *LocalFilesystem) rootedOpenFile(p string, flag int, perm fs.FileMode) (*os.File, error) {
	for range maxLinkHops {
		w, err := l.walkTo(p, walkOptions{followFinal: true})
		if err != nil {
			return nil, err
		}
		fd, err := unix.Openat(w.dir(), w.name, flag|unix.O_NOFOLLOW|unix.O_CLOEXEC, syscallMode(perm))
		w.close()
		if errors.Is(err, unix.ELOOP) && flag&os.O_EXCL == 0 {
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: p, Err: err}
		}
		return os.NewFile(uintptr(fd), p), nil
	}
	return nil, &fs.PathError{Op: "open", Path: p, Err: syscall.ELOOP}
}

func (l *LocalFilesystem) rootedReadFile(p string) ([]byte, error) {
	f, err := l.rootedOpenFile(p, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (l *LocalFilesystem) rootedWrite(p string, flag int, perm fs.FileMode, data []byte) error {
	f, err := l.rootedOpenFile(p, flag, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (l *LocalFilesystem) rootedReadDir(p string) ([]DirEntry, error) {
	f, err := l.rootedOpenFile(p, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	result := make([]DirEntry, len(entries))
	for i, e := range entries {
		result[i] = DirEntry{Name: e.Name(), IsDir: e.IsDir()}
	}
	return result, nil
}

func (l *LocalFilesystem) rootedMkdir(p string, perm fs.FileMode) error {
	w, err := l.walkTo(p, walkOptions{})
	if err != nil {
		return err
	}
	defer w.close()
	return w.fail("mkdir", unix.Mkdirat(w.dir(), w.name, syscallMode(perm)))
}

func (l *LocalFilesystem) rootedMkdirAll(p string, perm fs.FileMode) error {
	w, err := l.walkTo(p, walkOptions{followFinal: true, makeDirs: true, perm: syscallMode(perm)})
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) && errors.Is(err, unix.ENOTDIR) {
			pathErr.Op = "mkdir"
		}
		return err
	}
	w.close()
	return nil
}

// removeAt removes name in dir as os.Remove does: as a file, or else as an empty directory.
func removeAt(dir int, name string) error {
	err := unix.Unlinkat(dir, name, 0)
	if err == nil {
		return nil
	}
	err1 := unix.Unlinkat(dir, name, unix.AT_REMOVEDIR)
	if err1 == nil {
		return nil
	}
	if !errors.Is(err1, unix.ENOTDIR) {
		err = err1
	}
	return err
}

func (l *LocalFilesystem) rootedRemove(p string) error {
	w, err := l.walkTo(p, walkOptions{})
	if err != nil {
		return err
	}
	defer w.close()
	if err := removeAt(w.dir(), w.name); err != nil {
		return w.fail("remove", err)
	}
	return nil
}

// removeAllAt removes name in dir and, for a directory, all it holds, opening each directory
// it descends into without following links.
func removeAllAt(dir int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(dir, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
		return nil
	}
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	names, err := f.Readdirnames(-1)
	if err == nil {
		for _, child := range names {
			if err = removeAllAt(fd, child); err != nil {
				break
			}
		}
	}
	f.Close()
	if err != nil {
		return err
	}
	if err := unix.Unlinkat(dir, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}

func (l *LocalFilesystem) rootedRemoveAll(p string) error {
	w, err := l.walkTo(p, walkOptions{})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer w.close()
	if err := removeAllAt(w.dir(), w.name); err != nil {
		return w.fail("unlinkat", err)
	}
	return nil
}

func (l *LocalFilesystem) rootedRename(oldPath, newPath string) error {
	from, err := l.walkTo(oldPath, walkOptions{})
	if err != nil {
		return err
	}
	defer from.close()
	to, err := l.walkTo(newPath, walkOptions{})
	if err != nil {
		return err
	}
	defer to.close()
	if err := unix.Renameat(from.dir(), from.name, to.dir(), to.name); err != nil {
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	return nil
}

func (l *LocalFilesystem) rootedCopy(src, dst string) error {
	in, err := l.rootedOpenFile(src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := l.rootedOpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
