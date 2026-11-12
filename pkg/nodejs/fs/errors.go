package fs

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// errnoNames gives Node's code and description for the errors a filesystem call can return.
var errnoNames = map[syscall.Errno][2]string{
	syscall.ENOENT:    {"ENOENT", "no such file or directory"},
	syscall.EEXIST:    {"EEXIST", "file already exists"},
	syscall.EACCES:    {"EACCES", "permission denied"},
	syscall.EPERM:     {"EPERM", "operation not permitted"},
	syscall.EISDIR:    {"EISDIR", "illegal operation on a directory"},
	syscall.ENOTDIR:   {"ENOTDIR", "not a directory"},
	syscall.ENOTEMPTY: {"ENOTEMPTY", "directory not empty"},
	syscall.EBADF:     {"EBADF", "bad file descriptor"},
	syscall.EINVAL:    {"EINVAL", "invalid argument"},
	syscall.EROFS:     {"EROFS", "read-only file system"},
	syscall.EMFILE:    {"EMFILE", "too many open files"},
	syscall.ENOSPC:    {"ENOSPC", "no space left on device"},
}

// errnoOf describes err the way Node does: its code, its description, and its errno, which
// is negative and platform-specific as in Node.
func errnoOf(err error) (code, description string, errno int) {
	var sysErr syscall.Errno
	if !errors.As(err, &sysErr) {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			sysErr = syscall.ENOENT
		case errors.Is(err, fs.ErrExist):
			sysErr = syscall.EEXIST
		case errors.Is(err, fs.ErrPermission):
			sysErr = syscall.EACCES
		case errors.Is(err, fs.ErrInvalid):
			sysErr = syscall.EINVAL
		default:
			sysErr = syscall.EIO
		}
	}
	if name, ok := errnoNames[sysErr]; ok {
		return name[0], name[1], -int(sysErr)
	}
	return "EIO", "i/o error", -int(syscall.EIO)
}

// fsErrorMessage is Node's message for a failed call, such as
// "ENOENT: no such file or directory, open 'missing/file'". A failed read names no path,
// because Node reads from a descriptor it already opened.
func fsErrorMessage(code, description, syscallName, path string) string {
	if errorPath(syscallName, path) == "" {
		return fmt.Sprintf("%s: %s, %s", code, description, syscallName)
	}
	return fmt.Sprintf("%s: %s, %s '%s'", code, description, syscallName, path)
}

// errorPath is the path Node attaches to a failed call's error, if any. Reads, writes and
// closes work on a descriptor, so their errors name none.
func errorPath(syscallName, path string) string {
	switch syscallName {
	case "read", "write", "close":
		return ""
	}
	return path
}

// fsFailure describes a failed synchronous call. A native function can only throw a string,
// so the fs_setup.js wrappers turn this into Node's Error and throw that instead.
func fsFailure(ctx *v8.Context, err error, syscallName, path string) *v8.Value {
	code, description, errno := errnoOf(err)
	obj, _ := ctx.NewObject()
	set := func(key, value string) {
		v, _ := ctx.NewString(value)
		obj.Set(key, v)
	}
	obj.Set("__fsError", ctx.True())
	set("message", fsErrorMessage(code, description, syscallName, path))
	set("code", code)
	set("syscall", syscallName)
	if p := errorPath(syscallName, path); p != "" {
		set("path", p)
	}
	obj.Set("errno", ctx.NewInteger(int64(errno)))
	return obj
}

// asyncFsError builds Node's Error for a failed asynchronous call, to pass to its callback.
func (f *FS) asyncFsError(ctx *v8.Context, err error, syscallName, path string) *v8.Value {
	code, description, errno := errnoOf(err)
	e := f.makeFsError(ctx, code, fsErrorMessage(code, description, syscallName, path), errorPath(syscallName, path), syscallName)
	if e != nil && e.IsObject() {
		e.Set("errno", ctx.NewInteger(int64(errno)))
	}
	return e
}

// writeWithFlags writes data to path, opening it with a Node flag such as 'w', 'a' or 'wx',
// and perm as the mode of a file it creates. On failure it also names the call that failed,
// "open", "write" or "close", as Node's error does.
func (f *FS) writeWithFlags(path string, data []byte, flag *v8.Value, perm fs.FileMode) (string, error) {
	flags, ok := parseOpenFlags(flag)
	if !ok {
		return "open", syscall.EINVAL
	}
	return writeFile(f.fs(), path, data, flags, perm)
}

// writeFile writes data to path opened with flags. On failure it also names the call that
// failed, as writeWithFlags does.
func writeFile(fsys runtime.Filesystem, path string, data []byte, flags int, perm fs.FileMode) (string, error) {
	file, err := fsys.OpenFile(path, flags, perm)
	if err != nil {
		return "open", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "write", err
	}
	if err := file.Close(); err != nil {
		return "close", err
	}
	return "", nil
}

// modeArg reads a mode argument, falling back to Node's default of 0o666.
func modeArg(args []*v8.Value, i int) fs.FileMode {
	if i < len(args) && args[i] != nil && args[i].IsNumber() {
		return fs.FileMode(args[i].Integer()) & fs.ModePerm
	}
	return 0o666
}

// argAt returns args[i], or nil if there is no such argument.
func argAt(args []*v8.Value, i int) *v8.Value {
	if i < len(args) {
		return args[i]
	}
	return nil
}
