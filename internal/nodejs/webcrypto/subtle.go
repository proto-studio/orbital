package webcrypto

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"math/big"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// This file implements the cryptographic heavy-lifting behind the Web Crypto API
// (SubtleCrypto) natively in Go, exposed to the JS glue in webcrypto.js as the
// global __webcrypto_native. Keeping the primitives in Go (rather than a JS
// polyfill) means the runtime uses Go's audited crypto/* implementations for RSA,
// ECDSA/ECDH, Ed25519, AES-GCM/CBC/KW and HMAC.
//
// Boundary conventions: keys are exchanged as JWK JSON (the same representation
// JOSE libraries use), binary payloads (plaintext/ciphertext/IV/AAD/signatures)
// as standard base64 strings, and structured algorithm parameters as JSON.

// jwk is the subset of JSON Web Key fields the implemented algorithms use. All
// binary fields are base64url (no padding), per RFC 7517/7518.
type jwk struct {
	Kty string `json:"kty"`
	// oct (symmetric)
	K string `json:"k,omitempty"`
	// RSA
	N  string `json:"n,omitempty"`
	E  string `json:"e,omitempty"`
	D  string `json:"d,omitempty"`
	P  string `json:"p,omitempty"`
	Q  string `json:"q,omitempty"`
	Dp string `json:"dp,omitempty"`
	Dq string `json:"dq,omitempty"`
	Qi string `json:"qi,omitempty"`
	// EC / OKP
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// algoParams carries the normalized algorithm object the JS layer sends. Fields
// are only populated when relevant to the operation.
type algoParams struct {
	Name       string `json:"name"`
	Hash       string `json:"hash,omitempty"`
	NamedCurve string `json:"namedCurve,omitempty"`
	Length     int    `json:"length,omitempty"`
	// RSA generation
	ModulusLength  int `json:"modulusLength,omitempty"`
	PublicExponent int `json:"publicExponent,omitempty"`
	// RSA-PSS
	SaltLength int `json:"saltLength,omitempty"`
	// AES-GCM / AES-CBC
	IV        string `json:"ivB64,omitempty"`
	AAD       string `json:"aadB64,omitempty"`
	TagLength int    `json:"tagLength,omitempty"`
}

var (
	b64u = base64.RawURLEncoding
	b64s = base64.StdEncoding
)

func b64uEnc(b []byte) string { return b64u.EncodeToString(b) }

func b64uDec(s string) ([]byte, error) {
	// Tolerate padded input just in case.
	if m := len(s) % 4; m != 0 {
		// RawURLEncoding rejects padding; strip any '=' the caller added.
	}
	// Strip padding characters if present.
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return b64u.DecodeString(s)
}

func b64sDec(s string) ([]byte, error) { return b64s.DecodeString(s) }

// registerNative installs the __webcrypto_native object with the Go-backed
// SubtleCrypto primitives.
func registerNative(rt *runtime.Runtime) error {
	iso := rt.Isolate()
	ctx := rt.Context()

	obj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	funcs := map[string]v8.FunctionCallback{
		"digest":      nativeDigest,
		"generateKey": nativeGenerateKey,
		"sign":        nativeSign,
		"verify":      nativeVerify,
		"encrypt":     nativeEncrypt,
		"decrypt":     nativeDecrypt,
		"deriveBits":  nativeDeriveBits,
		"pbkdf2":      nativePbkdf2,
		"aesKwWrap":   nativeAesKwWrap,
		"aesKwUnwrap": nativeAesKwUnwrap,
	}
	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := obj.Set(name, val); err != nil {
			return err
		}
	}

	return rt.SetGlobal("__webcrypto_native", obj)
}

func argStr(info *v8.FunctionCallbackInfo, i int) string {
	args := info.Args()
	if i >= len(args) {
		return ""
	}
	return args[i].String()
}

func throwf(ctx *v8.Context, format string, a ...interface{}) *v8.Value {
	return ctx.Throw(fmt.Sprintf(format, a...))
}

