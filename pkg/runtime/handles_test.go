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

// A runtime sharing another's limits counts against the same caps. Both are granted
// sockets and files, since the caps don't apply where access is closed.
func TestShareHandleLimits(t *testing.T) {
	granted := func(max int) *Config {
		return &Config{
			SocketFactory:  NewRealSocketFactory(),
			Filesystem:     NewMemoryFilesystem(),
			MaxOpenSockets: max,
			MaxOpenFiles:   max,
		}
	}
	parent, err := New(granted(1))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Dispose()
	child, err := New(granted(5))
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

// Closed access opens nothing, so a zero Config's caps don't turn its refusals into
// EMFILE.
func TestHandleLimits_OffWhereAccessIsClosed(t *testing.T) {
	rt, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := rt.OpenSocket(); err != nil {
		t.Fatalf("socket = %v, want no cap", err)
	}
	if _, err := rt.OpenFile(); err != nil {
		t.Fatalf("file = %v, want no cap", err)
	}
	granted, err := New(&Config{HTTPClient: NewRealHTTPClient(), Filesystem: NewMemoryFilesystem()})
	if err != nil {
		t.Fatal(err)
	}
	defer granted.Dispose()
	if _, err := granted.OpenSocket(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("socket with a real HTTP client = %v, want EMFILE", err)
	}
	if _, err := granted.OpenFile(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("file with a filesystem = %v, want EMFILE", err)
	}
}

// A closed parent's caps still bind a granted worker or realm that shares them: the
// parent skips counting only its own (refused) opens.
func TestShareHandleLimits_ClosedParentKeepsItsCaps(t *testing.T) {
	parent, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Dispose()
	child, err := New(&Config{
		SocketFactory:  NewRealSocketFactory(),
		Filesystem:     NewMemoryFilesystem(),
		MaxOpenSockets: -1,
		MaxOpenFiles:   -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Dispose()
	child.ShareHandleLimits(parent)
	if _, err := child.OpenSocket(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("child socket = %v, want EMFILE from the parent's cap of 0", err)
	}
	if _, err := child.OpenFile(); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("child file = %v, want EMFILE from the parent's cap of 0", err)
	}
}
