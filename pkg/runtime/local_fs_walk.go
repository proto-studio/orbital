//go:build darwin || linux

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
// which is "." when the path names a directory reached by the walk itself. Only the root and
// the current directory are held open, however deep the path.
type walk struct {
	root  int
	cur   int      // the directory holding name; root, or a descriptor of its own
	names []string // the names leading from the root to cur
	name  string
	path  string // the path as the caller gave it, for errors
}

func (w *walk) dir() int { return w.cur }

func (w *walk) close() {
	if w.cur != w.root {
		unix.Close(w.cur)
	}
	unix.Close(w.root)
	w.cur, w.root = -1, -1
}

// setCur makes fd the current directory, closing the one before.
func (w *walk) setCur(fd int) {
	if w.cur != w.root {
		unix.Close(w.cur)
	}
	w.cur = fd
}

// openDir opens name in dir as a directory, without following a link there. It asks only to
// search the directory where the OS allows, so a directory that can be searched but not
// listed can still be walked through.
func openDir(dir int, name string) (int, error) {
	fd, err := unix.Openat(dir, name, searchFlag|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.EINVAL) && searchFlag != unix.O_RDONLY {
		fd, err = unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	return fd, err
}

// up moves the walk to the parent of the current directory, by walking from the root again
// through the names that led here, which are directories, not links.
func (w *walk) up() error {
	names := w.names[:len(w.names)-1]
	w.setCur(w.root)
	w.names = nil
	for _, name := range names {
		fd, err := openDir(w.cur, name)
		if err != nil {
			return w.fail("open", err)
		}
		w.setCur(fd)
		w.names = append(w.names, name)
	}
	return nil
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
	openRoot := func() (int, error) {
		fd, err := unix.Open(absRoot, searchFlag|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.EINVAL) && searchFlag != unix.O_RDONLY {
			fd, err = unix.Open(absRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		}
		return fd, err
	}
	rootFd, err := openRoot()
	if errors.Is(err, unix.ENOENT) && opts.makeDirs {
		// MkdirAll makes a root that doesn't exist yet, as it did before the walk.
		if err := os.MkdirAll(absRoot, fs.FileMode(opts.perm)&fs.ModePerm); err != nil {
			return nil, err
		}
		rootFd, err = openRoot()
	}
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: p, Err: err}
	}
	w := &walk{root: rootFd, cur: rootFd, name: ".", path: p}
	if err := l.walkNames(w, splitNames(rel), opts, absRoot); err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}

func (l *LocalFilesystem) walkNames(w *walk, queue []string, opts walkOptions, absRoot string) error {
	hops := 0
	// fromLink counts the names at the front of the queue that came from link targets.
	// MkdirAll makes only the directories the path itself names, as os.MkdirAll does: a
	// link to a missing directory is an existing name, not a place to make one.
	fromLink := 0
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
			w.setCur(w.root)
			w.names = nil
			target = under
		}
		names := splitNames(target)
		// A target ending in "/" or "/." names a directory, so the name before it must be
		// one: a "." after it makes the walk open it as one rather than end there.
		if len(names) > 0 && (strings.HasSuffix(target, "/") || strings.HasSuffix(target, "/.")) {
			names = append(names, ".")
		}
		fromLink += len(names)
		return append(names, rest...), nil
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		linked := fromLink > 0
		if linked {
			fromLink--
		}
		last := len(queue) == 0
		w.name = "."
		if name == "." {
			continue
		}
		if name == ".." {
			if len(w.names) == 0 {
				return ErrOutsideSandbox
			}
			if err := w.up(); err != nil {
				return err
			}
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
			// A link to "." or to the root leaves nothing to walk: the walk ends at the
			// directory it reached.
			w.name = "."
			queue = next
			continue
		}
		fd, err := openDir(w.dir(), name)
		if err == nil {
			w.setCur(fd)
			w.names = append(w.names, name)
			continue
		}
		if errors.Is(err, unix.ENOENT) && opts.makeDirs {
			if linked {
				return w.fail("mkdir", unix.EEXIST)
			}
			if err := mkdirAt(w.dir(), name, opts.perm); err != nil && !errors.Is(err, unix.EEXIST) {
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
	// A trailing separator or "/." is dropped first, so the last name isn't taken twice. The
	// rest is left as written: ".." after a link must be resolved after that link.
	for len(p) > 1 && (strings.HasSuffix(p, "/") || strings.HasSuffix(p, "/.")) {
		p = strings.TrimSuffix(strings.TrimSuffix(p, "."), "/")
	}
	// The longest leading part that exists is resolved; filepath.Dir isn't used for it, since
	// it would collapse a ".." before the links ahead of it are resolved.
	names := strings.Split(p, string(filepath.Separator))
	for k := len(names) - 1; k > 0; k-- {
		dir := strings.Join(names[:k], string(filepath.Separator))
		if dir == "" {
			dir = string(filepath.Separator)
		}
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			// The rest is appended as written: a missing name followed by ".." must still
			// be looked up by the walk.
			return strings.TrimSuffix(real, string(filepath.Separator)) + string(filepath.Separator) +
				strings.Join(names[k:], string(filepath.Separator))
		}
	}
	return p
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

// rootedStat describes the file p names. Following, a link that appears at the final name
// after the walk read it makes the walk run again, as in rootedOpenFile.
func (l *LocalFilesystem) rootedStat(p string, follow bool) (*FileInfo, error) {
	for range maxLinkHops {
		w, err := l.walkTo(p, walkOptions{followFinal: follow})
		if err != nil {
			return nil, err
		}
		info, err := w.stat()
		w.close()
		if err == nil && follow && info.Mode&fs.ModeSymlink != 0 {
			continue
		}
		return info, err
	}
	return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.ELOOP}
}

