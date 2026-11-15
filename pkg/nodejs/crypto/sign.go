package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"hash"
	"math/big"

	gocrypto "crypto"

	"proto.zip/studio/orbital/pkg/v8"
)

// Signatures for createSign/createVerify and crypto.sign/verify. sign.js
// resolves keys and options; these sign or verify with the key behind a
// KeyObject handle. Errors cross as "CODE|message", as in cipher.go.

//go:embed sign.js
var signJS string

const (
	rsaPKCS1Padding = 1
	rsaPSSPadding   = 6
	pssSaltDigest   = -1
	pssSaltMaxAuto  = -2
)

type signError struct{ code, message string }

func (e *signError) Error() string { return e.code + "|" + e.message }

var (
	errUnsupportedOp  = &signError{"ERR_CRYPTO_UNSUPPORTED_OPERATION", "Unsupported crypto operation"}
	errInvalidDigest  = &signError{"ERR_OSSL_INVALID_DIGEST", "error:1C80007A:Provider routines::invalid digest"}
	errIllegalPadding = &signError{"ERR_OSSL_RSA_ILLEGAL_OR_UNSUPPORTED_PADDING_MODE", "error:1C8000A5:Provider routines::illegal or unsupported padding mode"}
	errDigestTooBig   = &signError{"ERR_OSSL_RSA_DIGEST_TOO_BIG_FOR_RSA_KEY", "error:02000070:rsa routines::digest too big for rsa key"}
	errSigFailed      = &signError{"", "Sign failed"}
)

// digestFor returns the hash for a digest name, with "" meaning the default
// OpenSSL picks (SHA-256) for RSA and ECDSA.
func digestFor(name string) (gocrypto.Hash, func() hash.Hash, bool) {
	if name == "" {
		name = "sha256"
	}
	h := nodeDigest(name)
	if h == nil {
		return 0, nil, false
	}
	return hashID(h), h, true
}

// hashID finds the crypto.Hash a digest constructor makes, by output size
// and block size, which is enough to tell the supported digests apart.
func hashID(h func() hash.Hash) gocrypto.Hash {
	sample := h()
	for _, id := range []gocrypto.Hash{gocrypto.MD5, gocrypto.SHA1, gocrypto.SHA224, gocrypto.SHA256, gocrypto.SHA384,
		gocrypto.SHA512, gocrypto.SHA512_224, gocrypto.SHA512_256, gocrypto.SHA3_224, gocrypto.SHA3_256,
		gocrypto.SHA3_384, gocrypto.SHA3_512} {
		if !id.Available() {
			continue
		}
		other := id.New()
		if other.Size() == sample.Size() && other.BlockSize() == sample.BlockSize() &&
			string(other.Sum([]byte("orbital"))) == string(sample.Sum([]byte("orbital"))) {
			return id
		}
	}
	return 0
}

type signParams struct {
	digest     string
	streaming  bool // createSign/createVerify, which always hash
	padding    int
	saltLength int
	p1363      bool
}

func digestOf(h func() hash.Hash, data []byte) []byte {
	d := h()
	d.Write(data)
	return d.Sum(nil)
}

// pssSalt maps Node's saltLength to Go's: RSA_PSS_SALTLEN_DIGEST is the
// hash length, and RSA_PSS_SALTLEN_MAX_SIGN / _AUTO (both -2) are Go's Auto,
// the largest salt when signing and any salt when verifying.
func pssSalt(n int) int {
	switch n {
	case pssSaltDigest:
		return rsa.PSSSaltLengthEqualsHash
	case pssSaltMaxAuto:
		return rsa.PSSSaltLengthAuto
	}
	return n
}

// Go's PSS takes a salt length of 0 to mean "automatic", so a PSS
// signature with an empty salt (RFC 8017 EMSA-PSS with sLen 0) is made and
// checked here.
func mgf1XOR(out []byte, h func() hash.Hash, seed []byte) {
	var counter [4]byte
	done := 0
	for done < len(out) {
		d := h()
		d.Write(seed)
		d.Write(counter[:])
		block := d.Sum(nil)
		for i := 0; i < len(block) && done < len(out); i++ {
			out[done] ^= block[i]
			done++
		}
		for i := 3; i >= 0; i-- {
			counter[i]++
			if counter[i] != 0 {
				break
			}
		}
	}
}

