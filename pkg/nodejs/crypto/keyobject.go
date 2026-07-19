package crypto

// KeyObject API (Node's crypto.generateKeyPair / createSecretKey /
// createPrivateKey / createPublicKey and the KeyObject class).
//
// All key material lives here in Go (Go's stdlib has fast, well-audited RSA/EC/
// EdDSA implementations); JS KeyObject instances hold only an opaque integer
// handle into the per-runtime registry (see Crypto.keys). The low-level helpers
// exchange strings — base64 for raw bytes, JSON for JWK/info — so no key bytes
// are re-encoded through V8's UTF-8 boundary.

import (
	"crypto"
	"crypto/ecdh"
	_ "embed"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed keyobject.js
var keyObjectJS string

// keyEntry is one stored key. Exactly one of the typed fields is populated
// (plus secret for symmetric keys), selected by kind/asymType.
type keyEntry struct {
	kind     string // "secret" | "public" | "private"
	asymType string // "rsa" | "ec" | "ed25519" | "x25519" | "" (secret)
	crv      string // JWK curve name for ec/OKP ("P-256", "Ed25519", ...)

	secret  []byte
	rsaPub  *rsa.PublicKey
	rsaPriv *rsa.PrivateKey
	ecPub   *ecdsa.PublicKey
	ecPriv  *ecdsa.PrivateKey
	edPub   ed25519.PublicKey
	edPriv  ed25519.PrivateKey
	xPub    *ecdh.PublicKey
	xPriv   *ecdh.PrivateKey
}

// storeKey adds an entry to this runtime's registry and returns its handle.
func (c *Crypto) storeKey(e *keyEntry) int64 {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	c.keySeq++
	id := c.keySeq
	c.keys[id] = e
	return id
}

// lookupKey resolves a handle to its entry.
func (c *Crypto) lookupKey(id int64) (*keyEntry, error) {
	c.keyMu.Lock()
	defer c.keyMu.Unlock()
	e, ok := c.keys[id]
	if !ok {
		return nil, errors.New("invalid key handle")
	}
	return e, nil
}

func b64uEncode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// b64uDecode accepts both raw (unpadded) and standard base64url, tolerating any
// padding the caller left on, matching Node's lenient JWK parsing.
func b64uDecode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// padLeft left-pads b to size bytes (fixed-width JWK coordinate encoding).
func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

// curveByName maps WebCrypto ("P-256") and OpenSSL ("prime256v1") curve names to
// a Go curve plus its canonical JWK name.
func curveByName(name string) (elliptic.Curve, string, error) {
	switch name {
	case "P-256", "prime256v1", "secp256r1":
		return elliptic.P256(), "P-256", nil
	case "P-384", "secp384r1":
		return elliptic.P384(), "P-384", nil
	case "P-521", "secp521r1":
		return elliptic.P521(), "P-521", nil
	default:
		return nil, "", errors.New("unsupported namedCurve: " + name)
	}
}

// --- key generation -------------------------------------------------------

func (c *Crypto) generateKeyPairFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("generateKeyPair: type is required")
	}
	kind := args[0].String()
	optsJSON := ""
	if len(args) >= 2 && !args[1].IsUndefined() && !args[1].IsNull() {
		optsJSON = args[1].String()
	}
	pubID, privID, err := c.doGenerateKeyPair(kind, optsJSON)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	obj, _ := ctx.NewObject()
	obj.Set("pub", ctx.NewNumber(float64(pubID)))
	obj.Set("priv", ctx.NewNumber(float64(privID)))
	return obj
}

