package nodejs

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

const mib = 1 << 20

// worker_threads.resourceLimits reads as in Node: {} on the main thread and once a worker
// has exited, the limits a worker was given before it starts, and the ones in effect once
// it's online, the same inside it. A worker that reaches its heap limit stops with
// ERR_WORKER_OUT_OF_MEMORY and then exits with 1. testdata/worker_resource_limits.node.json
// holds Node v20.18.1's output, regenerated with
//
//	ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run TestWorkerResourceLimitsMatchesNode
func TestWorkerResourceLimitsMatchesNode(t *testing.T) {
	const script = "testdata/worker_resource_limits.js"
	const expectedPath = "testdata/worker_resource_limits.node.json"
	if captureFromNode(t) {
		output, err := exec.Command("node", script).Output()
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(output, &got); err != nil {
			t.Fatal(err)
		}
		writeExpected(t, expectedPath, got)
		return
	}
	source, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{MaxWorkers: -1}, string(source))
	if want := readExpected(t, expectedPath); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// newBudgeted makes a Node runtime drawing from budget, with heaps of at most maxHeap.
func newBudgeted(t *testing.T, budget *runtime.MemoryBudget, maxHeap uint64) *Instance {
	t.Helper()
	inst, err := New(&runtime.Config{
		EnableConsole:  true,
		EnableTimers:   true,
		Timeout:        60 * time.Second,
		MemoryBudget:   budget,
		MaxWorkers:     -1,
		IsolateOptions: v8.IsolateOptions{MaxHeapSizeInBytes: maxHeap},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inst.Runtime.Dispose)
	return inst
}

// A runtime whose heap can't grow stops, and Run says it ran out of memory, where Node
// would end the process; the host carries on.
func TestMemoryBudgetMainRuntimeOutOfMemory(t *testing.T) {
	budget := runtime.NewMemoryBudget(256 * mib)
	inst := newBudgeted(t, budget, 0)
	_, err := inst.Runtime.Run(`const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] });`, "oom.js")
	var oom *runtime.OutOfMemoryError
	if !errors.As(err, &oom) || !errors.Is(err, runtime.ErrOutOfMemory) {
		t.Fatalf("Run = %v, want an *OutOfMemoryError", err)
	}
	if got, want := err.Error(), "JavaScript heap out of memory"; got != want {
		t.Fatalf("error %q, want %q", got, want)
	}
	if used := budget.Used(); used > budget.Limit() {
		t.Fatalf("budget used %d past its limit %d", used, budget.Limit())
	}
	if _, err := inst.Runtime.Run(`1`, "again.js"); !errors.As(err, &oom) {
		t.Fatalf("Run after running out = %v, want the same error", err)
	}
}

// Without a budget, a heap that reaches its own limit ends the runtime the same way, not
// the process.
func TestHeapLimitOutOfMemoryWithoutBudget(t *testing.T) {
	inst := newBudgeted(t, nil, 64*mib)
	_, err := inst.Runtime.Run(`const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] });`, "oom.js")
	if !errors.Is(err, runtime.ErrOutOfMemory) {
		t.Fatalf("Run = %v, want out of memory", err)
	}
}

// A Buffer or ArrayBuffer the budget has no room for throws Node's RangeError for an
// allocation that failed, and the script carries on.
func TestMemoryBudgetBufferAllocationFails(t *testing.T) {
	inst := newBudgeted(t, runtime.NewMemoryBudget(128*mib), 64*mib)
	got := runIn(t, inst, `
const tries = {};
for (const [name, make] of [
  ['ArrayBuffer', () => new ArrayBuffer(200 * 1024 * 1024)],
  ['Buffer.alloc', () => Buffer.alloc(200 * 1024 * 1024)],
  ['Buffer.allocUnsafe', () => Buffer.allocUnsafe(200 * 1024 * 1024)],
  ['Uint8Array', () => new Uint8Array(200 * 1024 * 1024)],
]) {
  try { make(); tries[name] = 'allocated'; } catch (e) { tries[name] = e.name + ': ' + e.message; }
}
tries.small = Buffer.alloc(1024 * 1024).length;
done(tries);
`)
	want := map[string]any{
		"ArrayBuffer":        "RangeError: Array buffer allocation failed",
		"Buffer.alloc":       "RangeError: Array buffer allocation failed",
		"Buffer.allocUnsafe": "RangeError: Array buffer allocation failed",
		"Uint8Array":         "RangeError: Array buffer allocation failed",
		"small":              float64(1024 * 1024),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Workers share the budget with the runtime that starts them, so starting them in a loop,
// each filling its heap, can't use more than the budget. Each one stops as Node stops a
// worker out of memory, or, once the budget has no room for another heap, fails to start.
func TestMemoryBudgetSharedWithWorkers(t *testing.T) {
	budget := runtime.NewMemoryBudget(256 * mib)
	inst := newBudgeted(t, budget, 64*mib)
	stop := make(chan struct{})
	peak := make(chan uint64, 1)
	go func() {
		var max uint64
		for {
			select {
			case <-stop:
				peak <- max
				return
			case <-time.After(time.Millisecond):
				if u := budget.Used(); u > max {
					max = u
				}
			}
		}
	}()
	got := runIn(t, inst, `
const { Worker } = require('worker_threads');
const held = [];
const fill = 'const a = []; for (;;) a.push({ x: Math.random(), y: [1, 2, 3] });';
const keep = 'const a = []; for (let i = 0; i < 400000; i++) a.push({ i }); require("worker_threads").parentPort.on("message", () => process.exit(0)); require("worker_threads").parentPort.postMessage("full");';
const results = { codes: {}, exits: [] };
let pending = 0;
function start(source, onMessage) {
  pending++;
  const w = new Worker(source, { eval: true });
  w.on('error', (e) => { results.codes[e.code] = (results.codes[e.code] || 0) + 1; });
  w.on('message', onMessage || (() => {}));
  w.on('exit', (code) => {
    results.exits.push(code);
    if (--pending === 0) done(results);
  });
  return w;
}
// Workers holding part of the heap they grew, then workers filling theirs.
let full = 0;
for (let i = 0; i < 6; i++) {
  held.push(start(keep, () => {
    if (++full === 6) {
      for (let j = 0; j < 4; j++) start(fill);
      for (const w of held) w.postMessage('go');
    }
  }));
}
`)
	close(stop)
	if max := <-peak; max > budget.Limit() {
		t.Fatalf("budget used %d at its peak, past its limit %d", max, budget.Limit())
	}
	codes := got["codes"].(map[string]any)
	if codes["ERR_WORKER_OUT_OF_MEMORY"] == nil {
		t.Fatalf("no worker ran out of memory: %v", got)
	}
	for code := range codes {
		if code != "ERR_WORKER_OUT_OF_MEMORY" && code != "ERR_WORKER_INIT_FAILED" {
			t.Fatalf("unexpected error %s: %v", code, got)
		}
	}
	if n := len(got["exits"].([]any)); n != 10 {
		t.Fatalf("%d workers exited, want 10: %v", n, got)
	}
}

// A worker the budget has no room to start fails as Node fails a worker it can't set up.
func TestMemoryBudgetWorkerInitFails(t *testing.T) {
	budget := runtime.NewMemoryBudget(256 * mib)
	inst := newBudgeted(t, budget, 64*mib)
	if err := budget.Charge(budget.Available()); err != nil {
		t.Fatal(err)
	}
	got := runIn(t, inst, `
const { Worker } = require('worker_threads');
const w = new Worker('1', { eval: true });
const events = [];
w.on('error', (e) => events.push([e.name, e.code, e.message]));
w.on('exit', (code) => { events.push(code); done({ events }); });
`)
	want := []any{
		[]any{"Error", "ERR_WORKER_INIT_FAILED", "Worker initialization failure: failed to create isolate: out of memory"},
		float64(1),
	}
	if !reflect.DeepEqual(got["events"], want) {
		t.Fatalf("got %v, want %v", got["events"], want)
	}
}

// A worker's limits are the configured ones, lowered to what the budget had room for when
// it started and to anything lower the script asked for, and never raised.
func TestMemoryBudgetWorkerResourceLimits(t *testing.T) {
	budget := runtime.NewMemoryBudget(512 * mib)
	inst := newBudgeted(t, budget, 1<<30)
	got := runIn(t, inst, `
const { Worker } = require('worker_threads');
async function limits(resourceLimits) {
  const w = new Worker('require("worker_threads").parentPort.postMessage(require("worker_threads").resourceLimits)', { eval: true, resourceLimits });
  const inner = await new Promise((resolve) => w.once('message', resolve));
  await new Promise((resolve) => w.once('exit', resolve));
  return inner;
}
(async () => {
  done({
    none: await limits(undefined),
    raised: await limits({ maxOldGenerationSizeMb: 100000, stackSizeMb: 100 }),
    lowered: await limits({ maxOldGenerationSizeMb: 32 }),
  });
})();
`)
	for _, name := range []string{"none", "raised"} {
		l := got[name].(map[string]any)
		old, young := l["maxOldGenerationSizeMb"].(float64), l["maxYoungGenerationSizeMb"].(float64)
		if old <= 0 || old+young > 512 || old > 1024-young {
			t.Errorf("%s: %v, past the 512 MiB budget", name, l)
		}
		if l["stackSizeMb"] != float64(4) {
			t.Errorf("%s: stack %v, want 4", name, l["stackSizeMb"])
		}
	}
	if old := got["lowered"].(map[string]any)["maxOldGenerationSizeMb"]; old != float64(32) {
		t.Errorf("lowered: old generation %v, want 32", old)
	}
}

// A script's resourceLimits can't raise a cap the configuration set.
func TestWorkerResourceLimitsKeepConfiguredCaps(t *testing.T) {
	inst, err := New(&runtime.Config{
		EnableConsole: true,
		EnableTimers:  true,
		Timeout:       20 * time.Second,
		MaxWorkers:    -1,
		IsolateOptions: v8.IsolateOptions{
			MaxHeapSizeInBytes:          1 << 30,
			CapOldGenerationSizeInBytes: 32 * mib,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	got := runIn(t, inst, `
const { Worker } = require('worker_threads');
const w = new Worker('require("worker_threads").parentPort.postMessage(require("worker_threads").resourceLimits)', { eval: true, resourceLimits: { maxOldGenerationSizeMb: 128 } });
w.once('message', (limits) => done(limits));
`)
	if old := got["maxOldGenerationSizeMb"]; old != float64(32) {
		t.Fatalf("old generation %v with a 32 MiB cap configured and 128 asked for, want 32", old)
	}
}

// Workers with old generations from nothing up to a few MiB either run or stop with
// ERR_WORKER_OUT_OF_MEMORY; none takes the process down. V8 counts the heap's pages
// against the limit, not just what's in them, which matters most in heaps this small.
func TestWorkerTinyHeapsRunOutOfMemory(t *testing.T) {
	got := runJS(t, &runtime.Config{MaxWorkers: -1}, `
const { Worker } = require('worker_threads');
const outcomes = {};
let pending = 0;
for (let mb = 0; mb <= 12; mb += 0.25) {
  for (let k = 0; k < 2; k++) {
    pending++;
    const w = new Worker('const a = []; for (let i = 0; i < 2e4; i++) a.push("x" + i); require("worker_threads").parentPort.postMessage(a.length)',
      { eval: true, resourceLimits: { maxOldGenerationSizeMb: mb } });
    let outcome = 'exited';
    w.on('message', () => { outcome = 'ran'; });
    w.on('error', (e) => { outcome = e.code; });
    w.on('exit', () => {
      outcomes[outcome] = (outcomes[outcome] || 0) + 1;
      if (--pending === 0) done(outcomes);
    });
  }
}
`)
	for outcome := range got {
		if outcome != "ran" && outcome != "ERR_WORKER_OUT_OF_MEMORY" {
			t.Fatalf("outcome %q: %v", outcome, got)
		}
	}
	if got["ran"] == nil || got["ERR_WORKER_OUT_OF_MEMORY"] == nil {
		t.Fatalf("want some workers to run and some to run out: %v", got)
	}
}

// A code range of Infinity, which Node takes as a number (and its own V8 then aborts on),
// reaches the worker and its parent as Infinity, with the other limits beside it.
func TestWorkerResourceLimitsInfiniteCodeRange(t *testing.T) {
	got := runJS(t, &runtime.Config{MaxWorkers: -1}, `
const { Worker } = require('worker_threads');
const w = new Worker('require("worker_threads").parentPort.postMessage(require("worker_threads").resourceLimits.codeRangeSizeMb === Infinity)', { eval: true, resourceLimits: { codeRangeSizeMb: Infinity } });
w.once('online', () => {
  const online = w.resourceLimits;
  w.once('message', (inside) => done({
    online: online.codeRangeSizeMb === Infinity && online.maxOldGenerationSizeMb > 0,
    inside,
  }));
});
`)
	if got["online"] != true || got["inside"] != true {
		t.Fatalf("got %v, want Infinity reported online and inside", got)
	}
}
