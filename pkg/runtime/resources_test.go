package runtime

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// mockCloser is a test helper that implements io.Closer
type mockCloser struct {
	closed bool
	mu     sync.Mutex
}

func (m *mockCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockCloser) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func TestResourceTracker_TrackAndClose(t *testing.T) {
	tracker := NewResourceTracker()

	r1 := &mockCloser{}
	r2 := &mockCloser{}
	r3 := &mockCloser{}

	tracker.Track(r1)
	tracker.Track(r2)
	tracker.Track(r3)

	tracker.CloseAll()

	if !r1.IsClosed() {
		t.Error("r1 should be closed")
	}
	if !r2.IsClosed() {
		t.Error("r2 should be closed")
	}
	if !r3.IsClosed() {
		t.Error("r3 should be closed")
	}
}

func TestResourceTracker_Untrack(t *testing.T) {
	tracker := NewResourceTracker()

	r1 := &mockCloser{}
	r2 := &mockCloser{}

	id1 := tracker.Track(r1)
	tracker.Track(r2)

	// Untrack r1
	tracker.Untrack(id1)

	// Close all - only r2 should be closed
	tracker.CloseAll()

	if r1.IsClosed() {
		t.Error("r1 should not be closed (was untracked)")
	}
	if !r2.IsClosed() {
		t.Error("r2 should be closed")
	}
}

func TestResourceTracker_Count(t *testing.T) {
	tracker := NewResourceTracker()

	if tracker.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", tracker.Count())
	}

	r1 := &mockCloser{}
	r2 := &mockCloser{}

	tracker.Track(r1)
	if tracker.Count() != 1 {
		t.Errorf("Count should be 1, got %d", tracker.Count())
	}

	id2 := tracker.Track(r2)
	if tracker.Count() != 2 {
		t.Errorf("Count should be 2, got %d", tracker.Count())
	}

	tracker.Untrack(id2)
	if tracker.Count() != 1 {
		t.Errorf("Count should be 1 after untrack, got %d", tracker.Count())
	}

	tracker.CloseAll()
	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after CloseAll, got %d", tracker.Count())
	}
}

func TestResourceTracker_Concurrent(t *testing.T) {
	tracker := NewResourceTracker()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &mockCloser{}
			tracker.Track(r)
		}()
	}

	wg.Wait()

	if tracker.Count() != 100 {
		t.Errorf("Should have 100 tracked resources, got %d", tracker.Count())
	}

	tracker.CloseAll()

	if tracker.Count() != 0 {
		t.Errorf("Should have 0 tracked resources after CloseAll, got %d", tracker.Count())
	}
}

func TestExecutionController_Timeout(t *testing.T) {
	controller := NewExecutionController(100 * time.Millisecond)

	// Wait for context to be cancelled
	select {
	case <-controller.Context().Done():
		// Success - timeout occurred
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout should have occurred")
	}

	// Check that context is done
	if !controller.IsDone() {
		t.Error("Controller should be done after timeout")
	}
}

func TestExecutionController_Kill(t *testing.T) {
	controller := NewExecutionController(0) // No timeout

	// Kill immediately
	controller.Kill("test kill")

	// Context should be done
	select {
	case <-controller.Context().Done():
		// Success
	default:
		t.Error("Context should be done after kill")
	}

	if !controller.IsKilled() {
		t.Error("Controller should be marked as killed")
	}

	if controller.KillReason() != "test kill" {
		t.Errorf("Kill reason mismatch: got %q", controller.KillReason())
	}
}

func TestExecutionController_NotKilled(t *testing.T) {
	controller := NewExecutionController(0)

	if controller.IsKilled() {
		t.Error("Controller should not be killed initially")
	}

	if controller.KillReason() != "" {
		t.Error("Kill reason should be empty initially")
	}
}

func TestExecutionController_MultipleKills(t *testing.T) {
	controller := NewExecutionController(0)

	// First kill
	controller.Kill("first")

	// Second kill should be ignored
	controller.Kill("second")

	// Reason should be from first kill
	if controller.KillReason() != "first" {
		t.Errorf("Kill reason should be 'first', got %q", controller.KillReason())
	}
}

func TestExecutionController_OnKill(t *testing.T) {
	controller := NewExecutionController(0)

	var called bool
	controller.OnKill(func() {
		called = true
	})

	controller.Kill("test")

	if !called {
		t.Error("OnKill callback should have been called")
	}
}

func TestKillError(t *testing.T) {
	err := &KillError{Reason: "user requested"}

	expected := "execution killed: user requested"
	if err.Error() != expected {
		t.Errorf("Error message mismatch: got %q, want %q", err.Error(), expected)
	}
}

func TestKillError_NoReason(t *testing.T) {
	err := &KillError{}

	if err.Error() != "execution killed" {
		t.Errorf("Error message mismatch: got %q", err.Error())
	}
}

func TestTimeoutError(t *testing.T) {
	err := &TimeoutError{Timeout: 30 * time.Second}

	expected := "execution timed out after 30s"
	if err.Error() != expected {
		t.Errorf("Error message mismatch: got %q, want %q", err.Error(), expected)
	}
}

func TestResourceTracker_WithRealResources(t *testing.T) {
	tracker := NewResourceTracker()

	buf := bytes.NewBuffer(nil)
	readCloser := io.NopCloser(buf)

	tracker.Track(readCloser)

	if tracker.Count() != 1 {
		t.Errorf("Count should be 1, got %d", tracker.Count())
	}

	tracker.CloseAll()

	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after CloseAll, got %d", tracker.Count())
	}
}

func TestResourceTracker_IsClosed(t *testing.T) {
	tracker := NewResourceTracker()

	if tracker.IsClosed() {
		t.Error("New tracker should not be closed")
	}

	tracker.CloseAll()

	if !tracker.IsClosed() {
		t.Error("Tracker should be closed after CloseAll")
	}
}

func TestResourceTracker_TrackAfterClose(t *testing.T) {
	tracker := NewResourceTracker()
	tracker.CloseAll()

	r := &mockCloser{}
	tracker.Track(r)

	// Resource should be closed immediately when tracked after close
	if !r.IsClosed() {
		t.Error("Resource tracked after close should be closed immediately")
	}
}

func TestExecutionController_CheckTimeout(t *testing.T) {
	controller := NewExecutionController(50 * time.Millisecond)

	// Initially no error
	if err := controller.CheckTimeout(); err != nil {
		t.Errorf("Should not have error initially: %v", err)
	}

	// Wait for timeout
	time.Sleep(100 * time.Millisecond)

	err := controller.CheckTimeout()
	if err == nil {
		t.Error("Should have timeout error")
	}

	if _, ok := err.(*TimeoutError); !ok {
		t.Errorf("Should be TimeoutError, got %T", err)
	}
}
