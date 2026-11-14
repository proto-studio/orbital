package fs

import (
	"io/fs"
	"syscall"
	"testing"

	"proto.zip/studio/orbital/pkg/nodejs/internal/sysconst"
)

// An OS error keeps its own code, description and errno, rather than turning into EIO.
func TestErrnoOf(t *testing.T) {
	cases := []struct {
		err         error
		code, descr string
		errno       int
	}{
		{&fs.PathError{Op: "open", Path: "/a", Err: syscall.ELOOP}, "ELOOP", "too many symbolic links encountered", -int(syscall.ELOOP)},
		{&fs.PathError{Op: "open", Path: "/a", Err: syscall.ENAMETOOLONG}, "ENAMETOOLONG", "name too long", -int(syscall.ENAMETOOLONG)},
		{&fs.PathError{Op: "write", Path: "/a", Err: syscall.EFBIG}, "EFBIG", "file too large", -int(syscall.EFBIG)},
		{syscall.EIO, "EIO", "i/o error", -int(syscall.EIO)},
		{fs.ErrNotExist, "ENOENT", "no such file or directory", -int(syscall.ENOENT)},
		{syscall.Errno(250), "UNKNOWN", "unknown error", -250},
	}
	for _, c := range cases {
		code, descr, errno := errnoOf(c.err)
		if code != c.code || descr != c.descr || errno != c.errno {
			t.Errorf("errnoOf(%v) = %q, %q, %d; want %q, %q, %d", c.err, code, descr, errno, c.code, c.descr, c.errno)
		}
	}
}

// Each description belongs to a code os.constants.errno has, so fs errors and os.constants
// name errors the same way.
func TestErrnoDescriptionsUseKnownCodes(t *testing.T) {
	known := map[string]bool{}
	for _, c := range sysconst.Errnos() {
		known[c.Name] = true
	}
	for code := range errnoDescriptions {
		if !known[code] {
			t.Errorf("errnoDescriptions has %s, which os.constants.errno lacks", code)
		}
	}
}
