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
	unref     atomic.Bool
	index     int // heap index
}

// SetRef sets whether this task keeps the event loop running. An unreferenced task
// still fires if the loop is running for another reason.
func (t *Task) SetRef(ref bool) {
	t.unref.Store(!ref)
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
	immediates  []*Task  // setImmediate: run in the check phase, after I/O
	checkBatch  []*Task  // the immediates the check phase is running
	ticks       []func() // scheduled by running JS: run ahead of everything else
	microtasks  []func() // native completions: run once no tick is waiting
	nextID      uint64
	running     bool
	shouldStop  bool
	pendingWork int32 // atomic counter for pending async work
	ctx         context.Context
	cancelCtx   context.CancelFunc
	drained     func()      // runs once both queues empty; see setDrainedHook
	platform    func() bool // runs in each poll phase; see setPlatformHook
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

// setDrainedHook sets a function to run on the loop's goroutine each time the tick and
// microtask queues have emptied, before the loop looks for more work. Call it before the loop starts.
func (el *EventLoop) setDrainedHook(fn func()) {
	el.drained = fn
}

// setPlatformHook sets a function to run on the loop's goroutine at the start of each poll
// phase, after the timers phase has run its whole batch, and again before the loop decides
// it has no work left. It reports whether it ran anything. Call it before the loop starts.
func (el *EventLoop) setPlatformHook(fn func() bool) {
	el.platform = fn
}

// runDrainedLocked runs the drained hook, and reports whether that queued more callbacks.
// el.mu must be held. It is released while the hook runs.
func (el *EventLoop) runDrainedLocked() bool {
	if el.drained == nil {
		return false
	}
	el.mu.Unlock()
	el.drained()
	el.mu.Lock()
	return len(el.ticks) > 0 || len(el.microtasks) > 0
}

// hasMicrotasks reports whether any ticks or microtasks are queued.
func (el *EventLoop) hasMicrotasks() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return len(el.ticks) > 0 || len(el.microtasks) > 0
}

// SetTimeout schedules a function to run after the specified delay.
func (el *EventLoop) SetTimeout(fn func(), delay time.Duration) *Task {
	return el.scheduleTimer(fn, delay, 0)
}

// SetInterval schedules a function to run repeatedly at the specified interval.
func (el *EventLoop) SetInterval(fn func(), interval time.Duration) *Task {
	return el.scheduleTimer(fn, interval, interval)
}

// SetImmediate schedules a function for the loop's check phase, as Node's setImmediate
// does: after the I/O callbacks that are ready, and before timers that come due later. An
// immediate scheduled by another immediate waits for the next turn of the loop.
func (el *EventLoop) SetImmediate(fn func()) *Task {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.nextID++
	task := &Task{id: el.nextID, fn: fn, index: -1}
	el.immediates = append(el.immediates, task)
	el.cond.Signal()
	return task
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

// EnqueueTick adds a callback that runs before any queued microtask, even one queued
// earlier. The runtime uses it for process.nextTick callbacks that no callback into
// JavaScript ran on returning. Native operations finish on their own goroutines, so their
// completions can already be queued by the time the script that started them schedules a
// tick, and Node promises the tick runs first. Scripts rely on this to add a listener for
// an event that their operation will emit.
func (el *EventLoop) EnqueueTick(fn func()) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.ticks = append(el.ticks, fn)
	el.cond.Signal()
}

// nextQueuedLocked removes the callback to run next, or returns nil if none is queued.
func (el *EventLoop) nextQueuedLocked() func() {
	queue := &el.microtasks
	if len(el.ticks) > 0 {
		queue = &el.ticks
	}
	if len(*queue) == 0 {
		return nil
	}
	fn := (*queue)[0]
	(*queue)[0] = nil
	*queue = (*queue)[1:]
	return fn
}

// AddPendingWork increments the pending work counter.
// Call this when starting an async operation.
func (el *EventLoop) AddPendingWork() {
	atomic.AddInt32(&el.pendingWork, 1)
}

// DonePendingWork decrements the pending work counter.
// Call this when an async operation completes.
func (el *EventLoop) DonePendingWork() {
	// The lock closes the gap between the loop checking for work and parking. Without it
	// the loop can miss this wakeup, and sleep until a timer it no longer has to wait for.
	el.mu.Lock()
	defer el.mu.Unlock()
	atomic.AddInt32(&el.pendingWork, -1)
	el.cond.Signal()
}

