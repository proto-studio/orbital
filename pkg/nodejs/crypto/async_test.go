package crypto

import (
	"sync/atomic"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
)

// The callback forms of randomBytes and generateKeyPair return before their
// work runs: the work waits here on the thread pool until the script has returned.
func TestCrypto_CallbackWorkRunsOffThread(t *testing.T) {
	scripts := map[string]string{
		"randomBytes": `__crypto_module.randomBytes(16, (err, buf) => {
			globalThis.result = String(err) + ' ' + buf.length;
		});`,
		"generateKeyPair": `__crypto_module.generateKeyPair('ed25519', (err, pub, priv) => {
			globalThis.result = String(err) + ' ' + pub.type + ' ' + priv.type;
		});`,
		"sign": `(() => {
			const c = __crypto_module;
			const { privateKey, publicKey } = c.generateKeyPairSync('ed25519');
			c.sign(null, Buffer.from('x'), privateKey, (err, sig) => {
				globalThis.result = String(err) + ' ' + sig.length;
			});
		})();`,
	}
	want := map[string]string{
		"randomBytes":     "null 16",
		"generateKeyPair": "null public private",
		"sign":            "null 64",
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			rt := setupRuntime(t)
			defer rt.Dispose()

			release := make(chan struct{})
			started := make(chan struct{})
			beforeOffThreadWork = func() {
				close(started)
				<-release
			}
			defer func() { beforeOffThreadWork = nil }()
			// If the work ran on the JS thread, RunScript would wait here
			// for it, so let it go after a while rather than hang.
			timeout := time.AfterFunc(5*time.Second, func() { close(release) })

			if _, err := rt.RunScript(script, "test.js"); err != nil {
				t.Fatalf("RunScript failed: %v", err)
			}
			if !timeout.Stop() {
				t.Fatal("the script waited for the work to finish")
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("the work never started")
			}
			close(release)

			rt.EventLoop().Run()
			got, err := rt.RunScript("globalThis.result", "result.js")
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != want[name] {
				t.Errorf("callback got %q, want %q", got.String(), want[name])
			}
		})
	}
}

// Callback work shares the runtime's thread pool: with ThreadPoolSize 2, no
// more than two tasks run at once, however many are started, and every
// callback still runs.
func TestCrypto_CallbackWorkUsesBoundedPool(t *testing.T) {
	rt, err := runtime.New(&runtime.Config{ThreadPoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if err := buffer.New().Register(rt); err != nil {
		t.Fatal(err)
	}
	if err := New().Register(rt); err != nil {
		t.Fatal(err)
	}

	var running, peak int32
	beforeOffThreadWork = func() {
		n := atomic.AddInt32(&running, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&running, -1)
	}
	defer func() { beforeOffThreadWork = nil }()

	if _, err := rt.RunScript(`
		globalThis.done = 0;
		const c = __crypto_module;
		for (let i = 0; i < 40; i++) c.randomBytes(8, (err) => { if (!err) done++; });
		for (let i = 0; i < 10; i++) c.generateKeyPair('ed25519', (err) => { if (!err) done++; });
		for (let i = 0; i < 10; i++) c.pbkdf2('pw', 'salt', 1, 16, 'sha256', (err) => { if (!err) done++; });
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	rt.EventLoop().Run()
	got, err := rt.RunScript("done", "result.js")
	if err != nil {
		t.Fatal(err)
	}
	if got.Integer() != 60 {
		t.Errorf("%d of 60 callbacks ran", got.Integer())
	}
	if peak > 2 {
		t.Errorf("%d tasks ran at once with ThreadPoolSize 2", peak)
	}
	if peak < 1 {
		t.Error("no task ran on the pool")
	}
}

// Killing the runtime drops the callback work still queued on its pool: with
// one thread busy, the ten calls queued behind it never run.
func TestCrypto_KillDropsQueuedWork(t *testing.T) {
	rt, err := runtime.New(&runtime.Config{ThreadPoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if err := buffer.New().Register(rt); err != nil {
		t.Fatal(err)
	}
	if err := New().Register(rt); err != nil {
		t.Fatal(err)
	}
	var ran int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	beforeOffThreadWork = func() {
		if atomic.AddInt32(&ran, 1) == 1 {
			started <- struct{}{}
			<-release
		}
	}
	defer func() { beforeOffThreadWork = nil }()
	if _, err := rt.RunScript(`
		for (let i = 0; i < 11; i++) __crypto_module.randomBytes(8, () => {});
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	<-started
	rt.Kill("test")
	close(release)
	time.Sleep(50 * time.Millisecond)
	if n := atomic.LoadInt32(&ran); n != 1 {
		t.Errorf("%d tasks ran after Kill, want only the one already running", n)
	}
}
