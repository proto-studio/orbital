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

// The limit counts the derived key too: three times its length for the key,
// its base64 form and the Buffer. A 30000-byte limit allows keys of up to
// 10000 bytes from pbkdf2, hkdf and scrypt.
func TestCrypto_MemoryLimitCapsKeyLength(t *testing.T) {
	got := runJS(t, &runtime.Config{CryptoMemoryLimit: 30000}, `
const crypto = require('crypto');
const outcome = (f) => { try { const v = f(); return v.byteLength; } catch (e) { return e.code + ' ' + e.message; } };
done({
  pbkdf2Max: outcome(() => crypto.pbkdf2Sync('pw', 'salt', 1, 10000, 'sha256')),
  pbkdf2Over: outcome(() => crypto.pbkdf2Sync('pw', 'salt', 1, 10001, 'sha256')),
  pbkdf2Callback: outcome(() => crypto.pbkdf2('pw', 'salt', 1, 2147483647, 'sha256', () => {})),
  hkdfOver: outcome(() => crypto.hkdfSync('sha512', 'key', 'salt', 'info', 16000)),
  scryptOver: outcome(() => crypto.scryptSync('pw', 'salt', 20000, { N: 2, r: 1, p: 1 })),
  native: outcome(() => __crypto_module._pbkdf2('', '', 1, 2147483647, 'sha256')),
});
`)
	wantJSON(t, got, map[string]any{
		"pbkdf2Max":      float64(10000),
		"pbkdf2Over":     `ERR_OUT_OF_RANGE The value of "keylen" is out of range. It must be <= 10000. Received 10001`,
		"pbkdf2Callback": `ERR_OUT_OF_RANGE The value of "keylen" is out of range. It must be <= 10000. Received 2147483647`,
		"hkdfOver":       `ERR_OUT_OF_RANGE The value of "length" is out of range. It must be <= 10000. Received 16000`,
		"scryptOver":     scryptMemoryError,
		"native":         "undefined pbkdf2: invalid arguments",
	})
}
