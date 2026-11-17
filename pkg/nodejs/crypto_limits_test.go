package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

const cryptoLimitScript = `
const crypto = require('crypto');
const outcome = (f) => { try { return f().length; } catch (e) { return e.code + ' ' + e.message; } };
const results = {
  small: outcome(() => crypto.scryptSync('pw', 'salt', 16, { N: 16, r: 1 })),
  large: outcome(() => crypto.scryptSync('pw', 'salt', 16, { N: 1024, r: 8 })),
  largeMaxmem: outcome(() => crypto.scryptSync('pw', 'salt', 16, { N: 1024, r: 8, maxmem: 64 << 20 })),
};
// The callback form checks its parameters before it starts, so it throws as Node does.
try {
  crypto.scrypt('pw', 'salt', 16, { N: 1024, r: 8 }, (err, key) => {
    results.callback = err ? err.code + ' ' + err.message : key.length;
    done(results);
  });
} catch (e) {
  results.callback = e.code + ' ' + e.message;
  done(results);
}
`

const scryptMemoryError = "ERR_CRYPTO_INVALID_SCRYPT_PARAMS Invalid scrypt params: error:030000AC:digital envelope routines::memory limit exceeded"

// With Config.CryptoMemoryLimit set, an scrypt call that needs more memory
// fails as one over its own maxmem does, even when the script raises maxmem.
func TestCrypto_MemoryLimitCapsScrypt(t *testing.T) {
	got := runJS(t, &runtime.Config{CryptoMemoryLimit: 512 << 10}, cryptoLimitScript)
	wantJSON(t, got, map[string]any{
		"small":       float64(16),
		"large":       scryptMemoryError,
		"largeMaxmem": scryptMemoryError,
		"callback":    scryptMemoryError,
	})
}

// Without a limit, scrypt behaves as in Node.
func TestCrypto_NoMemoryLimitByDefault(t *testing.T) {
	got := runJS(t, &runtime.Config{}, cryptoLimitScript)
	wantJSON(t, got, map[string]any{
		"small":       float64(16),
		"large":       float64(16),
		"largeMaxmem": float64(16),
		"callback":    float64(16),
	})
}
