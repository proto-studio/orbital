package crypto

import (
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
