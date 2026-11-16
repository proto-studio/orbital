package runtime

import "sync"

// DefaultThreadPoolSize is how many tasks the thread pool runs at once when
// Config.ThreadPoolSize is 0: four, the default size of Node's libuv
// threadpool.
const DefaultThreadPoolSize = 4

// threadPool runs queued work on at most size goroutines, as Node runs the
// callback forms of its crypto functions on libuv's threadpool. Work beyond
// that waits in the queue, in order, until a worker is free. Workers start
// as work arrives and exit when the queue is empty.
type threadPool struct {
	mu      sync.Mutex
	size    int
	running int
	queue   []func()
}

func newThreadPool(size int) *threadPool {
	if size <= 0 {
		size = DefaultThreadPoolSize
	}
	return &threadPool{size: size}
}

// submit queues work, starting a worker if fewer than size are running.
func (p *threadPool) submit(work func()) {
	p.mu.Lock()
	p.queue = append(p.queue, work)
	start := p.running < p.size
	if start {
		p.running++
	}
	p.mu.Unlock()
	if start {
		go p.worker()
	}
}

func (p *threadPool) worker() {
	for {
		p.mu.Lock()
		if len(p.queue) == 0 {
			p.running--
			p.mu.Unlock()
			return
		}
		work := p.queue[0]
		p.queue[0] = nil
		p.queue = p.queue[1:]
		p.mu.Unlock()
		work()
	}
}

// RunInThreadPool runs work on the runtime's thread pool: at most
// Config.ThreadPoolSize tasks run at once, and the rest wait their turn. It
// returns at once; work runs later on another goroutine. Callers that hand a
// result back to JavaScript keep the event loop alive with AddPendingWork
// until they have.
func (r *Runtime) RunInThreadPool(work func()) {
	r.threadPool.submit(work)
}
