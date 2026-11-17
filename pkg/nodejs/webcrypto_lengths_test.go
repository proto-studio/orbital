package nodejs

import (
	"strings"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Each WebCrypto native that takes a script-chosen size or length checks it
// itself: an oversized value is an error, not an attempt.
func TestWebCryptoNatives_RefuseOversizedLengths(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const n = __webcrypto_native;
const err = (f) => { try { f(); return 'returned'; } catch (e) { return String(e.message); } };
const rsa = JSON.parse(n.generateKey(JSON.stringify({ name: 'RSA-PSS', modulusLength: 1024 })));
done({
  hmacKey: err(() => n.generateKey(JSON.stringify({ name: 'HMAC', hash: 'SHA-256', length: 4294967288 }))),
  aesKey: err(() => n.generateKey(JSON.stringify({ name: 'AES-GCM', length: -8 }))),
  rsaKey: err(() => n.generateKey(JSON.stringify({ name: 'RSA-PSS', modulusLength: 20000 }))),
  pbkdf2: err(() => n.pbkdf2(JSON.stringify({ passwordB64: '', saltB64: '', iterations: 1, hash: 'SHA-256', length: 4294967288 }))),
  hkdf: err(() => n.hkdf(JSON.stringify({ keyB64: 'AA==', saltB64: '', infoB64: '', hash: 'SHA-256', length: 4294967288 }))),
  pssSalt: err(() => n.sign(JSON.stringify({ name: 'RSA-PSS', hash: 'SHA-256', saltLength: 2147483647 }), JSON.stringify(rsa.privateKey), 'AA==')),
  pssSaltVerify: String(n.verify(JSON.stringify({ name: 'RSA-PSS', hash: 'SHA-256', saltLength: 2147483647 }), JSON.stringify(rsa.publicKey), 'AA==', 'AA==')),
  nodeRSA: err(() => require('crypto').generateKeyPairSync('rsa', { modulusLength: 20000 })),
});
`)
	for name, want := range map[string]string{
		"hmacKey": "out of range",
		"aesKey":  "out of range",
		"rsaKey":  "modulus too large",
		"pbkdf2":  "out of range",
		"hkdf":    "hkdf",
		"pssSalt": "too long for the key",
		"nodeRSA": "modulus too large",
	} {
		if s, _ := got[name].(string); !strings.Contains(s, want) {
			t.Errorf("%s = %v, want an error containing %q", name, got[name], want)
		}
	}
	if got["pssSaltVerify"] != "false" {
		t.Errorf("pssSaltVerify = %v, want false", got["pssSaltVerify"])
	}
}
