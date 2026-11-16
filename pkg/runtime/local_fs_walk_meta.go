//go:build unix

package runtime

import (
	"errors"
	"io/fs"
	"time"

	"golang.org/x/sys/unix"
)

// The metadata calls of a LocalFilesystem with a root. Each walks the path as the other
// calls do, then acts on the final name relative to the directory the walk reached, without
// following a link there: AT_SYMLINK_NOFOLLOW, or a descriptor opened with O_NOFOLLOW.

// walkFinal walks p, following a final link, and runs op on where it ended.
func (l *LocalFilesystem) walkFinal(p, opName string, op func(w *walk) error) error {
	w, err := l.walkTo(p, walkOptions{followFinal: true})
	if err != nil {
		return err
	}
	defer w.close()
	return w.fail(opName, op(w))
}

// rootedTruncate truncates a regular file, as truncate(2) does: a directory is EISDIR, and a
// pipe or device EINVAL, found before the open, which doesn't wait for a reader either way.
func (l *LocalFilesystem) rootedTruncate(p string, size int64) error {
	for range maxLinkHops {
		w, err := l.walkTo(p, walkOptions{followFinal: true})
		if err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstatat(w.dir(), w.name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			w.close()
			return w.fail("open", err)
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			w.close()
			continue // a link appeared after the walk read the name; walk again
		case unix.S_IFDIR:
			w.close()
			return w.fail("open", unix.EISDIR)
		case unix.S_IFREG:
		default:
			w.close()
			return w.fail("open", unix.EINVAL)
		}
		fd, err := unix.Openat(w.dir(), w.name, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		w.close()
		if err != nil {
			return &fs.PathError{Op: "open", Path: p, Err: err}
		}
		err = unix.Ftruncate(fd, size)
		unix.Close(fd)
		if err != nil {
			return &fs.PathError{Op: "truncate", Path: p, Err: err}
		}
		return nil
	}
	return &fs.PathError{Op: "open", Path: p, Err: unix.ELOOP}
}

func (l *LocalFilesystem) rootedChmod(p string, mode fs.FileMode) error {
	return l.walkFinal(p, "chmod", func(w *walk) error { return chmodAt(w.dir(), w.name, syscallMode(mode)) })
}

func (l *LocalFilesystem) rootedChown(p string, uid, gid int) error {
	return l.walkFinal(p, "chown", func(w *walk) error {
		return unix.Fchownat(w.dir(), w.name, uid, gid, unix.AT_SYMLINK_NOFOLLOW)
	})
}

func (l *LocalFilesystem) rootedChtimes(p string, atime, mtime time.Time) error {
	return l.walkFinal(p, "chtimes", func(w *walk) error {
		return unix.UtimesNanoAt(w.dir(), w.name, []unix.Timespec{timespec(atime), timespec(mtime)}, unix.AT_SYMLINK_NOFOLLOW)
	})
}

func (l *LocalFilesystem) rootedAccess(p string, mode uint32) error {
	return l.walkFinal(p, "access", func(w *walk) error {
		return unix.Faccessat(w.dir(), w.name, mode, unix.AT_SYMLINK_NOFOLLOW)
	})
}

// rootedStatfs describes the filesystem of the final name, or of its directory when the name
// can't be opened, which is the same filesystem unless the name is a mount point.
func (l *LocalFilesystem) rootedStatfs(p string) (*FsStats, error) {
	var stats *FsStats
	err := l.walkFinal(p, "statfs", func(w *walk) error {
		var st unix.Stat_t
		if err := unix.Fstatat(w.dir(), w.name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		fd, err := unix.Openat(w.dir(), w.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return err
			}
			fd = -1
		}
		target := w.dir()
		if fd >= 0 {
			defer unix.Close(fd)
			target = fd
		}
		stats, err = statfsFd(target)
		return err
	})
	return stats, err
}