func (c *Crypto) doGenerateKeyPair(kind, optsJSON string) (pubID, privID int64, err error) {
	var opts struct {
		ModulusLength int    `json:"modulusLength"`
		NamedCurve    string `json:"namedCurve"`
	}
	if optsJSON != "" {
		_ = json.Unmarshal([]byte(optsJSON), &opts)
	}

	switch strings.ToLower(kind) {
	case "rsa", "rsa-pss":
		bits := opts.ModulusLength
		if bits == 0 {
			bits = 2048
		}
		key, e := rsa.GenerateKey(rand.Reader, bits)
		if e != nil {
			return 0, 0, e
		}
		pub := &keyEntry{kind: "public", asymType: "rsa", rsaPub: &key.PublicKey}
		priv := &keyEntry{kind: "private", asymType: "rsa", rsaPriv: key}
		return c.storeKey(pub), c.storeKey(priv), nil

	case "ec":
		curve, crv, e := curveByName(opts.NamedCurve)
		if e != nil {
			return 0, 0, e
		}
		key, e := ecdsa.GenerateKey(curve, rand.Reader)
		if e != nil {
			return 0, 0, e
		}
		pub := &keyEntry{kind: "public", asymType: "ec", crv: crv, ecPub: &key.PublicKey}
		priv := &keyEntry{kind: "private", asymType: "ec", crv: crv, ecPriv: key}
		return c.storeKey(pub), c.storeKey(priv), nil

	case "ed25519":
		edPub, edPriv, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return 0, 0, e
		}
		pub := &keyEntry{kind: "public", asymType: "ed25519", crv: "Ed25519", edPub: edPub}
		priv := &keyEntry{kind: "private", asymType: "ed25519", crv: "Ed25519", edPriv: edPriv}
		return c.storeKey(pub), c.storeKey(priv), nil

	case "x25519":
		xPriv, e := ecdh.X25519().GenerateKey(rand.Reader)
		if e != nil {
			return 0, 0, e
		}
		pub := &keyEntry{kind: "public", asymType: "x25519", crv: "X25519", xPub: xPriv.PublicKey()}
		priv := &keyEntry{kind: "private", asymType: "x25519", crv: "X25519", xPriv: xPriv}
		return c.storeKey(pub), c.storeKey(priv), nil

	default:
		return 0, 0, errors.New("unsupported key type: " + kind)
	}
}

// --- construction from raw / JWK / PEM ------------------------------------

func (c *Crypto) createSecretKeyFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("createSecretKey: key is required")
	}
	raw, err := base64.StdEncoding.DecodeString(args[0].String())
	if err != nil {
		return ctx.Throw("createSecretKey: " + err.Error())
	}
	id := c.storeKey(&keyEntry{kind: "secret", secret: raw})
	return ctx.NewNumber(float64(id))
}

