package crypto

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"math/big"

	"proto.zip/studio/orbital/pkg/v8"
)

// Diffie-Hellman for createDiffieHellman / getDiffieHellman, createECDH and
// crypto.diffieHellman. dh.js keeps each object's keys and calls these
// stateless helpers; errors cross as "CODE|message", as in sign.go.

//go:embed dh.js
var dhJS string

func dhErr(code, message string) error { return &signError{code, message} }

var (
	errKeyTooSmall      = dhErr("ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too small")
	errKeyTooLarge      = dhErr("ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too large")
	errInvalidECDHPub   = dhErr("ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY", "Public key is not valid for specified curve")
	errInvalidECDHPriv  = dhErr("ERR_CRYPTO_INVALID_KEYTYPE", "Private key is not valid for specified curve.")
	errECPointConvert   = dhErr("ERR_CRYPTO_OPERATION_FAILED", "Failed to convert Buffer to EC_POINT")
	errIncompatibleKeys = func(a, b string) error {
		return dhErr("ERR_CRYPTO_INCOMPATIBLE_KEY", "Incompatible key types for Diffie-Hellman: "+a+" and "+b)
	}
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func b64Arg(args []*v8.Value, i int) []byte {
	if i >= len(args) {
		return nil
	}
	b, _ := base64.StdEncoding.DecodeString(args[i].String())
	return b
}

func throwOrString(ctx *v8.Context, s string, err error) *v8.Value {
	if err != nil {
		return ctx.Throw(err.Error())
	}
	v, _ := ctx.NewString(s)
	return v
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// --- finite-field DH -------------------------------------------------------

// dhSafePrime makes a safe prime of bits bits that suits generator g, as
// OpenSSL's DH_generate_parameters does (p = 23 mod 24 for 2, p = 3 or 7
// mod 10 for 5).
func dhSafePrime(bits int, g int64) (*big.Int, error) {
	one := big.NewInt(1)
	m := new(big.Int)
	for {
		q, err := rand.Prime(rand.Reader, bits-1)
		if err != nil {
			return nil, err
		}
		p := new(big.Int).Lsh(q, 1)
		p.Add(p, one)
		switch g {
		case 2:
			if m.Mod(p, big.NewInt(24)).Int64() != 23 {
				continue
			}
		case 5:
			if r := m.Mod(p, big.NewInt(10)).Int64(); r != 3 && r != 7 {
				continue
			}
		}
		if p.BitLen() == bits && p.ProbablyPrime(20) {
			return p, nil
		}
	}
}

// dhCheck is the verifyError value OpenSSL's DH_check gives for p and g.
func dhCheck(p, g *big.Int) int {
	const notPrime, notSafe, badGenerator, tooSmall = 0x01, 0x02, 0x08, 0x80
	flags := 0
	if p.BitLen() < 512 {
		flags |= tooSmall
	}
	if !p.ProbablyPrime(20) {
		flags |= notPrime
	} else if q := new(big.Int).Rsh(p, 1); !q.ProbablyPrime(20) {
		flags |= notSafe
	}
	pm1 := new(big.Int).Sub(p, big.NewInt(1))
	if g.Cmp(big.NewInt(1)) <= 0 || g.Cmp(pm1) >= 0 {
		flags |= badGenerator
	}
	return flags
}

// dhPrivateBits is the private exponent size OpenSSL uses for a group of
// the given size: twice its security strength.
func dhPrivateBits(pBits int) int {
	switch {
	case pBits >= 8192:
		return 400
	case pBits >= 6144:
		return 352
	case pBits >= 4096:
		return 304
	case pBits >= 3072:
		return 256
	case pBits >= 2048:
		return 224
	case pBits >= 1536:
		return 192
	}
	return 160
}

func (c *Crypto) dhGeneratePrimeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("dh: missing arguments")
	}
	bits := args[0].Integer()
	if bits < 2 || bits > 10000 {
		return ctx.Throw(dhErr("ERR_OSSL_BN_BITS_TOO_SMALL", "error:01800076:bignum routines::bits too small").Error())
	}
	p, err := dhSafePrime(int(bits), args[1].Integer())
	return throwOrString(ctx, b64(p.Bytes()), err)
}

func (c *Crypto) dhGroupFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	hexPrime, ok := modpGroups[info.Args()[0].String()]
	if !ok {
		v, _ := ctx.NewString("")
		return v
	}
	p, _ := new(big.Int).SetString(hexPrime, 16)
	v, _ := ctx.NewString(b64(p.Bytes()))
	return v
}

