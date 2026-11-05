package runtime

import (
	"reflect"
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

func runsTicksFirst(t *testing.T, drain func(el *EventLoop)) {
	t.Helper()
	el := NewEventLoop()
	var got []string
	record := func(name string) func() {
		return func() { got = append(got, name) }
	}
	// The first completion was queued before the tick, and schedules one of its own, which
	// has to run before the completion behind it.
	el.EnqueueMicrotask(func() {
		got = append(got, "completion 1")
		el.EnqueueTick(record("tick 2"))
	})
	el.EnqueueMicrotask(record("completion 2"))
	el.EnqueueTick(func() {
		got = append(got, "tick 1a")
		el.EnqueueTick(record("tick 1c"))
	})
	el.EnqueueTick(record("tick 1b"))
	if !el.HasPendingWork() {
		t.Fatal("queued callbacks are not pending work")
	}

	drain(el)

	want := []string{"tick 1a", "tick 1b", "tick 1c", "completion 1", "tick 2", "completion 2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
	if el.HasPendingWork() {
		t.Error("work is still pending")
	}
}

func TestRun_TicksRunBeforeQueuedCompletions(t *testing.T) {
	runsTicksFirst(t, (*EventLoop).Run)
}

func TestRunOnce_TicksRunBeforeQueuedCompletions(t *testing.T) {
	runsTicksFirst(t, func(el *EventLoop) { el.RunOnce() })
}

func TestRun_StopsDuringEndlessTicks(t *testing.T) {
	el := NewEventLoop()
	var tick func()
	tick = func() { el.EnqueueTick(tick) }
	el.EnqueueTick(tick)

	done := make(chan struct{})
	go func() {
		el.Run()
		close(done)
	}()
	el.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after Stop")
	}
}

func TestClearAllMicrotasks_ClearsTicks(t *testing.T) {
	el := NewEventLoop()
	el.EnqueueTick(func() { t.Error("cleared tick ran") })
	el.EnqueueMicrotask(func() { t.Error("cleared microtask ran") })
	el.ClearAllMicrotasks()
	if el.HasPendingWork() {
		t.Error("work is still pending")
	}
	el.RunOnce()
}
