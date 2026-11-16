package crypto

import (
	"testing"

	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

func newKeyRuntime(t *testing.T) (*runtime.Runtime, *Crypto) {
	t.Helper()
	rt, err := runtime.New(&runtime.Config{EnableTimers: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.New().Register(rt); err != nil {
		rt.Dispose()
		t.Fatal(err)
	}
	c := New()
	if err := c.Register(rt); err != nil {
		rt.Dispose()
		t.Fatal(err)
	}
	return rt, c
}

func keyCount(c *Crypto) int {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	return len(c.keys)
}

// collect runs a full GC and lets FinalizationRegistry callbacks run.
func collect(t *testing.T, rt *runtime.Runtime) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if err := rt.Isolate().RequestGarbageCollectionForTesting(v8.FullGarbageCollection); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.RunScript(`0`, "tick.js"); err != nil {
			t.Fatal(err)
		}
		rt.EventLoop().Run()
	}
}

// A KeyObject's native key is freed once the KeyObject is collected, so
// making and dropping keys keeps the registry bounded.
func TestKeyObject_FreedWhenCollected(t *testing.T) {
	rt, c := newKeyRuntime(t)
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		globalThis.kept = __crypto_module.createSecretKey(Buffer.from('kept'));
		(() => {
			const c = __crypto_module;
			const pair = c.generateKeyPairSync('ed25519');
			const pem = pair.privateKey.export({ format: 'pem', type: 'pkcs8' });
			const pubPem = pair.publicKey.export({ format: 'pem', type: 'spki' });
			for (let i = 0; i < 300; i++) {
				c.createSecretKey(Buffer.from('k' + i));
				c.createPrivateKey(pem);
				c.createPublicKey(pubPem);
			}
		})();
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	if n := keyCount(c); n < 900 {
		t.Fatalf("only %d keys before collection; the test makes 900", n)
	}
	collect(t, rt)
	if n := keyCount(c); n > 50 {
		t.Errorf("%d keys left after collection", n)
	}
	got, err := rt.RunScript(`kept.export().toString()`, "kept.js")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "kept" {
		t.Errorf("a live key was freed: export gave %q", got.String())
	}
}

// createPrivateKey/createPublicKey of a KeyObject make a KeyObject with its
// own handle, so collecting one leaves the other working.
func TestKeyObject_CopiesHaveOwnHandles(t *testing.T) {
	rt, c := newKeyRuntime(t)
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		const kc = __crypto_module;
		globalThis.orig = kc.generateKeyPairSync('ed25519').privateKey;
		(() => { for (let i = 0; i < 100; i++) kc.createPrivateKey(orig); })();
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	collect(t, rt)
	got, err := rt.RunScript(`orig.export({ format: 'jwk' }).crv`, "orig.js")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Ed25519" {
		t.Errorf("original key after its copies were collected: %q", got.String())
	}
	_ = c
}

// Cipher contexts that are never finished are freed once their Cipheriv is
// collected.
func TestCipher_FreedWhenCollected(t *testing.T) {
	rt, _ := newKeyRuntime(t)
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		(() => {
			const c = __crypto_module;
			for (let i = 0; i < 300; i++) {
				c.createCipheriv('aes-128-cbc', Buffer.alloc(16), Buffer.alloc(16)).update('x');
			}
		})();
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		v, err := rt.RunScript(`__crypto_module._cipherCount()`, "count.js")
		if err != nil {
			t.Fatal(err)
		}
		return v.Integer()
	}
	if n := count(); n < 300 {
		t.Fatalf("only %d contexts before collection; the test makes 300", n)
	}
	collect(t, rt)
	if n := count(); n > 20 {
		t.Errorf("%d cipher contexts left after collection", n)
	}
}
