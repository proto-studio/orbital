package runtime

import (
	"time"

	"golang.org/x/sys/unix"
)

// chmodAt sets the mode of name in dir without following a link there.
func chmodAt(dir int, name string, mode uint32) error {
	return unix.Fchmodat(dir, name, mode, unix.AT_SYMLINK_NOFOLLOW)
}

// addStatDetails copies what fstatat reported, beyond what FileInfo's main fields carry.
func addStatDetails(fi *FileInfo, st *unix.Stat_t) {
	fi.HasDetails = true
	fi.Dev, fi.Ino, fi.Nlink, fi.Rdev = uint64(st.Dev), st.Ino, uint64(st.Nlink), uint64(st.Rdev)
	fi.Uid, fi.Gid = int(st.Uid), int(st.Gid)
	fi.Blksize, fi.Blocks = int64(st.Blksize), st.Blocks
	fi.AccessTime = time.Unix(st.Atim.Unix())
	fi.ChangeTime = time.Unix(st.Ctim.Unix())
	fi.BirthTime = time.Unix(st.Btim.Unix())
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

// searchFlag opens a directory only to search it: O_EXEC, which with O_DIRECTORY is macOS's
// O_SEARCH. An older macOS that doesn't know it gets O_RDONLY instead.
const searchFlag = 0x40000000

// mkdirKeepsSticky says whether mkdir sets the sticky bit it is given. macOS's doesn't.
const mkdirKeepsSticky = false

// linkFollowsFinal says whether link(2) follows a final symbolic link. macOS's does.
const linkFollowsFinal = true
