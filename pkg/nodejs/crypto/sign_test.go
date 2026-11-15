package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"math/big"
	"testing"
)

// An empty-salt PSS check on a key whose modulus is one bit past a byte
// boundary rejects a too-large representative instead of panicking.
func TestSign_PSSNoSaltOddModulus(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1025)
	if err != nil {
		t.Skip("1025-bit keys need GODEBUG=rsa1024min=0:", err)
	}
	sum := sha256.Sum256([]byte("x"))
	sig := new(big.Int).Sub(key.N, big.NewInt(1)).FillBytes(make([]byte, (key.N.BitLen()+7)/8))
	if verifyPSSNoSalt(&key.PublicKey, sha256.New, sum[:], sig) {
		t.Error("N-1 verified")
	}
}

// Signing PSS with an empty salt is refused rather than done in variable time.
func TestSign_PSSNoSaltSigningRefused(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signWith(key, []byte("x"), signParams{digest: "sha256", padding: rsaPSSPadding, saltLength: 0}); err != errUnsupportedOp {
		t.Errorf("got %v, want %v", err, errUnsupportedOp)
	}
}

// Ed25519 JWKs with keys of the wrong size are refused at import, and a
// short key reaching verification or signing is an error, not a panic.
func TestSign_Ed25519WrongKeySize(t *testing.T) {
	seed := "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"
	for _, j := range []*jwk{
		{Kty: "OKP", Crv: "Ed25519", X: ""},
		{Kty: "OKP", Crv: "Ed25519", X: "AAAA"},
		{Kty: "OKP", Crv: "Ed25519", X: seed, D: "AAAA"},
	} {
		if _, err := jwkToEntry(j, j.D != ""); err == nil {
			t.Errorf("%+v imported", *j)
		}
	}
	if ok, err := verifyWith(ed25519.PublicKey{}, []byte("x"), make([]byte, 64), signParams{}); ok || err != nil {
		t.Errorf("verify with an empty key: %v, %v", ok, err)
	}
	if _, err := signWith(ed25519.PrivateKey{1}, []byte("x"), signParams{}); err == nil {
		t.Error("signed with a 1-byte key")
	}
}

// An RSA JWK exponent too large for crypto/rsa is refused rather than
// wrapped around to a negative int.
func TestSign_RSAJWKExponentTooLarge(t *testing.T) {
	j := &jwk{Kty: "RSA", N: "Dw", E: "__________8"}
	if _, err := jwkToEntry(j, false); err == nil {
		t.Error("imported e = 2^64-1")
	}
	if verifyPSSNoSalt(&rsa.PublicKey{N: big.NewInt(15), E: -1}, sha256.New, make([]byte, 32), []byte{0}) {
		t.Error("verified with e = -1")
	}
}

// A CryptoKey is converted to a KeyObject once and reused, so signing with
// it again doesn't add registry entries.
func TestSign_CryptoKeyConvertedOnce(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const { privateKey } = c.generateKeyPairSync('ed25519');
		class CryptoKey { get [Symbol.toStringTag]() { return 'CryptoKey'; } get type() { return 'private'; } }
		const ck = new CryptoKey();
		ck._jwk = privateKey.export({ format: 'jwk' });
		return c._fromCryptoKey(ck) === c._fromCryptoKey(ck) && c.sign(null, Buffer.from('x'), ck).length === 64;
	})()`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Boolean() {
		t.Error("CryptoKey converted more than once")
	}
}

// Unpadded private-key RSA operations are refused rather than done in
// variable time.
func TestPKE_NoPaddingPrivateRefused(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	e := &keyEntry{kind: "private", asymType: "rsa", rsaPriv: key}
	for _, op := range []string{"privateDecrypt", "privateEncrypt"} {
		if _, err := rsaCrypt(op, e, make([]byte, 256), rsaNoPadding, "sha1", nil); err != errNoPaddingPrivate {
			t.Errorf("%s: got %v, want %v", op, err, errNoPaddingPrivate)
		}
	}
}
