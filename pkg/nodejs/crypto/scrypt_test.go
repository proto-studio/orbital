package crypto

import (
	"encoding/hex"
	"testing"
)

// The test vectors from RFC 7914, section 12.
func TestScryptRFC7914(t *testing.T) {
	cases := []struct {
		password, salt string
		n, r, p        int
		want           string
	}{
		{"", "", 16, 1, 1, "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906"},
		{"password", "NaCl", 1024, 8, 16, "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640"},
		{"pleaseletmein", "SodiumChloride", 16384, 8, 1, "7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887"},
	}
	for _, c := range cases {
		got, err := scryptKey([]byte(c.password), []byte(c.salt), c.n, c.r, c.p, 64)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Errorf("scrypt(%q, %q, N=%d, r=%d, p=%d) = %x", c.password, c.salt, c.n, c.r, c.p, got)
		}
	}
}

// The natives behind pbkdf2 and scrypt are reachable from scripts, so they
// check their own arguments instead of trusting kdf.js.
func TestCrypto_KDFNativesCheckArguments(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	calls := []string{
		`__crypto_module._pbkdf2('', '', 1, Number.MAX_SAFE_INTEGER, 'sha256')`,
		`__crypto_module._pbkdf2('', '', 0, 16, 'sha256')`,
		`__crypto_module._scrypt('', '', 2 ** 40, 1, 1, 32 << 20, 16)`,
		`__crypto_module._scrypt('', '', 16, 1, 1, 32 << 20, Number.MAX_SAFE_INTEGER)`,
		`__crypto_module._scrypt('', '', 16, 1, 1, -1, 16)`,
	}
	for _, call := range calls {
		result, err := rt.RunScript(`(() => { try { `+call+`; return 'returned'; } catch (e) { return 'threw'; } })()`, "test.js")
		if err != nil {
			t.Fatalf("%s: %v", call, err)
		}
		if result.String() != "threw" {
			t.Errorf("%s: %s, want a throw", call, result.String())
		}
	}
}
