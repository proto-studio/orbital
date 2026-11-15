package crypto

import "testing"

// The natives behind checkPrime and generatePrime are reachable from scripts,
// so they check their own arguments instead of trusting extras.js.
func TestCrypto_PrimeNativesCheckArguments(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	calls := []string{
		`__crypto_module._checkPrime('d', -1)`,
		`__crypto_module._checkPrime('d', -1, () => {})`,
		`__crypto_module._checkPrime('d', 2 ** 40)`,
		`__crypto_module._generatePrime(Number.MAX_SAFE_INTEGER, false, '', '')`,
		`__crypto_module._generatePrime(0, false, '', '', () => {})`,
		`__crypto_module._generatePrime(-5, false, '', '')`,
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
