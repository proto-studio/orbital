package nodejs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// markerClient is an injected HTTP client that stands in for one carrying its
// parent's embedding context. Every call is answered with the owner's marker.
type markerClient struct {
	marker string
	calls  atomic.Int64
}

func (c *markerClient) Do(ctx context.Context, req *runtime.Request) (*runtime.Response, error) {
	c.calls.Add(1)
	return &runtime.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Headers:    map[string][]string{"content-type": {"text/plain"}},
		Body:       []byte(c.marker),
	}, nil
}

func (c *markerClient) Get(ctx context.Context, url string) (*runtime.Response, error) {
	return c.Do(ctx, &runtime.Request{Method: "GET", URL: url})
}

func (c *markerClient) Post(ctx context.Context, url, contentType string, body []byte) (*runtime.Response, error) {
	return c.Do(ctx, &runtime.Request{Method: "POST", URL: url})
}

const probeJS = `
async function probe() {
  const res = await fetch('http://marker.test/');
  return {
    fs: require('fs').readFileSync('/marker.txt', 'utf8'),
    env: process.env.MARKER,
    net: await res.text(),
  };
}
`

const workerJS = probeJS + `
const { parentPort, workerData } = require('worker_threads');
probe().then(async (self) => {
  // Nested children must inherit the same configuration.
  if (workerData && workerData.nest) {
    const { Worker } = require('worker_threads');
    const nested = new Worker('/worker.js');
    self.nested = await new Promise((resolve, reject) => {
      nested.once('message', resolve);
      nested.once('error', reject);
    });
  }
  parentPort.postMessage(self);
}).catch((e) => parentPort.postMessage({ workerFailure: String(e && e.stack || e) }));
`

// hooks.mjs runs in the isolated loader realm and reports what that realm observes.
const hooksMJS = `
import fs from 'node:fs';
import { Worker } from 'node:worker_threads';
export async function load(url, context, next) {
  if (url.endsWith('probe.mjs')) {
    const res = await fetch('http://marker.test/');
    const seen = {
      fs: fs.readFileSync('/marker.txt', 'utf8'),
      env: process.env.MARKER,
      net: await res.text(),
    };
    // A worker started from inside the loader realm.
    seen.worker = await new Promise((resolve, reject) => {
      const worker = new Worker('/worker.js');
      worker.once('message', resolve);
      worker.once('error', reject);
    });
    return { format: 'module', shortCircuit: true, source: 'export default ' + JSON.stringify(seen) };
  }
  return next(url, context);
}
`

// parentFixture is a parent with its own filesystem, environment and network marker.
type parentFixture struct {
	marker string
	client *markerClient
	inst   *Instance
}

func newParent(t *testing.T, marker string) *parentFixture {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"marker.txt": marker,
		"worker.js":  workerJS,
		"hooks.mjs":  hooksMJS,
		"probe.mjs":  "export default 'hooks did not run'",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	client := &markerClient{marker: marker}
	inst, err := New(&runtime.Config{
		EnableConsole: true,
		EnableTimers:  true,
		Filesystem:    runtime.NewLocalFilesystem(root),
		Environment:   runtime.NewSandboxedEnvironment(map[string]string{"MARKER": marker}),
		HTTPClient:    client,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inst.Runtime.Dispose)
	return &parentFixture{marker: marker, client: client, inst: inst}
}

// startWorker runs a real Worker through JavaScript and returns what it observed.
func (p *parentFixture) startWorker(t *testing.T, data string) map[string]any {
	t.Helper()
	return runIn(t, p.inst, fmt.Sprintf(`
{
  const { Worker } = require('worker_threads');
  const worker = new Worker('/worker.js', { workerData: %s });
  worker.once('error', (e) => done({ error: e.message }));
  worker.once('message', (m) => done(m));
}
`, data))
}

// startLoader registers a real hook and imports through the real loader realm.
func (p *parentFixture) startLoader(t *testing.T) map[string]any {
	t.Helper()
	val, err := p.inst.ESM.RunModule(`
import { register } from 'node:module';
register('/hooks.mjs', 'file:///');
const probe = await import('/probe.mjs');
globalThis.__result = JSON.stringify(probe.default);
`, "/main.mjs")
	if err != nil {
		t.Fatalf("loader run failed: %v", err)
	}
	_ = val
	p.inst.Runtime.EventLoop().Run()
	return resultOf(t, p.inst)
}

func wantMarkers(t *testing.T, label string, got any, marker string) {
	t.Helper()
	seen, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("%s: no observations: %v", label, got)
	}
	for _, key := range []string{"fs", "env", "net"} {
		if seen[key] != marker {
			t.Fatalf("%s: %s = %v, want parent %s's marker (observed %v)", label, key, seen[key], marker, seen)
		}
	}
}

func TestChildrenUseOwningParentAfterLaterParentExists(t *testing.T) {
	a := newParent(t, "A")
	b := newParent(t, "B")

	wantMarkers(t, "A worker", a.startWorker(t, "null"), "A")
	wantMarkers(t, "A loader", a.startLoader(t), "A")

	wantMarkers(t, "B worker", b.startWorker(t, "null"), "B")
	wantMarkers(t, "B loader", b.startLoader(t), "B")

	// Interleaved: back to A, after B has made children of its own.
	wantMarkers(t, "A worker again", a.startWorker(t, "null"), "A")
}

func TestLoaderRealmCreatedAfterLaterParentExists(t *testing.T) {
	// Reverse creation order relative to first use, so a "last writer wins" provider
	// fails for the first parent used.
	b := newParent(t, "B")
	a := newParent(t, "A")
	wantMarkers(t, "B loader", b.startLoader(t), "B")
	wantMarkers(t, "A loader", a.startLoader(t), "A")
}

