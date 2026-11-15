package fs

import (
	"io/fs"
	"math"
	"syscall"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// The calls that change a file's size, mode, owner and times, flush it, check access to it
// or describe its filesystem. fs_setup.js checks their arguments first. A Filesystem or File
// without the optional interfaces these need fails them with ENOSYS.

// pathOp is a call that runs op on the path in the first argument, and reports its failure
// as Node's error for syscallName.
func (f *FS) pathOp(args []*v8.Value, syscallName string, op func(runtime.MetadataFilesystem, string) error) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		m, ok := fsys.(runtime.MetadataFilesystem)
		if !ok {
			return failed(syscall.ENOSYS, syscallName, path)
		}
		return failed(op(m, path), syscallName, path)
	}, -1, nil
}

// fdOp is a call that runs op on the file for the descriptor in the first argument, and
// reports its failure as Node's error for syscallName.
func (f *FS) fdOp(args []*v8.Value, syscallName string, op func(runtime.MetadataFile) error) (func() opResult, int, *v8.Value) {
	fd := fdAt(args)
	return func() opResult {
		file, ok := f.fileForFD(fd)
		if !ok {
			return failed(syscall.EBADF, syscallName, "")
		}
		m, ok := file.(runtime.MetadataFile)
		if !ok {
			return failed(syscall.ENOSYS, syscallName, "")
		}
		return failed(op(m), syscallName, "")
	}, fd, nil
}

// timeAt reads a time given in seconds, with a fraction.
func timeAt(args []*v8.Value, i int) time.Time {
	if i >= len(args) || args[i] == nil {
		return time.Now()
	}
	whole, frac := math.Modf(args[i].Number())
	return time.Unix(int64(whole), int64(math.Round(frac*1e9)))
}

// truncateOp backs truncate(path, len). Node opens the file to truncate it, so its errors
// name open.
func (f *FS) truncateOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	size := intAt(args, 1)
	return f.pathOp(args, "open", func(m runtime.MetadataFilesystem, path string) error { return m.Truncate(path, size) })
}

func (f *FS) ftruncateOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	size := intAt(args, 1)
	return f.fdOp(args, "ftruncate", func(m runtime.MetadataFile) error { return m.Truncate(size) })
}

// fsyncOp backs fsync and fdatasync, which both flush the whole file here.
func (f *FS) fsyncOp(syscallName string) fsOp {
	return func(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
		return f.fdOp(args, syscallName, func(m runtime.MetadataFile) error { return m.Sync() })
	}
}

// fileMode turns a numeric mode such as 0o4755 into a Go FileMode.
func fileMode(mode int64) fs.FileMode {
	m := fs.FileMode(mode) & fs.ModePerm
	if mode&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if mode&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if mode&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

func (f *FS) chmodOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	mode := fileMode(intAt(args, 1))
	return f.pathOp(args, "chmod", func(m runtime.MetadataFilesystem, path string) error { return m.Chmod(path, mode) })
}

func (f *FS) fchmodOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	mode := fileMode(intAt(args, 1))
	return f.fdOp(args, "fchmod", func(m runtime.MetadataFile) error { return m.Chmod(mode) })
}

func (f *FS) chownOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	uid, gid := int(intAt(args, 1)), int(intAt(args, 2))
	return f.pathOp(args, "chown", func(m runtime.MetadataFilesystem, path string) error { return m.Chown(path, uid, gid) })
}

func (f *FS) fchownOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	uid, gid := int(intAt(args, 1)), int(intAt(args, 2))
	return f.fdOp(args, "fchown", func(m runtime.MetadataFile) error { return m.Chown(uid, gid) })
}

func (f *FS) utimesOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	atime, mtime := timeAt(args, 1), timeAt(args, 2)
	return f.pathOp(args, "utime", func(m runtime.MetadataFilesystem, path string) error { return m.Chtimes(path, atime, mtime) })
}

func (f *FS) futimesOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	atime, mtime := timeAt(args, 1), timeAt(args, 2)
	return f.fdOp(args, "futime", func(m runtime.MetadataFile) error { return m.Chtimes(atime, mtime) })
}

// accessOp backs access(path, mode), checking mode's R_OK, W_OK and X_OK bits as access(2)
// does. A Filesystem that can't check them is asked only whether the path exists.
func (f *FS) accessOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	mode := uint32(intAt(args, 1))
	fsys := f.fs()
	return func() opResult {
		if m, ok := fsys.(runtime.MetadataFilesystem); ok {
			return failed(m.Access(path, mode), "access", path)
		}
		if !fsys.Exists(path) {
			return failed(syscall.ENOENT, "access", path)
		}
		return opResult{}
	}, -1, nil
}

// statfsOp backs statfs(path), giving the fields of Node's fs.StatFs.
func (f *FS) statfsOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		s, ok := fsys.(runtime.StatfsFilesystem)
		if !ok {
			return failed(syscall.ENOSYS, "statfs", path)
		}
		st, err := s.Statfs(path)
		if err != nil {
			return failed(err, "statfs", path)
		}
		return opResult{value: func(ctx *v8.Context) *v8.Value {
			obj, _ := ctx.NewObject()
			for _, field := range []struct {
				key   string
				value float64
			}{
				{"type", float64(st.Type)}, {"bsize", float64(st.Bsize)}, {"blocks", float64(st.Blocks)},
				{"bfree", float64(st.Bfree)}, {"bavail", float64(st.Bavail)}, {"files", float64(st.Files)},
				{"ffree", float64(st.Ffree)},
			} {
				obj.Set(field.key, ctx.NewNumber(field.value))
			}
			return obj
		}}
	}, -1, nil
}
