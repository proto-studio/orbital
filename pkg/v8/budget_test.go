package v8_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

const mb = 1 << 20

func newBudgetContext(t *testing.T, opts v8.IsolateOptions) (*v8.Isolate, *v8.Context) {
	t.Helper()
	iso, err := v8.NewIsolateWithOptions(opts)
	if err != nil {
		t.Fatalf("NewIsolateWithOptions: %v", err)
	}
	ctx, err := iso.NewContext()
	if err != nil {
		iso.Dispose()
		t.Fatalf("NewContext: %v", err)
	}
	t.Cleanup(func() {
		ctx.Dispose()
		iso.Dispose()
	})
	return iso, ctx
}

func TestMemoryBudgetCharge(t *testing.T) {
	b := v8.NewMemoryBudget(10)
	if err := b.Charge(6); err != nil {
		t.Fatalf("Charge(6): %v", err)
	}
	if err := b.Charge(5); !errors.Is(err, v8.ErrOutOfMemory) {
		t.Fatalf("Charge(5) over the limit = %v, want ErrOutOfMemory", err)
	}
	if got := b.Used(); got != 6 {
		t.Fatalf("Used after a refused charge = %d, want 6", got)
	}
	if err := b.Charge(4); err != nil {
		t.Fatalf("Charge(4) to the limit: %v", err)
	}
	b.Release(10)
	if got, avail := b.Used(), b.Available(); got != 0 || avail != 10 {
		t.Fatalf("after Release: Used %d Available %d, want 0 and 10", got, avail)
	}

	var none *v8.MemoryBudget
	if err := none.Charge(1 << 62); err != nil {
		t.Fatalf("nil budget Charge: %v", err)
	}
}

// An ArrayBuffer is charged while it's alive and fails to allocate, as V8 reports it, when
// it doesn't fit.
func TestMemoryBudgetArrayBuffers(t *testing.T) {
	b := v8.NewMemoryBudget(256 * mb)
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 64 * mb, MemoryBudget: b})
	before := b.Used()
	if _, err := ctx.RunScript(`globalThis.keep = new ArrayBuffer(32 * 1024 * 1024); undefined`, "keep.js"); err != nil {
		t.Fatal(err)
	}
	if got := b.Used() - before; got < 32*mb {
		t.Fatalf("a live 32 MiB ArrayBuffer added %d to the budget", got)
	}
	v, err := ctx.RunScript(`try { new ArrayBuffer(512 * 1024 * 1024); 'allocated' } catch (e) { e.name + ': ' + e.message }`, "big.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "RangeError: Array buffer allocation failed"; got != want {
		t.Fatalf("over the budget: %q, want %q", got, want)
	}
	held := b.Used()
	if _, err := ctx.RunScript(`globalThis.keep = null; undefined`, "drop.js"); err != nil {
		t.Fatal(err)
	}
	// V8 frees collected ArrayBuffers' contents in the background, after the collection.
	deadline := time.Now().Add(5 * time.Second)
	for b.Used()+32*mb > held && time.Now().Before(deadline) {
		if err := iso.RequestGarbageCollectionForTesting(v8.FullGarbageCollection); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.Used(); got+32*mb > held {
		t.Fatalf("collected ArrayBuffer still charged: used %d, %d while it was alive", got, held)
	}
}

// A heap grows from the budget past what it starts with, and an isolate that can't grow
// any more runs out of memory without taking the process with it.
func TestMemoryBudgetHeapOutOfMemory(t *testing.T) {
	b := v8.NewMemoryBudget(128 * mb)
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MemoryBudget: b})
	var hook atomic.Int32
	iso.OnOutOfMemory(func() { hook.Add(1) })
	if _, err := ctx.RunScript(`globalThis.a = []; for (let i = 0; i < 300000; i++) a.push({ i, s: 'x' + i })`, "grow.js"); err != nil {
		t.Fatalf("growing within the budget: %v", err)
	}
	if iso.OutOfMemory() {
		t.Fatal("out of memory within the budget")
	}
	_, err := ctx.RunScript(`for (;;) a.push({ x: Math.random(), y: [1, 2, 3] })`, "oom.js")
	if err == nil || !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("filling the heap: %v, want termination", err)
	}
	if !iso.OutOfMemory() || hook.Load() != 1 {
		t.Fatalf("OutOfMemory %v, hook called %d times; want true and once", iso.OutOfMemory(), hook.Load())
	}
	if used := b.Used(); used > 128*mb {
		t.Fatalf("budget used %d past its limit", used)
	}
}

