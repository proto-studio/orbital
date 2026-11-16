package crypto

import (
	"strings"

	"proto.zip/studio/orbital/pkg/v8"
)

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

// nodeCipherNames is crypto.getCiphers() in Node 20.18.1.
var nodeCipherNames = []string{
	"aes-128-cbc", "aes-128-ccm", "aes-128-cfb", "aes-128-cfb1", "aes-128-cfb8", "aes-128-ctr",
	"aes-128-ecb", "aes-128-gcm", "aes-128-ocb", "aes-128-ofb", "aes-128-xts", "aes-192-cbc",
	"aes-192-ccm", "aes-192-cfb", "aes-192-cfb1", "aes-192-cfb8", "aes-192-ctr", "aes-192-ecb",
	"aes-192-gcm", "aes-192-ocb", "aes-192-ofb", "aes-256-cbc", "aes-256-ccm", "aes-256-cfb",
	"aes-256-cfb1", "aes-256-cfb8", "aes-256-ctr", "aes-256-ecb", "aes-256-gcm", "aes-256-ocb",
	"aes-256-ofb", "aes-256-xts", "aes128", "aes128-wrap", "aes192", "aes192-wrap", "aes256",
	"aes256-wrap", "aria-128-cbc", "aria-128-ccm", "aria-128-cfb", "aria-128-cfb1", "aria-128-cfb8",
	"aria-128-ctr", "aria-128-ecb", "aria-128-gcm", "aria-128-ofb", "aria-192-cbc", "aria-192-ccm",
	"aria-192-cfb", "aria-192-cfb1", "aria-192-cfb8", "aria-192-ctr", "aria-192-ecb", "aria-192-gcm",
	"aria-192-ofb", "aria-256-cbc", "aria-256-ccm", "aria-256-cfb", "aria-256-cfb1", "aria-256-cfb8",
	"aria-256-ctr", "aria-256-ecb", "aria-256-gcm", "aria-256-ofb", "aria128", "aria192", "aria256",
	"camellia-128-cbc", "camellia-128-cfb", "camellia-128-cfb1", "camellia-128-cfb8",
	"camellia-128-ctr", "camellia-128-ecb", "camellia-128-ofb", "camellia-192-cbc",
	"camellia-192-cfb", "camellia-192-cfb1", "camellia-192-cfb8", "camellia-192-ctr",
	"camellia-192-ecb", "camellia-192-ofb", "camellia-256-cbc", "camellia-256-cfb",
	"camellia-256-cfb1", "camellia-256-cfb8", "camellia-256-ctr", "camellia-256-ecb",
	"camellia-256-ofb", "camellia128", "camellia192", "camellia256", "chacha20", "chacha20-poly1305",
	"des-ede", "des-ede-cbc", "des-ede-cfb", "des-ede-ecb", "des-ede-ofb", "des-ede3", "des-ede3-cbc",
	"des-ede3-cfb", "des-ede3-cfb1", "des-ede3-cfb8", "des-ede3-ecb", "des-ede3-ofb", "des3",
	"des3-wrap", "id-aes128-CCM", "id-aes128-GCM", "id-aes128-wrap", "id-aes128-wrap-pad",
	"id-aes192-CCM", "id-aes192-GCM", "id-aes192-wrap", "id-aes192-wrap-pad", "id-aes256-CCM",
	"id-aes256-GCM", "id-aes256-wrap", "id-aes256-wrap-pad", "id-smime-alg-CMS3DESwrap", "sm4",
	"sm4-cbc", "sm4-cfb", "sm4-ctr", "sm4-ecb", "sm4-ofb",
}

// supportedCiphers is the part of nodeCipherNames createCipheriv knows
// (cipher.go), in Node's order.
func supportedCiphers() []string {
	var out []string
	for _, n := range nodeCipherNames {
		if _, ok := cipherSpecs[strings.ToLower(n)]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (c *Crypto) getCiphersFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return stringArray(info.Context(), supportedCiphers())
}