func TestNestedChildrenInheritConfiguration(t *testing.T) {
	a := newParent(t, "A")
	_ = newParent(t, "B") // Exists throughout, and must never be observed.

	result := a.startWorker(t, "{ nest: true }")
	wantMarkers(t, "worker", result, "A")
	wantMarkers(t, "nested worker", result["nested"], "A")

	loader := a.startLoader(t)
	wantMarkers(t, "loader realm", loader, "A")
	wantMarkers(t, "worker inside loader realm", loader["worker"], "A")
}

func TestConcurrentParentsKeepTheirOwnChildren(t *testing.T) {
	const parents = 6
	var wg sync.WaitGroup
	errs := make(chan string, parents*2)
	for i := range parents {
		wg.Add(1)
		go func(marker string) {
			defer wg.Done()
			goruntime.LockOSThread()
			defer goruntime.UnlockOSThread()

			root, err := os.MkdirTemp(t.TempDir(), "parent")
			if err != nil {
				errs <- err.Error()
				return
			}
			os.WriteFile(filepath.Join(root, "marker.txt"), []byte(marker), 0o644)
			os.WriteFile(filepath.Join(root, "worker.js"), []byte(workerJS), 0o644)
			inst, err := New(&runtime.Config{
				EnableConsole: true,
				EnableTimers:  true,
				Filesystem:    runtime.NewLocalFilesystem(root),
				Environment:   runtime.NewSandboxedEnvironment(map[string]string{"MARKER": marker}),
				HTTPClient:    &markerClient{marker: marker},
				Timeout:       30 * time.Second,
			})
			if err != nil {
				errs <- err.Error()
				return
			}
			defer inst.Runtime.Dispose()

			// Several workers, started while other parents are being created.
			script := `
const { Worker } = require('worker_threads');
const seen = [];
let pending = 3;
for (let i = 0; i < 3; i++) {
  const w = new Worker('/worker.js');
  w.once('error', (e) => { globalThis.__result = JSON.stringify({ error: e.message }); });
  w.once('message', (m) => {
    seen.push(m);
    if (--pending === 0) globalThis.__result = JSON.stringify({ seen });
  });
}
`
			if _, err := inst.Runtime.Run(script, "test.js"); err != nil {
				errs <- err.Error()
				return
			}
			val, err := inst.Runtime.GetGlobal("__result")
			if err != nil || val == nil || val.IsUndefined() {
				errs <- marker + ": no result"
				return
			}
			want := fmt.Sprintf(`{"fs":"%[1]s","env":"%[1]s","net":"%[1]s"}`, marker)
			wantAll := fmt.Sprintf(`{"seen":[%[1]s,%[1]s,%[1]s]}`, want)
			if got := val.String(); got != wantAll {
				errs <- fmt.Sprintf("parent %s observed %s", marker, got)
			}
		}(fmt.Sprintf("P%d", i))
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestDisposedParentStopsItsWorkerWithoutAffectingOthers(t *testing.T) {
	a := newParent(t, "A")
	b := newParent(t, "B")

	// A long-running child of A that keeps using A's HTTP client.
	if _, err := a.inst.Runtime.RunScript(`
const { Worker } = require('worker_threads');
globalThis.w = new Worker("setInterval(() => fetch('http://marker.test/'), 5);", { eval: true });
`, "start.js"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for a.client.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	a.inst.Runtime.Dispose()

	// The worker stops: the count settles once in-flight calls drain.
	var settled int64
	for range 100 {
		settled = a.client.calls.Load()
		time.Sleep(20 * time.Millisecond)
		if a.client.calls.Load() == settled {
			break
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := a.client.calls.Load(); got != settled {
		t.Fatalf("worker kept running after its parent was disposed (%d -> %d calls)", settled, got)
	}

	// Another parent is unaffected, and later children still get its configuration.
	wantMarkers(t, "B worker", b.startWorker(t, "null"), "B")
	wantMarkers(t, "B loader", b.startLoader(t), "B")
}

func TestDisposingParentDisposesLoaderRealm(t *testing.T) {
	a := newParent(t, "A")
	wantMarkers(t, "A loader", a.startLoader(t), "A")
	before := a.inst.Runtime.ResourceTracker().Count()
	if before == 0 {
		t.Fatal("loader realm is not tracked by its parent")
	}
	a.inst.Runtime.Dispose()
	if got := a.inst.Runtime.ResourceTracker().Count(); got != 0 {
		t.Fatalf("%d resources left after dispose", got)
	}
}

func TestWorkerEntryIsReadThroughInjectedFilesystem(t *testing.T) {
	// The file exists on the host but outside this parent's filesystem.
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte(`require('worker_threads').parentPort.postMessage({ escaped: true });`), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newParent(t, "A")
	result := runIn(t, a.inst, fmt.Sprintf(`
const { Worker } = require('worker_threads');
const w = new Worker(%q);
w.once('message', (m) => done(m));
w.once('error', (e) => done({ error: e.message }));
`, outside))
	if result["escaped"] == true {
		t.Fatal("worker entry was read from the host filesystem")
	}
	if result["error"] == nil {
		t.Fatalf("expected an error, result = %v", result)
	}
}

func TestWithoutProviderWorkerIsUnavailable(t *testing.T) {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := Register(rt); err != nil {
		t.Fatal(err)
	}
	// A provider belonging to another instance must not be picked up.
	_ = newParent(t, "A")
	val, err := rt.RunScript(`
try { new (require('worker_threads').Worker)('/x.js'); 'constructed' } catch (e) { e.message }
`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	if got := val.String(); got != "worker_threads.Worker is not available in this runtime" {
		t.Fatalf("got %q", got)
	}
}
