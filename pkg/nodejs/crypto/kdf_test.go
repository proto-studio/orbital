package crypto

import "testing"

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
