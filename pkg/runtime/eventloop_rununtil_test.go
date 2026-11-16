package runtime

import (
	"testing"
	"time"
)

// RunUntil waits for a timer without spinning, stops as soon as done reports true,
// and reports false when the loop runs out of work first.
func TestEventLoopRunUntil(t *testing.T) {
	el := NewEventLoop()
	fired := false
	// Timed from before the timer is set, so it can't look early.
	start := time.Now()
	el.SetTimeout(func() { fired = true }, 50*time.Millisecond)
	later := el.SetTimeout(func() {}, time.Hour)
	if !el.RunUntil(func() bool { return fired }) {
		t.Fatal("RunUntil reported false after the timer fired")
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("returned after %v", elapsed)
	}
	later.Cancel()
	if el.RunUntil(func() bool { return false }) {
		t.Fatal("RunUntil reported true with nothing left to run")
	}
}
