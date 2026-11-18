package runtime

import (
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

// walk resolves p. Links along the way are followed, and a final one as final says. A
// missing directory along the way is taken as an empty one, since files here need no parent;
// a file that isn't a directory is ENOTDIR. The caller holds m.mu.
func (m *MemoryFilesystem) walk(op, p string, finalLink finalLink) (resolved, error) {
	if p == "" {
		return resolved{}, &fs.PathError{Op: op, Path: p, Err: syscall.ENOENT}
	}
	rest := strings.Split(p, "/")
	// The last given names in rest are p's own; any before them come from link targets.
	given := len(rest)
	cur := "/"
	links := 0
	missingTarget := false
	for len(rest) > 0 {
		name := rest[0]
		fromTarget := len(rest) > given
		rest = rest[1:]
		if !fromTarget {
			given--
		}
		switch name {
		case "", ".":
			continue
		case "..":
			cur = path.Dir(cur)
			continue
		}
		next := path.Join(cur, name)
		mf, ok := m.files[next]
		// final is set for the last name in the path; a "/" after it still asks for a directory.
		final, trailing := true, len(rest) > 0
		for _, r := range rest {
			if r != "" && r != "." {
				final = false
				break
			}
		}
		if !ok {
			if fromTarget {
				missingTarget = true
			}
			cur = next
			continue
		}
		if mf.isLink() && final && trailing && finalLink == entryFinal && !entrySlashFollows {
			return resolved{}, &fs.PathError{Op: op, Path: p, Err: syscall.ENOTDIR}
		}
		if mf.isLink() && (!final || finalLink == followFinal || trailing) {
			links++
			if links > maxSymlinks {
				return resolved{}, &fs.PathError{Op: op, Path: p, Err: syscall.ELOOP}
			}
			if strings.HasPrefix(mf.target, "/") {
				cur = "/"
			}
			rest = append(strings.Split(mf.target, "/"), rest...)
			continue
		}
		if (!final || trailing) && !mf.isDir {
			return resolved{}, &fs.PathError{Op: op, Path: p, Err: syscall.ENOTDIR}
		}
		cur = next
	}
	return resolved{path: cur, file: m.files[cur], missingTarget: missingTarget}, nil
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
