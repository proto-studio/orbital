package runtime

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The pool runs at most size tasks at once, starts no more than size
// goroutines however much work is queued, and runs every task.
func TestThreadPool_Bounded(t *testing.T) {
	for _, size := range []int{1, 3, DefaultThreadPoolSize} {
		p := newThreadPool(size, -1, nil)
		var running, peak, done int32
		var wg sync.WaitGroup
		release := make(chan struct{})
		const tasks = 200
		before := runtime.NumGoroutine()
		wg.Add(tasks)
		for i := 0; i < tasks; i++ {
			p.submit(func() {
				defer wg.Done()
				n := atomic.AddInt32(&running, 1)
				for {
					old := atomic.LoadInt32(&peak)
					if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
						break
					}
				}
				<-release
				atomic.AddInt32(&running, -1)
				atomic.AddInt32(&done, 1)
			})
		}
		// All the work is queued; only size workers may exist.
		time.Sleep(50 * time.Millisecond)
		if extra := runtime.NumGoroutine() - before; extra > size {
			t.Errorf("size %d: %d goroutines started for %d tasks", size, extra, tasks)
		}
		close(release)
		wg.Wait()
		if peak > int32(size) {
			t.Errorf("size %d: %d tasks ran at once", size, peak)
		}
		if done != tasks {
			t.Errorf("size %d: %d of %d tasks ran", size, done, tasks)
		}
	}
}

// A size of 0 or less means Node's default of 4.
func TestThreadPool_DefaultSize(t *testing.T) {
	for _, size := range []int{0, -1} {
		if got := newThreadPool(size, -1, nil).size; got != DefaultThreadPoolSize {
			t.Errorf("newThreadPool(%d).size = %d, want %d", size, got, DefaultThreadPoolSize)
		}
	}
}

// Once the runtime stops, work that hasn't started is dropped and new work
// isn't queued; the task already running finishes.
func TestThreadPool_DropsQueuedWorkWhenStopped(t *testing.T) {
	done := make(chan struct{})
	p := newThreadPool(1, -1, done)
	var ran int32
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	p.submit(func() {
		close(started)
		<-release
		atomic.AddInt32(&ran, 1)
		close(finished)
	})
	<-started
	for i := 0; i < 10; i++ {
		p.submit(func() { atomic.AddInt32(&ran, 1) })
	}
	close(done)
	p.submit(func() { atomic.AddInt32(&ran, 1) })
	close(release)
	<-finished
	// Let the worker look at the queue again before checking.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		idle := p.running == 0
		p.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if n := atomic.LoadInt32(&ran); n != 1 {
		t.Errorf("%d tasks ran, want only the one already running", n)
	}
}

// With a queue limit, submit waits for room instead of letting the queue grow, and every
// task still runs.
func TestThreadPool_QueueLimitWaitsForRoom(t *testing.T) {
	for _, maxQueue := range []int{0, 1, 5} {
		p := newThreadPool(2, maxQueue, nil)
		var peakQueue, ran int32
		const tasks = 50
		var wg sync.WaitGroup
		wg.Add(tasks)
		for i := 0; i < tasks; i++ {
			p.submit(func() {
				defer wg.Done()
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&ran, 1)
			})
			p.mu.Lock()
			if q := int32(len(p.queue)); q > atomic.LoadInt32(&peakQueue) {
				atomic.StoreInt32(&peakQueue, q)
			}
			p.mu.Unlock()
		}
		wg.Wait()
		if peakQueue > int32(maxQueue) {
			t.Errorf("maxQueue %d: %d tasks waited", maxQueue, peakQueue)
		}
		if ran != tasks {
			t.Errorf("maxQueue %d: %d of %d tasks ran", maxQueue, ran, tasks)
		}
	}
}

// A submit waiting for room gives up when the runtime stops, without running its work.
func TestThreadPool_WaitingSubmitStopsWithTheRuntime(t *testing.T) {
	done := make(chan struct{})
	p := newThreadPool(1, 0, done)
	release := make(chan struct{})
	p.submit(func() { <-release })
	defer close(release)

	var ran int32
	returned := make(chan struct{})
	go func() {
		p.submit(func() { atomic.AddInt32(&ran, 1) })
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("submit returned while the pool was full")
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("submit kept waiting after the runtime stopped")
	}
	if atomic.LoadInt32(&ran) != 0 {
		t.Error("work submitted after the runtime stopped ran")
	}
}
