package crypto

import (
	"strings"
	"testing"
)

// rsaPublicExponentProbe generates RSA keys with various publicExponent values
// and records the exponent each key ends up with, or the error. It runs as-is on
// Node and on Orbital; rsaPublicExponentWant is its output on Node v20.18.1.
const rsaPublicExponentProbe = `
const crypto = __crypto_module;
const out = [];
const tryit = (f) => { try { return f(); } catch (e) { return e.name + ' ' + e.code + ' ' + e.message; } };
for (const e of [3, 17, 257, 65537, 0x7fffffff, null, undefined]) {
  out.push(tryit(() => {
    const { publicKey, privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 1024, publicExponent: e });
    const pj = privateKey.export({ format: 'jwk' });
    const back = crypto.createPrivateKey(privateKey.export({ format: 'pem', type: 'pkcs8' })).export({ format: 'jwk' });
    const n = Buffer.from(pj.n, 'base64url');
    return [publicKey.export({ format: 'jwk' }).e, pj.e, back.e, back.n === pj.n, n.length * 8, (n[0] & 0x80) !== 0].join(' ');
  }));
}
for (const e of [1, 2, 4, 65536, -1, 1.5, NaN, 2 ** 32, 2 ** 33, '3', 3n, true, {}, [3]]) {
  out.push(tryit(() => { crypto.generateKeyPairSync('rsa', { modulusLength: 1024, publicExponent: e }); return 'ok'; }));
}
out.push(tryit(() => { crypto.generateKeyPair('rsa', { modulusLength: 1024, publicExponent: '3' }, () => {}); return 'no throw'; }));
crypto.generateKeyPair('rsa', { modulusLength: 1024, publicExponent: 3 }, (err, pub) => {
  out.push(String(err) + ' ' + pub.export({ format: 'jwk' }).e);
  crypto.generateKeyPair('rsa', { modulusLength: 1024, publicExponent: 4 }, (err, pub) => {
    out.push(err.message + ' ' + pub);
    globalThis.__rsaOut = JSON.stringify(out, null, 1);
  });
});
`

var rsaPublicExponentWant = `[
 "Aw Aw Aw true 1024 true",
 "EQ EQ EQ true 1024 true",
 "AQE AQE AQE true 1024 true",
 "AQAB AQAB AQAB true 1024 true",
 "f____w f____w f____w true 1024 true",
 "AQAB AQAB AQAB true 1024 true",
 "AQAB AQAB AQAB true 1024 true",
 "Error undefined error:020000B2:rsa routines::pub exponent out of range",
 "Error undefined error:020000B2:rsa routines::pub exponent out of range",
 "Error undefined error:020000B2:rsa routines::pub exponent out of range",
 "Error undefined error:020000B2:rsa routines::pub exponent out of range",
 "RangeError ERR_OUT_OF_RANGE The value of \"options.publicExponent\" is out of range. It must be >= 0 && <= 4294967295. Received -1",
 "RangeError ERR_OUT_OF_RANGE The value of \"options.publicExponent\" is out of range. It must be an integer. Received 1.5",
 "RangeError ERR_OUT_OF_RANGE The value of \"options.publicExponent\" is out of range. It must be an integer. Received NaN",
 "RangeError ERR_OUT_OF_RANGE The value of \"options.publicExponent\" is out of range. It must be >= 0 && <= 4294967295. Received 4294967296",
 "RangeError ERR_OUT_OF_RANGE The value of \"options.publicExponent\" is out of range. It must be >= 0 && <= 4294967295. Received 8_589_934_592",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received type string ('3')",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received type bigint (3n)",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received type boolean (true)",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received an instance of Object",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received an instance of Array",
 "TypeError ERR_INVALID_ARG_TYPE The \"options.publicExponent\" property must be of type number. Received type string ('3')",
 "null Aw",
 "error:020000B2:rsa routines::pub exponent out of range undefined"
]`

func TestCrypto_GenerateKeyPairRSAPublicExponent(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	if _, err := rt.Run(rsaPublicExponentProbe, "rsa_exponent.js"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	got, err := rt.RunScript("globalThis.__rsaOut", "result.js")
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	gotLines := strings.Split(got.String(), "\n")
	wantLines := strings.Split(rsaPublicExponentWant, "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("got %d lines, want %d:\n%s", len(gotLines), len(wantLines), got.String())
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("line %d:\n got: %s\nwant: %s", i, gotLines[i], wantLines[i])
		}
	}
}
