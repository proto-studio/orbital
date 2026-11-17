package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// crypto.subtle verifies RSA-PSS with saltLength 0 against an empty salt, and checks
// saltLength, as Node does.
func TestWebCryptoPSS_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/webcrypto_pss.js", "testdata/webcrypto_pss.node.json"

	if captureFromNode(t) {
		output, err := exec.Command("node", scriptPath).Output()
		if err != nil {
			t.Fatal(err)
		}
		var results map[string]any
		if err := json.Unmarshal(output, &results); err != nil {
			t.Fatal(err)
		}
		writeExpected(t, expectedPath, results)
		return
	}

	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	got := runJS(t, &runtime.Config{}, string(script))
	wantJSON(t, got, readExpected(t, expectedPath))
}

// Signing RSA-PSS with saltLength 0 isn't offered, as in node:crypto: Go's RSA can't make an
// empty salt. It rejects with NotSupportedError rather than making a salted signature, through
// SubtleCrypto and through the native directly.
func TestWebCryptoPSS_SignWithEmptySalt(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
(async () => {
  const { subtle } = globalThis.crypto;
  const algo = { name: 'RSA-PSS', hash: 'SHA-256', modulusLength: 1024, publicExponent: new Uint8Array([1, 0, 1]) };
  const { privateKey } = await subtle.generateKey(algo, true, ['sign', 'verify']);
  const data = new Uint8Array([1, 2, 3]);
  let sign0;
  try { await subtle.sign({ name: 'RSA-PSS', saltLength: 0 }, privateKey, data); sign0 = 'resolved'; } catch (e) { sign0 = e.name + ': ' + e.message; }
  let native0;
  try {
    __webcrypto_native.sign(JSON.stringify({ name: 'RSA-PSS', hash: 'SHA-256', saltLength: 0 }), JSON.stringify(privateKey._jwk), 'AQID');
    native0 = 'returned';
  } catch (e) { native0 = e.message; }
  done({ sign0, native0 });
})();
`)
	wantJSON(t, got, map[string]any{
		"sign0":   "NotSupportedError: Unsupported crypto operation",
		"native0": "sign PS: saltLength 0 is not supported",
	})
}
