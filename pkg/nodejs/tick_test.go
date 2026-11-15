package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// A chain of ticks that each queue the next has one pending at a time, so the queue must
// not hold on to every tick it has run.
func TestNextTick_RecursiveChainReleasesMemory(t *testing.T) {
	result := runJS(t, &runtime.Config{}, `
const start = process.memoryUsage().heapUsed;
let n = 0, peak = 0;
const step = () => {
  if (++n % 50000 === 0) peak = Math.max(peak, process.memoryUsage().heapUsed - start);
  if (n < 1000000) process.nextTick(step);
  else done({ n, small: peak < 20e6, peakMB: Math.round(peak / 1e6) });
};
process.nextTick(step);
`)
	if result["n"] != float64(1000000) || result["small"] != true {
		t.Errorf("result = %v, want 1000000 ticks with under 20 MB held", result)
	}
}

// queueMicrotask schedules through V8 itself, as Node's does, so a script that replaces
// Promise's species or then cannot stop it, and the callback still runs in order with
// promise reactions queued around it.
func TestQueueMicrotask_IgnoresPromisePatches(t *testing.T) {
	result := runJS(t, &runtime.Config{}, `
const order = [];
Promise.resolve().then(() => order.push('p1'));
const species = Object.getOwnPropertyDescriptor(Promise, Symbol.species);
Object.defineProperty(Promise, Symbol.species, { get() { throw new Error('species'); }, configurable: true });
const then = Promise.prototype.then;
let threw = null;
try {
  queueMicrotask(() => order.push('qm'));
  Promise.prototype.then = function () { throw new Error('patched then'); };
  queueMicrotask(() => order.push('qm2'));
} catch (e) {
  threw = e.message;
}
Promise.prototype.then = then;
Object.defineProperty(Promise, Symbol.species, species);
Promise.resolve().then(() => order.push('p2'));
setTimeout(() => done({ order, threw }), 0);
`)
	wantJSON(t, result, map[string]any{"order": []any{"p1", "qm", "qm2", "p2"}, "threw": nil})
}
