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
		p := newThreadPool(size)
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
		if got := newThreadPool(size).size; got != DefaultThreadPoolSize {
			t.Errorf("newThreadPool(%d).size = %d, want %d", size, got, DefaultThreadPoolSize)
		}
	}
}
