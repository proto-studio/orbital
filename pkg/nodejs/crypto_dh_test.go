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

// For the MODP groups, whose subgroup order is known, computeSecret refuses a
// peer key outside the prime-order subgroup (p-2 and p-4 here, as -1 isn't a
// square mod these primes), whether the
// object came from getDiffieHellman or from the group's prime. A key in it, 4,
// is used. Node 20 doesn't check this; other moduli aren't checked, nor
// are keys when the generator itself is outside the subgroup.
func TestCryptoDH_RefusesKeysOutsideTheSubgroup(t *testing.T) {
	got := runJS(t, &runtime.Config{}, `
const crypto = require('crypto');
const outcome = (f) => { try { return f().length; } catch (e) { return e.code + ' ' + e.message; } };
const group = crypto.getDiffieHellman('modp14');
const prime = group.getPrime();
const minus = (n) => Buffer.from((BigInt('0x' + prime.toString('hex')) - n).toString(16), 'hex');
const minusTwo = minus(2n);
const keyed = (dh) => { dh.generateKeys(); return dh; };
done({
  groupMinusTwo: outcome(() => keyed(crypto.getDiffieHellman('modp14')).computeSecret(minusTwo)),
  groupMinusFour: outcome(() => keyed(crypto.getDiffieHellman('modp14')).computeSecret(minus(4n))),
  primeMinusTwo: outcome(() => keyed(crypto.createDiffieHellman(prime, 2)).computeSecret(minusTwo)),
  groupFour: outcome(() => keyed(crypto.getDiffieHellman('modp14')).computeSecret(Buffer.from([4]))),
  otherPrime: outcome(() => keyed(crypto.createDiffieHellman(crypto.getDiffieHellman('modp2').getPrime().subarray(0, 64), 2)).computeSecret(Buffer.from([3]))),
  // -1 isn't a square mod the MODP primes, so generator p-2 makes keys outside the subgroup.
  nonSquareGenerator: (() => {
    const a = crypto.createDiffieHellman(prime, minusTwo); const b = crypto.createDiffieHellman(prime, minusTwo);
    a.generateKeys(); b.generateKeys();
    return a.computeSecret(b.getPublicKey()).equals(b.computeSecret(a.getPublicKey()));
  })(),
});
`)
	invalid := "ERR_CRYPTO_INVALID_KEYTYPE Unspecified validation error"
	wantJSON(t, got, map[string]any{
		"groupMinusTwo":      invalid,
		"groupMinusFour":     invalid,
		"primeMinusTwo":      invalid,
		"groupFour":          float64(256),
		"otherPrime":         float64(64),
		"nonSquareGenerator": true,
	})
}
