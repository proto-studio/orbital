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

// The limit counts the derived key too: six times its length for the key,
// its base64 form, that form's C copy and V8 string, and the Buffer. It
// counts the inputs as they're copied out of V8 and decoded, ten bytes for
// each character of their base64 form, before copying them. With pbkdf2's
// 120 bytes of input ('pw' and 'salt'), a 60120-byte limit allows keys of up
// to 10000 bytes.
func TestCrypto_MemoryLimitCapsKeyLength(t *testing.T) {
	got := runJS(t, &runtime.Config{CryptoMemoryLimit: 60120}, `
const crypto = require('crypto');
const outcome = (f) => { try { const v = f(); return v.byteLength; } catch (e) { return e.code + ' ' + e.message; } };
const big = 'x'.repeat(30000);
done({
  pbkdf2Max: outcome(() => crypto.pbkdf2Sync('pw', 'salt', 1, 10000, 'sha256')),
  pbkdf2Over: outcome(() => crypto.pbkdf2Sync('pw', 'salt', 1, 10001, 'sha256')),
  pbkdf2Callback: outcome(() => crypto.pbkdf2('pw', 'salt', 1, 20000, 'sha256', () => {})),
  pbkdf2BigInput: outcome(() => crypto.pbkdf2Sync(big, 'salt', 1, 16, 'sha256')),
  hkdfOver: outcome(() => crypto.hkdfSync('sha512', 'key', 'salt', 'info', 16000)),
  hkdfBigInput: outcome(() => crypto.hkdfSync('sha512', big, 'salt', 'info', 16)),
  scryptOver: outcome(() => crypto.scryptSync('pw', 'salt', 20000, { N: 2, r: 1, p: 1 })),
  scryptBigInput: outcome(() => crypto.scryptSync(big, 'salt', 16, { N: 2, r: 1, p: 1 })),
  native: outcome(() => __crypto_module._pbkdf2('', '', 1, 20000, 'sha256')),
  nativeBigInput: outcome(() => __crypto_module._pbkdf2('A'.repeat(40000), '', 1, 16, 'sha256')),
  nativeHkdfBigInput: outcome(() => __crypto_module._hkdf('sha256', 'A'.repeat(40000), '', '', 16)),
  nativeNotString: outcome(() => __crypto_module._pbkdf2({ toString() { return ''; } }, '', 1, 16, 'sha256')),
  nativeScryptBigInput: outcome(() => __crypto_module._scrypt('A'.repeat(40000), '', 2, 1, 1, 32 << 20, 16)),
});
`)
	wantJSON(t, got, map[string]any{
		"pbkdf2Max":            float64(10000),
		"pbkdf2Over":           `ERR_OUT_OF_RANGE The value of "keylen" is out of range. It must be <= 10000. Received 10001`,
		"pbkdf2Callback":       `ERR_OUT_OF_RANGE The value of "keylen" is out of range. It must be <= 10000. Received 20000`,
		"pbkdf2BigInput":       `ERR_OUT_OF_RANGE The value of "keylen" is out of range. It must be <= 0. Received 16`,
		"hkdfOver":             `ERR_OUT_OF_RANGE The value of "length" is out of range. It must be <= 9986. Received 16000`,
		"hkdfBigInput":         `ERR_OUT_OF_RANGE The value of "length" is out of range. It must be <= 0. Received 16`,
		"scryptOver":           scryptMemoryError,
		"scryptBigInput":       scryptMemoryError,
		"native":               "undefined pbkdf2: invalid arguments",
		"nativeBigInput":       "undefined pbkdf2: invalid arguments",
		"nativeNotString":      "undefined pbkdf2: invalid arguments",
		"nativeHkdfBigInput":   "undefined hkdf: invalid arguments",
		"nativeScryptBigInput": "undefined scrypt: invalid parameters",
	})
}

// The limit covers WebCrypto's PBKDF2 and HKDF derivations too, through
// SubtleCrypto and through the natives directly, counting the params at
// 24 bytes a character. The params must be a string, so the native derives
// from the params that were checked.
func TestCrypto_MemoryLimitCapsWebCrypto(t *testing.T) {
	got := runJS(t, &runtime.Config{CryptoMemoryLimit: 62000}, `
(async () => {
  const subtle = globalThis.crypto.subtle;
  const n = __webcrypto_native;
  const err = (f) => { try { f(); return 'returned'; } catch (e) { return String(e.message); } };
  const base = await subtle.importKey('raw', new Uint8Array(8), 'PBKDF2', false, ['deriveBits']);
  const params = { name: 'PBKDF2', salt: new Uint8Array(8), iterations: 1, hash: 'SHA-256' };
  const ok = (await subtle.deriveBits(params, base, 79000)).byteLength;
  let over;
  try { await subtle.deriveBits(params, base, 80000); over = 'resolved'; } catch (e) { over = 'rejected'; }
  done({
    ok, over,
    pbkdf2Native: err(() => n.pbkdf2(JSON.stringify({ passwordB64: '', saltB64: '', iterations: 1, hash: 'SHA-256', length: 4000000000 }))).includes('crypto memory limit'),
    hkdfNative: err(() => n.hkdf(JSON.stringify({ keyB64: 'AA==', saltB64: '', infoB64: '', hash: 'SHA-512', length: 90000 }))).includes('crypto memory limit'),
    notString: (() => {
      let calls = 0;
      const params = { toString() { return calls++ ? JSON.stringify({ passwordB64: '', saltB64: '', iterations: 1, hash: 'SHA-256', length: 8000000 }) : '{}'; } };
      return err(() => n.pbkdf2(params)).includes('crypto memory limit');
    })(),
    bigParams: err(() => n.pbkdf2(JSON.stringify({ passwordB64: 'A'.repeat(20000), saltB64: '', iterations: 1, hash: 'SHA-256', length: 8 }))).includes('crypto memory limit'),
  });
})();
`)
	wantJSON(t, got, map[string]any{"ok": float64(9875), "over": "rejected", "pbkdf2Native": true, "hkdfNative": true, "bigParams": true, "notString": true})
}
