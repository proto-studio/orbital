package crypto

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// scrypt (RFC 7914), built on the standard library's PBKDF2 and SHA-256.

const scryptMemoryError = "error:030000AC:digital envelope routines::memory limit exceeded"

// errScryptParams is a parameter set OpenSSL refuses without a reason.
var errScryptParams = errors.New("")

// scryptCheck applies OpenSSL's checks to scrypt's parameters, in its order.
// It returns errScryptParams, an error with OpenSSL's memory message, or nil.
// maxmem 0 means OpenSSL's default of 32 MiB, which Node never passes.
func scryptCheck(n, r, p, maxmem uint64) error {
	if r == 0 || p == 0 || n < 2 || n&(n-1) != 0 {
		return errScryptParams
	}
	memErr := errors.New(scryptMemoryError)
	if 16*r <= 63 && n >= uint64(1)<<(16*r) {
		return memErr
	}
	const prMax = 1<<30 - 1
	if p > prMax/r {
		return memErr
	}
	blen := p * 128 * r
	if blen > math.MaxInt32 {
		return memErr
	}
	if n+2 > (math.MaxUint64/(32*4))/r {
		return memErr
	}
	vlen := 32 * r * (n + 2) * 4
	if blen > math.MaxUint64-vlen {
		return memErr
	}
	if maxmem == 0 {
		maxmem = 32 * 1024 * 1024
	}
	if blen+vlen > maxmem {
		return memErr
	}
	return nil
}

// scryptKey derives keyLen bytes. The parameters must have passed scryptCheck.
func scryptKey(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if keyLen == 0 {
		return []byte{}, nil
	}
	b, err := pbkdf2.Key(sha256.New, string(password), salt, 1, p*128*r)
	if err != nil {
		return nil, err
	}
	xy := make([]uint32, 64*r)
	v := make([]uint32, 32*n*r)
	for i := 0; i < p; i++ {
		scryptROMix(b[i*128*r:(i+1)*128*r], r, n, v, xy)
	}
	return pbkdf2.Key(sha256.New, string(password), b, 1, keyLen)
}

func scryptROMix(b []byte, r, n int, v, xy []uint32) {
	words := 32 * r
	x, y := xy[:words], xy[words:]
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	for i := 0; i < n; i++ {
		copy(v[i*words:], x)
		scryptBlockMix(x, y, r)
		x, y = y, x
	}
	for i := 0; i < n; i++ {
		j := int(uint64(x[(2*r-1)*16])|uint64(x[(2*r-1)*16+1])<<32) & (n - 1)
		vj := v[j*words : (j+1)*words]
		for k := range x {
			x[k] ^= vj[k]
		}
		scryptBlockMix(x, y, r)
		x, y = y, x
	}
	for i, w := range x {
		binary.LittleEndian.PutUint32(b[i*4:], w)
	}
}

// scryptBlockMix writes BlockMix(in) to out: even-numbered Salsa outputs
// first, then the odd-numbered ones.
func scryptBlockMix(in, out []uint32, r int) {
	var t [16]uint32
	copy(t[:], in[(2*r-1)*16:])
	for i := 0; i < 2*r; i++ {
		for k := 0; k < 16; k++ {
			t[k] ^= in[i*16+k]
		}
		salsa208(&t)
		dst := (i/2)*16 + (i%2)*r*16
		copy(out[dst:dst+16], t[:])
	}
}

// salsa208 applies the Salsa20/8 core to b in place.
func salsa208(b *[16]uint32) {
	x := *b
	for i := 0; i < 8; i += 2 {
		x[4] ^= bits.RotateLeft32(x[0]+x[12], 7)
		x[8] ^= bits.RotateLeft32(x[4]+x[0], 9)
		x[12] ^= bits.RotateLeft32(x[8]+x[4], 13)
		x[0] ^= bits.RotateLeft32(x[12]+x[8], 18)
		x[9] ^= bits.RotateLeft32(x[5]+x[1], 7)
		x[13] ^= bits.RotateLeft32(x[9]+x[5], 9)
		x[1] ^= bits.RotateLeft32(x[13]+x[9], 13)
		x[5] ^= bits.RotateLeft32(x[1]+x[13], 18)
		x[14] ^= bits.RotateLeft32(x[10]+x[6], 7)
		x[2] ^= bits.RotateLeft32(x[14]+x[10], 9)
		x[6] ^= bits.RotateLeft32(x[2]+x[14], 13)
		x[10] ^= bits.RotateLeft32(x[6]+x[2], 18)
		x[3] ^= bits.RotateLeft32(x[15]+x[11], 7)
		x[7] ^= bits.RotateLeft32(x[3]+x[15], 9)
		x[11] ^= bits.RotateLeft32(x[7]+x[3], 13)
		x[15] ^= bits.RotateLeft32(x[11]+x[7], 18)
		x[1] ^= bits.RotateLeft32(x[0]+x[3], 7)
		x[2] ^= bits.RotateLeft32(x[1]+x[0], 9)
		x[3] ^= bits.RotateLeft32(x[2]+x[1], 13)
		x[0] ^= bits.RotateLeft32(x[3]+x[2], 18)
		x[6] ^= bits.RotateLeft32(x[5]+x[4], 7)
		x[7] ^= bits.RotateLeft32(x[6]+x[5], 9)
		x[4] ^= bits.RotateLeft32(x[7]+x[6], 13)
		x[5] ^= bits.RotateLeft32(x[4]+x[7], 18)
		x[11] ^= bits.RotateLeft32(x[10]+x[9], 7)
		x[8] ^= bits.RotateLeft32(x[11]+x[10], 9)
		x[9] ^= bits.RotateLeft32(x[8]+x[11], 13)
		x[10] ^= bits.RotateLeft32(x[9]+x[8], 18)
		x[12] ^= bits.RotateLeft32(x[15]+x[14], 7)
		x[13] ^= bits.RotateLeft32(x[12]+x[15], 9)
		x[14] ^= bits.RotateLeft32(x[13]+x[12], 13)
		x[15] ^= bits.RotateLeft32(x[14]+x[13], 18)
	}
	for i := range b {
		b[i] += x[i]
	}
}
