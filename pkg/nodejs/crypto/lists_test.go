package crypto

import (
	"strconv"
	"testing"
)

// Every cipher getCiphers() lists can be created with the key and IV
// lengths getCipherInfo gives for it.
func TestLists_CiphersWork(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()
	result, err := rt.RunScript(`(() => {
		const c = __crypto_module;
		const failed = [];
		const names = c.getCiphers();
		for (const name of names) {
			try {
				const info = c.getCipherInfo(name);
				const e = c.createCipheriv(name, Buffer.alloc(info.keyLength, 1), info.ivLength ? Buffer.alloc(info.ivLength, 2) : null);
				e.update('x');
				e.final();
			} catch (err) {
				failed.push(name + ': ' + err.message);
			}
		}
		return JSON.stringify({ count: names.length, failed });
	})()`, "test.js")
	if err != nil {
		t.Fatal(err)
	}
	n := len(supportedCiphers())
	if want := `{"count":` + strconv.Itoa(n) + `,"failed":[]}`; result.String() != want {
		t.Errorf("got %s, want %s", result.String(), want)
	}
	if n < 30 {
		t.Errorf("only %d ciphers listed", n)
	}
}
