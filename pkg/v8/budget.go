package v8

/*
#include "v8go.h"
*/
import "C"

import (
	"errors"
	"runtime"
)

// ErrOutOfMemory reports that memory didn't fit in a MemoryBudget: a charge,
// or an isolate whose heap the budget had no room to start.
var ErrOutOfMemory = errors.New("out of memory")

// MemoryBudget is an amount of memory shared by the isolates made with it
// (IsolateOptions.MemoryBudget) and whatever else charges it. Each isolate's
// ArrayBuffer contents and heap draw from it: an ArrayBuffer that doesn't fit
// fails to allocate, and a heap that can't grow runs out of memory.
//
// Its methods are safe to call from any goroutine. A nil *MemoryBudget has no
// limit: every charge fits.
type MemoryBudget struct {
	ptr *C.v8go_budget
}

// NewMemoryBudget makes a budget of limit bytes.
func NewMemoryBudget(limit uint64) *MemoryBudget {
	b := &MemoryBudget{ptr: C.v8go_budget_new(C.size_t(limit))}
	runtime.SetFinalizer(b, func(b *MemoryBudget) { C.v8go_budget_unref(b.ptr) })
	return b
}

// Charge takes n bytes from the budget, or fails with ErrOutOfMemory, taking
// nothing, when they don't fit. Give them back with Release.
func (b *MemoryBudget) Charge(n uint64) error {
	if b == nil || n == 0 {
		return nil
	}
	ok := C.v8go_budget_charge(b.ptr, C.size_t(n)) != 0
	runtime.KeepAlive(b)
	if !ok {
		return ErrOutOfMemory
	}
	return nil
}

// Release gives back n bytes taken with Charge.
func (b *MemoryBudget) Release(n uint64) {
	if b == nil || n == 0 {
		return
	}
	C.v8go_budget_release(b.ptr, C.size_t(n))
	runtime.KeepAlive(b)
}

// Limit is the budget's size in bytes, or 0 for a nil budget, which has no limit.
func (b *MemoryBudget) Limit() uint64 {
	if b == nil {
		return 0
	}
	n := uint64(C.v8go_budget_limit(b.ptr))
	runtime.KeepAlive(b)
	return n
}

// Used is how many bytes are taken from the budget now.
func (b *MemoryBudget) Used() uint64 {
	if b == nil {
		return 0
	}
	n := uint64(C.v8go_budget_used(b.ptr))
	runtime.KeepAlive(b)
	return n
}

// Available is how many bytes are left in the budget.
func (b *MemoryBudget) Available() uint64 {
	if b == nil {
		return ^uint64(0)
	}
	limit, used := b.Limit(), b.Used()
	if used >= limit {
		return 0
	}
	return limit - used
}
