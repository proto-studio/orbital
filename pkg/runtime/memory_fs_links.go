package runtime

import (
	"bytes"
	"io/fs"
	"path"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"
)

// Symbolic and hard links in the in-memory filesystem. Files are kept by their canonical
// path, which names no link. Every call resolves the path it is given to that canonical path
// first, following links as the OS does: each link in the middle of a path, and the last one
// unless the call acts on the link itself. A ".." after a link applies to where the link
// leads. A link's target, absolute or relative, is resolved within the filesystem, so no link
// leads out of it.

// maxSymlinks is how many links one path may pass through before the OS reports ELOOP.
var maxSymlinks = func() int {
	if goruntime.GOOS == "darwin" {
		return 32
	}
	return 40
}()

// isLink reports whether mf is a symbolic link.
func (mf *memFile) isLink() bool { return mf.mode&fs.ModeSymlink != 0 }

// newFile makes a file with one name, giving it the next inode number. The caller holds m.mu.
func (m *MemoryFilesystem) newFile(mf *memFile) *memFile {
	m.lastIno++
	mf.ino = m.lastIno
	mf.nlink = 1
	if mf.modTime.IsZero() {
		mf.modTime = time.Now()
	}
	return mf
}

// unname drops the name canonical, as unlink does: the file lives on under its other names.
// The caller holds m.mu.
func (m *MemoryFilesystem) unname(canonical string) {
	if mf, ok := m.files[canonical]; ok {
		mf.nlink--
		delete(m.files, canonical)
	}
}

// How resolve treats a final symbolic link.
type finalLink int

const (
	followFinal finalLink = iota // follow it, as stat and open do
	keepFinal                    // act on it, as lstat and readlink do; a "/" after it follows it
	// entryFinal acts on the directory entry, as unlink, rmdir and rename do. A "/" after a
	// link follows it on macOS, as keepFinal does; Linux refuses it with ENOTDIR.
	entryFinal
)

// entrySlashFollows is whether unlink, rmdir and rename follow a final link with a "/" after
// it, as macOS's do; Linux's refuse it.
var entrySlashFollows = goruntime.GOOS == "darwin"

// resolved is where a path leads.
type resolved struct {
	path string   // the canonical path
	file *memFile // the file there, or nil
	// missingTarget is set when a name a link's target gives is missing, as for a link that
	// leads nowhere.
	missingTarget bool
}

// resolve gives the canonical path p names, and the file there, or nil if there is none,
// following a final link if follow is set.
func (m *MemoryFilesystem) resolve(op, p string, follow bool) (string, *memFile, error) {
	final := keepFinal
	if follow {
		final = followFinal
	}
	r, err := m.walk(op, p, final)
	return r.path, r.file, err
}

// The OS's limits on a path, a name in it and a link's target, past which it gives
// ENAMETOOLONG: PATH_MAX and NAME_MAX.
var pathMax = func() int {
	if goruntime.GOOS == "darwin" {
		return 1024
	}
	return 4096
}()

const nameMax = 255

// walk resolves p. Links along the way are followed, and a final one as final says. A
// missing directory along the way is taken as an empty one, since files here need no parent;
// a file that isn't a directory is ENOTDIR. It takes time linear in the path and the targets
// of the links it follows. The caller holds m.mu.
func (m *MemoryFilesystem) walk(op, p string, finalLink finalLink) (resolved, error) {
	fail := func(errno syscall.Errno) (resolved, error) {
		return resolved{}, &fs.PathError{Op: op, Path: p, Err: errno}
	}
	if p == "" {
		return fail(syscall.ENOENT)
	}
	if len(p) >= pathMax {
		return fail(syscall.ENAMETOOLONG)
	}
	// The names still to take, last first, so a link's target is pushed on top of them. The
	// bottom given of them are p's own; any above come from link targets. names counts those
	// that aren't "" or ".", so the last name in the path is known at once.
	var stack []string
	names := 0
	push := func(path string) {
		parts := strings.Split(path, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			stack = append(stack, parts[i])
			if parts[i] != "" && parts[i] != "." {
				names++
			}
		}
	}
	push(p)
	given := len(stack)
	cur := make([]byte, 1, len(p)+1)
	cur[0] = '/'
	links := 0
	missingTarget := false
	for len(stack) > 0 {
		fromTarget := len(stack) > given
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !fromTarget {
			given--
		}
		if name == "" || name == "." {
			continue
		}
		names--
		if len(name) > nameMax {
			return fail(syscall.ENAMETOOLONG)
		}
		if name == ".." {
			cur = cur[:max(bytes.LastIndexByte(cur, '/'), 1)]
			continue
		}
		parent := len(cur)
		if parent > 1 {
			cur = append(cur, '/')
		}
		cur = append(cur, name...)
		mf, ok := m.files[string(cur)]
		// final is set for the last name in the path; a "/" after it still asks for a directory.
		final, trailing := names == 0, len(stack) > 0
		if !ok {
			if fromTarget {
				missingTarget = true
			}
			continue
		}
		if mf.isLink() && final && trailing && finalLink == entryFinal && !entrySlashFollows {
			return fail(syscall.ENOTDIR)
		}
		if mf.isLink() && (!final || finalLink == followFinal || trailing) {
			links++
			if links > maxSymlinks {
				return fail(syscall.ELOOP)
			}
			cur = cur[:parent]
			if strings.HasPrefix(mf.target, "/") {
				cur = cur[:1]
			}
			push(mf.target)
			continue
		}
		if (!final || trailing) && !mf.isDir {
			return fail(syscall.ENOTDIR)
		}
	}
	canonical := string(cur)
	return resolved{path: canonical, file: m.files[canonical], missingTarget: missingTarget}, nil
}

