package runtime

import (
	"syscall"
	"time"
)

// addDetails copies what the OS reported about a file, beyond what os.FileInfo carries.
func addDetails(fi *FileInfo, sys any) {
	st, ok := sys.(*syscall.Stat_t)
	if !ok {
		return
	}
	fi.HasDetails = true
	fi.Dev, fi.Ino, fi.Nlink, fi.Rdev = uint64(st.Dev), st.Ino, uint64(st.Nlink), uint64(st.Rdev)
	fi.Uid, fi.Gid = int(st.Uid), int(st.Gid)
	fi.Blksize, fi.Blocks = int64(st.Blksize), st.Blocks
	fi.AccessTime = time.Unix(st.Atimespec.Unix())
	fi.ChangeTime = time.Unix(st.Ctimespec.Unix())
	fi.BirthTime = time.Unix(st.Birthtimespec.Unix())
}

func statfs(path string) (*FsStats, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil, err
	}
	return &FsStats{
		Type: int64(st.Type), Bsize: int64(st.Bsize), Blocks: st.Blocks, Bfree: st.Bfree,
		Bavail: st.Bavail, Files: st.Files, Ffree: st.Ffree,
	}, nil
}
