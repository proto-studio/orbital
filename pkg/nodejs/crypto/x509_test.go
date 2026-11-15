package crypto

import (
	gocrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// An RSA-PSS signature must use the salt length the certificate declares
// (the hash length, for the PSS algorithms Go parses), as OpenSSL requires.
func TestX509_PSSSaltLength(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pss"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		SignatureAlgorithm: x509.SHA256WithRSAPSS,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyCertSignature(cert, &key.PublicKey) {
		t.Fatal("the certificate's own signature doesn't verify")
	}
	sum := sha256.Sum256(cert.RawTBSCertificate)
	short, err := rsa.SignPSS(rand.Reader, key, gocrypto.SHA256, sum[:], &rsa.PSSOptions{SaltLength: 20})
	if err != nil {
		t.Fatal(err)
	}
	cert.Signature = short
	if verifyCertSignature(cert, &key.PublicKey) {
		t.Error("a signature with a 20-byte salt verified for a certificate declaring 32")
	}
}