func hashByName(name string) (crypto.Hash, func() hash.Hash, bool) {
	switch name {
	case "SHA-1":
		return crypto.SHA1, sha1.New, true
	case "SHA-256":
		return crypto.SHA256, sha256.New, true
	case "SHA-384":
		return crypto.SHA384, sha512.New384, true
	case "SHA-512":
		return crypto.SHA512, sha512.New, true
	}
	return 0, nil, false
}

func hashSum(h crypto.Hash, newHash func() hash.Hash, data []byte) []byte {
	hh := newHash()
	hh.Write(data)
	return hh.Sum(nil)
}

// ---------------------------------------------------------------------------
// digest
// ---------------------------------------------------------------------------

// nativeDigest hashes data natively. Routing digest through Go (rather than the
// crypto module's string boundary) keeps it binary-safe: data crosses as base64,
// so embedded NUL bytes survive (the crypto module marshals via NUL-terminated C
// strings, which truncates binary inputs — fatal for e.g. ECDH-ES Concat KDF).
func nativeDigest(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	name := argStr(info, 0)
	if len(name) >= 3 && name[:3] != "SHA" && name[:3] != "sha" {
		return throwf(ctx, "digest: unsupported algorithm %q", name)
	}
	// Normalize "sha256" / "SHA-256" -> "SHA-256".
	norm := name
	switch name {
	case "sha1", "SHA1":
		norm = "SHA-1"
	case "sha256", "SHA256":
		norm = "SHA-256"
	case "sha384", "SHA384":
		norm = "SHA-384"
	case "sha512", "SHA512":
		norm = "SHA-512"
	}
	_, nh, ok := hashByName(norm)
	if !ok {
		return throwf(ctx, "digest: unsupported algorithm %q", name)
	}
	data, err := b64sDec(argStr(info, 1))
	if err != nil {
		return throwf(ctx, "digest: invalid data: %v", err)
	}
	hh := nh()
	hh.Write(data)
	return bytesResult(ctx, hh.Sum(nil))
}

// ---------------------------------------------------------------------------
// generateKey
// ---------------------------------------------------------------------------

func nativeGenerateKey(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var p algoParams
	if err := json.Unmarshal([]byte(argStr(info, 0)), &p); err != nil {
		return throwf(ctx, "generateKey: invalid params: %v", err)
	}

	switch p.Name {
	case "AES-GCM", "AES-CBC", "AES-KW", "AES-CTR", "HMAC":
		length := p.Length
		if length == 0 {
			if p.Name == "HMAC" {
				if _, nh, ok := hashByName(p.Hash); ok {
					length = nh().Size() * 8
				} else {
					length = 256
				}
			} else {
				length = 256
			}
		}
		raw := make([]byte, length/8)
		if _, err := rand.Read(raw); err != nil {
			return throwf(ctx, "generateKey: %v", err)
		}
		out := map[string]jwk{"secret": {Kty: "oct", K: b64uEnc(raw)}}
		return jsonResult(ctx, out)

	case "RSASSA-PKCS1-v1_5", "RSA-PSS", "RSA-OAEP":
		bits := p.ModulusLength
		if bits == 0 {
			bits = 2048
		}
		priv, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return throwf(ctx, "generateKey RSA: %v", err)
		}
		pubJWK, privJWK := rsaToJWK(priv)
		return jsonResult(ctx, map[string]jwk{"publicKey": pubJWK, "privateKey": privJWK})

	case "ECDSA", "ECDH":
		curve, crv, err := curveByName(p.NamedCurve)
		if err != nil {
			return throwf(ctx, "generateKey EC: %v", err)
		}
		priv, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return throwf(ctx, "generateKey EC: %v", err)
		}
		pubJWK, privJWK := ecToJWK(priv, crv)
		return jsonResult(ctx, map[string]jwk{"publicKey": pubJWK, "privateKey": privJWK})

	case "Ed25519":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return throwf(ctx, "generateKey Ed25519: %v", err)
		}
		pubJWK := jwk{Kty: "OKP", Crv: "Ed25519", X: b64uEnc(pub)}
		privJWK := jwk{Kty: "OKP", Crv: "Ed25519", X: b64uEnc(pub), D: b64uEnc(priv.Seed())}
		return jsonResult(ctx, map[string]jwk{"publicKey": pubJWK, "privateKey": privJWK})

	case "X25519":
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return throwf(ctx, "generateKey X25519: %v", err)
		}
		pubJWK := jwk{Kty: "OKP", Crv: "X25519", X: b64uEnc(priv.PublicKey().Bytes())}
		privJWK := jwk{Kty: "OKP", Crv: "X25519", X: b64uEnc(priv.PublicKey().Bytes()), D: b64uEnc(priv.Bytes())}
		return jsonResult(ctx, map[string]jwk{"publicKey": pubJWK, "privateKey": privJWK})
	}

	return throwf(ctx, "generateKey: unsupported algorithm %q", p.Name)
}

