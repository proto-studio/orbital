package crypto

import (
	gocrypto "crypto"
	"crypto/dsa" //nolint:staticcheck // Node verifies DSA SPKACs.
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math"
	"math/big"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

// Natives behind extras.js: random bytes for randomFill, primes, and the
// legacy SPKAC Certificate helpers.

//go:embed extras.js
var extrasJS string

// offThread runs work now and returns its result, or, when callback is a
// function, runs it on the runtime's thread pool and calls callback(undefined, result) or
// callback(message) on the JS thread, as Node runs these on its threadpool.
// result builds the JS value on the JS thread.
func (c *Crypto) offThread(ctx *v8.Context, callback *v8.Value, work func() (string, error)) *v8.Value {
	if callback == nil || !callback.IsFunction() {
		out, err := work()
		if err != nil {
			return ctx.Throw(err.Error())
		}
		v, _ := ctx.NewString(out)
		return v
	}
	loop := c.rt.EventLoop()
	jsCtx := c.rt.Context()
	loop.AddPendingWork()
	c.rt.RunInThreadPool(func() {
		defer loop.DonePendingWork()
		out, err := work()
		loop.EnqueueMicrotask(func() {
			if err != nil {
				msg, _ := jsCtx.NewString(err.Error())
				c.rt.MakeCallback(callback, nil, msg)
				return
			}
			v, _ := jsCtx.NewString(out)
			c.rt.MakeCallback(callback, nil, jsCtx.Undefined(), v)
		})
	})
	return ctx.Undefined()
}

func optionalCallback(args []*v8.Value, i int) *v8.Value {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// randomBase64Func backs _randomBase64(size[, callback]): size random bytes as
// base64.
func (c *Crypto) randomBase64Func(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("randomBase64: size is required")
	}
	size := args[0].Integer()
	if size < 0 || size > maxNativeBytes {
		return ctx.Throw(outOfRange("size", 0, maxNativeBytes, size))
	}
	return c.offThread(ctx, optionalCallback(args, 1), func() (string, error) {
		buf := make([]byte, size)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(buf), nil
	})
}

func hexInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return new(big.Int)
	}
	return n
}

// checkPrimeFunc backs _checkPrime(hex, checks[, callback]): "true" or
// "false". checks 0 means Go's default, the Baillie-PSW test alone.
func (c *Crypto) checkPrimeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("checkPrime: candidate and checks are required")
	}
	n := hexInt(args[0].String())
	checks := args[1].Integer()
	// extras.js checks these, but scripts can reach the native directly.
	if checks < 0 || checks > math.MaxInt32 {
		return ctx.Throw("checkPrime: checks out of range")
	}
	return c.offThread(ctx, optionalCallback(args, 2), func() (string, error) {
		if n.ProbablyPrime(int(checks)) {
			return "true", nil
		}
		return "false", nil
	})
}

const errBitsTooSmall = "error:01800076:bignum routines::bits too small"

