package crypto

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"

	"proto.zip/studio/orbital/internal/bigmod"
	"proto.zip/studio/orbital/pkg/v8"
)

// Diffie-Hellman for createDiffieHellman / getDiffieHellman, createECDH and
// crypto.diffieHellman. dh.js keeps each object's keys and calls these
// stateless helpers; errors cross as "CODE|message", as in sign.go. ECDH uses
// Go's crypto/ecdh. Finite-field DH does its private-key exponentiation with
// a copy of Go's constant-time bigmod (internal/bigmod); math/big handles
// only public values (primes, generators, checks).

//go:embed dh.js
var dhJS string

func dhErr(code, message string) error { return &signError{code, message} }

// dhMaxModulusBits is OpenSSL's largest Diffie-Hellman modulus
// (OPENSSL_DH_MAX_MODULUS_BITS).
const dhMaxModulusBits = 10000

// dhCheckMaxModulusBits is the largest modulus OpenSSL's DH_check looks at
// (OPENSSL_DH_CHECK_MAX_MODULUS_BITS); a larger one is "modulus too large".
const dhCheckMaxModulusBits = 32768

var (
	errKeyTooSmall      = dhErr("ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too small")
	errKeyTooLarge      = dhErr("ERR_CRYPTO_INVALID_KEYLEN", "Supplied key is too large")
	errModulusTooSmall  = dhErr("ERR_OSSL_DH_MODULUS_TOO_SMALL", "error:0280007E:Diffie-Hellman routines::modulus too small")
	errModulusTooLarge  = dhErr("ERR_OSSL_DH_MODULUS_TOO_LARGE", "error:02800067:Diffie-Hellman routines::modulus too large")
	errNoInverse        = dhErr("ERR_OSSL_BN_NO_INVERSE", "error:0180006C:bignum routines::no inverse")
	errInvalidKeyType   = dhErr("ERR_CRYPTO_INVALID_KEYTYPE", "Invalid key type")
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
// OpenSSL's DH_generate_parameters does (p = 23 mod 24 for 2, p = 3 mod 10
// for 5). It sieves candidates for p = 2q+1 by small primes, checking both q
// and p, before testing either for primality, as OpenSSL does: testing each
// candidate in full makes the time it takes vary by seconds.
func dhSafePrime(bits int, g int64) (*big.Int, error) {
	// q's step and residue keep p's residue for the generator.
	step, rem := int64(2), int64(1)
	switch g {
	case 2:
		step, rem = 12, 11 // q = 11 mod 12, so p = 23 mod 24
	case 5:
		step, rem = 10, 1 // q = 1 mod 10, so p = 3 mod 10
	}
	const window = 1 << 16
	residues := make([]uint64, len(sievePrimes))
	m := new(big.Int)
	for {
		// q has bits-1 bits with its top two set, so p = 2q+1 has bits bits.
		q, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(bits-1)))
		if err != nil {
			return nil, err
		}
		q.SetBit(q, bits-2, 1)
		q.SetBit(q, bits-3, 1)
		q.Sub(q, m.Mod(q, big.NewInt(step)))
		q.Add(q, big.NewInt(rem))
		for i, sp := range sievePrimes {
			residues[i] = m.Mod(q, new(big.Int).SetUint64(sp)).Uint64()
		}
	candidates:
		for delta := uint64(0); delta < window*uint64(step); delta += uint64(step) {
			for i, sp := range sievePrimes {
				r := (residues[i] + delta) % sp
				if r == 0 || (2*r+1)%sp == 0 {
					continue candidates
				}
			}
			cand := new(big.Int).Add(q, new(big.Int).SetUint64(delta))
			p := new(big.Int).Lsh(cand, 1)
			p.SetBit(p, 0, 1)
			if p.BitLen() != bits {
				break
			}
			if cand.ProbablyPrime(0) && p.ProbablyPrime(0) && cand.ProbablyPrime(20) && p.ProbablyPrime(20) {
				return p, nil
			}
		}
	}
}

// sievePrimes are the odd primes below 2048, for dhSafePrime's sieve.
var sievePrimes = func() []uint64 {
	const n = 2048
	composite := make([]bool, n)
	var primes []uint64
	for i := 3; i < n; i += 2 {
		if composite[i] {
			continue
		}
		primes = append(primes, uint64(i))
		for j := i * i; j < n; j += 2 * i {
			composite[j] = true
		}
	}
	return primes
}()

// dhCheck is the verifyError value OpenSSL's DH_check gives for p and g.
// The caller rules out moduli over dhCheckMaxModulusBits first.
func dhCheck(p, g *big.Int) int {
	const notPrime, notSafe, badGenerator, tooSmall, tooLarge = 0x01, 0x02, 0x08, 0x80, 0x100
	flags := 0
	if p.BitLen() < 512 {
		flags |= tooSmall
	}
	if p.BitLen() > dhMaxModulusBits {
		flags |= tooLarge
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
	if bits < 512 {
		return ctx.Throw(errModulusTooSmall.Error())
	}
	if bits > dhMaxModulusBits {
		return ctx.Throw(errModulusTooLarge.Error())
	}
	p, err := dhSafePrime(int(bits), args[1].Integer())
	return throwOrString(ctx, b64(p.Bytes()), err)
}

func (c *Crypto) dhGroupFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("dh: missing arguments")
	}
	// Node looks group names up regardless of case.
	hexPrime, ok := modpGroups[strings.ToLower(args[0].String())]
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
	if p.BitLen() > dhCheckMaxModulusBits {
		return info.Context().Throw(errModulusTooLarge.Error())
	}
	g := new(big.Int).SetBytes(b64Arg(args, 1))
	return info.Context().NewNumber(float64(dhCheck(p, g)))
}

