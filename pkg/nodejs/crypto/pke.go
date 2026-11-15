package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	_ "embed"
	"encoding/base64"
	"math/big"

	"proto.zip/studio/orbital/pkg/v8"
)

// publicEncrypt, privateDecrypt, privateEncrypt and publicDecrypt with RSA
// keys, over Go's crypto/rsa and, for the unpadded public-key forms and
// publicDecrypt that Go doesn't offer, the public RSA operation itself.
// Private-key operations always go through crypto/rsa. Errors cross
// as "CODE|message" with OpenSSL's reasons.

//go:embed pke.js
var pkeJS string

const rsaNoPadding, rsaOAEPPadding = 3, 4

func rsaErr(code, message string) error { return &signError{code, message} }

var (
	errTooLargeForKey     = rsaErr("ERR_OSSL_RSA_DATA_TOO_LARGE_FOR_KEY_SIZE", "error:0200006E:rsa routines::data too large for key size")
	errTooSmallForKey     = rsaErr("ERR_OSSL_RSA_DATA_TOO_SMALL_FOR_KEY_SIZE", "error:0200007A:rsa routines::data too small for key size")
	errTooLargeForModulus = rsaErr("ERR_OSSL_RSA_DATA_TOO_LARGE_FOR_MODULUS", "error:02000084:rsa routines::data too large for modulus")
	errGreaterThanModLen  = rsaErr("ERR_OSSL_RSA_DATA_GREATER_THAN_MOD_LEN", "error:0200006C:rsa routines::data greater than mod len")
	errOAEPDecoding       = rsaErr("ERR_OSSL_RSA_OAEP_DECODING_ERROR", "error:02000079:rsa routines::oaep decoding error")
	errInvalidPadding     = rsaErr("ERR_OSSL_RSA_INVALID_PADDING", "error:0200008A:rsa routines::invalid padding")
	errBlockTypeNot01     = rsaErr("ERR_OSSL_RSA_BLOCK_TYPE_IS_NOT_01", "error:0200006A:rsa routines::block type is not 01")
	errBadFixedHeader     = rsaErr("ERR_OSSL_RSA_BAD_FIXED_HEADER_DECRYPT", "error:02000066:rsa routines::bad fixed header decrypt")
	errNullBeforeBlock    = rsaErr("ERR_OSSL_RSA_NULL_BEFORE_BLOCK_MISSING", "error:02000071:rsa routines::null before block missing")
	errBadPadByteCount    = rsaErr("ERR_OSSL_RSA_BAD_PAD_BYTE_COUNT", "error:02000067:rsa routines::bad pad byte count")
	errInvalidOAEPDigest  = rsaErr("ERR_OSSL_EVP_INVALID_DIGEST", "Invalid digest used")
	errNotForKeyType      = rsaErr("ERR_OSSL_EVP_OPERATION_NOT_SUPPORTED_FOR_THIS_KEYTYPE", "error:03000096:digital envelope routines::operation not supported for this keytype")
	errUnknownPadding     = rsaErr("", "error:00000000:lib(0)::reason(0)")
	// Unpadded private-key operations would need the RSA step done with
	// math/big, which isn't constant time, so they aren't offered.
	errNoPaddingPrivate = rsaErr("ERR_CRYPTO_UNSUPPORTED_OPERATION", "Unsupported crypto operation")
)

// rawRSA computes in^e mod n for an input of at most k bytes, as OpenSSL's
// unpadded operations do, returning exactly k bytes. Only for public
// exponents: math/big is not constant time.
func rawRSA(in []byte, exp, n *big.Int, k int) ([]byte, error) {
	if len(in) > k {
		return nil, errGreaterThanModLen
	}
	m := new(big.Int).SetBytes(in)
	if m.Cmp(n) >= 0 {
		return nil, errTooLargeForModulus
	}
	return new(big.Int).Exp(m, exp, n).FillBytes(make([]byte, k)), nil
}

