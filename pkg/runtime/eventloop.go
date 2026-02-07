package runtime

import (
	"container/heap"
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a unit of work to be executed by the event loop.
type Task struct {
	id        uint64
	fn        func()
	scheduled time.Time
	interval  time.Duration // 0 for one-shot timers
	cancelled atomic.Bool
	index     int // heap index
}

// Cancel cancels this task.
func (t *Task) Cancel() {
	t.cancelled.Store(true)
}

// taskHeap implements heap.Interface for tasks ordered by scheduled time.
type taskHeap []*Task

// Len returns the number of tasks in the heap.
func (h taskHeap) Len() int { return len(h) }

// Less returns true if task i should be executed before task j.
func (h taskHeap) Less(i, j int) bool { return h[i].scheduled.Before(h[j].scheduled) }

// Swap swaps two tasks in the heap.
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

// Push adds a task to the heap.
func (h *taskHeap) Push(x interface{}) {
	n := len(*h)
	task := x.(*Task)
	task.index = n
	*h = append(*h, task)
}

// Pop removes and returns the task with the earliest scheduled time.
func (h *taskHeap) Pop() interface{} {
	old := *h
	n := len(old)
	task := old[n-1]
	old[n-1] = nil
	task.index = -1
	*h = old[0 : n-1]
	return task
}

// EventLoop manages async operations in the runtime.
type EventLoop struct {
	mu          sync.Mutex
	cond        *sync.Cond
	timers      taskHeap
	microtasks  []func()
	nextID      uint64
	running     bool
	shouldStop  bool
	pendingWork int32 // atomic counter for pending async work
	ctx         context.Context
	cancelCtx   context.CancelFunc
}

// NewEventLoop creates a new event loop.
func NewEventLoop() *EventLoop {
	ctx, cancel := context.WithCancel(context.Background())
	el := &EventLoop{
		timers:    make(taskHeap, 0),
		ctx:       ctx,
		cancelCtx: cancel,
	}
	el.cond = sync.NewCond(&el.mu)
	heap.Init(&el.timers)
	return el
}

// NewEventLoopWithContext creates a new event loop with a parent context.
// The event loop will stop when the context is cancelled.
func NewEventLoopWithContext(ctx context.Context) *EventLoop {
	childCtx, cancel := context.WithCancel(ctx)
	el := &EventLoop{
		timers:    make(taskHeap, 0),
		ctx:       childCtx,
		cancelCtx: cancel,
	}
	el.cond = sync.NewCond(&el.mu)
	heap.Init(&el.timers)
	return el
}

// SetContext sets the context for the event loop.
// If the context is cancelled, the event loop will stop.
func (el *EventLoop) SetContext(ctx context.Context) {
	el.mu.Lock()
	defer el.mu.Unlock()
	
	// Cancel old context
	if el.cancelCtx != nil {
		el.cancelCtx()
	}
	
	el.ctx, el.cancelCtx = context.WithCancel(ctx)
}

// SetTimeout schedules a function to run after the specified delay.
func (el *EventLoop) SetTimeout(fn func(), delay time.Duration) *Task {
	return el.scheduleTimer(fn, delay, 0)
}

// SetInterval schedules a function to run repeatedly at the specified interval.
func (el *EventLoop) SetInterval(fn func(), interval time.Duration) *Task {
	return el.scheduleTimer(fn, interval, interval)
}

// SetImmediate schedules a function to run as soon as possible.
func (el *EventLoop) SetImmediate(fn func()) *Task {
	return el.scheduleTimer(fn, 0, 0)
}

// scheduleTimer adds a timer task to the event loop.
func (el *EventLoop) scheduleTimer(fn func(), delay, interval time.Duration) *Task {
	el.mu.Lock()
	defer el.mu.Unlock()

	el.nextID++
	task := &Task{
		id:        el.nextID,
		fn:        fn,
		scheduled: time.Now().Add(delay),
		interval:  interval,
	}

	heap.Push(&el.timers, task)
	el.cond.Signal()

	return task
}

// ClearTimer cancels a scheduled timer.
func (el *EventLoop) ClearTimer(task *Task) {
	if task != nil {
		task.Cancel()
	}
}

// EnqueueMicrotask adds a microtask to be executed before the next task.
func (el *EventLoop) EnqueueMicrotask(fn func()) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.microtasks = append(el.microtasks, fn)
	el.cond.Signal()
}

// AddPendingWork increments the pending work counter.
// Call this when starting an async operation.
func (el *EventLoop) AddPendingWork() {
	atomic.AddInt32(&el.pendingWork, 1)
}

// DonePendingWork decrements the pending work counter.
// Call this when an async operation completes.
func (el *EventLoop) DonePendingWork() {
	atomic.AddInt32(&el.pendingWork, -1)
	el.cond.Signal()
}