func (c *Crypto) dhCheckFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	p := new(big.Int).SetBytes(b64Arg(args, 0))
	g := new(big.Int).SetBytes(b64Arg(args, 1))
	return info.Context().NewNumber(float64(dhCheck(p, g)))
}

// dhKeysFunc backs _dhKeys(primeB64, generatorB64, privateB64, privateBits):
// {priv, pub} as base64, making a private key when privateB64 is "". A
// privateBits of 0 or less picks the size for the prime's strength.
func (c *Crypto) dhKeysFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	p := new(big.Int).SetBytes(b64Arg(args, 0))
	g := new(big.Int).SetBytes(b64Arg(args, 1))
	if p.Sign() == 0 {
		return ctx.Throw("dh: no prime")
	}
	x := new(big.Int).SetBytes(b64Arg(args, 2))
	if x.Sign() == 0 {
		bits := int(args[3].Integer())
		if bits <= 0 || bits > p.BitLen() {
			bits = dhPrivateBits(p.BitLen())
		}
		limit := new(big.Int).Lsh(big.NewInt(1), uint(bits))
		if limit.Cmp(p) > 0 {
			limit.Sub(p, big.NewInt(2))
		}
		r, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return ctx.Throw(err.Error())
		}
		x = r.Add(r, big.NewInt(1))
	}
	y := new(big.Int).Exp(g, x, p)
	v, _ := ctx.NewString(jsonString(map[string]string{"priv": b64(x.Bytes()), "pub": b64(y.Bytes())}))
	return v
}

// dhSecretFunc backs _dhSecret(primeB64, privateB64, otherB64): the shared
// secret, padded to the prime's length as Node pads it.
func (c *Crypto) dhSecretFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	p := new(big.Int).SetBytes(b64Arg(args, 0))
	x := new(big.Int).SetBytes(b64Arg(args, 1))
	y := new(big.Int).SetBytes(b64Arg(args, 2))
	pm1 := new(big.Int).Sub(p, big.NewInt(1))
	if y.Cmp(big.NewInt(1)) <= 0 {
		return ctx.Throw(errKeyTooSmall.Error())
	}
	if y.Cmp(pm1) >= 0 {
		return ctx.Throw(errKeyTooLarge.Error())
	}
	s := new(big.Int).Exp(y, x, p)
	return throwOrString(ctx, b64(s.FillBytes(make([]byte, (p.BitLen()+7)/8))), nil)
}

// --- ECDH ---------------------------------------------------------------------

var ecdhCurves = map[string]struct {
	curve  ecdh.Curve
	params elliptic.Curve
}{
	"prime256v1": {ecdh.P256(), elliptic.P256()},
	"secp384r1":  {ecdh.P384(), elliptic.P384()},
	"secp521r1":  {ecdh.P521(), elliptic.P521()},
}

// decodePoint reads a compressed, uncompressed or hybrid point as an
// uncompressed one, checking it is on the curve.
func decodePoint(name string, b []byte) ([]byte, bool) {
	cv, ok := ecdhCurves[name]
	if !ok || len(b) == 0 {
		return nil, false
	}
	switch b[0] {
	case 2, 3:
		x, y := elliptic.UnmarshalCompressed(cv.params, b)
		if x == nil {
			return nil, false
		}
		return elliptic.Marshal(cv.params, x, y), true
	case 6, 7:
		u := append([]byte{4}, b[1:]...)
		if _, err := cv.curve.NewPublicKey(u); err != nil {
			return nil, false
		}
		size := (cv.params.Params().BitSize + 7) / 8
		if y := u[1+size:]; y[len(y)-1]&1 != b[0]&1 {
			return nil, false
		}
		return u, true
	case 4:
		if _, err := cv.curve.NewPublicKey(b); err != nil {
			return nil, false
		}
		return b, true
	}
	return nil, false
}

// formatPoint writes an uncompressed point in Node's format.
func formatPoint(name string, u []byte, format string) []byte {
	size := (ecdhCurves[name].params.Params().BitSize + 7) / 8
	x, y := u[1:1+size], u[1+size:]
	odd := y[len(y)-1] & 1
	switch format {
	case "compressed":
		return append([]byte{2 + odd}, x...)
	case "hybrid":
		return append([]byte{6 + odd}, u[1:]...)
	}
	return u
}

