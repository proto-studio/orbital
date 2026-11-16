package crypto

import "testing"

// The key agreement native checks the kinds of the keys behind its handles
// itself, since scripts can call it with any handles.
func TestDH_KeyAgreementChecksKinds(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const out = [];
		for (const type of ['x25519', 'ec']) {
			const opts = type === 'ec' ? { namedCurve: 'P-256' } : undefined;
			const { publicKey, privateKey } = c.generateKeyPairSync(type, opts);
			for (const [a, b] of [[publicKey, publicKey], [publicKey, privateKey]]) {
				try {
					c._keyAgreement(c._keyHandle(a), c._keyHandle(b));
					out.push('returned');
				} catch (e) {
					out.push(String(e.message || e).split('|')[0]);
				}
			}
			out.push(c._keyAgreement(c._keyHandle(privateKey), c._keyHandle(privateKey)).length > 0);
		}
		return JSON.stringify(out);
	})()`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `["ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE","ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE",true,` +
		`"ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE","ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE",true]`
	if got := result.String(); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
