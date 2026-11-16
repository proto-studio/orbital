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

// Finite-field Diffie-Hellman isn't offered until it can be done in constant time
// (#242): each entry point throws ERR_FEATURE_UNAVAILABLE_ON_PLATFORM.
func TestCryptoDH_FiniteFieldUnavailable(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const crypto = require('crypto');
const results = {};
const dhKey = { type: 'private', asymmetricKeyType: 'dh' };
Object.setPrototypeOf(dhKey, crypto.KeyObject.prototype);
const calls = {
	createDiffieHellman: () => crypto.createDiffieHellman(2048),
	createDiffieHellmanPrime: () => crypto.createDiffieHellman(Buffer.from([23]), 5),
	getDiffieHellman: () => crypto.getDiffieHellman('modp14'),
	createDiffieHellmanGroup: () => crypto.createDiffieHellmanGroup('modp14'),
	DiffieHellman: () => new crypto.DiffieHellman(2048),
	DiffieHellmanGroup: () => new crypto.DiffieHellmanGroup('modp14'),
	diffieHellman: () => crypto.diffieHellman({ privateKey: dhKey, publicKey: dhKey }),
};
for (const [name, f] of Object.entries(calls)) {
	try {
		f();
		results[name] = 'no error';
	} catch (e) {
		results[name] = [e.name, e.code, e.message.includes('issues/242')].join(' ');
	}
}
done(results);
`)
	for _, name := range []string{"createDiffieHellman", "createDiffieHellmanPrime", "getDiffieHellman",
		"createDiffieHellmanGroup", "DiffieHellman", "DiffieHellmanGroup", "diffieHellman"} {
		if want := "TypeError ERR_FEATURE_UNAVAILABLE_ON_PLATFORM true"; got[name] != want {
			t.Errorf("%s: got %v, want %q", name, got[name], want)
		}
	}
}
