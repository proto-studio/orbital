package crypto

import (
	"testing"
	"time"
)

// The callback forms of randomBytes and generateKeyPair return before their
// work runs: the work waits here on a goroutine until the script has returned.
func TestCrypto_CallbackWorkRunsOffThread(t *testing.T) {
	scripts := map[string]string{
		"randomBytes": `__crypto_module.randomBytes(16, (err, buf) => {
			globalThis.result = String(err) + ' ' + buf.length;
		});`,
		"generateKeyPair": `__crypto_module.generateKeyPair('ed25519', (err, pub, priv) => {
			globalThis.result = String(err) + ' ' + pub.type + ' ' + priv.type;
		});`,
	}
	want := map[string]string{
		"randomBytes":     "null 16",
		"generateKeyPair": "null public private",
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
