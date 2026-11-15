package crypto

import "proto.zip/studio/orbital/pkg/v8"

// The names crypto.getHashes(), getCurves() and getCiphers() list: Node's
// lists, in Node's order, kept to the ones Orbital implements, so a library
// that picks an algorithm from a list can use it.

// nodeHashNames is crypto.getHashes() in Node 20.18.1.
var nodeHashNames = []string{
	"RSA-MD5", "RSA-RIPEMD160", "RSA-SHA1", "RSA-SHA1-2", "RSA-SHA224", "RSA-SHA256", "RSA-SHA3-224",
	"RSA-SHA3-256", "RSA-SHA3-384", "RSA-SHA3-512", "RSA-SHA384", "RSA-SHA512", "RSA-SHA512/224",
	"RSA-SHA512/256", "RSA-SM3", "blake2b512", "blake2s256", "id-rsassa-pkcs1-v1_5-with-sha3-224",
	"id-rsassa-pkcs1-v1_5-with-sha3-256", "id-rsassa-pkcs1-v1_5-with-sha3-384",
	"id-rsassa-pkcs1-v1_5-with-sha3-512", "md5", "md5-sha1", "md5WithRSAEncryption", "ripemd",
	"ripemd160", "ripemd160WithRSA", "rmd160", "sha1", "sha1WithRSAEncryption", "sha224",
	"sha224WithRSAEncryption", "sha256", "sha256WithRSAEncryption", "sha3-224", "sha3-256", "sha3-384",
	"sha3-512", "sha384", "sha384WithRSAEncryption", "sha512", "sha512-224",
	"sha512-224WithRSAEncryption", "sha512-256", "sha512-256WithRSAEncryption", "sha512WithRSAEncryption",
	"shake128", "shake256", "sm3", "sm3WithRSAEncryption", "ssl3-md5", "ssl3-sha1",
}

func supportedHashes() []string {
	var out []string
	for _, n := range nodeHashNames {
		if nodeDigest(n) != nil {
			out = append(out, n)
		}
	}
	return out
}

// supportedCurves is the part of Node's crypto.getCurves() the KeyObject API
// supports, in Node's (sorted) order.
var supportedCurves = []string{"prime256v1", "secp384r1", "secp521r1"}

func stringArray(ctx *v8.Context, items []string) *v8.Value {
	arr, _ := ctx.NewArray(len(items))
	for i, s := range items {
		v, _ := ctx.NewString(s)
		arr.SetIndex(i, v)
	}
	return arr
}

func (c *Crypto) getCurvesFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return stringArray(info.Context(), supportedCurves)
}

// getCiphersFunc lists no ciphers: node:crypto has no createCipheriv yet.
func (c *Crypto) getCiphersFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return stringArray(info.Context(), nil)
}
