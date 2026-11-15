package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// randomFill, getRandomValues, webcrypto, the prime functions, Certificate and the other
// remaining node:crypto helpers behave as in Node.
func TestCryptoExtras_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/crypto_extras.js", "testdata/crypto_extras.node.json"

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
