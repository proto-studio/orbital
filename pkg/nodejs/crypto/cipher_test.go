package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"
)

// The streaming GCM context gives the same ciphertext and tag as Go's AEAD,
// for IVs of several lengths.
func TestCipher_GCMMatchesAEAD(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	block, _ := aes.NewCipher(key)
	for _, ivLen := range []int{1, 8, 12, 16, 60} {
		iv := make([]byte, ivLen)
		rand.Read(iv)
		msg := make([]byte, 1000)
		rand.Read(msg)
		cc, err := newCipherCtx("aes-256-gcm", key, iv, true, false, -1)
		if err != nil {
			t.Fatal(err)
		}
		cc.aad = []byte("aad")
		var out []byte
		for i := 0; i < len(msg); i += 77 {
			part, _ := cc.update(msg[i:min(i+77, len(msg))])
			out = append(out, part...)
		}
		if _, err := cc.final(); err != nil {
			t.Fatal(err)
		}
		aead, _ := cipher.NewGCMWithNonceSize(block, ivLen)
		want := aead.Seal(nil, iv, msg, []byte("aad"))
		if !bytes.Equal(append(out, cc.tag...), want) {
			t.Errorf("IV length %d: output differs from Seal", ivLen)
		}
	}
}

// Only the low 32 bits of GCM's counter count up.
func TestCipher_GCMCounterWrapsIn32Bits(t *testing.T) {
	block, _ := aes.NewCipher(make([]byte, 16))
	start := bytes.Repeat([]byte{0xaa}, 16)
	copy(start[12:], []byte{0xff, 0xff, 0xff, 0xff})
	g := &gcmCTR{b: block, counter: append([]byte{}, start...), ks: make([]byte, 16), used: 16}
	got := make([]byte, 32)
	g.XORKeyStream(got, make([]byte, 32))
	next := append([]byte{}, start[:12]...)
	next = append(next, 0, 0, 0, 0)
	want := make([]byte, 32)
	block.Encrypt(want[:16], start)
	block.Encrypt(want[16:], next)
	if !bytes.Equal(got, want) {
		t.Error("the counter carried past its low 32 bits")
	}
}

// Finishing a cipher frees its context, so the table doesn't grow with use.
func TestCipher_FinalFreesContext(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	if _, err := rt.RunScript(`
		const c = __crypto_module;
		for (let i = 0; i < 50; i++) {
			const e = c.createCipheriv('aes-128-gcm', Buffer.alloc(16), Buffer.alloc(12));
			e.update('x');
			e.final();
			e.getAuthTag();
			const d = c.createDecipheriv('aes-128-cbc', Buffer.alloc(16), Buffer.alloc(16));
			try { d.final(); } catch (err) {}
		}
	`, "test.js"); err != nil {
		t.Fatal(err)
	}
	result, err := rt.RunScript(`__crypto_module._cipherCount()`, "count.js")
	if err != nil {
		t.Fatal(err)
	}
	if n := result.Integer(); n != 0 {
		t.Errorf("%d cipher contexts left after final", n)
	}
}
