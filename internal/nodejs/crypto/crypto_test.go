package crypto

import (
	"testing"

	"proto.zip/studio/orbital/internal/nodejs/buffer"
	"proto.zip/studio/orbital/pkg/runtime"
)

func setupRuntime(t *testing.T) *runtime.Runtime {
	rt, err := runtime.New(nil)
	if err != nil {
		t.Fatalf("Failed to create runtime: %v", err)
	}

	// Register buffer module first (needed for some crypto operations)
	bufMod := buffer.New()
	if err := bufMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register buffer module: %v", err)
	}

	cryptoMod := New()
	if err := cryptoMod.Register(rt); err != nil {
		rt.Dispose()
		t.Fatalf("Failed to register crypto module: %v", err)
	}

	return rt
}

func TestCrypto_RandomBytes(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const bytes = __crypto_module.randomBytes(16);
		Buffer.isBuffer(bytes) && bytes.length === 16;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("randomBytes(16) should return a Buffer of length 16")
	}
}

func TestCrypto_RandomBytes_Unique(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const a = __crypto_module.randomBytes(16);
		const b = __crypto_module.randomBytes(16);
		a.toString('hex') !== b.toString('hex');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("Two random byte generations should not be identical")
	}
}

func TestCrypto_RandomUUID(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`__crypto_module.randomUUID()`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	uuid := result.String()
	if len(uuid) != 36 {
		t.Errorf("UUID should be 36 chars, got %d: %q", len(uuid), uuid)
	}
}

func TestCrypto_RandomUUID_Unique(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const a = __crypto_module.randomUUID();
		const b = __crypto_module.randomUUID();
		a !== b;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("Two UUIDs should not be identical")
	}
}

func TestCrypto_CreateHash_MD5(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__crypto_module.createHash('md5').update('hello').digest('hex')
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// MD5 of "hello" is 5d41402abc4b2a76b9719d911017c592
	if result.String() != "5d41402abc4b2a76b9719d911017c592" {
		t.Errorf("Expected MD5 hash, got %q", result.String())
	}
}

func TestCrypto_CreateHash_SHA256(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__crypto_module.createHash('sha256').update('hello').digest('hex')
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// SHA256 of "hello" is 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if result.String() != expected {
		t.Errorf("Expected SHA256 hash %q, got %q", expected, result.String())
	}
}

func TestCrypto_CreateHmac(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__crypto_module.createHmac('sha256', 'secret').update('hello').digest('hex')
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Just verify we get a hex string of expected length
	if len(result.String()) != 64 {
		t.Errorf("Expected 64-char hex HMAC, got %d chars: %q", len(result.String()), result.String())
	}
}

func TestCrypto_GetHashes(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const hashes = __crypto_module.getHashes();
		Array.isArray(hashes) && hashes.length > 0 && hashes.includes('sha256');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("getHashes() should return array including 'sha256'")
	}
}

func TestCrypto_HashDigestBase64(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__crypto_module.createHash('sha256').update('hello').digest('base64')
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// Base64 of SHA256 of "hello"
	expected := "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="
	if result.String() != expected {
		t.Errorf("Expected %q, got %q", expected, result.String())
	}
}

func TestCrypto_HashUpdate_Multiple(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const hash = __crypto_module.createHash('md5');
		hash.update('hello');
		hash.update(' ');
		hash.update('world');
		hash.digest('hex');
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// MD5 of "hello world"
	expected := "5eb63bbbe01eeed093cb22bb8f5acdc3"
	if result.String() != expected {
		t.Errorf("Expected %q, got %q", expected, result.String())
	}
}

func TestCrypto_RandomInt(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		const n = __crypto_module.randomInt(0, 100);
		n >= 0 && n < 100;
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	if !result.Boolean() {
		t.Error("randomInt should return value in range [0, 100)")
	}
}

func TestCrypto_HmacSha1(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	result, err := rt.RunScript(`
		__crypto_module.createHmac('sha1', 'key').update('message').digest('hex')
	`, "test.js")
	if err != nil {
		t.Fatalf("RunScript failed: %v", err)
	}

	// HMAC-SHA1 of "message" with key "key"
	expected := "2088df74d5f2146b48146caf4965377e9d0be3a4"
	if result.String() != expected {
		t.Errorf("Expected %q, got %q", expected, result.String())
	}
}

func TestCrypto_Name(t *testing.T) {
	c := New()
	if c.Name() != "crypto" {
		t.Errorf("Name() should return 'crypto', got %q", c.Name())
	}
}