func jsonResult(ctx *v8.Context, v interface{}) *v8.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return throwf(ctx, "marshal: %v", err)
	}
	val, _ := ctx.NewString(string(b))
	return val
}

// ---------------------------------------------------------------------------
// sign / verify
// ---------------------------------------------------------------------------

func nativeSign(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var p algoParams
	if err := json.Unmarshal([]byte(argStr(info, 0)), &p); err != nil {
		return throwf(ctx, "sign: invalid params: %v", err)
	}
	var k jwk
	if err := json.Unmarshal([]byte(argStr(info, 1)), &k); err != nil {
		return throwf(ctx, "sign: invalid key: %v", err)
	}
	data, err := b64sDec(argStr(info, 2))
	if err != nil {
		return throwf(ctx, "sign: invalid data: %v", err)
	}

	switch p.Name {
	case "HMAC":
		_, nh, ok := hashByName(p.Hash)
		if !ok {
			return throwf(ctx, "sign HMAC: unsupported hash %q", p.Hash)
		}
		key, err := b64uDec(k.K)
		if err != nil {
			return throwf(ctx, "sign HMAC: bad key: %v", err)
		}
		mac := hmac.New(nh, key)
		mac.Write(data)
		return bytesResult(ctx, mac.Sum(nil))

	case "RSASSA-PKCS1-v1_5":
		ch, nh, ok := hashByName(p.Hash)
		if !ok {
			return throwf(ctx, "sign RS: unsupported hash %q", p.Hash)
		}
		priv, err := jwkToRSAPrivate(k)
		if err != nil {
			return throwf(ctx, "sign RS: %v", err)
		}
		sig, err := rsa.SignPKCS1v15(rand.Reader, priv, ch, hashSum(ch, nh, data))
		if err != nil {
			return throwf(ctx, "sign RS: %v", err)
		}
		return bytesResult(ctx, sig)

	case "RSA-PSS":
		ch, nh, ok := hashByName(p.Hash)
		if !ok {
			return throwf(ctx, "sign PS: unsupported hash %q", p.Hash)
		}
		priv, err := jwkToRSAPrivate(k)
		if err != nil {
			return throwf(ctx, "sign PS: %v", err)
		}
		opts := &rsa.PSSOptions{SaltLength: p.SaltLength, Hash: ch}
		sig, err := rsa.SignPSS(rand.Reader, priv, ch, hashSum(ch, nh, data), opts)
		if err != nil {
			return throwf(ctx, "sign PS: %v", err)
		}
		return bytesResult(ctx, sig)

	case "ECDSA":
		ch, nh, ok := hashByName(p.Hash)
		if !ok {
			return throwf(ctx, "sign ES: unsupported hash %q", p.Hash)
		}
		priv, err := jwkToECDSAPrivate(k)
		if err != nil {
			return throwf(ctx, "sign ES: %v", err)
		}
		r, s, err := ecdsa.Sign(rand.Reader, priv, hashSum(ch, nh, data))
		if err != nil {
			return throwf(ctx, "sign ES: %v", err)
		}
		size := (priv.Curve.Params().BitSize + 7) / 8
		sig := append(padLeft(r.Bytes(), size), padLeft(s.Bytes(), size)...)
		return bytesResult(ctx, sig)

	case "Ed25519":
		priv, err := jwkToEd25519Private(k)
		if err != nil {
			return throwf(ctx, "sign EdDSA: %v", err)
		}
		return bytesResult(ctx, ed25519.Sign(priv, data))
	}

	return throwf(ctx, "sign: unsupported algorithm %q", p.Name)
}

