// Package runtime provides the JavaScript runtime environment.
package runtime

import (
	"context"
	"io"
	"sync"
	"time"
)

// ResourceTracker tracks open resources (files, sockets, etc.) for cleanup.
type ResourceTracker struct {
	mu        sync.Mutex
	resources map[uint64]io.Closer
	nextID    uint64
	closed    bool
}

// NewResourceTracker creates a new resource tracker.
func NewResourceTracker() *ResourceTracker {
	return &ResourceTracker{
		resources: make(map[uint64]io.Closer),
	}
}

// Track adds a resource to be tracked and returns its ID.
func (rt *ResourceTracker) Track(resource io.Closer) uint64 {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.closed {
		// If already closed, close the resource immediately
		resource.Close()
		return 0
	}

	rt.nextID++
	id := rt.nextID
	rt.resources[id] = resource
	return id
}

// Untrack removes a resource from tracking (e.g., when closed normally).
func (rt *ResourceTracker) Untrack(id uint64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.resources, id)
}

// CloseAll closes all tracked resources.
func (rt *ResourceTracker) CloseAll() []error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.closed = true
	var errors []error

	for id, resource := range rt.resources {
		if err := resource.Close(); err != nil {
			errors = append(errors, err)
		}
		delete(rt.resources, id)
	}

	return errors
}

// Count returns the number of tracked resources.
func (rt *ResourceTracker) Count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.resources)
}

// IsClosed returns true if the tracker has been closed.
func (rt *ResourceTracker) IsClosed() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.closed
}

// ExecutionController manages execution timeouts and kill switches.
type ExecutionController struct {
	ctx        context.Context
	cancel     context.CancelFunc
	timeout    time.Duration
	killed     bool
	killReason string
	mu         sync.Mutex
	onKill     []func()
}

// NewExecutionController creates a new execution controller.
// If timeout is 0, no timeout is set.
func NewExecutionController(timeout time.Duration) *ExecutionController {
	var ctx context.Context
	var cancel context.CancelFunc

	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	ec := &ExecutionController{
		ctx:     ctx,
		cancel:  cancel,
		timeout: timeout,
		onKill:  make([]func(), 0),
	}

	return ec
}

// Context returns the execution context.
func (ec *ExecutionController) Context() context.Context {
	return ec.ctx
}

// Kill immediately stops execution with the given reason.
func (ec *ExecutionController) Kill(reason string) {
	ec.mu.Lock()
	if ec.killed {
		ec.mu.Unlock()
		return
	}
	ec.killed = true
	ec.killReason = reason
	callbacks := make([]func(), len(ec.onKill))
	copy(callbacks, ec.onKill)
	ec.mu.Unlock()

	// Cancel the context
	ec.cancel()

	// Call all kill callbacks
	for _, fn := range callbacks {
		fn()
	}
}

// IsKilled returns true if Kill has been called.
func (ec *ExecutionController) IsKilled() bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return ec.killed
}

// KillReason returns the reason for killing, if killed.
func (ec *ExecutionController) KillReason() string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return ec.killReason
}

// IsDone returns true if the execution should stop (killed or timed out).
func (ec *ExecutionController) IsDone() bool {
	select {
	case <-ec.ctx.Done():
		return true
	default:
		return false
	}
}

// Done returns a channel that's closed when execution should stop.
func (ec *ExecutionController) Done() <-chan struct{} {
	return ec.ctx.Done()
}

// Err returns the context error (context.Canceled or context.DeadlineExceeded).
func (ec *ExecutionController) Err() error {
	return ec.ctx.Err()
}

// OnKill registers a callback to be called when Kill is invoked.
func (ec *ExecutionController) OnKill(fn func()) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.onKill = append(ec.onKill, fn)
}

// CheckTimeout checks if a timeout has occurred and returns an appropriate error.
func (ec *ExecutionController) CheckTimeout() error {
	select {
	case <-ec.ctx.Done():
		if ec.killed {
			return &KillError{Reason: ec.killReason}
		}
		if ec.ctx.Err() == context.DeadlineExceeded {
			return &TimeoutError{Timeout: ec.timeout}
		}
		return ec.ctx.Err()
	default:
		return nil
	}
}

// KillError is returned when execution is killed.
type KillError struct {
	Reason string
}

// Error implements the error interface.
func (e *KillError) Error() string {
	if e.Reason != "" {
		return "execution killed: " + e.Reason
	}
	return "execution killed"
}

// TimeoutError is returned when execution times out.
type TimeoutError struct {
	Timeout time.Duration
}

// Error implements the error interface.
func (e *TimeoutError) Error() string {
	return "execution timed out after " + e.Timeout.String()
}