// generatePrime returns a prime of exactly bits bits, as OpenSSL's
// BN_generate_prime_ex does: with safe, (p-1)/2 is prime too, and with add,
// p % add == rem (rem defaults to 1, or 3 for a safe prime).
func generatePrime(bits int, safe bool, add, rem *big.Int) (*big.Int, error) {
	if bits < 2 || (safe && add == nil && bits < 6 && bits != 3) {
		return nil, errors.New(errBitsTooSmall)
	}
	if add != nil && add.Sign() > 0 && rem == nil {
		if safe {
			rem = big.NewInt(3)
		} else {
			rem = big.NewInt(1)
		}
	}
	one := big.NewInt(1)
	lo := new(big.Int).Lsh(one, uint(bits-1))
	span := new(big.Int).Set(lo)
	half := new(big.Int)
	m := new(big.Int)
	minimum := big.NewInt(3)
	if safe {
		minimum.SetInt64(5)
	}
	isWanted := func(p *big.Int) bool {
		if !p.ProbablyPrime(20) {
			return false
		}
		return !safe || half.Rsh(p, 1).ProbablyPrime(20)
	}
	if add != nil && add.Sign() == 0 {
		return nil, errors.New("error:01800067:bignum routines::div by zero")
	}
	if add == nil {
		// Unconstrained: always satisfiable, so search until found.
		// rand.Prime sets the top two bits, so 2q+1 has exactly bits bits.
		for {
			if !safe {
				return rand.Prime(rand.Reader, bits)
			}
			q, err := rand.Prime(rand.Reader, bits-1)
			if err != nil {
				return nil, err
			}
			p := new(big.Int).Lsh(q, 1)
			p.Add(p, one)
			if p.BitLen() == bits && p.ProbablyPrime(20) {
				return p, nil
			}
		}
	}
	// Give up rather than loop for ever on a combination with no prime.
	for attempt := 0; attempt < 1<<16; attempt++ {
		r, err := rand.Int(rand.Reader, span)
		if err != nil {
			return nil, err
		}
		p := r.Add(r, lo)
		p.SetBit(p, 0, 1)
		// As OpenSSL's probable_prime_dh: round down to rem mod add, step up
		// once if that lost the top bit, then try successive values of the
		// progression. The result can be longer than bits.
		p.Sub(p, m.Mod(p, add))
		p.Add(p, rem)
		if p.BitLen() < bits || p.Cmp(minimum) < 0 {
			p.Add(p, add)
		}
		for step := 0; step < 1<<12; step++ {
			if isWanted(p) {
				return p, nil
			}
			p.Add(p, add)
		}
	}
	return nil, errors.New("error:1C800066:Provider routines::too many attempts")
}

// generatePrimeFunc backs _generatePrime(bits, safe, addHex, remHex[,
// callback]): the prime as hex. addHex and remHex are "" when not given;
// extras.js has checked them against bits.
func (c *Crypto) generatePrimeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("generatePrime: missing arguments")
	}
	size := args[0].Integer()
	if size < 1 || size > maxPrimeBits {
		return ctx.Throw(outOfRange("size", 1, maxPrimeBits, size))
	}
	bits := int(size)
	safe := args[1].Boolean()
	var add, rem *big.Int
	if s := args[2].String(); s != "" {
		add = hexInt(s)
	}
	if s := args[3].String(); s != "" {
		rem = hexInt(s)
	}
	// extras.js passes unsigned values; check again for direct calls.
	if (add != nil && add.Sign() < 0) || (rem != nil && rem.Sign() < 0) {
		return ctx.Throw("generatePrime: add and rem must not be negative")
	}
	return c.offThread(ctx, optionalCallback(args, 4), func() (string, error) {
		p, err := generatePrime(bits, safe, add, rem)
		if err != nil {
			return "", err
		}
		return p.Text(16), nil
	})
}

// --- SPKAC (Netscape signed public key and challenge) ---------------------

type spkacPKAC struct {
	SPKI      asn1.RawValue
	Challenge string `asn1:"ia5"`
}

type spkacSigned struct {
	PKAC      asn1.RawValue
	Algorithm struct {
		Algorithm  asn1.ObjectIdentifier
		Parameters asn1.RawValue `asn1:"optional"`
	}
	Signature asn1.BitString
}

// parseSPKAC decodes base64 SPKAC text into its parts.
func parseSPKAC(text []byte) (*spkacSigned, *spkacPKAC, error) {
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, string(text))
	der, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, nil, err
	}
	var signed spkacSigned
	if _, err := asn1.Unmarshal(der, &signed); err != nil {
		return nil, nil, err
	}
	var pkac spkacPKAC
	if _, err := asn1.Unmarshal(signed.PKAC.FullBytes, &pkac); err != nil {
		return nil, nil, err
	}
	return &signed, &pkac, nil
}

