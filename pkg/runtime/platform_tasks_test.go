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