// Run starts the event loop and blocks until there's no more work.
func (el *EventLoop) Run() {
	el.run(nil)
}

// RunUntil runs the event loop until done reports true, checked after each callback,
// or until there's no more work. It waits for timers and asynchronous I/O the way Run
// does, without spinning. It reports whether done became true.
func (el *EventLoop) RunUntil(done func() bool) bool {
	return el.run(done)
}

func (el *EventLoop) run(done func() bool) bool {
	el.mu.Lock()
	el.running = true
	el.shouldStop = false
	ctx := el.ctx
	el.mu.Unlock()

	// Start a goroutine to watch for context cancellation, for this call only. It only
	// wakes the loop, which then sees ctx is done: it can run after this call returns, when
	// a Stop would end the next Run instead, and cancel that Run's context.
	returned := make(chan struct{})
	defer close(returned)
	go func() {
		select {
		case <-ctx.Done():
			el.wake()
		case <-returned:
		}
	}()

	cancelled := func() bool {
		select {
		case <-ctx.Done():
			el.mu.Lock()
			el.running = false
			el.mu.Unlock()
			return true
		default:
			return false
		}
	}

	// finished reports whether RunUntil's predicate is satisfied; it is checked after each
	// callback.
	finished := func() bool {
		if done == nil || !done() {
			return false
		}
		el.mu.Lock()
		el.running = false
		el.mu.Unlock()
		return true
	}

	// What the code that ran before the loop queued comes first: its ticks, and then the
	// drained hook, which reports the promises it left rejected, as Node does once a
	// script has run.
	el.runTicks(ctx)
	if finished() {
		return true
	}
	// hooked records that the drained hook has run since the last callback, so the poll
	// phase needn't run it again when it finds nothing to do.
	hooked := true

	// Each turn follows libuv's phases as far as Node can tell them apart: timers that are
	// due, then I/O callbacks (the queued native completions), then immediates. Between
	// two timers or two immediates only process.nextTick callbacks run, as in Node.
	for {
		if cancelled() {
			return false
		}

		// As in libuv, a turn runs only while something keeps the loop alive; then it runs
		// all its phases, unreferenced timers and immediates included. Queued callbacks
		// and the drained hook still run below, and the loop ends after them.
		el.mu.Lock()
		alive := el.aliveLocked()
		el.mu.Unlock()

		// Timers phase: every timer due when the phase starts. Timers scheduled while it
		// runs wait for the next turn, even with no delay.
		now := time.Now()
		for alive {
			el.mu.Lock()
			for el.timers.Len() > 0 && el.timers[0].cancelled.Load() {
				heap.Pop(&el.timers)
			}
			if el.timers.Len() == 0 || el.timers[0].scheduled.After(now) || el.shouldStop {
				el.mu.Unlock()
				break
			}
			task := heap.Pop(&el.timers).(*Task)
			el.mu.Unlock()
			if cancelled() {
				return false
			}
			task.fn()
			// Reschedule if it's an interval timer
			if task.interval > 0 && !task.cancelled.Load() {
				el.mu.Lock()
				task.scheduled = time.Now().Add(task.interval)
				heap.Push(&el.timers, task)
				el.mu.Unlock()
			}
			el.runTicks(ctx)
			hooked = true
			if finished() {
				return true
			}
		}

		// Poll phase, without waiting: the tasks V8 has queued for the isolate, then the
		// I/O completions and ticks queued so far.
		if el.platform != nil {
			if cancelled() {
				return false
			}
			if el.platform() {
				hooked = false
			}
		}
		el.mu.Lock()
		for {
			for fn := el.nextQueuedLocked(); fn != nil; fn = el.nextQueuedLocked() {
				el.mu.Unlock()
				if cancelled() {
					return false
				}
				fn()
				hooked = false
				el.mu.Lock()
			}
			if hooked {
				break
			}
			hooked = true
			if !el.runDrainedLocked() {
				break
			}
			hooked = false
		}
		el.mu.Unlock()
		if finished() {
			return true
		}

		// Check if we should stop
		el.mu.Lock()
		if el.shouldStop {
			el.running = false
			el.mu.Unlock()
			return false
		}

		// Check phase: the immediates queued before it started.
		if alive && len(el.immediates) > 0 {
			batch := el.immediates
			el.immediates = nil
			// CancelAllTimers reaches the batch through this while it runs.
			el.checkBatch = batch
			el.mu.Unlock()
			if el.runCheckBatch(ctx, batch, func() bool {
				el.runTicks(ctx)
				hooked = true
				return finished()
			}) {
				return true
			}
			continue
		}

		// Cancelled timers are not work: they must not keep the loop alive until
		// their deadline. Once the earliest timer is live, one live timer exists.
		for el.timers.Len() > 0 && el.timers[0].cancelled.Load() {
			heap.Pop(&el.timers)
		}

		// Check if there's any work left. Unreferenced timers run while something else
		// keeps the loop alive, but do not count as work themselves.
		hasTimers := el.timers.Len() > 0
		hasPending := atomic.LoadInt32(&el.pendingWork) > 0

		if !hasPending && !el.aliveLocked() && len(el.ticks) == 0 {
			// A callback in this turn may have left work for the platform, such as
			// FinalizationRegistry cleanup; it runs before the loop ends, as in Node.
			if el.platform != nil {
				el.mu.Unlock()
				if el.platform() {
					continue
				}
				el.mu.Lock()
			}
			el.running = false
			el.mu.Unlock()
			return false
		}

		// Anything queued since the poll, or a timer already due, is handled by the next
		// turn without waiting.
		if len(el.ticks) > 0 || len(el.microtasks) > 0 || len(el.immediates) > 0 {
			el.mu.Unlock()
			continue
		}
		var waitDuration time.Duration
		if hasTimers {
			waitDuration = time.Until(el.timers[0].scheduled)
			if waitDuration <= 0 {
				el.mu.Unlock()
				continue
			}
		}

		// Wait for the next timer deadline or a wakeup signal. A new microtask,
		// timer, or async I/O completion enqueued from another goroutine calls
		// el.cond.Signal() to wake us. cond.Wait atomically releases el.mu while
		// parked and reacquires it on wake, and every enqueue path holds el.mu up
		// to its Signal, so wakeups raised just before we park are not lost.
		if hasTimers {
			// Bound the wait by the next timer deadline. Use a stoppable timer plus
			// a stop channel so an *early* wakeup (e.g. an inbound socket read/accept
			// completing) returns immediately.
			timer := time.NewTimer(waitDuration)
			stop := make(chan struct{})
			go func() {
				select {
				case <-timer.C:
					el.wake()
				case <-ctx.Done():
					el.wake()
				case <-stop:
				}
			}()
			el.cond.Wait()
			el.mu.Unlock()
			close(stop)
			timer.Stop()
		} else {
			// No timers pending: park until a signal (async I/O completion) or
			// context cancellation. The stop channel lets the watcher goroutine exit
			// promptly on wake instead of leaking until ctx is cancelled.
			stop := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					el.wake()
				case <-stop:
				}
			}()
			el.cond.Wait()
			el.mu.Unlock()
			close(stop)
		}
	}
}