// Run starts the event loop and blocks until there's no more work.
func (el *EventLoop) Run() {
	el.mu.Lock()
	el.running = true
	el.shouldStop = false
	ctx := el.ctx
	el.mu.Unlock()

	// Start a goroutine to watch for context cancellation
	go func() {
		<-ctx.Done()
		el.Stop()
	}()

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			el.mu.Lock()
			el.running = false
			el.mu.Unlock()
			return
		default:
		}

		el.mu.Lock()

		// Process all microtasks first
		for len(el.microtasks) > 0 {
			microtasks := el.microtasks
			el.microtasks = nil
			el.mu.Unlock()

			for _, fn := range microtasks {
				// Check for context cancellation before each microtask
				select {
				case <-ctx.Done():
					el.mu.Lock()
					el.running = false
					el.mu.Unlock()
					return
				default:
				}
				fn()
			}

			el.mu.Lock()
		}

		// Check if we should stop
		if el.shouldStop {
			el.running = false
			el.mu.Unlock()
			return
		}

		// Check if there's any work left
		hasTimers := el.timers.Len() > 0
		hasPending := atomic.LoadInt32(&el.pendingWork) > 0

		if !hasTimers && !hasPending {
			el.running = false
			el.mu.Unlock()
			return
		}

		// Get the next timer
		var waitDuration time.Duration
		if hasTimers {
			nextTask := el.timers[0]
			waitDuration = time.Until(nextTask.scheduled)

			if waitDuration <= 0 {
				// Timer is ready, execute it
				task := heap.Pop(&el.timers).(*Task)
				el.mu.Unlock()

				if !task.cancelled.Load() {
					// Check for context cancellation before executing
					select {
					case <-ctx.Done():
						el.mu.Lock()
						el.running = false
						el.mu.Unlock()
						return
					default:
					}

					task.fn()

					// Reschedule if it's an interval timer
					if task.interval > 0 && !task.cancelled.Load() {
						el.mu.Lock()
						task.scheduled = time.Now().Add(task.interval)
						heap.Push(&el.timers, task)
						el.mu.Unlock()
					}
				}
				continue
			}
		}

		// Wait for the next timer or a signal
		if hasTimers {
			// Wait with timeout, but also check context
			done := make(chan struct{})
			go func() {
				select {
				case <-time.After(waitDuration):
				case <-ctx.Done():
				}
				el.cond.Signal()
				close(done)
			}()
			el.cond.Wait()
			el.mu.Unlock()
			<-done
		} else {
			// Wait for a signal, but also check context
			go func() {
				<-ctx.Done()
				el.cond.Signal()
			}()
			el.cond.Wait()
			el.mu.Unlock()
		}
	}
}

// RunOnce processes one iteration of the event loop.
func (el *EventLoop) RunOnce() bool {
	el.mu.Lock()
	defer el.mu.Unlock()

	// Process microtasks
	for len(el.microtasks) > 0 {
		microtasks := el.microtasks
		el.microtasks = nil
		el.mu.Unlock()

		for _, fn := range microtasks {
			fn()
		}

		el.mu.Lock()
	}

	// Process one ready timer
	if el.timers.Len() > 0 {
		nextTask := el.timers[0]
		if time.Now().After(nextTask.scheduled) || time.Now().Equal(nextTask.scheduled) {
			task := heap.Pop(&el.timers).(*Task)
			el.mu.Unlock()

			if !task.cancelled.Load() {
				task.fn()

				if task.interval > 0 && !task.cancelled.Load() {
					el.mu.Lock()
					task.scheduled = time.Now().Add(task.interval)
					heap.Push(&el.timers, task)
					return true
				}
			}
			return true
		}
	}

	return el.timers.Len() > 0 || atomic.LoadInt32(&el.pendingWork) > 0
}

// Stop signals the event loop to stop.
func (el *EventLoop) Stop() {
	el.mu.Lock()
	el.shouldStop = true
	if el.cancelCtx != nil {
		el.cancelCtx()
	}
	el.cond.Signal()
	el.mu.Unlock()
}

// CancelAllTimers cancels all pending timers.
func (el *EventLoop) CancelAllTimers() {
	el.mu.Lock()
	defer el.mu.Unlock()
	
	for el.timers.Len() > 0 {
		task := heap.Pop(&el.timers).(*Task)
		task.Cancel()
	}
}

// ClearAllMicrotasks clears all pending microtasks.
func (el *EventLoop) ClearAllMicrotasks() {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.microtasks = nil
}

// HasPendingWork returns true if there's still work to be done.
func (el *EventLoop) HasPendingWork() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.timers.Len() > 0 || len(el.microtasks) > 0 || atomic.LoadInt32(&el.pendingWork) > 0
}