// Without a budget, reaching the configured heap limit also runs out of memory, rather
// than V8 aborting the process.
func TestHeapLimitOutOfMemoryWithoutBudget(t *testing.T) {
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb})
	_, err := ctx.RunScript(`const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] })`, "oom.js")
	if err == nil || !iso.OutOfMemory() {
		t.Fatalf("filling the heap: %v, OutOfMemory %v", err, iso.OutOfMemory())
	}
}

// Isolates sharing a budget share its room: one holding most of it leaves the next too
// little to start, and disposing it gives its share back.
func TestMemoryBudgetSharedByIsolates(t *testing.T) {
	b := v8.NewMemoryBudget(96 * mb)
	opts := v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb, MemoryBudget: b}
	first, err := v8.NewIsolateWithOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	rest := b.Available()
	if err := b.Charge(rest); err != nil {
		t.Fatal(err)
	}
	if _, err := v8.NewIsolateWithOptions(opts); !errors.Is(err, v8.ErrOutOfMemory) {
		t.Fatalf("second isolate with no room left: %v, want ErrOutOfMemory", err)
	}
	b.Release(rest)
	first.Dispose()
	if got := b.Used(); got != 0 {
		t.Fatalf("Used after the only isolate is disposed = %d, want 0", got)
	}
	second, err := v8.NewIsolateWithOptions(opts)
	if err != nil {
		t.Fatalf("isolate once the first gave its share back: %v", err)
	}
	second.Dispose()
}

func TestIsolateLimitsCapsOnlyLower(t *testing.T) {
	iso, err := v8.NewIsolateWithOptions(v8.IsolateOptions{
		MaxHeapSizeInBytes:          64 * mb,
		CapOldGenerationSizeInBytes: 1 << 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Dispose()
	base := iso.Limits()
	if base.MaxOldGenerationSizeInBytes == 0 || base.MaxOldGenerationSizeInBytes > 64*mb {
		t.Fatalf("old generation %d for a 64 MiB heap", base.MaxOldGenerationSizeInBytes)
	}

	low, err := v8.NewIsolateWithOptions(v8.IsolateOptions{
		MaxHeapSizeInBytes:            64 * mb,
		CapOldGenerationSizeInBytes:   20 * mb,
		CapYoungGenerationSizeInBytes: 2 * mb,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer low.Dispose()
	if got := low.Limits(); got.MaxOldGenerationSizeInBytes != 20*mb || got.MaxYoungGenerationSizeInBytes > 2*mb {
		t.Fatalf("lowered limits %+v", got)
	}
}

// A large object can take the heap past its limit in one allocation. Running out of memory
// then still stops only the isolate: the limit the callback gives V8 covers what's in use,
// with or without a budget.
func TestHeapOvershotByLargeObjectOutOfMemory(t *testing.T) {
	for _, budget := range []*v8.MemoryBudget{nil, v8.NewMemoryBudget(256 * mb)} {
		iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb, MemoryBudget: budget})
		_, _ = ctx.RunScript(`const a = new Array(16 * 1024 * 1024).fill(0); const b = new Array(16 * 1024 * 1024).fill(1); a.length + b.length`, "large.js")
		if !iso.OutOfMemory() {
			t.Fatalf("budget %v: 128 MiB of arrays in a 32 MiB heap didn't run out of memory", budget != nil)
		}
	}
}

// V8 collects garbage before it gives up on an ArrayBuffer, and asks whether the heap may
// grow while it does. ArrayBuffers the budget refuses mustn't grow the heap, or run it out
// of memory, when it isn't full.
func TestMemoryBudgetRefusedArrayBuffersLeaveHeap(t *testing.T) {
	b := v8.NewMemoryBudget(128 * mb)
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 64 * mb, MemoryBudget: b})
	before := b.Used()
	v, err := ctx.RunScript(`let failed = 0; for (let i = 0; i < 20; i++) { try { new ArrayBuffer(200 * 1024 * 1024) } catch (e) { failed++ } } failed`, "refused.js")
	if err != nil || v.Integer() != 20 {
		t.Fatalf("refused allocations: %v %v", v, err)
	}
	if iso.OutOfMemory() || b.Used() != before {
		t.Fatalf("OutOfMemory %v, used %d after refusals, %d before", iso.OutOfMemory(), b.Used(), before)
	}
}

