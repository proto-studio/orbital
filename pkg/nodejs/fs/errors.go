package fs

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"

	"proto.zip/studio/orbital/pkg/nodejs/internal/sysconst"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// errnoDescriptions gives libuv's description, which Node uses in its messages, for the errors
// a filesystem call can return. The code itself comes from sysconst, as os.constants.errno does.
var errnoDescriptions = map[string]string{
	"ENOENT":       "no such file or directory",
	"EEXIST":       "file already exists",
	"EACCES":       "permission denied",
	"EPERM":        "operation not permitted",
	"EISDIR":       "illegal operation on a directory",
	"ENOTDIR":      "not a directory",
	"ENOTEMPTY":    "directory not empty",
	"EBADF":        "bad file descriptor",
	"EINVAL":       "invalid argument",
	"EROFS":        "read-only file system",
	"EMFILE":       "too many open files",
	"ENFILE":       "file table overflow",
	"ENOSPC":       "no space left on device",
	"EIO":          "i/o error",
	"ELOOP":        "too many symbolic links encountered",
	"ENAMETOOLONG": "name too long",
	"EFBIG":        "file too large",
	"EBUSY":        "resource busy or locked",
	"EAGAIN":       "resource temporarily unavailable",
	"EINTR":        "interrupted system call",
	"EMLINK":       "too many links",
	"ENODEV":       "no such device",
	"ENXIO":        "no such device or address",
	"ENOMEM":       "not enough memory",
	"ENOSYS":       "function not implemented",
	"ESPIPE":       "invalid seek",
	"ETXTBSY":      "text file is busy",
	"EXDEV":        "cross-device link not permitted",
	"EDQUOT":       "disk quota exceeded",
	"EOVERFLOW":    "value too large for defined data type",
	"EFAULT":       "bad address in system call argument",
	"E2BIG":        "argument list too long",
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
	if code, ok := sysconst.ErrnoName(sysErr); ok {
		if description, ok := errnoDescriptions[code]; ok {
			return code, description, -int(sysErr)
		}
	}
	// An errno outside the table keeps its number, so the failure isn't mistaken for another.
	return "UNKNOWN", "unknown error", -int(sysErr)
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
	case "read", "write", "close", "fstat":
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

// fsFailureTo describes a failed call on two paths, such as rename, whose message names both:
// "ENOENT: no such file or directory, rename 'a' -> 'b'".
func fsFailureTo(ctx *v8.Context, err error, syscallName, path, dest string) *v8.Value {
	obj := fsFailure(ctx, err, syscallName, path)
	code, description, _ := errnoOf(err)
	message, _ := ctx.NewString(fmt.Sprintf("%s: %s, %s '%s' -> '%s'", code, description, syscallName, path, dest))
	obj.Set("message", message)
	destVal, _ := ctx.NewString(dest)
	obj.Set("dest", destVal)
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