// dhModulus is p as a bigmod modulus for the private-key exponentiation,
// with OpenSSL's size limits: under 512 bits is "modulus too small", over
// 10000 bits "modulus too large", and an even modulus has no Montgomery form
// ("no inverse").
func dhModulus(p *big.Int, tooLarge error) (*bigmod.Modulus, error) {
	if p.BitLen() < 512 {
		return nil, errModulusTooSmall
	}
	if p.BitLen() > dhMaxModulusBits {
		return nil, tooLarge
	}
	if p.Bit(0) == 0 {
		return nil, errNoInverse
	}
	return bigmod.NewModulus(p.Bytes())
}

// dhExp is base^exp mod m in constant time with respect to exp, the private
// key: it uses Go's bigmod (a copy in internal/bigmod), whose exponentiation
// takes the same time for every exponent of a given length. The exponent is
// padded to the modulus's length so its own length doesn't vary with its
// value. The base is public.
func dhExp(base *big.Int, exp []byte, m *bigmod.Modulus, p *big.Int) ([]byte, error) {
	size := m.Size()
	b, err := bigmod.NewNat().SetBytes(new(big.Int).Mod(base, p).FillBytes(make([]byte, size)), m)
	if err != nil {
		return nil, err
	}
	if len(exp) < size {
		exp = append(make([]byte, size-len(exp)), exp...)
	}
	return bigmod.NewNat().Exp(b, exp, m).Bytes(m), nil
}

// dhKeysFunc backs _dhKeys(primeB64, generatorB64, privateB64, privateBits, hasPrivate):
// {priv, pub} as base64, making a private key unless hasPrivate. A
// privateBits of 0 or less picks the size for the prime's strength.
func (c *Crypto) dhKeysFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("dh: missing arguments")
	}
	p := new(big.Int).SetBytes(b64Arg(args, 0))
	g := new(big.Int).SetBytes(b64Arg(args, 1))
	m, err := dhModulus(p, errModulusTooLarge)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	// A private key the script set is used as it is, even zero, as OpenSSL
	// does; only an object without one gets a new one.
	x := b64Arg(args, 2)
	if len(args) < 5 || !args[4].Boolean() {
		bits := int(args[3].Integer())
		if bits <= 0 || bits > p.BitLen() {
			bits = dhPrivateBits(p.BitLen())
		}
		// x in [1, min(2^bits, p-2)]; p has at least 512 bits here.
		limit := new(big.Int).Lsh(big.NewInt(1), uint(bits))
		if limit.Cmp(p) > 0 {
			limit.Sub(p, big.NewInt(2))
		}
		r, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return ctx.Throw(err.Error())
		}
		x = r.Add(r, big.NewInt(1)).Bytes()
	}
	y, err := dhExp(g, x, m, p)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	v, _ := ctx.NewString(jsonString(map[string]string{
		"priv": b64(new(big.Int).SetBytes(x).Bytes()),
		"pub":  b64(new(big.Int).SetBytes(y).Bytes()),
	}))
	return v
}

// dhSecretFunc backs _dhSecret(primeB64, privateB64, otherB64): the shared
// secret, padded to the prime's length as Node pads it.
func (c *Crypto) dhSecretFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	p := new(big.Int).SetBytes(b64Arg(args, 0))
	x := b64Arg(args, 1)
	y := new(big.Int).SetBytes(b64Arg(args, 2))
	m, err := dhModulus(p, errInvalidKeyType)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	pm1 := new(big.Int).Sub(p, big.NewInt(1))
	if y.Cmp(big.NewInt(1)) <= 0 {
		return ctx.Throw(errKeyTooSmall.Error())
	}
	if y.Cmp(pm1) >= 0 {
		return ctx.Throw(errKeyTooLarge.Error())
	}
	// A zero private key would make the secret 1 whatever the peer's key;
	// OpenSSL refuses it. The check reads every byte, whatever their values.
	var nonzero byte
	for _, b := range x {
		nonzero |= b
	}
	if nonzero == 0 {
		return ctx.Throw(errInvalidKeyType.Error())
	}
	s, err := dhExp(y, x, m, p)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	return throwOrString(ctx, b64(s), nil)
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
	// dh.js checks the kinds, but scripts can reach this native directly.
	if priv.kind != "private" {
		return ctx.Throw(dhErr("ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE", "Invalid key object type "+priv.kind+", expected private.").Error())
	}
	if pub.kind != "private" && pub.kind != "public" {
		return ctx.Throw(dhErr("ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE", "Invalid key object type "+pub.kind+", expected private or public.").Error())
	}
	if priv.asymType != pub.asymType || (priv.asymType == "ec" && priv.crv != pub.crv) {
		return ctx.Throw(errIncompatibleKeys(priv.asymType, pub.asymType).Error())
	}
	var secret []byte
	switch priv.asymType {
	case "x25519":
		p := pub.xPub
		if p == nil && pub.xPriv != nil {
			p = pub.xPriv.PublicKey()
		}
		if priv.xPriv == nil || p == nil {
			return ctx.Throw(dhErr("ERR_CRYPTO_OPERATION_FAILED", "Failed to compute shared secret").Error())
		}
		secret, err = priv.xPriv.ECDH(p)
	case "ec":
		var a *ecdh.PrivateKey
		var b *ecdh.PublicKey
		ecPub := pub.ecPub
		if ecPub == nil && pub.ecPriv != nil {
			ecPub = &pub.ecPriv.PublicKey
		}
		if priv.ecPriv == nil || ecPub == nil {
			return ctx.Throw(dhErr("ERR_CRYPTO_OPERATION_FAILED", "Failed to compute shared secret").Error())
		}
		if a, err = priv.ecPriv.ECDH(); err == nil {
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