// A cloned view whose contents don't fit in the receiving isolate's budget fails to
// deserialize, as an allocation from JavaScript fails, rather than aborting the process.
func TestMemoryBudgetDeserializeRefused(t *testing.T) {
	_, from := newBudgetContext(t, v8.IsolateOptions{})
	view, err := from.RunScript(`new Uint8Array(64 * 1024 * 1024)`, "view.js")
	if err != nil {
		t.Fatal(err)
	}
	proto, err := from.RunScript(`Uint8Array.prototype`, "proto.js")
	if err != nil {
		t.Fatal(err)
	}
	data, err := from.SerializeValue(view, proto)
	if err != nil {
		t.Fatal(err)
	}

	b := v8.NewMemoryBudget(48 * mb)
	_, to := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb, MemoryBudget: b})
	toProto, err := to.RunScript(`Uint8Array.prototype`, "proto.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := to.DeserializeValue(data, toProto); err == nil || !strings.Contains(err.Error(), "Array buffer allocation failed") {
		t.Fatalf("deserializing past the budget: %v, want Array buffer allocation failed", err)
	}
}

// An embedder's near-heap-limit callback is asked first, and a limit it raises is charged
// to the budget; when the budget can't cover it, the isolate runs out of memory rather
// than V8 aborting the process.
func TestNearHeapLimitCallbackChargedToBudget(t *testing.T) {
	b := v8.NewMemoryBudget(96 * mb)
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb, MemoryBudget: b})
	var calls atomic.Int32
	if err := iso.AddNearHeapLimitCallback(func(current, initial uint64) uint64 {
		calls.Add(1)
		return current + 512*mb
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = ctx.RunScript(`const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] })`, "oom.js")
	if calls.Load() == 0 || !iso.OutOfMemory() {
		t.Fatalf("callback called %d times, OutOfMemory %v; want called and out of memory", calls.Load(), iso.OutOfMemory())
	}
	if used := b.Used(); used > b.Limit() {
		t.Fatalf("budget used %d past its limit %d", used, b.Limit())
	}
}

// An isolate that ran out of memory can't run JavaScript any more, even after
// CancelTerminateExecution, so running more in it can't keep raising its limit.
func TestOutOfMemoryIsolateStaysStopped(t *testing.T) {
	iso, ctx := newBudgetContext(t, v8.IsolateOptions{MaxHeapSizeInBytes: 32 * mb, MemoryBudget: v8.NewMemoryBudget(128 * mb)})
	fill := `globalThis.a = globalThis.a || []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] })`
	if _, err := ctx.RunScript(fill, "oom.js"); err == nil || !iso.OutOfMemory() {
		t.Fatalf("filling the heap: %v, OutOfMemory %v", err, iso.OutOfMemory())
	}
	first, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		iso.CancelTerminateExecution()
		if v, err := ctx.RunScript(`1 + 1`, "after.js"); err == nil {
			t.Fatalf("ran JavaScript after running out of memory: %v", v)
		}
		_, _ = ctx.RunScript(fill, "again.js")
	}
	after, err := iso.GetHeapStatistics()
	if err != nil {
		t.Fatal(err)
	}
	if after.HeapSizeLimit > first.HeapSizeLimit {
		t.Fatalf("heap limit grew from %d to %d after running out of memory", first.HeapSizeLimit, after.HeapSizeLimit)
	}
}
