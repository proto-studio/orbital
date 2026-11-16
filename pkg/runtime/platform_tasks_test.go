package runtime

import (
	"testing"
	"time"
)

// FinalizationRegistry callbacks run in a later poll phase, not inside the
// callback that dropped the objects, as in Node.
func TestRuntime_FinalizationRegistryRunsBetweenCallbacks(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		globalThis.inCallback = false;
		globalThis.cleaned = [];
		globalThis.duringCallback = 0;
		globalThis.registry = new FinalizationRegistry((held) => {
			cleaned.push(held);
			if (inCallback) duringCallback++;
		});
	`, "setup.js"); err != nil {
		t.Fatal(err)
	}
	var cleanedInside int64 = -1
	rt.EventLoop().EnqueueMicrotask(func() {
		if _, err := rt.RunScript(`
			inCallback = true;
			(() => { for (let i = 0; i < 50; i++) registry.register({}, i); })();
			gc();
		`, "callback.js"); err != nil {
			t.Error(err)
		}
		// Still inside the callback: nothing has been cleaned up yet.
		v, err := rt.RunScript(`const n = cleaned.length; inCallback = false; n`, "inside.js")
		if err != nil {
			t.Error(err)
			return
		}
		cleanedInside = v.Integer()
	})
	// A later turn, as a pending timer gives in Node: cleanup runs in a poll phase,
	// and the loop ends without one when nothing keeps it alive.
	rt.EventLoop().SetTimeout(func() {}, 10*time.Millisecond)
	rt.EventLoop().Run()

	if cleanedInside != 0 {
		t.Errorf("%d cleanup callbacks ran inside the callback", cleanedInside)
	}
	v, err := rt.RunScript(`JSON.stringify([cleaned.length, duringCallback])`, "result.js")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "[50,0]" {
		t.Errorf("cleaned, during callback = %s, want [50,0]", v.String())
	}
}

// An embedder driving the loop with RunOnce runs FinalizationRegistry
// cleanup too, and RunOnce reports the call that ran it as work done.
func TestRuntime_RunOnceRunsPlatformTasks(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		globalThis.cleaned = 0;
		const reg = new FinalizationRegistry(() => { cleaned++; });
		(() => { for (let i = 0; i < 20; i++) reg.register({}, i); })();
		gc();
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	if !rt.EventLoop().RunOnce() {
		t.Error("RunOnce reported no work although it ran cleanup")
	}
	for i := 0; i < 10 && rt.EventLoop().RunOnce(); i++ {
	}
	v, err := rt.RunScript(`cleaned`, "result.js")
	if err != nil {
		t.Fatal(err)
	}
	if v.Integer() != 20 {
		t.Errorf("%d of 20 cleanup callbacks ran through RunOnce", v.Integer())
	}
}

// RunUntil checks its predicate after each platform task, as after any other
// callback: once one registry's cleanup satisfies it, the next doesn't run.
func TestRuntime_RunUntilChecksAfterEachPlatformTask(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		globalThis.cleaned = 0;
		const r1 = new FinalizationRegistry(() => { cleaned++; });
		const r2 = new FinalizationRegistry(() => { cleaned++; });
		(() => { r1.register({}, 1); r2.register({}, 2); })();
		gc();
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		v, err := rt.RunScript(`cleaned`, "count.js")
		if err != nil {
			t.Fatal(err)
		}
		return v.Integer()
	}
	if !rt.EventLoop().RunUntil(func() bool { return count() >= 1 }) {
		t.Fatal("RunUntil didn't report its predicate satisfied")
	}
	if n := count(); n != 1 {
		t.Errorf("%d cleanups ran before RunUntil returned, want 1", n)
	}
}

// A native completion that RunOnce runs can leave cleanup behind; RunOnce
// runs it before reporting the loop idle, so a "for RunOnce() {}" driver
// doesn't stop with it still queued.
func TestRuntime_RunOnceRunsCleanupItsCallbacksLeave(t *testing.T) {
	rt, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		globalThis.cleaned = 0;
		globalThis.reg = new FinalizationRegistry(() => { cleaned++; });
	`, "setup.js"); err != nil {
		t.Fatal(err)
	}
	rt.EventLoop().EnqueueMicrotask(func() {
		if _, err := rt.RunScript(`(() => { for (let i = 0; i < 5; i++) reg.register({}, i); })(); gc();`, "drop.js"); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 10 && rt.EventLoop().RunOnce(); i++ {
	}
	v, err := rt.RunScript(`cleaned`, "result.js")
	if err != nil {
		t.Fatal(err)
	}
	if v.Integer() != 5 {
		t.Errorf("%d of 5 cleanups ran by the time RunOnce reported no work", v.Integer())
	}
}
