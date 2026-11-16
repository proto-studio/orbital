package crypto

import (
	gocrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
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

// checkPrivateKey compares in Go from the key's PKCS#8 form, so checking the
// same key again doesn't add key registry entries.
func TestX509_CheckPrivateKeyAddsNoKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "k"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Dispose()
	if err := buffer.New().Register(rt); err != nil {
		t.Fatal(err)
	}
	c := New()
	if err := c.Register(rt); err != nil {
		t.Fatal(err)
	}
	script := `(() => {
		const crypto = __crypto_module;
		const cert = new crypto.X509Certificate(Buffer.from('` + base64.StdEncoding.EncodeToString(der) + `', 'base64'));
		const key = crypto.createPrivateKey(Buffer.from('` + base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})) + `', 'base64').toString());
		globalThis.check = () => cert.checkPrivateKey(key);
		return check();
	})()`
	result, err := rt.RunScript(script, "test.js")
	if err != nil || !result.Boolean() {
		t.Fatalf("checkPrivateKey: %v, %v", result, err)
	}
	before := len(c.keys)
	for i := 0; i < 5; i++ {
		if _, err := rt.RunScript("check()", "test.js"); err != nil {
			t.Fatal(err)
		}
	}
	if after := len(c.keys); after != before {
		t.Errorf("registry grew from %d to %d keys", before, after)
	}
}