func nativeVerify(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var p algoParams
	if err := json.Unmarshal([]byte(argStr(info, 0)), &p); err != nil {
		return throwf(ctx, "verify: invalid params: %v", err)
	}
	var k jwk
	if err := json.Unmarshal([]byte(argStr(info, 1)), &k); err != nil {
		return throwf(ctx, "verify: invalid key: %v", err)
	}
	sig, err := b64sDec(argStr(info, 2))
	if err != nil {
		return throwf(ctx, "verify: invalid signature: %v", err)
	}
	data, err := b64sDec(argStr(info, 3))
	if err != nil {
		return throwf(ctx, "verify: invalid data: %v", err)
	}

	ok := false
	switch p.Name {
	case "HMAC":
		_, nh, has := hashByName(p.Hash)
		if !has {
			return throwf(ctx, "verify HMAC: unsupported hash %q", p.Hash)
		}
		key, err := b64uDec(k.K)
		if err != nil {
			return throwf(ctx, "verify HMAC: bad key: %v", err)
		}
		mac := hmac.New(nh, key)
		mac.Write(data)
		ok = hmac.Equal(mac.Sum(nil), sig)

	case "RSASSA-PKCS1-v1_5":
		ch, nh, has := hashByName(p.Hash)
		if !has {
			return throwf(ctx, "verify RS: unsupported hash %q", p.Hash)
		}
		pub, err := jwkToRSAPublic(k)
		if err != nil {
			return throwf(ctx, "verify RS: %v", err)
		}
		ok = rsa.VerifyPKCS1v15(pub, ch, hashSum(ch, nh, data), sig) == nil

	case "RSA-PSS":
		ch, nh, has := hashByName(p.Hash)
		if !has {
			return throwf(ctx, "verify PS: unsupported hash %q", p.Hash)
		}
		pub, err := jwkToRSAPublic(k)
		if err != nil {
			return throwf(ctx, "verify PS: %v", err)
		}
		opts := &rsa.PSSOptions{SaltLength: p.SaltLength, Hash: ch}
		ok = rsa.VerifyPSS(pub, ch, hashSum(ch, nh, data), sig, opts) == nil

	case "ECDSA":
		ch, nh, has := hashByName(p.Hash)
		if !has {
			return throwf(ctx, "verify ES: unsupported hash %q", p.Hash)
		}
		pub, err := jwkToECDSAPublic(k)
		if err != nil {
			return throwf(ctx, "verify ES: %v", err)
		}
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(sig) == 2*size {
			r := new(big.Int).SetBytes(sig[:size])
			s := new(big.Int).SetBytes(sig[size:])
			ok = ecdsa.Verify(pub, hashSum(ch, nh, data), r, s)
		}

	case "Ed25519":
		pub, err := jwkToEd25519Public(k)
		if err != nil {
			return throwf(ctx, "verify EdDSA: %v", err)
		}
		ok = ed25519.Verify(pub, data, sig)

	default:
		return throwf(ctx, "verify: unsupported algorithm %q", p.Name)
	}

	if ok {
		val, _ := ctx.NewString("true")
		return val
	}
	val, _ := ctx.NewString("false")
	return val
}

// ---------------------------------------------------------------------------
// encrypt / decrypt
// ---------------------------------------------------------------------------

func nativeEncrypt(info *v8.FunctionCallbackInfo) *v8.Value {
	return aeadOp(info, true)
}

func nativeDecrypt(info *v8.FunctionCallbackInfo) *v8.Value {
	return aeadOp(info, false)
}

