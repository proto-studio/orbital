package runtime

import "sync"

// DefaultThreadPoolSize is how many tasks the thread pool runs at once when
// Config.ThreadPoolSize is 0: four, the default size of Node's libuv
// threadpool.
const DefaultThreadPoolSize = 4

// threadPool runs queued work on at most size goroutines, as Node runs the
// callback forms of its crypto functions on libuv's threadpool. Work beyond
// that waits in the queue, in order, until a worker is free. Workers start
// as work arrives and exit when the queue is empty. Once done is closed (the
// runtime was killed, timed out or disposed), work not yet started is
// dropped: nothing would receive its result.
type threadPool struct {
	mu      sync.Mutex
	size    int
	running int
	queue   []func()
	done    <-chan struct{}
}

func newThreadPool(size int, done <-chan struct{}) *threadPool {
	if size <= 0 {
		size = DefaultThreadPoolSize
	}
	return &threadPool{size: size, done: done}
}

func (p *threadPool) stopped() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// submit queues work, starting a worker if fewer than size are running.
func (p *threadPool) submit(work func()) {
	if p.stopped() {
		return
	}
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
		if p.stopped() {
			for i := range p.queue {
				p.queue[i] = nil
			}
			p.queue = nil
		}
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
// until they have. Once the runtime is killed, times out or is disposed,
// work that hasn't started is dropped, and new work isn't queued; work
// already running finishes.
func (r *Runtime) RunInThreadPool(work func()) {
	r.threadPool.submit(work)
}
