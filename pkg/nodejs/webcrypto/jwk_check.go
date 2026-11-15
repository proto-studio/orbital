package webcrypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/json"

	"proto.zip/studio/orbital/pkg/v8"
)

// Key material checks for importKey('jwk', …). webcrypto.js checks the JWK's
// other members; this decodes the base64url key members the way Node does and
// checks they form a key, so a malformed key fails at import with Node's
// DataError instead of later.

// decodeBase64Lenient decodes base64 or base64url as Node's decoder does:
// either alphabet is accepted, decoding stops at the first padding character,
// and other characters are skipped.
func decodeBase64Lenient(s string) []byte {
	out := make([]byte, 0, len(s)*3/4)
	var acc uint32
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			i = len(s)
			continue
		default:
			continue
		}
		acc = acc<<6 | uint32(v)
		n++
		if n == 4 {
			out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return out
}

// jwkMember decodes a JWK member: absent members are nil, and members that
// aren't strings decode as empty, as in Node.
func jwkMember(raw json.RawMessage) []byte {
	if raw == nil {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return []byte{}
	}
	return decodeBase64Lenient(s)
}

type jwkCheckResult struct {
	// Error is "" when the key is usable, else the DataError message.
	Error string `json:"error,omitempty"`
	// Canonical base64url of the decoded members, to store in place of the
	// caller's spelling.
	K *string `json:"k,omitempty"`
	X *string `json:"x,omitempty"`
	Y *string `json:"y,omitempty"`
	D *string `json:"d,omitempty"`
	// KeyBytes is the length of an oct key.
	KeyBytes int `json:"keyBytes"`
}

const invalidKeyData = "Invalid keyData"

func b64uPtr(b []byte) *string {
	s := b64uEnc(b)
	return &s
}

// fixedWidth writes the big-endian integer b in exactly size bytes, or
// reports that it doesn't fit.
func fixedWidth(b []byte, size int) ([]byte, bool) {
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	if len(b) > size {
		return nil, false
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out, true
}

// checkJWKMaterial checks the key members of a JWK whose kty and crv
// webcrypto.js has already matched to the algorithm.
func checkJWKMaterial(members map[string]json.RawMessage) jwkCheckResult {
	var kty, crv string
	_ = json.Unmarshal(members["kty"], &kty)
	_ = json.Unmarshal(members["crv"], &crv)
	x, y, d := jwkMember(members["x"]), jwkMember(members["y"]), jwkMember(members["d"])

	switch kty {
	case "oct":
		k := jwkMember(members["k"])
		if k == nil {
			return jwkCheckResult{Error: invalidKeyData}
		}
		return jwkCheckResult{K: b64uPtr(k), KeyBytes: len(k)}

	case "EC":
		curve, err := ecdhCurveByName(crv)
		if err != nil || x == nil || y == nil {
			return jwkCheckResult{Error: invalidKeyData}
		}
		// Node reads EC members as big-endian integers, so leading zero
		// bytes may be missing or extra.
		size := map[string]int{"P-256": 32, "P-384": 48, "P-521": 66}[crv]
		var okX, okY bool
		x, okX = fixedWidth(x, size)
		y, okY = fixedWidth(y, size)
		if !okX || !okY {
			return jwkCheckResult{Error: invalidKeyData}
		}
		point := append(append([]byte{0x04}, x...), y...)
		pub, err := curve.NewPublicKey(point)
		if err != nil {
			return jwkCheckResult{Error: invalidKeyData}
		}
		res := jwkCheckResult{X: b64uPtr(x), Y: b64uPtr(y)}
		if d != nil {
			var okD bool
			if d, okD = fixedWidth(d, size); !okD {
				return jwkCheckResult{Error: invalidKeyData}
			}
			priv, err := curve.NewPrivateKey(d)
			if err != nil || !priv.PublicKey().Equal(pub) {
				return jwkCheckResult{Error: invalidKeyData}
			}
			res.D = b64uPtr(d)
		}
		return res

	case "OKP":
		if x == nil || len(x) != 32 {
			return jwkCheckResult{Error: invalidKeyData}
		}
		res := jwkCheckResult{X: b64uPtr(x)}
		if d != nil {
			if len(d) != 32 {
				return jwkCheckResult{Error: invalidKeyData}
			}
			var derived []byte
			switch crv {
			case "Ed25519":
				derived = ed25519.NewKeyFromSeed(d).Public().(ed25519.PublicKey)
			case "X25519":
				priv, err := ecdh.X25519().NewPrivateKey(d)
				if err != nil {
					return jwkCheckResult{Error: invalidKeyData}
				}
				derived = priv.PublicKey().Bytes()
			}
			if !bytes.Equal(derived, x) {
				return jwkCheckResult{Error: invalidKeyData}
			}
			res.D = b64uPtr(d)
		}
		return res
	}
	return jwkCheckResult{Error: invalidKeyData}
}

// nativeCheckJWK backs the key-material check: it takes the JWK as JSON and
// returns a jwkCheckResult as JSON.
func nativeCheckJWK(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argStr(info, 0)), &members); err != nil {
		return throwf(ctx, "checkJWK: invalid JWK: %v", err)
	}
	return jsonResult(ctx, checkJWKMaterial(members))
}
