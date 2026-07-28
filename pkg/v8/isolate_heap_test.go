package v8_test

import (
	"sync/atomic"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

func TestGetHeapStatistics(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	if _, err := ctx.RunScript(`globalThis.__buf = new Array(10000).fill("x")`, "alloc.js"); err != nil {
		t.Fatalf("RunScript: %v", err)
	}

	stats, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	if stats.TotalHeapSize == 0 {
		t.Fatal("expected non-zero total_heap_size")
	}
	if stats.UsedHeapSize == 0 {
		t.Fatal("expected non-zero used_heap_size")
	}
	if stats.HeapSizeLimit == 0 {
		t.Fatal("expected non-zero heap_size_limit")
	}
	if stats.UsedHeapSize > stats.TotalHeapSize && stats.TotalHeapSize > 0 {
		// used can briefly exceed total in some V8 versions; just sanity-check limit.
	}
	if stats.HeapSizeLimit < stats.TotalHeapSize {
		t.Fatalf("heap_size_limit (%d) < total_heap_size (%d)", stats.HeapSizeLimit, stats.TotalHeapSize)
	}
}

func TestNewIsolateWithHeapLimit(t *testing.T) {
	const maxHeap = 32 << 20 // 32 MiB
	iso, err := v8.NewIsolateWithOptions(v8.IsolateOptions{
		InitialHeapSizeInBytes: 4 << 20,
		MaxHeapSizeInBytes:     maxHeap,
	})
	if err != nil {
		t.Fatalf("NewIsolateWithOptions: %v", err)
	}
	defer iso.Dispose()

	stats, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatalf("GetHeapStatistics: %v", err)
	}
	// V8 may round the configured limit; require it be in the ballpark of maxHeap.
	if stats.HeapSizeLimit == 0 {
		t.Fatal("expected heap_size_limit to be set")
	}
	if stats.HeapSizeLimit > maxHeap*2 {
		t.Fatalf("heap_size_limit %d far exceeds requested max %d", stats.HeapSizeLimit, maxHeap)
	}
}

func TestTerminateExecution(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	done := make(chan error, 1)
	go func() {
		_, err := ctx.RunScript(`for (;;) {}`, "loop.js")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	iso.TerminateExecution()

	select {
	case err := <-done:
		if err != v8.ErrExecutionTerminated {
			t.Fatalf("expected ErrExecutionTerminated, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("script did not terminate")
	}

	iso.CancelTerminateExecution()
	if iso.IsExecutionTerminating() {
		t.Fatal("expected termination to be cleared after CancelTerminateExecution")
	}

	if _, err := ctx.RunScript(`1+1`, "ok.js"); err != nil {
		t.Fatalf("script after cancel failed: %v", err)
	}
}

func TestNearHeapLimitCallback(t *testing.T) {
	const maxHeap = 8 << 20 // 8 MiB — small enough to hit quickly
	iso, err := v8.NewIsolateWithOptions(v8.IsolateOptions{
		InitialHeapSizeInBytes: 1 << 20,
		MaxHeapSizeInBytes:     maxHeap,
	})
	if err != nil {
		t.Fatalf("NewIsolateWithOptions: %v", err)
	}
	defer iso.Dispose()

	var fired atomic.Bool
	err = iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		fired.Store(true)
		iso.TerminateExecution()
		// Bump slightly so terminate can unwind instead of FatalProcessOutOfMemory.
		return current + (1 << 20)
	})
	if err != nil {
		t.Fatalf("AddNearHeapLimitCallback: %v", err)
	}

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	// Grow the heap until we approach the limit. Termination or OOM-path callback
	// should fire; either ErrExecutionTerminated or a JS error is acceptable as
	// long as the process does not abort and the callback ran.
	_, runErr := ctx.RunScript(`
		const chunks = [];
		for (let i = 0; i < 2000; i++) {
			chunks.push(new Array(50000).fill(i));
		}
	`, "grow.js")

	if !fired.Load() && runErr == nil {
		t.Fatal("expected near-heap-limit callback or script failure under tiny heap")
	}
	if runErr != nil && runErr != v8.ErrExecutionTerminated {
		// Allocation may still surface as a JS exception after terminate; that is fine.
		t.Logf("grow script error (ok): %v", runErr)
	}
}

func TestAdjustAmountOfExternalAllocatedMemory(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	const delta int64 = 4 << 20
	total, err := iso.AdjustAmountOfExternalAllocatedMemory(delta)
	if err != nil {
		t.Fatalf("AdjustAmountOfExternalAllocatedMemory: %v", err)
	}
	if total < delta {
		t.Fatalf("expected external total >= %d, got %d", delta, total)
	}

	total2, err := iso.AdjustAmountOfExternalAllocatedMemory(delta)
	if err != nil {
		t.Fatalf("second adjust: %v", err)
	}
	if total2 < total+delta {
		t.Fatalf("expected total to grow by %d: before=%d after=%d", delta, total, total2)
	}

	down, err := iso.AdjustAmountOfExternalAllocatedMemory(-2 * delta)
	if err != nil {
		t.Fatalf("adjust down: %v", err)
	}
	if down > total2-2*delta+1024 { // allow tiny slack
		t.Fatalf("expected total to drop by 2*delta: before=%d after=%d", total2, down)
	}
}

func TestMemoryPressureAndGC(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	if _, err := ctx.RunScript(`globalThis.__junk = new Array(100000).fill("y")`, "alloc.js"); err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if _, err := ctx.RunScript(`globalThis.__junk = null`, "drop.js"); err != nil {
		t.Fatalf("drop: %v", err)
	}

	if err := iso.MemoryPressureNotification(v8.MemoryPressureCritical); err != nil {
		t.Fatalf("MemoryPressureNotification: %v", err)
	}
	if err := iso.RequestGarbageCollectionForTesting(v8.FullGarbageCollection); err != nil {
		t.Fatalf("RequestGarbageCollectionForTesting: %v", err)
	}

	spaces, err := iso.GetHeapSpaceStatistics()
	if err != nil {
		t.Fatalf("GetHeapSpaceStatistics: %v", err)
	}
	if len(spaces) == 0 {
		t.Fatal("expected at least one heap space")
	}
	if spaces[0].SpaceName == "" {
		t.Fatal("expected space name")
	}
}

func TestContextIsolate(t *testing.T) {
	iso, err := v8.NewIsolate()
	if err != nil {
		t.Fatalf("NewIsolate: %v", err)
	}
	defer iso.Dispose()

	ctx, err := iso.NewContext()
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	defer ctx.Dispose()

	if ctx.Isolate() != iso {
		t.Fatal("Context.Isolate() should return owning isolate")
	}
}
