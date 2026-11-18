package runtime

import (
	"errors"
	"testing"

	"proto.zip/studio/orbital/pkg/v8"
)

// However little room the budget leaves, New either makes the runtime or says it ran
// out of memory: a heap that runs out partway through setting up isn't some other error.
func TestNewOutOfMemoryAtAnySetupStep(t *testing.T) {
	const mb = 1 << 20
	sawOOM, sawOK := false, false
	for room := uint64(0); room <= 24*mb; room += mb / 4 {
		budget := NewMemoryBudget(64 * mb)
		if err := budget.Charge(budget.Limit() - room); err != nil {
			t.Fatal(err)
		}
		rt, err := New(&Config{MemoryBudget: budget, IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: 16 * mb}})
		var oom *OutOfMemoryError
		switch {
		case err == nil:
			sawOK = true
			rt.Dispose()
		case errors.As(err, &oom) || errors.Is(err, ErrOutOfMemory):
			sawOOM = true
		default:
			t.Fatalf("with %d bytes left: %v, want a runtime or out of memory", room, err)
		}
	}
	if !sawOOM || !sawOK {
		t.Fatalf("saw out of memory %v, saw a runtime %v; want both across the sweep", sawOOM, sawOK)
	}
}
