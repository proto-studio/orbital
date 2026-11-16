//go:build darwin || linux

package runtime

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The link calls of a LocalFilesystem with a root, made relative to the directory the walk
// reached, as the other calls are.

func (l *LocalFilesystem) rootedSymlink(target, p string) error {
	w, err := l.walkTo(p, walkOptions{})
	if err != nil {
		return err
	}
	defer w.close()
	if err := unix.Symlinkat(target, w.dir(), w.name); err != nil {
		return &os.LinkError{Op: "symlink", Old: target, New: p, Err: err}
	}
	return nil
}

func (l *LocalFilesystem) rootedReadlink(p string) (string, error) {
	w, err := l.walkTo(p, walkOptions{})
	if err != nil {
		return "", err
	}
	defer w.close()
	target, err := readlinkat(w.dir(), w.name)
	return target, w.fail("readlink", err)
}

// rootedLink links what the walk of existing reaches.
func (l *LocalFilesystem) rootedLink(existing, p string) error {
	// As link(2) does, a final link is followed on macOS and linked itself on Linux. Either
	// way linkat gets no flag, so it never follows a link the walk didn't.
	from, err := l.walkTo(existing, walkOptions{followFinal: linkFollowsFinal})
	if err != nil {
		return err
	}
	defer from.close()
	to, err := l.walkTo(p, walkOptions{})
	if err != nil {
		return err
	}
	defer to.close()
	if err := unix.Linkat(from.dir(), from.name, to.dir(), to.name, 0); err != nil {
		return &os.LinkError{Op: "link", Old: existing, New: p, Err: err}
	}
	return nil
}

// rootedRealpath gives the path the walk reached, from the root. The path must exist.
func (l *LocalFilesystem) rootedRealpath(p string) (string, error) {
	w, err := l.walkTo(p, walkOptions{followFinal: true})
	if err != nil {
		return "", err
	}
	defer w.close()
	if _, err := w.stat(); err != nil {
		return "", err
	}
	return filepath.Clean(string(filepath.Separator) + filepath.Join(append(append([]string{}, w.names...), w.name)...)), nil
}
