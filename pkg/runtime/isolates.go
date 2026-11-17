package runtime

import (
	"errors"
	"sync"
)

// ErrWorkerLimit reports that starting a worker or loader realm would go over
// Config.MaxWorkers. Scripts see it as Node's ERR_WORKER_INIT_FAILED.
var ErrWorkerLimit = errors.New("Worker limit reached")

// isolateLimit counts the workers and loader realms open at once across a runtime and
// every runtime it starts, which share one (see ShareIsolateLimit).
type isolateLimit struct {
	mu   sync.Mutex
	open int
	max  int // negative: no limit
}

// StartIsolate takes one of Config.MaxWorkers, for a worker or loader realm about to be
// created. It fails with ErrWorkerLimit when none is left. The caller calls release once
// that runtime has been disposed; calling it more than once is harmless.
func (rt *Runtime) StartIsolate() (release func(), err error) {
	l := rt.isolates
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.max >= 0 && l.open >= l.max {
		return nil, ErrWorkerLimit
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

// ShareIsolateLimit makes rt count the workers and loader realms it starts against
// parent's Config.MaxWorkers, as a worker or loader realm parent starts does, so nested
// workers can't go past the cap.
func (rt *Runtime) ShareIsolateLimit(parent *Runtime) {
	rt.isolates = parent.isolates
}