func aeadOp(info *v8.FunctionCallbackInfo, encrypting bool) *v8.Value {
	ctx := info.Context()
	var p algoParams
	if err := json.Unmarshal([]byte(argStr(info, 0)), &p); err != nil {
		return throwf(ctx, "crypt: invalid params: %v", err)
	}
	var k jwk
	if err := json.Unmarshal([]byte(argStr(info, 1)), &k); err != nil {
		return throwf(ctx, "crypt: invalid key: %v", err)
	}
	data, err := b64sDec(argStr(info, 2))
	if err != nil {
		return throwf(ctx, "crypt: invalid data: %v", err)
	}

	switch p.Name {
	case "AES-GCM":
		key, err := b64uDec(k.K)
		if err != nil {
			return throwf(ctx, "AES-GCM: bad key: %v", err)
		}
		iv, err := b64sDec(p.IV)
		if err != nil {
			return throwf(ctx, "AES-GCM: bad iv: %v", err)
		}
		var aad []byte
		if p.AAD != "" {
			if aad, err = b64sDec(p.AAD); err != nil {
				return throwf(ctx, "AES-GCM: bad aad: %v", err)
			}
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return throwf(ctx, "AES-GCM: %v", err)
		}
		gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
		if err != nil {
			return throwf(ctx, "AES-GCM: %v", err)
		}
		if encrypting {
			// WebCrypto returns ciphertext||tag, which is Go's Seal output.
			return bytesResult(ctx, gcm.Seal(nil, iv, data, aad))
		}
		out, err := gcm.Open(nil, iv, data, aad)
		if err != nil {
			return throwf(ctx, "AES-GCM: decryption failed")
		}
		return bytesResult(ctx, out)

	case "AES-CBC":
		key, err := b64uDec(k.K)
		if err != nil {
			return throwf(ctx, "AES-CBC: bad key: %v", err)
		}
		iv, err := b64sDec(p.IV)
		if err != nil {
			return throwf(ctx, "AES-CBC: bad iv: %v", err)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return throwf(ctx, "AES-CBC: %v", err)
		}
		if encrypting {
			padded := pkcs7Pad(data, block.BlockSize())
			out := make([]byte, len(padded))
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
			return bytesResult(ctx, out)
		}
		if len(data)%block.BlockSize() != 0 || len(data) == 0 {
			return throwf(ctx, "AES-CBC: invalid ciphertext length")
		}
		out := make([]byte, len(data))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
		unpadded, err := pkcs7Unpad(out, block.BlockSize())
		if err != nil {
			return throwf(ctx, "AES-CBC: %v", err)
		}
		return bytesResult(ctx, unpadded)

	case "RSA-OAEP":
		_, nh, ok := hashByName(p.Hash)
		if !ok {
			return throwf(ctx, "RSA-OAEP: unsupported hash %q", p.Hash)
		}
		if encrypting {
			pub, err := jwkToRSAPublic(k)
			if err != nil {
				return throwf(ctx, "RSA-OAEP: %v", err)
			}
			out, err := rsa.EncryptOAEP(nh(), rand.Reader, pub, data, nil)
			if err != nil {
				return throwf(ctx, "RSA-OAEP: %v", err)
			}
			return bytesResult(ctx, out)
		}
		priv, err := jwkToRSAPrivate(k)
		if err != nil {
			return throwf(ctx, "RSA-OAEP: %v", err)
		}
		out, err := rsa.DecryptOAEP(nh(), rand.Reader, priv, data, nil)
		if err != nil {
			return throwf(ctx, "RSA-OAEP: decryption failed")
		}
		return bytesResult(ctx, out)
	}

	return throwf(ctx, "crypt: unsupported algorithm %q", p.Name)
}

// ---------------------------------------------------------------------------
// deriveBits (ECDH / X25519)
// ---------------------------------------------------------------------------

