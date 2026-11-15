package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// crypto.subtle.generateKey makes RSA keys with the publicExponent it is given, and refuses
// the ones Node refuses with the same errors.
func TestWebCryptoRSAExponent_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/webcrypto_rsa_exponent.js", "testdata/webcrypto_rsa_exponent.node.json"

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
