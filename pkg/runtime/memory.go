package runtime

import (
	"proto.zip/studio/orbital/pkg/v8"
)

// MemoryBudget is an amount of memory a runtime and every worker and loader realm it
// starts use together (Config.MemoryBudget). Each one's JavaScript heap and the
// contents of its ArrayBuffers and Buffers draw from it. Code outside the runtime that
// allocates on scripts' behalf, such as a memory filesystem, charges it too, with
// Charge and Release. Its methods are safe to call from any goroutine, and a nil
// *MemoryBudget has no limit.
type MemoryBudget = v8.MemoryBudget

// NewMemoryBudget makes a budget of limit bytes.
func NewMemoryBudget(limit uint64) *MemoryBudget {
	return v8.NewMemoryBudget(limit)
}

// ErrOutOfMemory reports memory that didn't fit in a MemoryBudget. MemoryBudget.Charge
// fails with it, and so does every *OutOfMemoryError.
var ErrOutOfMemory = v8.ErrOutOfMemory

// OutOfMemoryError is what Run returns once the runtime has run out of memory: its
// heap reached its limit, or couldn't grow because the MemoryBudget was used up. In
// Node, that is fatal to the process ("JavaScript heap out of memory"); here only the
// runtime stops, and it can't run anything further.
type OutOfMemoryError struct{}

// Error implements the error interface.
func (*OutOfMemoryError) Error() string {
	return "JavaScript heap out of memory"
}

// Is makes errors.Is(err, ErrOutOfMemory) true for an *OutOfMemoryError.
func (*OutOfMemoryError) Is(target error) bool {
	return target == ErrOutOfMemory
}

// OutOfMemory reports whether the runtime has run out of memory, which ended it.
func (rt *Runtime) OutOfMemory() bool {
	return rt.outOfMemory.Load()
}

// MemoryBudget returns the budget the runtime draws from (Config.MemoryBudget), or nil
// when it has none.
func (rt *Runtime) MemoryBudget() *MemoryBudget {
	return rt.memoryBudget
}

// watchMemory ends the runtime once its isolate runs out of memory. V8 has terminated
// the JavaScript running by then; this keeps the runtime from carrying on.
func (rt *Runtime) watchMemory() {
	rt.isolate.OnOutOfMemory(func() {
		// Called inside V8's garbage collector, where the runtime can't be stopped
		// in place: Kill takes locks the interrupted code may hold.
		if rt.outOfMemory.CompareAndSwap(false, true) {
			go rt.Kill("out of memory")
		}
	})
}