// jwk mirrors the RFC 7517/7518 members Orbital reads/writes.
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	D   string `json:"d,omitempty"`
	P   string `json:"p,omitempty"`
	Q   string `json:"q,omitempty"`
	Dp  string `json:"dp,omitempty"`
	Dq  string `json:"dq,omitempty"`
	Qi  string `json:"qi,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
	K   string `json:"k,omitempty"`
}

func (c *Crypto) createPrivateKeyJWKFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return c.createFromJWK(info, true)
}

func (c *Crypto) createPublicKeyJWKFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return c.createFromJWK(info, false)
}

func (c *Crypto) createFromJWK(info *v8.FunctionCallbackInfo, wantPrivate bool) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("createKey: jwk is required")
	}
	var j jwk
	if err := json.Unmarshal([]byte(args[0].String()), &j); err != nil {
		return ctx.Throw("createKey: invalid JWK: " + err.Error())
	}
	e, err := jwkToEntry(&j, wantPrivate)
	if err != nil {
		return ctx.Throw("createKey: " + err.Error())
	}
	return ctx.NewNumber(float64(c.storeKey(e)))
}

func jwkToEntry(j *jwk, wantPrivate bool) (*keyEntry, error) {
	switch j.Kty {
	case "oct":
		raw, err := b64uDecode(j.K)
		if err != nil {
			return nil, err
		}
		return &keyEntry{kind: "secret", secret: raw}, nil

	case "RSA":
		n, err := b64uDecode(j.N)
		if err != nil {
			return nil, err
		}
		eb, err := b64uDecode(j.E)
		if err != nil {
			return nil, err
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(eb).Int64())}
		if !wantPrivate || j.D == "" {
			return &keyEntry{kind: "public", asymType: "rsa", rsaPub: pub}, nil
		}
		d, err := b64uDecode(j.D)
		if err != nil {
			return nil, err
		}
		p, err := b64uDecode(j.P)
		if err != nil {
			return nil, err
		}
		q, err := b64uDecode(j.Q)
		if err != nil {
			return nil, err
		}
		priv := &rsa.PrivateKey{
			PublicKey: *pub,
			D:         new(big.Int).SetBytes(d),
			Primes:    []*big.Int{new(big.Int).SetBytes(p), new(big.Int).SetBytes(q)},
		}
		priv.Precompute()
		if err := priv.Validate(); err != nil {
			return nil, err
		}
		return &keyEntry{kind: "private", asymType: "rsa", rsaPriv: priv}, nil

	case "EC":
		curve, crv, err := curveByName(j.Crv)
		if err != nil {
			return nil, err
		}
		x, err := b64uDecode(j.X)
		if err != nil {
			return nil, err
		}
		y, err := b64uDecode(j.Y)
		if err != nil {
			return nil, err
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !wantPrivate || j.D == "" {
			return &keyEntry{kind: "public", asymType: "ec", crv: crv, ecPub: pub}, nil
		}
		d, err := b64uDecode(j.D)
		if err != nil {
			return nil, err
		}
		priv := &ecdsa.PrivateKey{PublicKey: *pub, D: new(big.Int).SetBytes(d)}
		return &keyEntry{kind: "private", asymType: "ec", crv: crv, ecPriv: priv}, nil

	case "OKP":
		x, err := b64uDecode(j.X)
		if err != nil {
			return nil, err
		}
		switch j.Crv {
		case "Ed25519":
			if !wantPrivate || j.D == "" {
				return &keyEntry{kind: "public", asymType: "ed25519", crv: "Ed25519", edPub: ed25519.PublicKey(x)}, nil
			}
			d, err := b64uDecode(j.D)
			if err != nil {
				return nil, err
			}
			return &keyEntry{kind: "private", asymType: "ed25519", crv: "Ed25519", edPriv: ed25519.NewKeyFromSeed(d)}, nil
		case "X25519":
			if !wantPrivate || j.D == "" {
				pk, err := ecdh.X25519().NewPublicKey(x)
				if err != nil {
					return nil, err
				}
				return &keyEntry{kind: "public", asymType: "x25519", crv: "X25519", xPub: pk}, nil
			}
			d, err := b64uDecode(j.D)
			if err != nil {
				return nil, err
			}
			sk, err := ecdh.X25519().NewPrivateKey(d)
			if err != nil {
				return nil, err
			}
			return &keyEntry{kind: "private", asymType: "x25519", crv: "X25519", xPriv: sk}, nil
		default:
			return nil, errors.New("unsupported OKP curve: " + j.Crv)
		}

	default:
		return nil, errors.New("unsupported JWK kty: " + j.Kty)
	}
}

// createKeyFromDERFunc backs WebCrypto's spki/pkcs8 importKey: it parses a DER
// buffer (args: base64(DER), wantPrivate bool) into a KeyObject handle whose JWK
// the caller then reads via _keyExportJWK.
func (c *Crypto) createKeyFromDERFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("createKey: der and mode are required")
	}
	der, err := base64.StdEncoding.DecodeString(args[0].String())
	if err != nil {
		return ctx.Throw("createKey: " + err.Error())
	}
	e, err := derToEntry(der, args[1].Boolean())
	if err != nil {
		return ctx.Throw("createKey: " + err.Error())
	}
	return ctx.NewNumber(float64(c.storeKey(e)))
}

func derToEntry(der []byte, wantPrivate bool) (*keyEntry, error) {
	if wantPrivate {
		if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
			return privKeyToEntry(k)
		}
		if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
			return privKeyToEntry(k)
		}
		if k, err := x509.ParseECPrivateKey(der); err == nil {
			return privKeyToEntry(k)
		}
		return nil, errors.New("unsupported or invalid PKCS#8 private key")
	}
	if k, err := x509.ParsePKIXPublicKey(der); err == nil {
		return pubKeyToEntry(k)
	}
	if k, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return pubKeyToEntry(k)
	}
	return nil, errors.New("unsupported or invalid SPKI public key")
}

// createKeyFromPEMFunc backs createPrivateKey/createPublicKey when given a PEM
// string (args: pem, wantPrivate bool).
func (c *Crypto) createKeyFromPEMFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.Throw("createKey: pem and mode are required")
	}
	block, _ := pem.Decode([]byte(args[0].String()))
	if block == nil {
		return ctx.Throw("createKey: no PEM data found")
	}
	wantPrivate := args[1].Boolean()
	e, err := pemBlockToEntry(block, wantPrivate)
	if err != nil {
		return ctx.Throw("createKey: " + err.Error())
	}
	return ctx.NewNumber(float64(c.storeKey(e)))
}

func pemBlockToEntry(block *pem.Block, wantPrivate bool) (*keyEntry, error) {
	if wantPrivate {
		var key any
		var err error
		switch block.Type {
		case "RSA PRIVATE KEY":
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			key, err = x509.ParseECPrivateKey(block.Bytes)
		default:
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		}
		if err != nil {
			return nil, err
		}
		return privKeyToEntry(key)
	}
	var pub any
	var err error
	switch block.Type {
	case "RSA PUBLIC KEY":
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	}
	if err != nil {
		return nil, err
	}
	return pubKeyToEntry(pub)
}

func privKeyToEntry(key any) (*keyEntry, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &keyEntry{kind: "private", asymType: "rsa", rsaPriv: k}, nil
	case *ecdsa.PrivateKey:
		_, crv, err := curveByName(k.Curve.Params().Name)
		if err != nil {
			crv = k.Curve.Params().Name
		}
		return &keyEntry{kind: "private", asymType: "ec", crv: crv, ecPriv: k}, nil
	case ed25519.PrivateKey:
		return &keyEntry{kind: "private", asymType: "ed25519", crv: "Ed25519", edPriv: k}, nil
	case *ecdh.PrivateKey:
		return &keyEntry{kind: "private", asymType: "x25519", crv: "X25519", xPriv: k}, nil
	default:
		return nil, errors.New("unsupported private key type")
	}
}

func pubKeyToEntry(key any) (*keyEntry, error) {
	switch k := key.(type) {
	case *rsa.PublicKey:
		return &keyEntry{kind: "public", asymType: "rsa", rsaPub: k}, nil
	case *ecdsa.PublicKey:
		_, crv, err := curveByName(k.Curve.Params().Name)
		if err != nil {
			crv = k.Curve.Params().Name
		}
		return &keyEntry{kind: "public", asymType: "ec", crv: crv, ecPub: k}, nil
	case ed25519.PublicKey:
		return &keyEntry{kind: "public", asymType: "ed25519", crv: "Ed25519", edPub: k}, nil
	case *ecdh.PublicKey:
		return &keyEntry{kind: "public", asymType: "x25519", crv: "X25519", xPub: k}, nil
	default:
		return nil, errors.New("unsupported public key type")
	}
}

// --- introspection --------------------------------------------------------

func (c *Crypto) argEntry(info *v8.FunctionCallbackInfo) (*keyEntry, *v8.Value) {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil, ctx.Throw("key handle is required")
	}
	e, err := c.lookupKey(int64(args[0].Integer()))
	if err != nil {
		return nil, ctx.Throw(err.Error())
	}
	return e, nil
}

func (c *Crypto) keyInfoFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	e, throw := c.argEntry(info)
	if throw != nil {
		return throw
	}
	out := map[string]any{"type": e.kind}
	if e.kind == "secret" {
		out["symmetricKeySize"] = len(e.secret)
	} else {
		out["asymmetricKeyType"] = e.asymType
	}
	b, _ := json.Marshal(out)
	v, _ := ctx.NewString(string(b))
	return v
}

func (c *Crypto) keyExportSecretFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	e, throw := c.argEntry(info)
	if throw != nil {
		return throw
	}
	if e.kind != "secret" {
		return ctx.Throw("export: not a secret key")
	}
	v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(e.secret))
	return v
}

func (c *Crypto) keyExportJWKFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	e, throw := c.argEntry(info)
	if throw != nil {
		return throw
	}
	j, err := entryToJWK(e)
	if err != nil {
		return ctx.Throw("export: " + err.Error())
	}
	b, _ := json.Marshal(j)
	v, _ := ctx.NewString(string(b))
	return v
}

func entryToJWK(e *keyEntry) (*jwk, error) {
	switch e.asymType {
	case "":
		if e.kind == "secret" {
			return &jwk{Kty: "oct", K: b64uEncode(e.secret)}, nil
		}
		return nil, errors.New("cannot export JWK for this key")

	case "rsa":
		pub := e.rsaPub
		if pub == nil && e.rsaPriv != nil {
			pub = &e.rsaPriv.PublicKey
		}
		j := &jwk{Kty: "RSA", N: b64uEncode(pub.N.Bytes()), E: b64uEncode(big.NewInt(int64(pub.E)).Bytes())}
		if e.kind == "private" && e.rsaPriv != nil {
			p := e.rsaPriv
			j.D = b64uEncode(p.D.Bytes())
			if len(p.Primes) >= 2 {
				j.P = b64uEncode(p.Primes[0].Bytes())
				j.Q = b64uEncode(p.Primes[1].Bytes())
			}
			if p.Precomputed.Dp != nil {
				j.Dp = b64uEncode(p.Precomputed.Dp.Bytes())
				j.Dq = b64uEncode(p.Precomputed.Dq.Bytes())
				j.Qi = b64uEncode(p.Precomputed.Qinv.Bytes())
			}
		}
		return j, nil

	case "ec":
		pub := e.ecPub
		if pub == nil && e.ecPriv != nil {
			pub = &e.ecPriv.PublicKey
		}
		size := (pub.Curve.Params().BitSize + 7) / 8
		j := &jwk{Kty: "EC", Crv: e.crv,
			X: b64uEncode(padLeft(pub.X.Bytes(), size)),
			Y: b64uEncode(padLeft(pub.Y.Bytes(), size))}
		if e.kind == "private" && e.ecPriv != nil {
			j.D = b64uEncode(padLeft(e.ecPriv.D.Bytes(), size))
		}
		return j, nil

	case "ed25519":
		pub := e.edPub
		if pub == nil && e.edPriv != nil {
			pub = e.edPriv.Public().(ed25519.PublicKey)
		}
		j := &jwk{Kty: "OKP", Crv: "Ed25519", X: b64uEncode(pub)}
		if e.kind == "private" && e.edPriv != nil {
			j.D = b64uEncode(e.edPriv.Seed())
		}
		return j, nil

	case "x25519":
		pub := e.xPub
		if pub == nil && e.xPriv != nil {
			pub = e.xPriv.PublicKey()
		}
		j := &jwk{Kty: "OKP", Crv: "X25519", X: b64uEncode(pub.Bytes())}
		if e.kind == "private" && e.xPriv != nil {
			j.D = b64uEncode(e.xPriv.Bytes())
		}
		return j, nil

	default:
		return nil, errors.New("unsupported key type for JWK export")
	}
}

// cryptoPriv / cryptoPub return the stdlib key interfaces for PEM/DER marshaling.
func (e *keyEntry) cryptoPriv() (crypto.PrivateKey, error) {
	switch e.asymType {
	case "rsa":
		return e.rsaPriv, nil
	case "ec":
		return e.ecPriv, nil
	case "ed25519":
		return e.edPriv, nil
	case "x25519":
		return e.xPriv, nil
	}
	return nil, errors.New("not an exportable private key")
}

func (e *keyEntry) cryptoPub() (crypto.PublicKey, error) {
	switch e.asymType {
	case "rsa":
		if e.rsaPub != nil {
			return e.rsaPub, nil
		}
		return &e.rsaPriv.PublicKey, nil
	case "ec":
		if e.ecPub != nil {
			return e.ecPub, nil
		}
		return &e.ecPriv.PublicKey, nil
	case "ed25519":
		if e.edPub != nil {
			return e.edPub, nil
		}
		return e.edPriv.Public(), nil
	case "x25519":
		if e.xPub != nil {
			return e.xPub, nil
		}
		return e.xPriv.PublicKey(), nil
	}
	return nil, errors.New("not an exportable public key")
}

// keyExportDERFunc returns base64(DER) for the requested encoding type.
func (c *Crypto) keyExportDERFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	e, throw := c.argEntry(info)
	if throw != nil {
		return throw
	}
	typ := ""
	if a := info.Args(); len(a) >= 2 {
		typ = a[1].String()
	}
	der, _, err := e.marshalDER(typ)
	if err != nil {
		return ctx.Throw("export: " + err.Error())
	}
	v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(der))
	return v
}

// keyExportPEMFunc returns a PEM string for the requested encoding type.
func (c *Crypto) keyExportPEMFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	e, throw := c.argEntry(info)
	if throw != nil {
		return throw
	}
	typ := ""
	if a := info.Args(); len(a) >= 2 {
		typ = a[1].String()
	}
	der, blockType, err := e.marshalDER(typ)
	if err != nil {
		return ctx.Throw("export: " + err.Error())
	}
	out := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	v, _ := ctx.NewString(string(out))
	return v
}

// marshalDER encodes the key to DER and reports the matching PEM block type.
// Empty typ selects the default (pkcs8/spki).
func (e *keyEntry) marshalDER(typ string) (der []byte, blockType string, err error) {
	if e.kind == "private" {
		priv, perr := e.cryptoPriv()
		if perr != nil {
			return nil, "", perr
		}
		switch typ {
		case "pkcs1":
			rk, ok := priv.(*rsa.PrivateKey)
			if !ok {
				return nil, "", errors.New("pkcs1 is only valid for RSA keys")
			}
			return x509.MarshalPKCS1PrivateKey(rk), "RSA PRIVATE KEY", nil
		case "sec1":
			ek, ok := priv.(*ecdsa.PrivateKey)
			if !ok {
				return nil, "", errors.New("sec1 is only valid for EC keys")
			}
			b, e2 := x509.MarshalECPrivateKey(ek)
			return b, "EC PRIVATE KEY", e2
		default: // pkcs8
			b, e2 := x509.MarshalPKCS8PrivateKey(priv)
			return b, "PRIVATE KEY", e2
		}
	}

	pub, perr := e.cryptoPub()
	if perr != nil {
		return nil, "", perr
	}
	switch typ {
	case "pkcs1":
		rk, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, "", errors.New("pkcs1 is only valid for RSA keys")
		}
		return x509.MarshalPKCS1PublicKey(rk), "RSA PUBLIC KEY", nil
	default: // spki
		b, e2 := x509.MarshalPKIXPublicKey(pub)
		return b, "PUBLIC KEY", e2
	}
}

// keyEqualsFunc backs KeyObject.equals for symmetric keys (constant-time) and
// asymmetric keys (JWK comparison).
func (c *Crypto) keyEqualsFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 2 {
		return ctx.False()
	}
	a, err := c.lookupKey(int64(args[0].Integer()))
	if err != nil {
		return ctx.False()
	}
	b, err := c.lookupKey(int64(args[1].Integer()))
	if err != nil {
		return ctx.False()
	}
	if a.kind != b.kind {
		return ctx.False()
	}
	if a.kind == "secret" {
		if subtleConstEq(a.secret, b.secret) {
			return ctx.True()
		}
		return ctx.False()
	}
	ja, e1 := entryToJWK(a)
	jb, e2 := entryToJWK(b)
	if e1 != nil || e2 != nil {
		return ctx.False()
	}
	ba, _ := json.Marshal(ja)
	bb, _ := json.Marshal(jb)
	if string(ba) == string(bb) {
		return ctx.True()
	}
	return ctx.False()
}

func subtleConstEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
