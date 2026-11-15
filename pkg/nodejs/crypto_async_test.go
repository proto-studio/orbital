package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// randomBytes checks its arguments as Node does, and the callback forms of randomBytes and
// generateKeyPair call back later and leave timers running meanwhile.
func TestCryptoAsync_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/crypto_async.js", "testdata/crypto_async.node.json"

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
