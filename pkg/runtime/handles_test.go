package runtime

import (
	"errors"
	"syscall"
	"testing"
)

func TestHandleLimit_CountsAgainstTheCap(t *testing.T) {
	l := &handleLimit{max: 2}
	r1, err := l.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.acquire(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.acquire(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("third acquire = %v, want EMFILE", err)
	}
	r1()
	r1() // a second release gives nothing more back
	if _, err := l.acquire(); err != nil {
		t.Fatalf("acquire after a release = %v", err)
	}
	if _, err := l.acquire(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("acquire past the cap after a double release = %v, want EMFILE", err)
	}
}

func TestHandleLimit_ZeroAllowsNoneAndNegativeAllowsAny(t *testing.T) {
	if _, err := (&handleLimit{max: 0}).acquire(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("cap 0: %v, want EMFILE", err)
	}
	unlimited := &handleLimit{max: -1}
	for i := 0; i < 1000; i++ {
		if _, err := unlimited.acquire(); err != nil {
			t.Fatalf("no cap: acquire %d = %v", i, err)
		}
	}
}

// A runtime sharing another's limits counts against the same caps.
func TestShareHandleLimits(t *testing.T) {
	parent, err := New(&Config{MaxOpenSockets: 1, MaxOpenFiles: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Dispose()
	child, err := New(&Config{MaxOpenSockets: 5, MaxOpenFiles: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Dispose()
	child.ShareHandleLimits(parent)
	if _, err := parent.OpenSocket(); err != nil {
		t.Fatal(err)
	}
	if _, err := child.OpenSocket(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("child socket = %v, want EMFILE from the parent's cap", err)
	}
	if _, err := child.OpenFile(); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.OpenFile(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("parent file = %v, want EMFILE", err)
	}
}
