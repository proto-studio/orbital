package fs

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"syscall"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// The symbolic and hard link calls, realpath and mkdtemp. Every path goes through the
// Filesystem, so a LocalFilesystem with a root checks each one as it checks any other path.

func linkFS(fsys runtime.Filesystem) (runtime.LinkFilesystem, error) {
	if l, ok := fsys.(runtime.LinkFilesystem); ok {
		return l, nil
	}
	return nil, syscall.ENOSYS
}

func stringValue(s string) func(ctx *v8.Context) *v8.Value {
	return func(ctx *v8.Context) *v8.Value {
		v, _ := ctx.NewString(s)
		return v
	}
}

// symlinkOp backs symlink(target, path). Its errors name both, as Node's do.
func (f *FS) symlinkOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	target, path := stringArg(args, 0), stringArg(args, 1)
	fsys := f.fs()
	return func() opResult {
		l, err := linkFS(fsys)
		if err == nil {
			err = l.Symlink(target, path)
		}
		if err != nil {
			return opResult{err: err, syscall: "symlink", path: target, dest: path}
		}
		return opResult{}
	}, -1, nil
}

func (f *FS) readlinkOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		l, err := linkFS(fsys)
		if err != nil {
			return failed(err, "readlink", path)
		}
		target, err := l.Readlink(path)
		if err != nil {
			return failed(err, "readlink", path)
		}
		// As base64, since a target need not be UTF-8 and a string would lose its bytes.
		return opResult{value: stringValue(base64.StdEncoding.EncodeToString([]byte(target)))}
	}, -1, nil
}

func (f *FS) linkOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	existing, path := stringArg(args, 0), stringArg(args, 1)
	fsys := f.fs()
	return func() opResult {
		l, err := linkFS(fsys)
		if err == nil {
			err = l.Link(existing, path)
		}
		if err != nil {
			return opResult{err: err, syscall: "link", path: existing, dest: path}
		}
		return opResult{}
	}, -1, nil
}

// realpathOp backs realpath(path). A Filesystem without links resolves nothing, so the path
// need only exist.
func (f *FS) realpathOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	path := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		l, err := linkFS(fsys)
		if err != nil {
			if _, err := fsys.Stat(path); err != nil {
				return failed(err, "realpath", path)
			}
			return opResult{value: stringValue(path)}
		}
		real, err := l.Realpath(path)
		if err != nil {
			return failed(err, "realpath", path)
		}
		return opResult{value: stringValue(real)}
	}, -1, nil
}

const tempChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// mkdtempOp backs mkdtemp(prefix): it makes a directory named prefix and six random letters
// and digits, with mode 0o700, and gives its path. Errors name prefix + "XXXXXX", as Node's do.
func (f *FS) mkdtempOp(ctx *v8.Context, args []*v8.Value) (func() opResult, int, *v8.Value) {
	prefix := stringArg(args, 0)
	fsys := f.fs()
	return func() opResult {
		for range 100 {
			var random [6]byte
			if _, err := rand.Read(random[:]); err != nil {
				return failed(err, "mkdtemp", prefix+"XXXXXX")
			}
			name := []byte(prefix)
			for _, b := range random {
				name = append(name, tempChars[int(b)%len(tempChars)])
			}
			path := string(name)
			err := fsys.Mkdir(path, 0o700)
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			if err != nil {
				return failed(err, "mkdtemp", prefix+"XXXXXX")
			}
			return opResult{value: stringValue(path)}
		}
		return failed(syscall.EEXIST, "mkdtemp", prefix+"XXXXXX")
	}, -1, nil
}
