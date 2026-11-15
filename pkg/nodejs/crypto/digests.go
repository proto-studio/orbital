package crypto

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"hash"
	"strings"
)

// nodeDigests maps the digest names Node (OpenSSL) accepts, lowercased, to
// the Go standard library hash behind them. Node also has ripemd160,
// blake2b512, blake2s256, sm3, md5-sha1 and the shake functions, which the
// standard library doesn't; those names are unknown here.
var nodeDigests = map[string]func() hash.Hash{}

func init() {
	add := func(h func() hash.Hash, names ...string) {
		for _, n := range names {
			nodeDigests[strings.ToLower(n)] = h
		}
	}
	add(md5.New, "md5", "RSA-MD5", "md5WithRSAEncryption", "ssl3-md5")
	add(sha1.New, "sha1", "sha-1", "RSA-SHA1", "RSA-SHA1-2", "sha1WithRSAEncryption", "ssl3-sha1")
	add(sha256.New224, "sha224", "sha-224", "sha2-224", "RSA-SHA224", "sha224WithRSAEncryption")
	add(sha256.New, "sha256", "sha-256", "sha2-256", "RSA-SHA256", "sha256WithRSAEncryption")
	add(sha512.New384, "sha384", "sha-384", "sha2-384", "RSA-SHA384", "sha384WithRSAEncryption")
	add(sha512.New, "sha512", "sha-512", "sha2-512", "RSA-SHA512", "sha512WithRSAEncryption")
	add(sha512.New512_224, "sha512-224", "sha-512/224", "sha2-512/224", "RSA-SHA512/224", "sha512-224WithRSAEncryption")
	add(sha512.New512_256, "sha512-256", "sha-512/256", "sha2-512/256", "RSA-SHA512/256", "sha512-256WithRSAEncryption")
	add(func() hash.Hash { return sha3.New224() }, "sha3-224", "RSA-SHA3-224", "id-rsassa-pkcs1-v1_5-with-sha3-224")
	add(func() hash.Hash { return sha3.New256() }, "sha3-256", "RSA-SHA3-256", "id-rsassa-pkcs1-v1_5-with-sha3-256")
	add(func() hash.Hash { return sha3.New384() }, "sha3-384", "RSA-SHA3-384", "id-rsassa-pkcs1-v1_5-with-sha3-384")
	add(func() hash.Hash { return sha3.New512() }, "sha3-512", "RSA-SHA3-512", "id-rsassa-pkcs1-v1_5-with-sha3-512")
}

// nodeDigest returns the hash for a Node digest name, matched without regard
// to case as Node does, or nil.
func nodeDigest(name string) func() hash.Hash {
	return nodeDigests[strings.ToLower(name)]
}