// runCheckBatch runs a batch of immediates, calling after once each has run; it stops,
// reporting true, when after does. Whatever hasn't run when it stops, when the loop is
// stopped or cancelled, or when a callback panics goes back to the front of the queue, so
// a later Run runs it before anything queued since.
func (el *EventLoop) runCheckBatch(ctx context.Context, batch []*Task, after func() bool) bool {
	next := 0
	defer func() {
		el.mu.Lock()
		if next < len(batch) {
			el.immediates = append(append([]*Task(nil), batch[next:]...), el.immediates...)
		}
		el.checkBatch = nil
		el.mu.Unlock()
	}()
	for next < len(batch) {
		task := batch[next]
		if task.cancelled.Load() {
			next++
			continue
		}
		el.mu.Lock()
		stop := el.shouldStop || ctx.Err() != nil
		el.mu.Unlock()
		if stop {
			return false
		}
		next++
		task.fn()
		if after() {
			return true
		}
	}
	return false
}

// contextForTicks is the loop's context, for runTicks outside run.
func (el *EventLoop) contextForTicks() context.Context {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.ctx
}

// runTicks runs the queued process.nextTick callbacks, and then the drained hook, which
// reports rejections, as Node does after each timer and immediate. I/O completions stay
// queued for the poll phase. It stops early, reporting false, if the loop is stopped or
// ctx is cancelled.
func (el *EventLoop) runTicks(ctx context.Context) bool {
	el.mu.Lock()
	// The lock is released around each callback, and a callback can panic: only a lock
	// this function holds may be released on the way out.
	locked := true
	defer func() {
		if locked {
			el.mu.Unlock()
		}
	}()
	for {
		for len(el.ticks) > 0 {
			if el.shouldStop || ctx.Err() != nil {
				return false
			}
			fn := el.ticks[0]
			el.ticks[0] = nil
			el.ticks = el.ticks[1:]
			locked = false
			el.mu.Unlock()
			fn()
			el.mu.Lock()
			locked = true
		}
		if el.shouldStop || ctx.Err() != nil {
			return false
		}
		locked = false // runDrainedLocked releases the lock while the hook runs
		el.runDrainedLocked()
		locked = true
		if len(el.ticks) == 0 {
			return true
		}
	}
}

