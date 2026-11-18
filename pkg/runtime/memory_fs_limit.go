package runtime

import (
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/v8"
)

// memoryLimited is a Filesystem whose memory can be counted against a limit, such as a
// MemoryFilesystem, or a type that embeds one.
type memoryLimited interface {
	limitMemory(limit memoryLimit) (remove func(), ok bool)
	memoryInUse() int64
}

// budgetLimit charges a filesystem's memory to a MemoryBudget, which the heaps of the runtime
// and its workers draw from too. A heap that would grow into what the filesystem holds runs
// out of memory, and a filesystem that would grow into what the heaps hold fails with ENOSPC.
type budgetLimit struct{ budget *MemoryBudget }

func (l budgetLimit) charge(_, delta int64) bool {
	return l.budget.Charge(uint64(delta)) == nil
}

func (l budgetLimit) release(n int64) {
	l.budget.Release(uint64(n))
}

// isolateMemoryLimit keeps a filesystem's memory and an isolate's heap together within the
// isolate's heap limit.
type isolateMemoryLimit struct {
	iso   *v8.Isolate
	limit uint64

	// heapUsed is the heap's usage when last sampled. The filesystem's calls also run off the
	// isolate's thread, where reading the heap would wait for JavaScript to stop, so they use
	// the sample taken on the isolate's thread when the call was made.
	heapUsed atomic.Uint64

	collectedAt uint64 // the heap's usage after the last collection sample asked for
}

// newIsolateMemoryLimit is the limit of iso, created with opts: MaxHeapSizeInBytes, or, when
// that is 0, the limit V8 applies.
func newIsolateMemoryLimit(iso *v8.Isolate, opts v8.IsolateOptions) *isolateMemoryLimit {
	l := &isolateMemoryLimit{iso: iso, limit: opts.MaxHeapSizeInBytes}
	if stats, err := iso.GetHeapStatistics(); err == nil {
		if l.limit == 0 {
			l.limit = stats.HeapSizeLimit
		}
		l.heapUsed.Store(stats.UsedHeapSize)
	}
	return l
}

// sample records the heap's usage, given that the filesystem holds fsUsed bytes. It runs on
// the goroutine running the isolate's JavaScript.
//
// The heap's usage includes garbage until V8 collects it, so when the heap and the
// filesystem together come near the limit, sample collects garbage first, as V8 does before
// failing an allocation. It does that only once the heap has grown by a sixteenth of the limit
// since the last time, so calls made near the limit don't each wait for a collection.
func (l *isolateMemoryLimit) sample(fsUsed int64) {
	stats, err := l.iso.GetHeapStatistics()
	if err != nil {
		return
	}
	used := stats.UsedHeapSize
	margin := l.limit / 16
	if l.limit != 0 && used+uint64(fsUsed)+margin > l.limit && used > l.collectedAt+margin {
		if l.iso.RequestGarbageCollectionForTesting(v8.FullGarbageCollection) == nil {
			if stats, err := l.iso.GetHeapStatistics(); err == nil {
				used = stats.UsedHeapSize
			}
		}
		l.collectedAt = used
	}
	l.heapUsed.Store(used)
}

// charge checks the filesystem's usage after growing by delta, with the heap's, against the
// heap limit. Nothing is taken, so there is nothing to release.
func (l *isolateMemoryLimit) charge(used, delta int64) bool {
	if l.limit == 0 {
		return true // V8 reported no limit
	}
	total := used + delta
	return total >= 0 && l.heapUsed.Load()+uint64(total) <= l.limit
}

func (l *isolateMemoryLimit) release(int64) {}

// SampleHeapUsage records the isolate's heap usage, which a MemoryFilesystem's growth is
// checked with, together with what it holds, against the isolate's heap limit when the
// runtime has no MemoryBudget. The fs module
// calls it as each call starts, on the goroutine running JavaScript, so work it does off that
// goroutine is checked with the usage when the call was made.
func (rt *Runtime) SampleHeapUsage() {
	if rt.fsLimit != nil {
		rt.fsLimit.sample(rt.filesystem.(memoryLimited).memoryInUse())
	}
}

// FilesystemWork counts work on the runtime's filesystem that runs off the JavaScript thread,
// until done is called. Dispose waits for it before it stops counting a MemoryFilesystem
// against the runtime's memory limit, so work still running when the runtime stops can't
// grow the filesystem past it. Call it on the JavaScript thread, before starting the work.
func (rt *Runtime) FilesystemWork() (done func()) {
	rt.fsWork.Add(1)
	var once sync.Once
	return func() { once.Do(rt.fsWork.Done) }
}