func ecdhPrivate(name string, d []byte) (*ecdh.PrivateKey, error) {
	cv := ecdhCurves[name]
	size := (cv.params.Params().BitSize + 7) / 8
	n := new(big.Int).SetBytes(d)
	if len(d) > size && n.BitLen() > size*8 {
		return nil, errInvalidECDHPriv
	}
	k, err := cv.curve.NewPrivateKey(n.FillBytes(make([]byte, size)))
	if err != nil {
		return nil, errInvalidECDHPriv
	}
	return k, nil
}

// ecdhFunc backs _ecdh(op, curve, a, b, format):
//
//	"generate"            -> {priv, pub}
//	"fromPrivate", priv   -> {priv, pub}
//	"convert", point      -> the point in format
//	"secret", priv, point -> the shared secret
func (c *Crypto) ecdhFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 5 {
		return ctx.Throw("ecdh: missing arguments")
	}
	op, name, format := args[0].String(), args[1].String(), args[4].String()
	cv, ok := ecdhCurves[name]
	if !ok {
		return ctx.Throw(dhErr("ERR_CRYPTO_INVALID_CURVE", "Invalid EC curve name").Error())
	}
	keys := func(k *ecdh.PrivateKey) *v8.Value {
		return throwOrString(ctx, jsonString(map[string]string{"priv": b64(k.Bytes()), "pub": b64(k.PublicKey().Bytes())}), nil)
	}
	switch op {
	case "generate":
		k, err := cv.curve.GenerateKey(rand.Reader)
		if err != nil {
			return ctx.Throw(err.Error())
		}
		return keys(k)
	case "fromPrivate":
		k, err := ecdhPrivate(name, b64Arg(args, 2))
		if err != nil {
			return ctx.Throw(err.Error())
		}
		return keys(k)
	case "convert":
		u, ok := decodePoint(name, b64Arg(args, 2))
		if !ok {
			return ctx.Throw(errECPointConvert.Error())
		}
		return throwOrString(ctx, b64(formatPoint(name, u, format)), nil)
	case "secret":
		k, err := ecdhPrivate(name, b64Arg(args, 2))
		if err != nil {
			return ctx.Throw(err.Error())
		}
		u, ok := decodePoint(name, b64Arg(args, 3))
		if !ok {
			return ctx.Throw(errInvalidECDHPub.Error())
		}
		pub, err := cv.curve.NewPublicKey(u)
		if err != nil {
			return ctx.Throw(errInvalidECDHPub.Error())
		}
		s, err := k.ECDH(pub)
		if err != nil {
			return ctx.Throw(errInvalidECDHPub.Error())
		}
		return throwOrString(ctx, b64(s), nil)
	}
	return ctx.Throw("ecdh: unknown operation " + op)
}

// --- crypto.diffieHellman with KeyObjects ---------------------------------------

// keyAgreementFunc backs _keyAgreement(privateHandle, publicHandle).
func (c *Crypto) keyAgreementFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("diffieHellman: missing arguments")
	}
	priv, err := c.lookupKey(args[0].Integer())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	pub, err := c.lookupKey(args[1].Integer())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	if priv.asymType != pub.asymType || (priv.asymType == "ec" && priv.crv != pub.crv) {
		return ctx.Throw(errIncompatibleKeys(priv.asymType, pub.asymType).Error())
	}
	var secret []byte
	switch priv.asymType {
	case "x25519":
		p := pub.xPub
		if p == nil {
			p = pub.xPriv.PublicKey()
		}
		secret, err = priv.xPriv.ECDH(p)
	case "ec":
		var a *ecdh.PrivateKey
		var b *ecdh.PublicKey
		if a, err = priv.ecPriv.ECDH(); err == nil {
			ecPub := pub.ecPub
			if ecPub == nil {
				ecPub = &pub.ecPriv.PublicKey
			}
			if b, err = ecPub.ECDH(); err == nil {
				secret, err = a.ECDH(b)
			}
		}
	default:
		return ctx.Throw(dhErr("ERR_CRYPTO_INCOMPATIBLE_KEY", "Incompatible key types for Diffie-Hellman: "+priv.asymType+" and "+pub.asymType).Error())
	}
	if err != nil {
		return ctx.Throw(dhErr("ERR_CRYPTO_OPERATION_FAILED", "Failed to compute shared secret").Error())
	}
	return throwOrString(ctx, b64(secret), nil)
}
