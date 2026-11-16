package runtime

import (
	"testing"
	"time"
)

// RunUntil checks its predicate after each timer, not only after the whole timers phase.
func TestRunUntil_ChecksBetweenTimers(t *testing.T) {
	el := NewEventLoop()
	ran := 0
	el.SetTimeout(func() { ran++ }, 0)
	el.SetTimeout(func() { ran++ }, 0)
	time.Sleep(2 * time.Millisecond)
	if !el.RunUntil(func() bool { return ran >= 1 }) {
		t.Fatal("RunUntil did not report its predicate")
	}
	if ran != 1 {
		t.Fatalf("%d timers ran, want 1", ran)
	}
}

// RunOnce runs the pending immediates even when a timer is ready on every call.
func TestRunOnce_ImmediatesRunBesideReadyTimers(t *testing.T) {
	el := NewEventLoop()
	ticks := el.SetInterval(func() {}, time.Nanosecond)
	defer ticks.Cancel()
	ran := false
	el.SetImmediate(func() { ran = true })
	for i := 0; i < 3 && !ran; i++ {
		time.Sleep(time.Millisecond)
		el.RunOnce()
	}
	if !ran {
		t.Fatal("the immediate never ran")
	}
}

// A tick that stops the loop stops the ticks queued after it, between timers too.
func TestRun_StopInTickBetweenTimers(t *testing.T) {
	el := NewEventLoop()
	ran := 0
	var tick func()
	tick = func() {
		ran++
		el.Stop()
		el.EnqueueTick(tick)
	}
	el.SetTimeout(func() { el.EnqueueTick(tick) }, 0)
	done := make(chan struct{})
	go func() {
		el.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept draining ticks after Stop")
	}
	if ran != 1 {
		t.Fatalf("%d ticks ran, want 1", ran)
	}
}
