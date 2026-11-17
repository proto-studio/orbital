package crypto

import (
	"strings"
	"testing"
)

// Each native that takes a size or length checks it itself, since scripts
// can call it directly: an oversized value is an error, not an attempt.
func TestNatives_RefuseOversizedLengths(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	calls := map[string]string{
		"pbkdf2":         `__crypto_module._pbkdf2('', '', 1, 2147483647, 'sha256')`,
		"pbkdf2Callback": `__crypto_module._pbkdf2('', '', 1, 2147483647, 'sha256', () => {})`,
		"scrypt":         `__crypto_module._scrypt('', '', 16, 1, 1, 33554432, 2147483647)`,
		"randomBytes":    `__crypto_module.randomBytes(2147483647)`,
		"randomAsync":    `__crypto_module._randomBytesAsync(2147483647, () => {})`,
		"randomFill":     `__crypto_module._randomBase64(2147483647)`,
		"generatePrime":  `__crypto_module._generatePrime(2147483647, false, '', '')`,
	}
	for name, call := range calls {
		result, err := rt.RunScript(`(() => { try { `+call+`; return 'returned'; } catch (e) { return String(e.message); } })()`, name+".js")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(result.String(), "is out of range") {
			t.Errorf("%s: %s, want an out-of-range error", name, result.String())
		}
	}
}

// The JavaScript wrappers refuse lengths Node takes but a native can't
// return, with Node's ERR_OUT_OF_RANGE, and randomFill fills large buffers
// in pieces the native can return.
func TestWrappers_NativeLengthBounds(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const err = (f) => { try { f(); return 'returned'; } catch (e) { return e.code + ' ' + e.message; } };
		const out = [
			err(() => c.randomBytes(500000000)),
			err(() => c.pbkdf2Sync('', '', 1, 500000000, 'sha256')),
			err(() => c.scryptSync('', '', 500000000)),
			err(() => c.generatePrimeSync(2147483600)),
			c.randomBytes(16).length,
		];
		// Pieces: with the bound lowered, a 20-byte fill takes three native calls.
		const real = c._randomBase64;
		let calls = 0;
		c._randomBase64 = (n, cb) => { calls++; return real(n, cb); };
		c._maxNativeBytes = 7;
		c.randomFillSync(Buffer.alloc(20));
		out.push(calls);
		return JSON.stringify(out);
	})()`, "wrappers.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `["ERR_OUT_OF_RANGE The value of \"size\" is out of range. It must be >= 0 && <= 402653166. Received 500000000",` +
		`"ERR_OUT_OF_RANGE The value of \"keylen\" is out of range. It must be >= 0 && <= 402653166. Received 500000000",` +
		`"ERR_OUT_OF_RANGE The value of \"keylen\" is out of range. It must be >= 0 && <= 402653166. Received 500000000",` +
		`"ERR_OUT_OF_RANGE The value of \"size\" is out of range. It must be >= 1 && <= 2147483552. Received 2147483600",16,3]`
	if result.String() != want {
		t.Errorf("got  %s\nwant %s", result.String(), want)
	}
}

// Node's digit grouping in ERR_OUT_OF_RANGE messages.
func TestGroupDigits(t *testing.T) {
	for n, want := range map[int64]string{5: "5", 4294967295: "4294967295", 4294967297: "4_294_967_297", -4294967297: "-4_294_967_297"} {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %s, want %s", n, got, want)
		}
	}
}
