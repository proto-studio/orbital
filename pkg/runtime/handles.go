package runtime

import (
	"sync"
	"syscall"
)

// handleLimit counts the open handles of one kind, sockets or files, against a cap. A
// runtime shares its counts with the workers and loader realms it starts (see
// ShareHandleLimits), since they all draw on the host process's file descriptors.
type handleLimit struct {
	mu   sync.Mutex
	open int
	max  int // negative: no limit
}

// acquire takes a slot, or fails with EMFILE, the error the OS gives at its own limit.
// The returned release gives the slot back; calling it more than once is harmless.
func (l *handleLimit) acquire() (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.max >= 0 && l.open >= l.max {
		return nil, syscall.EMFILE
	}
	l.open++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.open--
			l.mu.Unlock()
		})
	}, nil
}

// OpenSocket takes one of Config.MaxOpenSockets, for a socket a script opens: a TCP
// connection or listener, a UDP socket, or a request through the HTTP client. It fails
// with syscall.EMFILE when none is left. The caller calls release once the socket closes.
func (rt *Runtime) OpenSocket() (release func(), err error) {
	return rt.sockets.acquire()
}

// OpenFile takes one of Config.MaxOpenFiles, for a file a script opens through fs,
// including one a call such as readFile opens only briefly. It fails with syscall.EMFILE
// when none is left. The caller calls release once the file closes.
func (rt *Runtime) OpenFile() (release func(), err error) {
	return rt.files.acquire()
}

// ShareHandleLimits makes rt count its sockets and files against parent's caps, as a
// worker or loader realm parent starts does, so starting more runtimes doesn't multiply
// what scripts can open.
func (rt *Runtime) ShareHandleLimits(parent *Runtime) {
	rt.sockets = parent.sockets
	rt.files = parent.files
}
