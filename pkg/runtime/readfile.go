package runtime

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"syscall"
)

// MaxReadFileSize is the largest file fs.readFile reads. Its contents reach JavaScript as
// one string, base64 for binary reads, and V8's longest string, String::kMaxLength on
// 64-bit platforms, holds 402,653,166 bytes as base64. Node reads files up to 2 GiB.
const MaxReadFileSize = (1<<29 - 24) / 4 * 3

// nodeMaxReadSize is the largest file Node's readFile reads, kIoMaxLength.
const nodeMaxReadSize = 1<<31 - 1

// MaxSourceSize is the largest file require, import and worker entry files load: their
// source is one string, and this is V8's longest, String::kMaxLength on 64-bit platforms.
// Node fails a larger file with ERR_STRING_TOO_LONG (StringTooLongMessage), or, for import
// of one over 2 GiB, with ERR_FS_FILE_TOO_LARGE (see FileTooLargeError.OverNodeLimit).
const MaxSourceSize = 1<<29 - 24

// StringTooLongMessage is Node's message for ERR_STRING_TOO_LONG.
const StringTooLongMessage = "Cannot create a string longer than 0x1fffffe8 characters"

// FileTooLargeError reports a file larger than ReadFileLimited's limit. Node reports it as
// a RangeError with code ERR_FS_FILE_TOO_LARGE.
type FileTooLargeError struct {
	Path string
	// Size is the size the file reports, or, for one that doesn't report its size (a
	// device or a pipe), how many bytes were read before giving up.
	Size int64
	// Limit is the limit it is over.
	Limit int64
}

// OverNodeLimit reports whether the file is over the 2 GiB Node's readFile refuses.
func (e *FileTooLargeError) OverNodeLimit() bool {
	return e.Size > nodeMaxReadSize
}

func (e *FileTooLargeError) Error() string {
	if e.OverNodeLimit() {
		return fmt.Sprintf("File size (%d) is greater than 2 GiB", e.Size)
	}
	return fmt.Sprintf("File size (%d) is greater than %d bytes", e.Size, e.Limit)
}

// ReadFileLimited reads the whole file at path through fsys, refusing one larger than
// limit with a *FileTooLargeError. It checks the size the file reports first, and never
// reads more than limit+1 bytes, so a file that grows, or one that never ends such as
// /dev/zero, can't use more memory than that. A directory fails with EISDIR, as a read of
// one does.
func ReadFileLimited(fsys Filesystem, path string, limit int64) ([]byte, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir {
		return nil, &fs.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
	}
	if info.Size > limit {
		return nil, &FileTooLargeError{Path: path, Size: info.Size, Limit: limit}
	}

	var buf bytes.Buffer
	if info.Size > 0 {
		buf.Grow(int(info.Size) + 1)
	}
	if _, err := buf.ReadFrom(io.LimitReader(f, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, &FileTooLargeError{Path: path, Size: int64(buf.Len()), Limit: limit}
	}
	return buf.Bytes(), nil
}
