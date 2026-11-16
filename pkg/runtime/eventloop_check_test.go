package runtime

import (
	"context"
	"fmt"
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

// When RunUntil's predicate is met by an immediate, the rest of its batch runs next time.
func TestRunUntil_KeepsTheRestOfTheBatch(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() { ran = append(ran, "a") })
	el.SetImmediate(func() { ran = append(ran, "b") })
	el.SetImmediate(func() { ran = append(ran, "c") })
	el.AddPendingWork() // keeps the loop alive while RunUntil returns early
	el.RunUntil(func() bool { return len(ran) == 1 })
	el.DonePendingWork()
	el.Run()
	if got := fmt.Sprint(ran); got != "[a b c]" {
		t.Fatalf("ran %s, want [a b c]", got)
	}
}

// RunOnce runs the ticks an immediate queued before the next immediate, as Run does.
func TestRunOnce_TicksBetweenImmediates(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() {
		ran = append(ran, "a")
		el.EnqueueTick(func() { ran = append(ran, "tick") })
	})
	el.SetImmediate(func() { ran = append(ran, "b") })
	el.RunOnce()
	if got := fmt.Sprint(ran); got != "[a tick b]" {
		t.Fatalf("ran %s, want [a tick b]", got)
	}
}

// An immediate that cancels everything stops the rest of its own batch too.
func TestCancelAllTimers_StopsTheRunningBatch(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() { ran = append(ran, "a"); el.CancelAllTimers() })
	el.SetImmediate(func() { ran = append(ran, "b") })
	el.Run()
	if got := fmt.Sprint(ran); got != "[a]" {
		t.Fatalf("ran %s, want [a]", got)
	}
}

// A completion queued before the turn starts keeps the loop alive through the turn, so
// what its callback schedules runs, as libuv counts a request until its callback ran.
func TestRun_QueuedCompletionKeepsTheTurnAlive(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.EnqueueMicrotask(func() {
		ran = append(ran, "io")
		el.SetImmediate(func() { ran = append(ran, "immediate") })
	})
	el.Run()
	if got := fmt.Sprint(ran); got != "[io immediate]" {
		t.Fatalf("ran %s, want [io immediate]", got)
	}
}

// An immediate that stops the loop leaves the rest of its batch for the next Run.
func TestRun_StopKeepsTheRestOfTheBatch(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() { ran = append(ran, "a"); el.Stop() })
	el.SetImmediate(func() { ran = append(ran, "b") })
	el.Run()
	el.SetContext(context.Background())
	el.Run()
	if got := fmt.Sprint(ran); got != "[a b]" {
		t.Fatalf("ran %s, want [a b]", got)
	}
}

// A Run stopped from a callback leaves nothing behind that stops or cancels a later Run:
// the goroutine that watched its context can still be running after it returns.
func TestRun_StopDoesNotReachTheNextRun(t *testing.T) {
	for i := 0; i < 500; i++ {
		el := NewEventLoop()
		el.SetImmediate(func() { el.Stop() })
		el.Run()
		el.SetContext(context.Background())
		ran := false
		el.SetImmediate(func() { ran = true })
		el.Run()
		if !ran {
			t.Fatalf("run %d: the second Run stopped before its immediate", i)
		}
	}
}

// A tick that panics leaves the loop's lock as it found it, so an embedder's recover gets
// the panic rather than the runtime failing on the lock.
func TestRunTicks_PanicKeepsTheLock(t *testing.T) {
	el := NewEventLoop()
	el.SetTimeout(func() { el.EnqueueTick(func() { panic("tick") }) }, 0)
	func() {
		defer func() {
			if r := recover(); r != "tick" {
				t.Fatalf("recovered %v, want the tick's panic", r)
			}
		}()
		el.Run()
	}()
	// The lock is usable afterwards.
	el.EnqueueTick(func() {})
}

// An immediate that panics leaves the rest of its batch for the next Run, for an embedder
// that recovers.
func TestRun_PanicKeepsTheRestOfTheBatch(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() { ran = append(ran, "a"); panic("a") })
	el.SetImmediate(func() { ran = append(ran, "b") })
	func() {
		defer func() { _ = recover() }()
		el.Run()
	}()
	el.Run()
	if got := fmt.Sprint(ran); got != "[a b]" {
		t.Fatalf("ran %s, want [a b]", got)
	}
}

// The same holds for RunOnce.
func TestRunOnce_PanicKeepsTheRestOfTheBatch(t *testing.T) {
	el := NewEventLoop()
	var ran []string
	el.SetImmediate(func() { ran = append(ran, "a"); panic("a") })
	el.SetImmediate(func() { ran = append(ran, "b") })
	func() {
		defer func() { _ = recover() }()
		el.RunOnce()
	}()
	el.RunOnce()
	if got := fmt.Sprint(ran); got != "[a b]" {
		t.Fatalf("ran %s, want [a b]", got)
	}
}
