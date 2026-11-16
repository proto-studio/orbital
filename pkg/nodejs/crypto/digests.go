package crypto

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"hash"
	"strings"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // Node still offers RIPEMD-160.
)

// nodeDigests maps the digest names Node (OpenSSL) accepts, lowercased, to
// the hash behind them: Go's standard library (SHA-3 and SHAKE from
// crypto/sha3), golang.org/x/crypto for RIPEMD-160 and BLAKE2, and md5Sha1
// below. Node also has sm3, which neither
// has; that name is unknown here.
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
	add(ripemd160.New, "ripemd160", "ripemd-160", "ripemd", "rmd160", "RSA-RIPEMD160", "ripemd160WithRSA")
	add(func() hash.Hash { h, _ := blake2b.New512(nil); return h }, "blake2b512", "blake2b-512")
	add(func() hash.Hash { h, _ := blake2s.New256(nil); return h }, "blake2s256", "blake2s-256")
	add(func() hash.Hash { return &md5Sha1{md5.New(), sha1.New()} }, "md5-sha1")
	add(func() hash.Hash { return &shake{sha3.NewSHAKE128(), sha3.NewSHAKE128, 16} }, "shake128", "shake-128")
	add(func() hash.Hash { return &shake{sha3.NewSHAKE256(), sha3.NewSHAKE256, 32} }, "shake256", "shake-256")
}

// isXOF reports whether a Node digest name is an extendable-output function.
// OpenSSL's PBKDF2, HKDF and HMAC refuse those, though the names are known.
func isXOF(name string) bool {
	_, ok := nodeDigest(name)().(*shake)
	return ok
}

// shake is a SHAKE function read to its default length, as OpenSSL's
// SHAKE128 (16 bytes) and SHAKE256 (32 bytes) digests are.
type shake struct {
	s    *sha3.SHAKE
	newS func() *sha3.SHAKE
	size int
}

func (h *shake) Write(p []byte) (int, error) { return h.s.Write(p) }
func (h *shake) Reset()                      { h.s.Reset() }
func (h *shake) Size() int                   { return h.size }
func (h *shake) BlockSize() int              { return h.s.BlockSize() }
func (h *shake) Sum(b []byte) []byte {
	// Read from a copy so the hash can still be written to.
	state, _ := h.s.MarshalBinary()
	c := h.newS()
	c.UnmarshalBinary(state)
	out := make([]byte, h.size)
	c.Read(out)
	return append(b, out...)
}

// md5Sha1 is OpenSSL's MD5-SHA1: the MD5 digest followed by the SHA-1
// digest of the same data, with a 64-byte block for HMAC.
type md5Sha1 struct{ md5, sha1 hash.Hash }

func (h *md5Sha1) Write(p []byte) (int, error) {
	h.md5.Write(p)
	return h.sha1.Write(p)
}
func (h *md5Sha1) Sum(b []byte) []byte { return h.sha1.Sum(h.md5.Sum(b)) }
func (h *md5Sha1) Reset()              { h.md5.Reset(); h.sha1.Reset() }
func (h *md5Sha1) Size() int           { return md5.Size + sha1.Size }
func (h *md5Sha1) BlockSize() int      { return 64 }

// nodeDigest returns the hash for a Node digest name, matched without regard
// to case as Node does, or nil.
func nodeDigest(name string) func() hash.Hash {
	return nodeDigests[strings.ToLower(name)]
}
