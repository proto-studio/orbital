package fs

import (
	"io/fs"
	"syscall"
	"testing"
)

// A failed read names the call that failed: open, or read once the file is open, as Node does.
func TestReadSyscall(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"missing", &fs.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}, "open"},
		{"read failure", &fs.PathError{Op: "read", Path: "/proc/self/mem", Err: syscall.EIO}, "read"},
		{"directory", &fs.PathError{Op: "read", Path: "/d", Err: syscall.EISDIR}, "read"},
		{"directory at open", &fs.PathError{Op: "open", Path: "/d", Err: syscall.EISDIR}, "read"},
		{"bare errno", syscall.EACCES, "open"},
	}
	for _, c := range cases {
		if got := readSyscall(c.err); got != c.want {
			t.Errorf("%s: readSyscall = %q, want %q", c.name, got, c.want)
		}
	}
	if got := fsErrorMessage("EIO", "i/o error", "read", "/proc/self/mem"); got != "EIO: i/o error, read" {
		t.Errorf("read failure message = %q", got)
	}
}