func pssNoSaltHash(h func() hash.Hash, mHash []byte) []byte {
	d := h()
	d.Write(make([]byte, 8))
	d.Write(mHash)
	return d.Sum(nil)
}

func signPSSNoSalt(k *rsa.PrivateKey, h func() hash.Hash, mHash []byte) ([]byte, error) {
	emBits := k.N.BitLen() - 1
	emLen := (emBits + 7) / 8
	hLen := len(mHash)
	if emLen < hLen+2 {
		return nil, errDigestTooBig
	}
	H := pssNoSaltHash(h, mHash)
	db := make([]byte, emLen-hLen-1)
	db[len(db)-1] = 0x01
	mgf1XOR(db, h, H)
	db[0] &= 0xff >> (8*emLen - emBits)
	em := append(append(db, H...), 0xbc)
	k.Precompute()
	m := new(big.Int).SetBytes(em)
	sig := new(big.Int).Exp(m, k.D, k.N)
	return sig.FillBytes(make([]byte, (k.N.BitLen()+7)/8)), nil
}

func verifyPSSNoSalt(k *rsa.PublicKey, h func() hash.Hash, mHash, sig []byte) bool {
	size := (k.N.BitLen() + 7) / 8
	if len(sig) != size {
		return false
	}
	s := new(big.Int).SetBytes(sig)
	if s.Cmp(k.N) >= 0 {
		return false
	}
	emBits := k.N.BitLen() - 1
	emLen := (emBits + 7) / 8
	em := new(big.Int).Exp(s, big.NewInt(int64(k.E)), k.N).FillBytes(make([]byte, emLen))
	hLen := len(mHash)
	if emLen < hLen+2 || em[emLen-1] != 0xbc {
		return false
	}
	db := append([]byte{}, em[:emLen-hLen-1]...)
	H := em[emLen-hLen-1 : emLen-1]
	if db[0]&^(0xff>>(8*emLen-emBits)) != 0 {
		return false
	}
	mgf1XOR(db, h, H)
	db[0] &= 0xff >> (8*emLen - emBits)
	for _, b := range db[:len(db)-1] {
		if b != 0 {
			return false
		}
	}
	if db[len(db)-1] != 0x01 {
		return false
	}
	return string(pssNoSaltHash(h, mHash)) == string(H)
}

func ecdsaSize(k *ecdsa.PublicKey) int {
	return (k.Curve.Params().BitSize + 7) / 8
}

func signWith(priv any, data []byte, p signParams) ([]byte, error) {
	switch k := priv.(type) {
	case ed25519.PrivateKey:
		if p.streaming {
			return nil, errUnsupportedOp
		}
		if p.digest != "" {
			return nil, errInvalidDigest
		}
		return ed25519.Sign(k, data), nil
	case *rsa.PrivateKey:
		id, h, ok := digestFor(p.digest)
		if !ok || id == 0 {
			return nil, errInvalidDigest
		}
		sum := digestOf(h, data)
		switch p.padding {
		case rsaPKCS1Padding:
			sig, err := rsa.SignPKCS1v15(rand.Reader, k, id, sum)
			if err != nil {
				return nil, errDigestTooBig
			}
			return sig, nil
		case rsaPSSPadding:
			if p.saltLength == 0 {
				return signPSSNoSalt(k, h, sum)
			}
			sig, err := rsa.SignPSS(rand.Reader, k, id, sum, &rsa.PSSOptions{SaltLength: pssSalt(p.saltLength)})
			if err != nil {
				return nil, errDigestTooBig
			}
			return sig, nil
		}
		return nil, errIllegalPadding
	case *ecdsa.PrivateKey:
		_, h, ok := digestFor(p.digest)
		if !ok {
			return nil, errInvalidDigest
		}
		der, err := ecdsa.SignASN1(rand.Reader, k, digestOf(h, data))
		if err != nil {
			return nil, errSigFailed
		}
		if !p.p1363 {
			return der, nil
		}
		var rs struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &rs); err != nil {
			return nil, errSigFailed
		}
		n := ecdsaSize(&k.PublicKey)
		out := make([]byte, 2*n)
		rs.R.FillBytes(out[:n])
		rs.S.FillBytes(out[n:])
		return out, nil
	}
	return nil, errUnsupportedOp
}

