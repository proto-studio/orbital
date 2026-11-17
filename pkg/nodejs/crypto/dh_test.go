package crypto

import (
	"math/big"
	"testing"
)

// The key agreement native checks the kinds of the keys behind its handles
// itself, since scripts can call it with any handles.
func TestDH_KeyAgreementChecksKinds(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const out = [];
		for (const type of ['x25519', 'ec']) {
			const opts = type === 'ec' ? { namedCurve: 'P-256' } : undefined;
			const { publicKey, privateKey } = c.generateKeyPairSync(type, opts);
			for (const [a, b] of [[publicKey, publicKey], [publicKey, privateKey]]) {
				try {
					c._keyAgreement(c._keyHandle(a), c._keyHandle(b));
					out.push('returned');
				} catch (e) {
					out.push(String(e.message || e).split('|')[0]);
				}
			}
			out.push(c._keyAgreement(c._keyHandle(privateKey), c._keyHandle(privateKey)).length > 0);
		}
		return JSON.stringify(out);
	})()`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `["ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE","ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE",true,` +
		`"ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE","ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE",true]`
	if got := result.String(); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// dhSafePrime's primes are safe and keep the residue OpenSSL picks for the
// generator.
func TestDHSafePrime(t *testing.T) {
	for _, g := range []int64{2, 5, 3} {
		p, err := dhSafePrime(512, g)
		if err != nil {
			t.Fatal(err)
		}
		q := new(big.Int).Rsh(p, 1)
		if p.BitLen() != 512 || !p.ProbablyPrime(20) || !q.ProbablyPrime(20) {
			t.Fatalf("g=%d: %x is not a 512-bit safe prime", g, p)
		}
		switch g {
		case 2:
			if r := new(big.Int).Mod(p, big.NewInt(24)).Int64(); r != 23 {
				t.Fatalf("g=2: p = %d mod 24, want 23", r)
			}
		case 5:
			if r := new(big.Int).Mod(p, big.NewInt(10)).Int64(); r != 3 {
				t.Fatalf("g=5: p = %d mod 10, want 3", r)
			}
		}
	}
}
