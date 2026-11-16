package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
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

// The streaming ChaCha20-Poly1305 context gives the same ciphertext and tag
// as x/crypto's AEAD, and opens what it seals.
func TestCipher_ChaChaPolyMatchesAEAD(t *testing.T) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	rand.Read(key)
	rand.Read(nonce)
	aead, _ := chacha20poly1305.New(key)
	for _, n := range []int{0, 1, 16, 63, 64, 65, 1000} {
		msg := make([]byte, n)
		rand.Read(msg)
		aad := make([]byte, n%37)
		rand.Read(aad)
		want := aead.Seal(nil, nonce, msg, aad)
		for _, decrypt := range []bool{false, true} {
			cc, err := newCipherCtx("chacha20-poly1305", key, nonce, true, decrypt, -1)
			if err != nil {
				t.Fatal(err)
			}
			cc.aadLen, cc.inAAD = uint64(len(aad)), true
			cc.mac.Write(aad)
			in := msg
			if decrypt {
				in = want[:n]
				cc.tag = want[n:]
			}
			var out []byte
			for i := 0; i < len(in); i += 29 {
				part, _ := cc.update(in[i:min(i+29, len(in))])
				out = append(out, part...)
			}
			if _, err := cc.final(); err != nil {
				t.Fatalf("length %d, decrypt %v: %v", n, decrypt, err)
			}
			if decrypt && !bytes.Equal(out, msg) {
				t.Errorf("length %d: opened text differs", n)
			}
			if !decrypt && !bytes.Equal(append(out, cc.tag...), want) {
				t.Errorf("length %d: output differs from Seal", n)
			}
		}
	}
}

// With crypto registered on its own (no string_decoder module), encoded
// output still works, and a character split across updates comes out whole.
func TestCipher_EncodedOutputWithoutStringDecoder(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const key = Buffer.alloc(16, 1), iv = Buffer.alloc(16, 2);
		const e = c.createCipheriv('aes-128-ctr', key, iv);
		const hex = e.update('hello', 'utf8', 'hex') + e.final('hex');
		const ct = Buffer.from(hex, 'hex');
		const text = Buffer.from('hé€😀', 'utf8');
		const ct2 = c.createCipheriv('aes-128-ctr', key, iv).update(text);
		const d = c.createDecipheriv('aes-128-ctr', key, iv);
		let utf8 = '';
		for (let i = 0; i < ct2.length; i++) utf8 += d.update(ct2.subarray(i, i + 1), undefined, 'utf8');
		utf8 += d.final('utf8');
		const u16 = Buffer.from('a😀b', 'utf16le');
		const ct3 = c.createCipheriv('aes-128-ctr', key, iv).update(u16);
		const d16 = c.createDecipheriv('aes-128-ctr', key, iv);
		let s16 = '';
		for (let i = 0; i < ct3.length; i++) s16 += d16.update(ct3.subarray(i, i + 1), undefined, 'utf16le');
		s16 += d16.final('utf16le');
		return JSON.stringify([ct.length, utf8 === text.toString(), s16 === 'a😀b']);
	})()`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.String(); got != "[5,true,true]" {
		t.Errorf("got %s", got)
	}
}