func verifyWith(pub any, data, sig []byte, p signParams) (bool, error) {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		if p.streaming {
			return false, errUnsupportedOp
		}
		if p.digest != "" {
			return false, errInvalidDigest
		}
		return ed25519.Verify(k, data, sig), nil
	case *rsa.PublicKey:
		id, h, ok := digestFor(p.digest)
		if !ok || id == 0 {
			return false, errInvalidDigest
		}
		sum := digestOf(h, data)
		switch p.padding {
		case rsaPKCS1Padding:
			return rsa.VerifyPKCS1v15(k, id, sum, sig) == nil, nil
		case rsaPSSPadding:
			if p.saltLength == 0 {
				return verifyPSSNoSalt(k, h, sum, sig), nil
			}
			return rsa.VerifyPSS(k, id, sum, sig, &rsa.PSSOptions{SaltLength: pssSalt(p.saltLength)}) == nil, nil
		}
		return false, errIllegalPadding
	case *ecdsa.PublicKey:
		_, h, ok := digestFor(p.digest)
		if !ok {
			return false, errInvalidDigest
		}
		sum := digestOf(h, data)
		if !p.p1363 {
			return ecdsa.VerifyASN1(k, sum, sig), nil
		}
		n := ecdsaSize(k)
		if len(sig) != 2*n {
			return false, nil
		}
		r := new(big.Int).SetBytes(sig[:n])
		s := new(big.Int).SetBytes(sig[n:])
		return ecdsa.Verify(k, sum, r, s), nil
	}
	return false, errUnsupportedOp
}

func signParamsFrom(args []*v8.Value, from int) signParams {
	return signParams{
		digest:     args[from].String(),
		streaming:  args[from+1].Boolean(),
		padding:    int(args[from+2].Integer()),
		saltLength: int(args[from+3].Integer()),
		p1363:      args[from+4].Boolean(),
	}
}

// signFunc backs _sign(handle, dataB64, digest, streaming, padding,
// saltLength, p1363): the signature as base64.
func (c *Crypto) signFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 7 {
		return ctx.Throw("sign: missing arguments")
	}
	e, err := c.lookupKey(args[0].Integer())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	priv, err := e.cryptoPriv()
	if err != nil || e.kind != "private" {
		return ctx.Throw(errUnsupportedOp.Error())
	}
	data, _ := base64.StdEncoding.DecodeString(args[1].String())
	params := signParamsFrom(args, 2)
	if len(args) > 7 && args[7].IsFunction() {
		// The callback form signs off the JS thread.
		c.runOffThread(args[7], func() func(*v8.Context) []*v8.Value {
			sig, err := signWith(priv, data, params)
			return func(ctx *v8.Context) []*v8.Value {
				if err != nil {
					return errorArgs(ctx, err)
				}
				v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(sig))
				return []*v8.Value{ctx.Undefined(), v}
			}
		})
		return ctx.Undefined()
	}
	sig, err := signWith(priv, data, params)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(sig))
	return v
}

// verifyFunc backs _verify(handle, dataB64, sigB64, digest, streaming,
// padding, saltLength, p1363): true or false.
func (c *Crypto) verifyFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 8 {
		return ctx.Throw("verify: missing arguments")
	}
	e, err := c.lookupKey(args[0].Integer())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	pub, err := e.cryptoPub()
	if err != nil {
		return ctx.Throw(errUnsupportedOp.Error())
	}
	data, _ := base64.StdEncoding.DecodeString(args[1].String())
	sig, _ := base64.StdEncoding.DecodeString(args[2].String())
	params := signParamsFrom(args, 3)
	if len(args) > 8 && args[8].IsFunction() {
		// The callback form verifies off the JS thread.
		c.runOffThread(args[8], func() func(*v8.Context) []*v8.Value {
			ok, err := verifyWith(pub, data, sig, params)
			return func(ctx *v8.Context) []*v8.Value {
				if err != nil {
					return errorArgs(ctx, err)
				}
				return []*v8.Value{ctx.Undefined(), ctx.NewBoolean(ok)}
			}
		})
		return ctx.Undefined()
	}
	ok, err := verifyWith(pub, data, sig, params)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	return ctx.NewBoolean(ok)
}