// present reports whether there is anything at canonical: a file, a link, the root, or a
// directory taken as there because files are under it. The caller holds m.mu.
func (m *MemoryFilesystem) present(canonical string) bool {
	if canonical == "/" {
		return true
	}
	if _, ok := m.files[canonical]; ok {
		return true
	}
	return m.hasChildren(canonical)
}

// hasChildren reports whether anything is under the directory at canonical. The caller
// holds m.mu.
func (m *MemoryFilesystem) hasChildren(canonical string) bool {
	prefix := strings.TrimSuffix(canonical, "/") + "/"
	for p := range m.files {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// newName checks that canonical, which a link is to be made at, is free and its directory is
// there, as link(2) and symlink(2) do. The caller holds m.mu.
func (m *MemoryFilesystem) newName(op, canonical string) error {
	if m.present(canonical) {
		return &fs.PathError{Op: op, Path: canonical, Err: syscall.EEXIST}
	}
	if !m.present(path.Dir(canonical)) {
		return &fs.PathError{Op: op, Path: canonical, Err: syscall.ENOENT}
	}
	return nil
}

// Symlink makes path a symbolic link to target, which is kept as given and resolved when the
// link is used.
func (m *MemoryFilesystem) Symlink(target, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if target == "" {
		return &fs.PathError{Op: "symlink", Path: p, Err: syscall.ENOENT}
	}
	if len(target) >= pathMax {
		return &fs.PathError{Op: "symlink", Path: p, Err: syscall.ENAMETOOLONG}
	}
	canonical, _, err := m.resolve("symlink", p, false)
	if err != nil {
		return err
	}
	if err := m.newName("symlink", canonical); err != nil {
		return err
	}
	m.files[canonical] = m.newFile(&memFile{mode: fs.ModeSymlink | 0o755, target: target})
	return nil
}

// Readlink gives the target of the symbolic link at path.
func (m *MemoryFilesystem) Readlink(p string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	canonical, mf, err := m.resolve("readlink", p, false)
	switch {
	case err != nil:
		return "", err
	case mf == nil && canonical != "/":
		return "", &fs.PathError{Op: "readlink", Path: p, Err: syscall.ENOENT}
	case mf == nil || !mf.isLink():
		return "", &fs.PathError{Op: "readlink", Path: p, Err: syscall.EINVAL}
	}
	return mf.target, nil
}

// linkFollows is whether link(2) follows a symbolic link it is given, as macOS's does; Linux's
// links the link itself.
var linkFollows = goruntime.GOOS == "darwin"

// Link makes path another name for the file existing names, sharing its contents and inode.
// A symbolic link at existing is treated as the platform's link(2) treats it, as Node leaves
// it to do.
func (m *MemoryFilesystem) Link(existing, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, mf, err := m.resolve("link", existing, linkFollows)
	if err != nil {
		return err
	}
	canonical, _, err := m.resolve("link", p, false)
	if err != nil {
		return err
	}
	if mf == nil {
		return &fs.PathError{Op: "link", Path: existing, Err: syscall.ENOENT}
	}
	if err := m.newName("link", canonical); err != nil {
		return err
	}
	if mf.isDir {
		return &fs.PathError{Op: "link", Path: existing, Err: syscall.EPERM}
	}
	mf.nlink++
	m.files[canonical] = mf
	return nil
}

// Realpath gives the canonical path p names, with every link resolved. The path must exist.
func (m *MemoryFilesystem) Realpath(p string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	canonical, _, err := m.resolve("realpath", p, true)
	if err != nil {
		return "", err
	}
	if !m.present(canonical) {
		return "", &fs.PathError{Op: "realpath", Path: p, Err: syscall.ENOENT}
	}
	return canonical, nil
}

var (
	_ LinkFilesystem  = (*MemoryFilesystem)(nil)
	_ LstatFilesystem = (*MemoryFilesystem)(nil)
)
