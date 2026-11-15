package fs

import (
	"io/fs"
	"math"
	"syscall"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// The synchronous calls that change a file's size, mode, owner and times, flush it, check
// access to it or describe its filesystem. Each returns nothing, or a value, or an fsFailure
// that fs_setup.js throws as Node's error; fs_setup.js checks the arguments and builds the
// callback and promise forms. A Filesystem or File without the optional interfaces these
// need fails them with ENOSYS.

func (f *FS) registerMetadata(regFn func(string, v8.FunctionCallback) error) error {
	for name, fn := range map[string]v8.FunctionCallback{
		"truncateSync":  f.truncateSyncFunc,
		"ftruncateSync": f.ftruncateSyncFunc,
		"fsyncSync":     func(info *v8.FunctionCallbackInfo) *v8.Value { return f.fsync(info, "fsync") },
		"fdatasyncSync": func(info *v8.FunctionCallbackInfo) *v8.Value { return f.fsync(info, "fdatasync") },
		"chmodSync":     f.chmodSyncFunc,
		"fchmodSync":    f.fchmodSyncFunc,
		"chownSync":     f.chownSyncFunc,
		"fchownSync":    f.fchownSyncFunc,
		"utimesSync":    f.utimesSyncFunc,
		"futimesSync":   f.futimesSyncFunc,
		"accessSync":    f.accessSyncFunc,
		"statfsSync":    f.statfsSyncFunc,
	} {
		if err := regFn(name, fn); err != nil {
			return err
		}
	}
	return nil
}

// metadataFS returns the filesystem's MetadataFilesystem, or ENOSYS.
func (f *FS) metadataFS() (runtime.MetadataFilesystem, error) {
	if m, ok := f.fs().(runtime.MetadataFilesystem); ok {
		return m, nil
	}
	return nil, syscall.ENOSYS
}

// metadataFile returns the open file for the descriptor in the first argument, or EBADF if
// there is none, or ENOSYS if it can't change its metadata.
func (f *FS) metadataFile(info *v8.FunctionCallbackInfo) (runtime.MetadataFile, error) {
	args := info.Args()
	if len(args) < 1 {
		return nil, syscall.EBADF
	}
	file, ok := f.fileForFD(int(args[0].Integer()))
	if !ok {
		return nil, syscall.EBADF
	}
	m, ok := file.(runtime.MetadataFile)
	if !ok {
		return nil, syscall.ENOSYS
	}
	return m, nil
}

// pathCall runs op on the path in the first argument, and reports its failure as Node's
// error for syscallName.
func (f *FS) pathCall(info *v8.FunctionCallbackInfo, syscallName string, op func(runtime.MetadataFilesystem, string) error) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	path := args[0].String()
	m, err := f.metadataFS()
	if err == nil {
		err = op(m, path)
	}
	if err != nil {
		return fsFailure(info.Context(), err, syscallName, path)
	}
	return nil
}

// fdCall runs op on the file for the descriptor in the first argument, and reports its
// failure as Node's error for syscallName.
func (f *FS) fdCall(info *v8.FunctionCallbackInfo, syscallName string, op func(runtime.MetadataFile) error) *v8.Value {
	m, err := f.metadataFile(info)
	if err == nil {
		err = op(m)
	}
	if err != nil {
		return fsFailure(info.Context(), err, syscallName, "")
	}
	return nil
}

// arg returns argument i, or undefined.
func arg(info *v8.FunctionCallbackInfo, i int) *v8.Value {
	if args := info.Args(); i < len(args) {
		return args[i]
	}
	return nil
}

func intArg(info *v8.FunctionCallbackInfo, i int) int64 {
	if v := arg(info, i); v != nil {
		return v.Integer()
	}
	return 0
}

// timeArg reads a time given in seconds, with a fraction.
func timeArg(info *v8.FunctionCallbackInfo, i int) time.Time {
	v := arg(info, i)
	if v == nil {
		return time.Now()
	}
	seconds := v.Number()
	whole, frac := math.Modf(seconds)
	return time.Unix(int64(whole), int64(math.Round(frac*1e9)))
}

// truncateSyncFunc backs fs.truncateSync(path, len). Node opens the file to truncate it, so
// its errors name open.
func (f *FS) truncateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	size := intArg(info, 1)
	return f.pathCall(info, "open", func(m runtime.MetadataFilesystem, path string) error {
		return m.Truncate(path, size)
	})
}

func (f *FS) ftruncateSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	size := intArg(info, 1)
	return f.fdCall(info, "ftruncate", func(m runtime.MetadataFile) error { return m.Truncate(size) })
}

// fsync backs fs.fsyncSync and fs.fdatasyncSync, which both flush the whole file here.
func (f *FS) fsync(info *v8.FunctionCallbackInfo, syscallName string) *v8.Value {
	return f.fdCall(info, syscallName, func(m runtime.MetadataFile) error { return m.Sync() })
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

func (f *FS) chmodSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	mode := fileMode(intArg(info, 1))
	return f.pathCall(info, "chmod", func(m runtime.MetadataFilesystem, path string) error {
		return m.Chmod(path, mode)
	})
}

func (f *FS) fchmodSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	mode := fileMode(intArg(info, 1))
	return f.fdCall(info, "fchmod", func(m runtime.MetadataFile) error { return m.Chmod(mode) })
}

func (f *FS) chownSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	uid, gid := int(intArg(info, 1)), int(intArg(info, 2))
	return f.pathCall(info, "chown", func(m runtime.MetadataFilesystem, path string) error {
		return m.Chown(path, uid, gid)
	})
}

func (f *FS) fchownSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	uid, gid := int(intArg(info, 1)), int(intArg(info, 2))
	return f.fdCall(info, "fchown", func(m runtime.MetadataFile) error { return m.Chown(uid, gid) })
}

func (f *FS) utimesSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	atime, mtime := timeArg(info, 1), timeArg(info, 2)
	return f.pathCall(info, "utime", func(m runtime.MetadataFilesystem, path string) error {
		return m.Chtimes(path, atime, mtime)
	})
}

func (f *FS) futimesSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	atime, mtime := timeArg(info, 1), timeArg(info, 2)
	return f.fdCall(info, "futime", func(m runtime.MetadataFile) error { return m.Chtimes(atime, mtime) })
}

// accessSyncFunc backs fs.accessSync(path, mode).
func (f *FS) accessSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	mode := uint32(intArg(info, 1))
	if _, ok := f.fs().(runtime.MetadataFilesystem); !ok {
		// Without access checks, the path need only exist, as the async fs.access does.
		path := arg(info, 0).String()
		if !f.fs().Exists(path) {
			return fsFailure(info.Context(), syscall.ENOENT, "access", path)
		}
		return nil
	}
	return f.pathCall(info, "access", func(m runtime.MetadataFilesystem, path string) error {
		return m.Access(path, mode)
	})
}

// statfsSyncFunc backs fs.statfsSync(path), returning the fields of Node's fs.StatFs.
func (f *FS) statfsSyncFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	path := args[0].String()
	s, ok := f.fs().(runtime.StatfsFilesystem)
	if !ok {
		return fsFailure(ctx, syscall.ENOSYS, "statfs", path)
	}
	st, err := s.Statfs(path)
	if err != nil {
		return fsFailure(ctx, err, "statfs", path)
	}
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
}