// wake signals a parked Run. Run holds el.mu until cond.Wait releases it, so taking the lock
// here means the signal cannot arrive before Run is parked, where it would be lost.
func (el *EventLoop) wake() {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.cond.Signal()
}

// RunOnce processes one iteration of the event loop.
func (el *EventLoop) RunOnce() bool {
	el.mu.Lock()

	// Process queued callbacks
	for fn := el.nextQueuedLocked(); fn != nil; fn = el.nextQueuedLocked() {
		el.mu.Unlock()
		fn()
		el.mu.Lock()
	}

	el.runDrainedLocked()

	ran := false

	// Process one ready timer
	if el.timers.Len() > 0 {
		nextTask := el.timers[0]
		if !time.Now().Before(nextTask.scheduled) {
			task := heap.Pop(&el.timers).(*Task)
			el.mu.Unlock()
			if !task.cancelled.Load() {
				task.fn()
				if task.interval > 0 && !task.cancelled.Load() {
					el.mu.Lock()
					task.scheduled = time.Now().Add(task.interval)
					heap.Push(&el.timers, task)
					el.mu.Unlock()
				}
				el.runTicks(el.contextForTicks())
			}
			el.mu.Lock()
			ran = true
		}
	}

	// Then the immediates queued so far, timer or not, so that a timer due on every call
	// can't hold them back.
	if len(el.immediates) > 0 {
		batch := el.immediates
		el.immediates = nil
		el.checkBatch = batch
		el.mu.Unlock()
		ctx := el.contextForTicks()
		el.runCheckBatch(ctx, batch, func() bool {
			// As between any two callbacks, the ticks the immediate queued run first.
			el.runTicks(ctx)
			return false
		})
		el.mu.Lock()
		ran = true
	}
	if ran {
		el.mu.Unlock()
		return true
	}

	hasWork := el.hasReferencedTimerLocked() || atomic.LoadInt32(&el.pendingWork) > 0
	el.mu.Unlock()
	return hasWork
}

// aliveLocked is libuv's uv_loop_alive: a referenced timer or immediate, pending I/O, or a
// completed operation whose callback is queued but hasn't run yet. el.mu must be held.
func (el *EventLoop) aliveLocked() bool {
	return el.hasReferencedTimerLocked() || atomic.LoadInt32(&el.pendingWork) > 0 || len(el.microtasks) > 0
}

// hasReferencedTimerLocked reports whether a live timer keeps the loop alive.
func (el *EventLoop) hasReferencedTimerLocked() bool {
	for _, task := range el.timers {
		if !task.cancelled.Load() && !task.unref.Load() {
			return true
		}
	}
	for _, task := range el.immediates {
		if !task.cancelled.Load() && !task.unref.Load() {
			return true
		}
	}
	return false
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
	for _, task := range el.immediates {
		task.Cancel()
	}
	el.immediates = nil
	for _, task := range el.checkBatch {
		task.Cancel()
	}
}

// ClearAllMicrotasks clears all pending microtasks.
func (el *EventLoop) ClearAllMicrotasks() {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.ticks = nil
	el.microtasks = nil
}

// isAlive reports whether anything that keeps a program running is left: see aliveLocked.
// Queued ticks don't count, since they run as part of whatever queued them.
func (el *EventLoop) isAlive() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.aliveLocked()
}

// HasPendingWork returns true if there's still work to be done.
func (el *EventLoop) HasPendingWork() bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.hasReferencedTimerLocked() || len(el.ticks) > 0 || len(el.microtasks) > 0 || atomic.LoadInt32(&el.pendingWork) > 0
}