// rootedOpenFile opens the file p names. A link that appears at the final name after the walk
// read it is not followed by the OS: the open fails, and the walk runs again.
func (l *LocalFilesystem) rootedOpenFile(p string, flag int, perm fs.FileMode) (*os.File, error) {
	// An exclusive create acts on the name itself, so an existing link there is EEXIST, as
	// O_EXCL makes it; and O_NOFOLLOW asked for by the caller refuses a link there with ELOOP.
	exclusive := flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0
	noFollow := flag&unix.O_NOFOLLOW != 0
	for range maxLinkHops {
		w, err := l.walkTo(p, walkOptions{followFinal: !exclusive && !noFollow})
		if err != nil {
			return nil, err
		}
		fd, err := unix.Openat(w.dir(), w.name, flag|unix.O_NOFOLLOW|unix.O_CLOEXEC, syscallMode(perm))
		w.close()
		if errors.Is(err, unix.ELOOP) && !exclusive && !noFollow {
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: p, Err: err}
		}
		return newFile(fd, p), nil
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
	// The entries' types come with the listing, which needs only read permission on the
	// directory. For a filesystem that leaves a type out, Go looks the entry up by the
	// directory's name, so the listing is read through a descriptor named by the host path
	// rather than the caller's.
	host, err := l.textualPath(p)
	if err != nil {
		return nil, err
	}
	dup, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "readdirent", Path: p, Err: err}
	}
	listing := os.NewFile(uintptr(dup), host)
	defer listing.Close()
	entries, err := listing.ReadDir(-1)
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
	return w.fail("mkdir", mkdirAt(w.dir(), w.name, syscallMode(perm)))
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
	// An empty directory goes at once, which needs no permission on the directory itself.
	if err := unix.Unlinkat(dir, name, unix.AT_REMOVEDIR); err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	names, err := f.Readdirnames(-1)
	// As os.RemoveAll does, it removes what it can and reports the first failure.
	for _, child := range names {
		if childErr := removeAllAt(fd, child); childErr != nil && err == nil {
			err = childErr
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
	// The copy only writes the destination, so it doesn't ask to read it.
	out, err := l.rootedOpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// newFile wraps an opened descriptor as os.OpenFile would: a pipe, socket or device is put in
// non-blocking mode first, so Go's poller manages it and closing the file ends a read that is
// waiting on it. Regular files and directories stay as they are, as os.OpenFile leaves them.
func newFile(fd int, name string) *os.File {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) == nil {
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFREG, unix.S_IFDIR:
		default:
			unix.SetNonblock(fd, true)
		}
	}
	return os.NewFile(uintptr(fd), name)
}

// mkdirAt makes name in dir with mode. macOS's mkdir drops the sticky bit, so, as os.Mkdir
// does there, it is set afterwards.
func mkdirAt(dir int, name string, mode uint32) error {
	if err := unix.Mkdirat(dir, name, mode); err != nil {
		return err
	}
	if mode&unix.S_ISVTX != 0 && !mkdirKeepsSticky {
		var st unix.Stat_t
		if err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		return unix.Fchmodat(dir, name, uint32(st.Mode&0o7777)|unix.S_ISVTX, unix.AT_SYMLINK_NOFOLLOW)
	}
	return nil
}
