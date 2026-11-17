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
//
// At most maxQueue tasks wait (no limit when it's negative). Each holds its
// inputs, so a full queue makes submit wait for a slot rather than grow.
type threadPool struct {
	mu       sync.Mutex
	size     int
	maxQueue int
	running  int
	queue    []func()
	done     <-chan struct{}
	// space is signaled when a task leaves the queue or a worker exits, and
	// broadcast when done closes, for a submit waiting for room.
	space       *sync.Cond
	watchedDone bool
}

func newThreadPool(size, maxQueue int, done <-chan struct{}) *threadPool {
	if size <= 0 {
		size = DefaultThreadPoolSize
	}
	p := &threadPool{size: size, maxQueue: maxQueue, done: done}
	p.space = sync.NewCond(&p.mu)
	return p
}

// full reports whether new work would have to wait for room. The caller holds mu.
func (p *threadPool) full() bool {
	return p.maxQueue >= 0 && p.running >= p.size && len(p.queue) >= p.maxQueue
}

func (p *threadPool) stopped() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// submit queues work, starting a worker if fewer than size are running. When
// the queue is full it waits until a task leaves it, or the pool stops, in
// which case the work is dropped like any other that hasn't started.
func (p *threadPool) submit(work func()) {
	if p.stopped() {
		return
	}
	p.mu.Lock()
	for p.full() {
		if !p.watchedDone {
			// Wake waiting submits when the runtime stops.
			p.watchedDone = true
			go func() {
				<-p.done
				p.mu.Lock()
				p.space.Broadcast()
				p.mu.Unlock()
			}()
		}
		if p.stopped() {
			p.mu.Unlock()
			return
		}
		p.space.Wait()
	}
	if p.stopped() {
		p.mu.Unlock()
		return
	}
	// A free slot takes the work directly, so the queue only holds work that
	// waits. Workers only exit with the queue empty, so a free slot means
	// nothing is waiting ahead of this work.
	if p.running < p.size {
		p.running++
		p.mu.Unlock()
		go p.worker(work)
		return
	}
	p.queue = append(p.queue, work)
	p.mu.Unlock()
}

// worker runs first, unless the runtime stopped before the worker began, then queued
// work until the queue is empty.
func (p *threadPool) worker(first func()) {
	if !p.stopped() {
		first()
	}
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
			p.space.Signal()
			p.mu.Unlock()
			return
		}
		work := p.queue[0]
		p.queue[0] = nil
		p.queue = p.queue[1:]
		p.space.Signal()
		p.mu.Unlock()
		work()
	}
}

// RunInThreadPool runs work on the runtime's thread pool: at most
// Config.ThreadPoolSize tasks run at once, and up to Config.ThreadPoolQueueSize
// more wait their turn. It returns once the work is queued, which, when the
// queue is full, is after a task has left it; work runs later on another
// goroutine. Callers that hand a
// result back to JavaScript keep the event loop alive with AddPendingWork
// until they have. Once the runtime is killed, times out or is disposed,
// work that hasn't started is dropped, and new work isn't queued; work
// already running finishes.
func (r *Runtime) RunInThreadPool(work func()) {
	r.threadPool.submit(work)
}
