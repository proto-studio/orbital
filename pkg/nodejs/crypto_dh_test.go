package nodejs

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// ECDH, crypto.diffieHellman and generateKey agree with Node, with the same errors.
func TestCryptoDH_MatchesNode(t *testing.T) {
	const scriptPath, expectedPath = "testdata/crypto_dh.js", "testdata/crypto_dh.node.json"

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

// Orbital has no dh KeyObjects, so diffieHellman refuses them.
func TestCryptoDH_DHKeyObjectsUnavailable(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const crypto = require('crypto');
const dhKey = { type: 'private', asymmetricKeyType: 'dh' };
Object.setPrototypeOf(dhKey, crypto.KeyObject.prototype);
try {
	crypto.diffieHellman({ privateKey: dhKey, publicKey: dhKey });
	done({ result: 'no error' });
} catch (e) {
	done({ result: e.name + ' ' + e.code });
}
`)
	if want := "TypeError ERR_FEATURE_UNAVAILABLE_ON_PLATFORM"; got["result"] != want {
		t.Errorf("got %v, want %q", got["result"], want)
	}
}
