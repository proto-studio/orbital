package sysconst

import (
	"syscall"
	"testing"
)

// Where two names share a number, the code reported for it is the one libuv uses.
func TestErrnoName(t *testing.T) {
	cases := map[syscall.Errno]string{
		syscall.ENOENT:    "ENOENT",
		syscall.ELOOP:     "ELOOP",
		syscall.EAGAIN:    "EAGAIN",
		syscall.ENOTSUP:   "ENOTSUP",
		syscall.ENOTEMPTY: "ENOTEMPTY",
	}
	for errno, want := range cases {
		if got, ok := ErrnoName(errno); !ok || got != want {
			t.Errorf("ErrnoName(%d) = %q, %v; want %q", errno, got, ok, want)
		}
	}
	if got, ok := ErrnoName(syscall.Errno(250)); ok {
		t.Errorf("ErrnoName(250) = %q; want none", got)
	}
}

// Every name a platform defines is one Node knows, so none is silently dropped.
func TestValuesHaveNodeNames(t *testing.T) {
	if len(errnos) != len(errnoValues) {
		t.Errorf("%d errno values but only %d have a Node name", len(errnoValues), len(errnos))
	}
	if len(signals) != len(signalValues) {
		t.Errorf("%d signal values but only %d have a Node name", len(signalValues), len(signals))
	}
	if s, ok := Signal("SIGUSR1"); !ok || s != syscall.SIGUSR1 {
		t.Errorf("Signal(SIGUSR1) = %d, %v", s, ok)
	}
}
