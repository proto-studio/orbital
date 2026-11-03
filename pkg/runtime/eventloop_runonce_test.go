package runtime

import (
	"testing"
	"time"
)

// One-shot ready timer must not panic with unlock of unlocked mutex.
func TestRunOnce_OneShotTimerNoDoubleUnlock(t *testing.T) {
	el := NewEventLoop()
	done := make(chan struct{}, 1)
	el.SetTimeout(func() { done <- struct{}{} }, 0)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("one-shot timer did not fire")
		default:
			el.RunOnce()
		}
	}
}

func TestRunOnce_IntervalTimerNoDoubleUnlock(t *testing.T) {
	el := NewEventLoop()
	n := 0
	el.SetInterval(func() { n++ }, time.Millisecond)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("interval never ran")
		default:
			el.RunOnce()
			if n >= 2 {
				return
			}
		}
	}
}

func TestRunDoesNotWaitForCancelledTimers(t *testing.T) {
	el := NewEventLoop()
	task := el.SetTimeout(func() {}, time.Minute)
	el.ClearTimer(task)

	done := make(chan struct{})
	go func() {
		el.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		el.Stop()
		t.Fatal("Run waited for a cancelled timer")
	}
}