func nativeDeriveBits(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var priv, pub jwk
	if err := json.Unmarshal([]byte(argStr(info, 0)), &priv); err != nil {
		return throwf(ctx, "deriveBits: invalid private key: %v", err)
	}
	if err := json.Unmarshal([]byte(argStr(info, 1)), &pub); err != nil {
		return throwf(ctx, "deriveBits: invalid public key: %v", err)
	}
	lengthBits := int(info.Args()[2].Integer())

	var secret []byte
	if priv.Crv == "X25519" {
		privKey, err := ecdh.X25519().NewPrivateKey(mustB64u(priv.D))
		if err != nil {
			return throwf(ctx, "deriveBits X25519: %v", err)
		}
		pubKey, err := ecdh.X25519().NewPublicKey(mustB64u(pub.X))
		if err != nil {
			return throwf(ctx, "deriveBits X25519: %v", err)
		}
		secret, err = privKey.ECDH(pubKey)
		if err != nil {
			return throwf(ctx, "deriveBits X25519: %v", err)
		}
	} else {
		curve, err := ecdhCurveByName(priv.Crv)
		if err != nil {
			return throwf(ctx, "deriveBits: %v", err)
		}
		privKey, err := curve.NewPrivateKey(mustB64u(priv.D))
		if err != nil {
			return throwf(ctx, "deriveBits EC: %v", err)
		}
		pubKey, err := ecdhPublicFromXY(curve, pub.X, pub.Y)
		if err != nil {
			return throwf(ctx, "deriveBits EC: %v", err)
		}
		secret, err = privKey.ECDH(pubKey)
		if err != nil {
			return throwf(ctx, "deriveBits EC: %v", err)
		}
	}

	// WebCrypto deriveBits returns exactly `length` bits; JOSE requests the full
	// coordinate size, so this is normally a no-op, but honor a shorter request.
	if lengthBits > 0 {
		n := lengthBits / 8
		if n <= len(secret) {
			secret = secret[:n]
		}
	}
	return bytesResult(ctx, secret)
}

// nativePbkdf2 implements PBKDF2 deriveBits (used by JOSE's PBES2 key wrapping).
// Params JSON: {passwordB64, saltB64, iterations, hash, length(bits)}.
func nativePbkdf2(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var p struct {
		Password   string `json:"passwordB64"`
		Salt       string `json:"saltB64"`
		Iterations int    `json:"iterations"`
		Hash       string `json:"hash"`
		Length     int    `json:"length"`
	}
	if err := json.Unmarshal([]byte(argStr(info, 0)), &p); err != nil {
		return throwf(ctx, "pbkdf2: invalid params: %v", err)
	}
	_, nh, ok := hashByName(p.Hash)
	if !ok {
		return throwf(ctx, "pbkdf2: unsupported hash %q", p.Hash)
	}
	password, err := b64sDec(p.Password)
	if err != nil {
		return throwf(ctx, "pbkdf2: bad password: %v", err)
	}
	salt, err := b64sDec(p.Salt)
	if err != nil {
		return throwf(ctx, "pbkdf2: bad salt: %v", err)
	}
	if p.Iterations <= 0 {
		return throwf(ctx, "pbkdf2: iterations must be positive")
	}
	dk, err := pbkdf2.Key(nh, string(password), salt, p.Iterations, p.Length/8)
	if err != nil {
		return throwf(ctx, "pbkdf2: %v", err)
	}
	return bytesResult(ctx, dk)
}

// ---------------------------------------------------------------------------
// AES-KW (RFC 3394 key wrap)
// ---------------------------------------------------------------------------

func nativeAesKwWrap(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	kek, err := b64sDec(argStr(info, 0))
	if err != nil {
		return throwf(ctx, "aesKwWrap: bad kek: %v", err)
	}
	plaintext, err := b64sDec(argStr(info, 1))
	if err != nil {
		return throwf(ctx, "aesKwWrap: bad key: %v", err)
	}
	out, err := aesKwWrap(kek, plaintext)
	if err != nil {
		return throwf(ctx, "aesKwWrap: %v", err)
	}
	return bytesResult(ctx, out)
}

func nativeAesKwUnwrap(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	kek, err := b64sDec(argStr(info, 0))
	if err != nil {
		return throwf(ctx, "aesKwUnwrap: bad kek: %v", err)
	}
	wrapped, err := b64sDec(argStr(info, 1))
	if err != nil {
		return throwf(ctx, "aesKwUnwrap: bad data: %v", err)
	}
	out, err := aesKwUnwrap(kek, wrapped)
	if err != nil {
		return throwf(ctx, "aesKwUnwrap: %v", err)
	}
	return bytesResult(ctx, out)
}

var aesKwIV = []byte{0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6, 0xA6}