var spkacSigHashes = map[string]gocrypto.Hash{
	"1.2.840.113549.1.1.4":   gocrypto.MD5,
	"1.2.840.113549.1.1.5":   gocrypto.SHA1,
	"1.2.840.113549.1.1.14":  gocrypto.SHA224,
	"1.2.840.113549.1.1.11":  gocrypto.SHA256,
	"1.2.840.113549.1.1.12":  gocrypto.SHA384,
	"1.2.840.113549.1.1.13":  gocrypto.SHA512,
	"1.2.840.10045.4.1":      gocrypto.SHA1,
	"1.2.840.10045.4.3.1":    gocrypto.SHA224,
	"1.2.840.10045.4.3.2":    gocrypto.SHA256,
	"1.2.840.10045.4.3.3":    gocrypto.SHA384,
	"1.2.840.10045.4.3.4":    gocrypto.SHA512,
	"1.2.840.10040.4.3":      gocrypto.SHA1,
	"2.16.840.1.101.3.4.3.1": gocrypto.SHA224,
	"2.16.840.1.101.3.4.3.2": gocrypto.SHA256,
	"1.3.101.112":            0, // Ed25519 signs the message itself
}

// isDSASigAlg reports whether an OID is a DSA signature algorithm.
func isDSASigAlg(oid string) bool {
	return oid == "1.2.840.10040.4.3" || oid == "2.16.840.1.101.3.4.3.1" || oid == "2.16.840.1.101.3.4.3.2"
}

func verifySPKAC(signed *spkacSigned, pub any) bool {
	h, ok := spkacSigHashes[signed.Algorithm.Algorithm.String()]
	if !ok {
		return false
	}
	msg := signed.PKAC.FullBytes
	sig := signed.Signature.RightAlign()
	oid := signed.Algorithm.Algorithm.String()
	if k, isEd := pub.(ed25519.PublicKey); isEd {
		return oid == "1.3.101.112" && ed25519.Verify(k, msg, sig)
	}
	if h == 0 || !h.Available() {
		return false
	}
	d := h.New()
	d.Write(msg)
	sum := d.Sum(nil)
	// The signature algorithm must be one for the key's type.
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return strings.HasPrefix(oid, "1.2.840.113549.1.1.") && rsa.VerifyPKCS1v15(k, h, sum, sig) == nil
	case *ecdsa.PublicKey:
		return strings.HasPrefix(oid, "1.2.840.10045.4.") && ecdsa.VerifyASN1(k, sum, sig)
	case *dsa.PublicKey:
		// Go's standard library still verifies DSA, which OpenSSL accepts here.
		var rs struct{ R, S *big.Int }
		if !isDSASigAlg(oid) {
			return false
		}
		if rest, err := asn1.Unmarshal(sig, &rs); err != nil || len(rest) != 0 || rs.R == nil || rs.S == nil {
			return false
		}
		// dsa.Verify takes at most the subgroup's size of the digest.
		if n := (k.Q.BitLen() + 7) / 8; len(sum) > n {
			sum = sum[:n]
		}
		return dsa.Verify(k, sum, rs.R, rs.S)
	}
	return false
}

// spkacFunc backs _spkac(op, base64Input): op is "challenge", "publicKey" or
// "verify". The challenge and public key come back as base64, with "!"
// for a challenge and "" for a key that couldn't be read; verify gives
// "true" or "false".
func (c *Crypto) spkacFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("spkac: missing arguments")
	}
	op := args[0].String()
	text, _ := base64.StdEncoding.DecodeString(args[1].String())
	out := ""
	signed, pkac, err := parseSPKAC(text)
	switch op {
	case "challenge":
		// "!" marks input that couldn't be read; an empty challenge is "".
		out = "!"
		if err == nil {
			out = base64.StdEncoding.EncodeToString([]byte(pkac.Challenge))
		}
	case "publicKey":
		if err == nil {
			// The key as the SPKAC carries it: re-encoding it fails for key
			// types crypto/x509 reads but won't write, such as DSA.
			if _, perr := x509.ParsePKIXPublicKey(pkac.SPKI.FullBytes); perr == nil {
				out = base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkac.SPKI.FullBytes}))
			}
		}
	case "verify":
		out = "false"
		if err == nil {
			if pub, perr := x509.ParsePKIXPublicKey(pkac.SPKI.FullBytes); perr == nil && verifySPKAC(signed, pub) {
				out = "true"
			}
		}
	}
	v, _ := ctx.NewString(out)
	return v
}
