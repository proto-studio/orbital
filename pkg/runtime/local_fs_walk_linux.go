package runtime

import (
	"errors"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// chmodAt sets the mode of name in dir without following a link there. Linux's fchmodat
// can't take AT_SYMLINK_NOFOLLOW on older kernels, so the name is opened with O_PATH and
// O_NOFOLLOW and its mode set through /proc/self/fd, which names that very file.
func chmodAt(dir int, name string, mode uint32) error {
	fd, err := unix.Openat(dir, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		// The name became a link after the walk; links have no mode of their own here.
		return unix.ELOOP
	}
	err = unix.Chmod("/proc/self/fd/"+strconv.Itoa(fd), mode)
	if errors.Is(err, unix.ENOENT) {
		// Without /proc, fchmodat2 takes the flag on newer kernels.
		return unix.Fchmodat(dir, name, mode, unix.AT_SYMLINK_NOFOLLOW)
	}
	return err
}

// addStatDetails copies what fstatat reported, beyond what FileInfo's main fields carry.
// Linux's stat has no birth time, so the change time stands in for it.
func addStatDetails(fi *FileInfo, st *unix.Stat_t) {
	fi.HasDetails = true
	fi.Dev, fi.Ino, fi.Nlink, fi.Rdev = uint64(st.Dev), st.Ino, uint64(st.Nlink), uint64(st.Rdev)
	fi.Uid, fi.Gid = int(st.Uid), int(st.Gid)
	fi.Blksize, fi.Blocks = int64(st.Blksize), st.Blocks
	fi.AccessTime = time.Unix(st.Atim.Unix())
	fi.ChangeTime = time.Unix(st.Ctim.Unix())
	fi.BirthTime = fi.ChangeTime
}

func statfsFd(fd int) (*FsStats, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return nil, err
	}
	return &FsStats{
		Type: int64(st.Type), Bsize: int64(st.Bsize), Blocks: st.Blocks, Bfree: st.Bfree,
		Bavail: st.Bavail, Files: st.Files, Ffree: st.Ffree,
	}, nil
}

// searchFlag opens a directory only as a place to walk from, with O_PATH, which needs no
// read permission.
const searchFlag = unix.O_PATH

// mkdirKeepsSticky says whether mkdir sets the sticky bit it is given. Linux's does.
const mkdirKeepsSticky = true

// linkFollowsFinal says whether link(2) follows a final symbolic link. Linux's doesn't.
const linkFollowsFinal = false