func aesKwWrap(kek, plaintext []byte) ([]byte, error) {
	if len(plaintext)%8 != 0 || len(plaintext) < 16 {
		return nil, fmt.Errorf("invalid key length for AES-KW")
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	n := len(plaintext) / 8
	r := make([][]byte, n)
	for i := 0; i < n; i++ {
		r[i] = make([]byte, 8)
		copy(r[i], plaintext[i*8:])
	}
	a := make([]byte, 8)
	copy(a, aesKwIV)
	buf := make([]byte, 16)
	for j := 0; j < 6; j++ {
		for i := 0; i < n; i++ {
			copy(buf[:8], a)
			copy(buf[8:], r[i])
			block.Encrypt(buf, buf)
			copy(a, buf[:8])
			t := uint64(n*j + i + 1)
			a[7] ^= byte(t)
			a[6] ^= byte(t >> 8)
			a[5] ^= byte(t >> 16)
			a[4] ^= byte(t >> 24)
			copy(r[i], buf[8:])
		}
	}
	out := make([]byte, 0, 8*(n+1))
	out = append(out, a...)
	for i := 0; i < n; i++ {
		out = append(out, r[i]...)
	}
	return out, nil
}

func aesKwUnwrap(kek, wrapped []byte) ([]byte, error) {
	if len(wrapped)%8 != 0 || len(wrapped) < 24 {
		return nil, fmt.Errorf("invalid wrapped key length for AES-KW")
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	n := len(wrapped)/8 - 1
	a := make([]byte, 8)
	copy(a, wrapped[:8])
	r := make([][]byte, n)
	for i := 0; i < n; i++ {
		r[i] = make([]byte, 8)
		copy(r[i], wrapped[(i+1)*8:])
	}
	buf := make([]byte, 16)
	for j := 5; j >= 0; j-- {
		for i := n - 1; i >= 0; i-- {
			t := uint64(n*j + i + 1)
			ai := make([]byte, 8)
			copy(ai, a)
			ai[7] ^= byte(t)
			ai[6] ^= byte(t >> 8)
			ai[5] ^= byte(t >> 16)
			ai[4] ^= byte(t >> 24)
			copy(buf[:8], ai)
			copy(buf[8:], r[i])
			block.Decrypt(buf, buf)
			copy(a, buf[:8])
			copy(r[i], buf[8:])
		}
	}
	if subtleConstEq(a, aesKwIV) != 1 {
		return nil, fmt.Errorf("AES-KW integrity check failed")
	}
	out := make([]byte, 0, 8*n)
	for i := 0; i < n; i++ {
		out = append(out, r[i]...)
	}
	return out, nil
}

func subtleConstEq(a, b []byte) int {
	if len(a) != len(b) {
		return 0
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	if v == 0 {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// JWK <-> Go key conversions
// ---------------------------------------------------------------------------

func rsaToJWK(priv *rsa.PrivateKey) (pub jwk, prv jwk) {
	priv.Precompute()
	n := priv.N.Bytes()
	e := big.NewInt(int64(priv.E)).Bytes()
	pub = jwk{Kty: "RSA", N: b64uEnc(n), E: b64uEnc(e)}
	prv = jwk{
		Kty: "RSA",
		N:   b64uEnc(n),
		E:   b64uEnc(e),
		D:   b64uEnc(priv.D.Bytes()),
		P:   b64uEnc(priv.Primes[0].Bytes()),
		Q:   b64uEnc(priv.Primes[1].Bytes()),
		Dp:  b64uEnc(priv.Precomputed.Dp.Bytes()),
		Dq:  b64uEnc(priv.Precomputed.Dq.Bytes()),
		Qi:  b64uEnc(priv.Precomputed.Qinv.Bytes()),
	}
	return pub, prv
}

func jwkToRSAPublic(k jwk) (*rsa.PublicKey, error) {
	nb, err := b64uDec(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := b64uDec(k.E)
	if err != nil {
		return nil, err
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: int(new(big.Int).SetBytes(eb).Int64()),
	}, nil
}

func jwkToRSAPrivate(k jwk) (*rsa.PrivateKey, error) {
	pub, err := jwkToRSAPublic(k)
	if err != nil {
		return nil, err
	}
	db, err := b64uDec(k.D)
	if err != nil {
		return nil, err
	}
	pb, err := b64uDec(k.P)
	if err != nil {
		return nil, err
	}
	qb, err := b64uDec(k.Q)
	if err != nil {
		return nil, err
	}
	priv := &rsa.PrivateKey{
		PublicKey: *pub,
		D:         new(big.Int).SetBytes(db),
		Primes: []*big.Int{
			new(big.Int).SetBytes(pb),
			new(big.Int).SetBytes(qb),
		},
	}
	priv.Precompute()
	if err := priv.Validate(); err != nil {
		return nil, err
	}
	return priv, nil
}

func curveByName(name string) (elliptic.Curve, string, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), "P-256", nil
	case "P-384":
		return elliptic.P384(), "P-384", nil
	case "P-521":
		return elliptic.P521(), "P-521", nil
	}
	return nil, "", fmt.Errorf("unsupported curve %q", name)
}

func ecdhCurveByName(name string) (ecdh.Curve, error) {
	switch name {
	case "P-256":
		return ecdh.P256(), nil
	case "P-384":
		return ecdh.P384(), nil
	case "P-521":
		return ecdh.P521(), nil
	}
	return nil, fmt.Errorf("unsupported curve %q", name)
}

func ecToJWK(priv *ecdsa.PrivateKey, crv string) (pub jwk, prv jwk) {
	size := (priv.Curve.Params().BitSize + 7) / 8
	x := padLeft(priv.X.Bytes(), size)
	y := padLeft(priv.Y.Bytes(), size)
	pub = jwk{Kty: "EC", Crv: crv, X: b64uEnc(x), Y: b64uEnc(y)}
	prv = jwk{Kty: "EC", Crv: crv, X: b64uEnc(x), Y: b64uEnc(y), D: b64uEnc(padLeft(priv.D.Bytes(), size))}
	return pub, prv
}

func jwkToECDSAPublic(k jwk) (*ecdsa.PublicKey, error) {
	curve, _, err := curveByName(k.Crv)
	if err != nil {
		return nil, err
	}
	xb, err := b64uDec(k.X)
	if err != nil {
		return nil, err
	}
	yb, err := b64uDec(k.Y)
	if err != nil {
		return nil, err
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xb),
		Y:     new(big.Int).SetBytes(yb),
	}, nil
}

func jwkToECDSAPrivate(k jwk) (*ecdsa.PrivateKey, error) {
	pub, err := jwkToECDSAPublic(k)
	if err != nil {
		return nil, err
	}
	db, err := b64uDec(k.D)
	if err != nil {
		return nil, err
	}
	return &ecdsa.PrivateKey{PublicKey: *pub, D: new(big.Int).SetBytes(db)}, nil
}

func ecdhPublicFromXY(curve ecdh.Curve, xB64, yB64 string) (*ecdh.PublicKey, error) {
	xb, err := b64uDec(xB64)
	if err != nil {
		return nil, err
	}
	yb, err := b64uDec(yB64)
	if err != nil {
		return nil, err
	}
	// Uncompressed point: 0x04 || X || Y (fixed-width coordinates).
	size := len(xb)
	if len(yb) > size {
		size = len(yb)
	}
	point := make([]byte, 0, 1+2*size)
	point = append(point, 0x04)
	point = append(point, padLeft(xb, size)...)
	point = append(point, padLeft(yb, size)...)
	return curve.NewPublicKey(point)
}

func jwkToEd25519Public(k jwk) (ed25519.PublicKey, error) {
	xb, err := b64uDec(k.X)
	if err != nil {
		return nil, err
	}
	if len(xb) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 public key size")
	}
	return ed25519.PublicKey(xb), nil
}

func jwkToEd25519Private(k jwk) (ed25519.PrivateKey, error) {
	db, err := b64uDec(k.D)
	if err != nil {
		return nil, err
	}
	if len(db) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid Ed25519 seed size")
	}
	return ed25519.NewKeyFromSeed(db), nil
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func bytesResult(ctx *v8.Context, b []byte) *v8.Value {
	val, _ := ctx.NewString(b64s.EncodeToString(b))
	return val
}

func mustB64u(s string) []byte {
	b, _ := b64uDec(s)
	return b
}

func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-pad], nil
}
