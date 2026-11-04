package runtime

import (
	"context"
	"testing"
	"time"
)

// A timer that expires as the loop goes to sleep must still wake it. Nothing else wakes an
// otherwise idle loop, so a lost signal hangs it for good.
func TestEventLoop_ShortTimerWakesIdleLoop(t *testing.T) {
	el := NewEventLoop()
	el.SetContext(context.Background())

	const rounds = 200000
	left := rounds
	var next func()
	next = func() {
		left--
		if left > 0 {
			el.SetTimeout(next, time.Nanosecond)
		}
	}
	el.SetTimeout(next, time.Nanosecond)

	done := make(chan struct{})
	go func() {
		el.Run()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("event loop hung with %d of %d timers left", left, rounds)
	}
}