// unpadType1 is OpenSSL's RSA_padding_check_PKCS1_type_1 over a full block.
func unpadType1(block []byte) ([]byte, error) {
	if block[0] != 0 {
		return nil, errInvalidPadding
	}
	p := block[1:]
	if len(p) == 0 || p[0] != 1 {
		return nil, errBlockTypeNot01
	}
	p = p[1:]
	i := 0
	for ; i < len(p); i++ {
		if p[i] != 0xff {
			if p[i] == 0 {
				break
			}
			return nil, errBadFixedHeader
		}
	}
	if i == len(p) {
		return nil, errNullBeforeBlock
	}
	if i < 8 {
		return nil, errBadPadByteCount
	}
	return p[i+1:], nil
}

func rsaCrypt(op string, e *keyEntry, data []byte, padding int, oaepHash string, label []byte) ([]byte, error) {
	var pub *rsa.PublicKey
	var priv *rsa.PrivateKey
	switch e.asymType {
	case "rsa":
		priv = e.rsaPriv
		pub = e.rsaPub
		if pub == nil && priv != nil {
			pub = &priv.PublicKey
		}
	default:
		return nil, errNotForKeyType
	}
	k := (pub.N.BitLen() + 7) / 8
	private := op == "privateDecrypt" || op == "privateEncrypt"
	if private && priv == nil {
		return nil, errNotForKeyType
	}
	switch op {
	case "publicEncrypt":
		switch padding {
		case rsaOAEPPadding:
			h := nodeDigest(oaepHash)
			if h == nil {
				return nil, errInvalidOAEPDigest
			}
			out, err := rsa.EncryptOAEP(h(), rand.Reader, pub, data, label)
			if err != nil {
				return nil, errTooLargeForKey
			}
			return out, nil
		case rsaPKCS1Padding:
			out, err := rsa.EncryptPKCS1v15(rand.Reader, pub, data)
			if err != nil {
				return nil, errTooLargeForKey
			}
			return out, nil
		case rsaNoPadding:
			if len(data) > k {
				return nil, errTooLargeForKey
			}
			if len(data) < k {
				return nil, errTooSmallForKey
			}
			return rawRSA(data, big.NewInt(int64(pub.E)), pub.N, k)
		}
	case "privateDecrypt":
		switch padding {
		case rsaOAEPPadding:
			h := nodeDigest(oaepHash)
			if h == nil {
				return nil, errInvalidOAEPDigest
			}
			if len(data) > k {
				return nil, errGreaterThanModLen
			}
			out, err := rsa.DecryptOAEP(h(), nil, priv, data, label)
			if err != nil {
				return nil, errOAEPDecoding
			}
			return out, nil
		case rsaNoPadding:
			return nil, errNoPaddingPrivate
		}
	case "privateEncrypt":
		switch padding {
		case rsaPKCS1Padding:
			out, err := rsa.SignPKCS1v15(nil, priv, 0, data)
			if err != nil {
				return nil, errTooLargeForKey
			}
			return out, nil
		case rsaNoPadding:
			return nil, errNoPaddingPrivate
		}
	case "publicDecrypt":
		switch padding {
		case rsaPKCS1Padding:
			block, err := rawRSA(data, big.NewInt(int64(pub.E)), pub.N, k)
			if err != nil {
				return nil, err
			}
			return unpadType1(block)
		case rsaNoPadding:
			return rawRSA(data, big.NewInt(int64(pub.E)), pub.N, k)
		}
	}
	return nil, errUnknownPadding
}

// rsaCryptFunc backs _rsaCrypt(op, handle, dataB64, padding, oaepHash,
// labelB64): the output as base64.
func (c *Crypto) rsaCryptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 6 {
		return ctx.Throw("rsaCrypt: missing arguments")
	}
	e, err := c.lookupKey(args[1].Integer())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	data, _ := base64.StdEncoding.DecodeString(args[2].String())
	label, _ := base64.StdEncoding.DecodeString(args[5].String())
	out, err := rsaCrypt(args[0].String(), e, data, int(args[3].Integer()), args[4].String(), label)
	if err != nil {
		return ctx.Throw(err.Error())
	}
	v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(out))
	return v
}
