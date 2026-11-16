package nodejs

import (
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// SubtleCrypto's spki/pkcs8 import and export read keys through node:crypto's
// key registry; the keys they add there are freed right after, so repeated
// imports and exports leave the registry as it was.
func TestWebCrypto_SPKIImportExportFreesKeys(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
(async () => {
	const subtle = globalThis.crypto.subtle;
	const pair = await subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
	const spki = await subtle.exportKey('spki', pair.publicKey);
	const pkcs8 = await subtle.exportKey('pkcs8', pair.privateKey);
	const before = __crypto_module._keyCount();
	for (let i = 0; i < 50; i++) {
		const pub = await subtle.importKey('spki', spki, { name: 'ECDSA', namedCurve: 'P-256' }, true, ['verify']);
		const priv = await subtle.importKey('pkcs8', pkcs8, { name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign']);
		await subtle.exportKey('spki', pub);
		await subtle.exportKey('pkcs8', priv);
	}
	done({ added: __crypto_module._keyCount() - before });
})();
`)
	if added, _ := got["added"].(float64); added != 0 {
		t.Errorf("%v keys left in the registry after 100 imports and exports", got["added"])
	}
}
