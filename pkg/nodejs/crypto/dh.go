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

// Key agreement for createECDH and crypto.diffieHellman, on Go's
// crypto/ecdh. dh.js keeps each object's keys and calls these stateless
// helpers; errors cross as "CODE|message", as in sign.go. Finite-field
// Diffie-Hellman isn't offered: its private-key exponentiation would need
// math/big, which isn't constant time.

//go:embed dh.js
var dhJS string

func dhErr(code, message string) error { return &signError{code, message} }

var (
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
