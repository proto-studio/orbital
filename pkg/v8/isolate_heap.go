package v8

/*
#include "v8go.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"runtime"
)

// IsolateOptions configures CreateParams for a new isolate.
// Heap limits can only be set at creation time — V8 will not let you change
// them later. Prefer MaxHeapSizeInBytes (ConfigureDefaultsFromHeapSize) for a
// hard per-isolate ceiling (e.g. 64–128 MiB per function).
type IsolateOptions struct {
	// InitialHeapSizeInBytes is the initial heap size, or 0 for V8's default.
	InitialHeapSizeInBytes uint64
	// MaxHeapSizeInBytes is the hard total heap limit, or 0 for V8's default.
	// When either this or InitialHeapSizeInBytes is non-zero, V8's
	// ConfigureDefaultsFromHeapSize is used.
	MaxHeapSizeInBytes uint64
	// MaxOldGenerationSizeInBytes caps only the old generation, or 0 to leave
	// unset. Can be combined with ConfigureDefaultsFromHeapSize; applied after.
	MaxOldGenerationSizeInBytes uint64
}

// NewIsolateWithOptions creates a V8 isolate with the given create-time options.
func NewIsolateWithOptions(opts IsolateOptions) (*Isolate, error) {
	if err := Initialize(); err != nil {
		return nil, err
	}

	var params C.v8go_isolate_create_params
	params.initial_heap_size_in_bytes = C.size_t(opts.InitialHeapSizeInBytes)
	params.max_heap_size_in_bytes = C.size_t(opts.MaxHeapSizeInBytes)
	params.max_old_generation_size_in_bytes = C.size_t(opts.MaxOldGenerationSizeInBytes)

	ptr := C.v8go_isolate_new_with_params(&params)
	if ptr == nil {
		return nil, errors.New("failed to create V8 isolate")
	}

	iso := &Isolate{ptr: ptr}
	runtime.SetFinalizer(iso, (*Isolate).release)
	return iso, nil
}

// HeapStatistics is a snapshot of V8 isolate heap usage.
type HeapStatistics struct {
	TotalHeapSize            uint64
	TotalHeapSizeExecutable  uint64
	TotalPhysicalSize        uint64
	TotalAvailableSize       uint64
	TotalGlobalHandlesSize   uint64
	UsedGlobalHandlesSize    uint64
	UsedHeapSize             uint64
	HeapSizeLimit            uint64
	MallocedMemory           uint64
	ExternalMemory           uint64
	PeakMallocedMemory       uint64
	NumberOfNativeContexts   uint64
	NumberOfDetachedContexts uint64
	TotalAllocatedBytes      uint64
}

// GetHeapStatistics returns current V8 heap statistics for this isolate.
func (i *Isolate) GetHeapStatistics() (HeapStatistics, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return HeapStatistics{}, ErrIsolateDisposed
	}
	var raw C.v8go_heap_statistics
	C.v8go_isolate_get_heap_statistics(i.ptr, &raw)
	return HeapStatistics{
		TotalHeapSize:            uint64(raw.total_heap_size),
		TotalHeapSizeExecutable:  uint64(raw.total_heap_size_executable),
		TotalPhysicalSize:        uint64(raw.total_physical_size),
		TotalAvailableSize:       uint64(raw.total_available_size),
		TotalGlobalHandlesSize:   uint64(raw.total_global_handles_size),
		UsedGlobalHandlesSize:    uint64(raw.used_global_handles_size),
		UsedHeapSize:             uint64(raw.used_heap_size),
		HeapSizeLimit:            uint64(raw.heap_size_limit),
		MallocedMemory:           uint64(raw.malloced_memory),
		ExternalMemory:           uint64(raw.external_memory),
		PeakMallocedMemory:       uint64(raw.peak_malloced_memory),
		NumberOfNativeContexts:   uint64(raw.number_of_native_contexts),
		NumberOfDetachedContexts: uint64(raw.number_of_detached_contexts),
		TotalAllocatedBytes:      uint64(raw.total_allocated_bytes),
	}, nil
}

// HeapSpaceStatistics describes one V8 heap space (debugging / introspection).
type HeapSpaceStatistics struct {
	SpaceName          string
	SpaceSize          uint64
	SpaceUsedSize      uint64
	SpaceAvailableSize uint64
	PhysicalSpaceSize  uint64
}

// GetHeapSpaceStatistics returns per-space heap statistics for debugging.
func (i *Isolate) GetHeapSpaceStatistics() ([]HeapSpaceStatistics, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return nil, ErrIsolateDisposed
	}
	n := int(C.v8go_isolate_number_of_heap_spaces(i.ptr))
	out := make([]HeapSpaceStatistics, 0, n)
	for idx := 0; idx < n; idx++ {
		var raw C.v8go_heap_space_statistics
		if C.v8go_isolate_get_heap_space_statistics(i.ptr, C.size_t(idx), &raw) == 0 {
			continue
		}
		name := ""
		if raw.space_name != nil {
			name = C.GoString(raw.space_name)
		}
		out = append(out, HeapSpaceStatistics{
			SpaceName:          name,
			SpaceSize:          uint64(raw.space_size),
			SpaceUsedSize:      uint64(raw.space_used_size),
			SpaceAvailableSize: uint64(raw.space_available_size),
			PhysicalSpaceSize:  uint64(raw.physical_space_size),
		})
	}
	return out, nil
}

// NearHeapLimitCallback is invoked when the isolate heap approaches its limit.
// Return the new heap limit in bytes. Typical pattern: bump the limit slightly
// so TerminateExecution can unwind cleanly, then call TerminateExecution()
// instead of letting V8 FatalProcessOutOfMemory and kill the process.
type NearHeapLimitCallback func(currentHeapLimit, initialHeapLimit uint64) (newHeapLimit uint64)

// AddNearHeapLimitCallback registers a near-heap-limit callback on this isolate.
// Only the most recently added callback is invoked by V8; replacing clears any
// previous Go-side registration for this isolate.
func (i *Isolate) AddNearHeapLimitCallback(cb NearHeapLimitCallback) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return ErrIsolateDisposed
	}
	if i.nearHeapLimitID != 0 {
		unregisterNearHeapLimitCallback(i.nearHeapLimitID)
		C.v8go_isolate_remove_near_heap_limit_callback(i.ptr, 0)
		i.nearHeapLimitID = 0
	}
	id := registerNearHeapLimitCallback(cb)
	i.nearHeapLimitID = id
	C.v8go_isolate_add_near_heap_limit_callback(i.ptr, C.int(id))
	return nil
}

// RemoveNearHeapLimitCallback removes the near-heap-limit callback and restores
// the heap limit. If heapLimit is 0 it is ignored by V8; otherwise V8 restores
// toward that limit (clamped to the current heap size).
func (i *Isolate) RemoveNearHeapLimitCallback(heapLimit uint64) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return ErrIsolateDisposed
	}
	if i.nearHeapLimitID != 0 {
		unregisterNearHeapLimitCallback(i.nearHeapLimitID)
		i.nearHeapLimitID = 0
	}
	C.v8go_isolate_remove_near_heap_limit_callback(i.ptr, C.size_t(heapLimit))
	return nil
}

// TerminateExecution forcefully terminates the current JavaScript execution
// on this isolate. Safe to call from another thread without the V8 lock.
func (i *Isolate) TerminateExecution() {
	i.mu.Lock()
	ptr := i.ptr
	i.mu.Unlock()
	if ptr == nil {
		return
	}
	C.v8go_isolate_terminate_execution(ptr)
}

// CancelTerminateExecution resumes execution capability after TerminateExecution.
func (i *Isolate) CancelTerminateExecution() {
	i.mu.Lock()
	ptr := i.ptr
	i.mu.Unlock()
	if ptr == nil {
		return
	}
	C.v8go_isolate_cancel_terminate_execution(ptr)
}

// IsExecutionTerminating reports whether JS is currently terminating because of
// TerminateExecution.
func (i *Isolate) IsExecutionTerminating() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return false
	}
	return C.v8go_isolate_is_execution_terminating(i.ptr) != 0
}

// MemoryPressureLevel hints V8 about process-wide memory pressure.
type MemoryPressureLevel int

const (
	MemoryPressureNone MemoryPressureLevel = iota
	MemoryPressureModerate
	MemoryPressureCritical
)

// MemoryPressureNotification nudges GC under process-wide memory pressure
// (e.g. before hard LRU eviction).
func (i *Isolate) MemoryPressureNotification(level MemoryPressureLevel) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return ErrIsolateDisposed
	}
	C.v8go_isolate_memory_pressure_notification(i.ptr, C.int(level))
	return nil
}

// AdjustAmountOfExternalAllocatedMemory tells V8 about Go-side buffers pinned
// into JS so heap limits account for them. Returns the new external memory total.
func (i *Isolate) AdjustAmountOfExternalAllocatedMemory(changeInBytes int64) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return 0, ErrIsolateDisposed
	}
	n := C.v8go_isolate_adjust_amount_of_external_allocated_memory(i.ptr, C.longlong(changeInBytes))
	return int64(n), nil
}

// GarbageCollectionType selects a GC kind for testing / idle shrink.
type GarbageCollectionType int

const (
	FullGarbageCollection GarbageCollectionType = iota
	MinorGarbageCollection
)

// RequestGarbageCollectionForTesting requests a GC. Intended for shrinking warm
// isolates before re-pooling; prefer MemoryPressureNotification in production.
func (i *Isolate) RequestGarbageCollectionForTesting(typ GarbageCollectionType) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.ptr == nil {
		return ErrIsolateDisposed
	}
	C.v8go_isolate_request_garbage_collection_for_testing(i.ptr, C.int(typ))
	return nil
}

// Isolate returns the isolate that owns this context.
func (c *Context) Isolate() *Isolate {
	return c.iso
}
