package nodejs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runWorkersCapped runs source with exactly the MaxWorkers cfg sets, unlike runJS, and
// with the host access runJS gives.
func runWorkersCapped(t *testing.T, cfg *runtime.Config, source string) map[string]any {
	t.Helper()
	cfg.EnableConsole, cfg.EnableTimers, cfg.Timeout = true, true, 20*time.Second
	inst, err := New(hostAccess(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	return runIn(t, inst, `const describe = (e) => e ? [e.name, e.code, e.message].join('|') : 'none';
`+source)
}

// writeWorker writes a worker script into a temporary directory and returns its path.
func writeWorker(t *testing.T, name, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const workerInitFailed = "Error|ERR_WORKER_INIT_FAILED|Worker initialization failure: Worker limit reached"

// A worker over the cap fails as Node reports a worker it couldn't start: 'error' with
// ERR_WORKER_INIT_FAILED, then 'exit' with code 1. A worker that has exited frees its place.
func TestWorkerCap_StartingOneTooManyFails(t *testing.T) {
	waits := writeWorker(t, "waits.js", `
const { parentPort } = require('worker_threads');
parentPort.postMessage('started');
parentPort.on('message', () => process.exit(0));
`)
	quick := writeWorker(t, "quick.js", `require('worker_threads').parentPort.postMessage('ran');`)
	got := runWorkersCapped(t, &runtime.Config{MaxWorkers: 1}, `
const { Worker } = require('worker_threads');
const results = {};
const first = new Worker(`+strconv.Quote(waits)+`);
first.once('message', () => {
  const second = new Worker(`+strconv.Quote(quick)+`);
  second.on('error', (e) => { results.second = describe(e); });
  second.on('exit', (code) => {
    results.secondExit = code;
    first.postMessage('stop');
  });
});
first.on('exit', () => {
  const third = new Worker(`+strconv.Quote(quick)+`);
  third.on('message', (m) => { results.third = m; });
  third.on('exit', () => done(results));
});
`)
	wantJSON(t, got, map[string]any{"second": workerInitFailed, "secondExit": float64(1), "third": "ran"})
}

// With the default of 0, no worker starts and module.register throws, since Node runs
// loader hooks on a worker of their own.
func TestWorkerCap_DefaultAllowsNone(t *testing.T) {
	quick := writeWorker(t, "quick.js", `require('worker_threads').parentPort.postMessage('ran');`)
	hooks := writeWorker(t, "hooks.mjs", `export async function load(u, c, n) { return n(u, c); }`)
	got := runWorkersCapped(t, &runtime.Config{}, `
const results = {};
try { require('node:module').register(`+strconv.Quote("file://"+hooks)+`); results.register = 'registered'; }
catch (e) { results.register = describe(e); }
const w = new (require('worker_threads').Worker)(`+strconv.Quote(quick)+`);
w.on('error', (e) => { results.worker = describe(e); });
w.on('exit', (code) => { results.exit = code; done(results); });
`)
	wantJSON(t, got, map[string]any{"register": workerInitFailed, "worker": workerInitFailed, "exit": float64(1)})
}

// A loader realm takes a place like a worker does.
func TestWorkerCap_LoaderRealmCounts(t *testing.T) {
	quick := writeWorker(t, "quick.js", `require('worker_threads').parentPort.postMessage('ran');`)
	hooks := writeWorker(t, "hooks.mjs", `export async function load(u, c, n) { return n(u, c); }`)
	got := runWorkersCapped(t, &runtime.Config{MaxWorkers: 1}, `
const results = {};
require('node:module').register(`+strconv.Quote("file://"+hooks)+`);
const w = new (require('worker_threads').Worker)(`+strconv.Quote(quick)+`);
w.on('error', (e) => { results.worker = describe(e); });
w.on('exit', () => done(results));
`)
	wantJSON(t, got, map[string]any{"worker": workerInitFailed})
}

// Nested workers count against the top runtime's cap.
func TestWorkerCap_NestedWorkersShareTheCap(t *testing.T) {
	quick := writeWorker(t, "quick.js", `require('worker_threads').parentPort.postMessage('ran');`)
	outer := writeWorker(t, "outer.js", `
const { Worker, parentPort } = require('worker_threads');
const inner = new Worker(`+strconv.Quote(quick)+`);
inner.on('error', (e) => parentPort.postMessage(e.code));
inner.on('message', (m) => parentPort.postMessage(m));
`)
	got := runWorkersCapped(t, &runtime.Config{MaxWorkers: 1}, `
const w = new (require('worker_threads').Worker)(`+strconv.Quote(outer)+`);
w.on('message', (m) => { done({ inner: m }); w.terminate(); });
`)
	wantJSON(t, got, map[string]any{"inner": "ERR_WORKER_INIT_FAILED"})
}
