package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/poly1305" //nolint:staticcheck // the building block of OpenSSL's streaming ChaCha20-Poly1305
	"proto.zip/studio/orbital/pkg/v8"
)

// createCipheriv / createDecipheriv: the native cipher contexts behind
// cipher.js. Each context is a handle into a per-runtime table, like keys.
// Errors cross as "CODE|message" for cipher.js to rethrow as Node does.

//go:embed cipher.js
var cipherJS string

// cipherSpec describes one cipher Node knows by name.
type cipherSpec struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	KeyLength int    `json:"keyLength"`
	IVLength  int    `json:"ivLength,omitempty"`
	BlockSize int    `json:"blockSize"`
	NID       int    `json:"nid"`
	des       bool
	chacha    bool
}

// cipherSpecs maps Node's (lowercased) cipher names to what Orbital
// implements, with the details getCipherInfo reports.
var cipherSpecs = map[string]cipherSpec{}

func init() {
	add := func(s cipherSpec, names ...string) {
		for _, n := range names {
			cipherSpecs[strings.ToLower(n)] = s
		}
	}
	nids := map[string][3]int{ // 128, 192, 256
		"cbc": {419, 423, 427}, "ecb": {418, 422, 426}, "ctr": {904, 905, 906}, "cfb": {421, 425, 429},
		"cfb8": {653, 654, 655}, "ofb": {420, 424, 428}, "gcm": {895, 898, 901},
	}
	for i, bits := range []int{128, 192, 256} {
		size := bits / 8
		b := []string{"128", "192", "256"}[i]
		add(cipherSpec{"aes-" + b + "-cbc", "cbc", size, 16, 16, nids["cbc"][i], false, false}, "aes-"+b+"-cbc", "aes"+b)
		add(cipherSpec{"aes-" + b + "-ecb", "ecb", size, 0, 16, nids["ecb"][i], false, false}, "aes-"+b+"-ecb")
		add(cipherSpec{"aes-" + b + "-ctr", "ctr", size, 16, 1, nids["ctr"][i], false, false}, "aes-"+b+"-ctr")
		add(cipherSpec{"aes-" + b + "-cfb", "cfb", size, 16, 1, nids["cfb"][i], false, false}, "aes-"+b+"-cfb")
		add(cipherSpec{"aes-" + b + "-cfb8", "cfb8", size, 16, 1, nids["cfb8"][i], false, false}, "aes-"+b+"-cfb8")
		add(cipherSpec{"aes-" + b + "-ofb", "ofb", size, 16, 1, nids["ofb"][i], false, false}, "aes-"+b+"-ofb")
		add(cipherSpec{"id-aes" + b + "-gcm", "gcm", size, 12, 1, nids["gcm"][i], false, false}, "aes-"+b+"-gcm", "id-aes"+b+"-gcm")
	}
	add(cipherSpec{"des-ede3-cbc", "cbc", 24, 8, 8, 44, true, false}, "des-ede3-cbc", "des3")
	add(cipherSpec{"des-ede3", "ecb", 24, 0, 8, 33, true, false}, "des-ede3", "des-ede3-ecb")
	add(cipherSpec{"des-ede-cbc", "cbc", 16, 8, 8, 43, true, false}, "des-ede-cbc")
	add(cipherSpec{"des-ede", "ecb", 16, 0, 8, 32, true, false}, "des-ede", "des-ede-ecb")
	add(cipherSpec{"chacha20", "stream", 32, 16, 1, 1019, false, true}, "chacha20")
	add(cipherSpec{"chacha20-poly1305", "stream", 32, 12, 1, 1018, false, true}, "chacha20-poly1305")
}

type cipherCtx struct {
	spec    cipherSpec
	decrypt bool
	block   cipher.Block
	mode    cipher.BlockMode // cbc, ecb
	stream  cipher.Stream    // ctr, cfb, ofb, cfb8, gcm
	pending []byte           // block modes: input not yet processed
	padding bool
	updated bool
	done    bool

	// chacha20-poly1305: the MAC runs alongside, as in OpenSSL
	mac     *poly1305.MAC
	aadLen  uint64
	textLen uint64
	inAAD   bool // AAD written since the last data, not yet padded

	// gcm
	aead   cipher.AEAD
	nonce  []byte
	aad    []byte
	plain  []byte // the whole plaintext, for the tag at final
	tagLen int
	tag    []byte // the tag: computed (encrypt) or expected (decrypt)
}

type cipherError struct{ code, message string }

func (e *cipherError) Error() string { return e.code + "|" + e.message }

var (
	errBadDecrypt    = &cipherError{"ERR_OSSL_BAD_DECRYPT", "error:1C800064:Provider routines::bad decrypt"}
	errWrongFinalLen = &cipherError{"ERR_OSSL_WRONG_FINAL_BLOCK_LENGTH", "error:1C80006B:Provider routines::wrong final block length"}
	errInvalidState  = &cipherError{"ERR_CRYPTO_INVALID_STATE", "Invalid state"}
	errUnsupported   = &cipherError{"", "Trying to add data in unsupported state"}
	errAuthenticate  = &cipherError{"", "Unsupported state or unable to authenticate data"}
	errUnknownCipher = &cipherError{"ERR_CRYPTO_UNKNOWN_CIPHER", "Unknown cipher"}
	errInvalidKeyLen = &cipherError{"ERR_CRYPTO_INVALID_KEYLEN", "Invalid key length"}
	errInvalidIV     = &cipherError{"ERR_CRYPTO_INVALID_IV", "Invalid initialization vector"}
	errStateFor      = func(op string) *cipherError {
		return &cipherError{"ERR_CRYPTO_INVALID_STATE", "Invalid state for operation " + op}
	}
	errInvalidAuthTag = func(n int) *cipherError {
		return &cipherError{"ERR_CRYPTO_INVALID_AUTH_TAG", "Invalid authentication tag length: " + itoa(n)}
	}
)

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func validGCMTagLen(n int) bool {
	return n == 4 || n == 8 || (n >= 12 && n <= 16)
}

// ecbMode is ECB as a cipher.BlockMode.
type ecbMode struct {
	b       cipher.Block
	decrypt bool
}

func (e ecbMode) BlockSize() int { return e.b.BlockSize() }
func (e ecbMode) CryptBlocks(dst, src []byte) {
	bs := e.b.BlockSize()
	for i := 0; i+bs <= len(src); i += bs {
		if e.decrypt {
			e.b.Decrypt(dst[i:i+bs], src[i:i+bs])
		} else {
			e.b.Encrypt(dst[i:i+bs], src[i:i+bs])
		}
	}
}

// cfb8 is CFB with an 8-bit feedback, as OpenSSL's aes-*-cfb8.
type cfb8 struct {
	b       cipher.Block
	reg     []byte
	out     []byte
	decrypt bool
}

func (c *cfb8) XORKeyStream(dst, src []byte) {
	for i := range src {
		c.b.Encrypt(c.out, c.reg)
		in := src[i]
		o := in ^ c.out[0]
		copy(c.reg, c.reg[1:])
		if c.decrypt {
			c.reg[len(c.reg)-1] = in
		} else {
			c.reg[len(c.reg)-1] = o
		}
		dst[i] = o
	}
}

// gcmCTR is GCM's counter mode: only the low 32 bits of the counter block
// count up (inc32), so it differs from cipher.NewCTR once they wrap.
type gcmCTR struct {
	b       cipher.Block
	counter []byte
	ks      []byte
	used    int
}

func (g *gcmCTR) XORKeyStream(dst, src []byte) {
	for i := range src {
		if g.used == len(g.ks) {
			g.b.Encrypt(g.ks, g.counter)
			binary.BigEndian.PutUint32(g.counter[12:], binary.BigEndian.Uint32(g.counter[12:])+1)
			g.used = 0
		}
		dst[i] = src[i] ^ g.ks[g.used]
		g.used++
	}
}

// chachaStream is OpenSSL's ChaCha20 over golang.org/x/crypto/chacha20: a
// 16-byte IV of a little-endian 32-bit block counter and a 96-bit nonce.
// When the counter wraps, OpenSSL carries into the nonce's first word (a
// 64-bit counter in effect); x/crypto stops there, so the cipher is remade
// with that word incremented.
type chachaStream struct {
	key   []byte
	words [4]uint32 // counter, then the nonce's words
	c     *chacha20.Cipher
	left  uint64 // bytes until the counter wraps
}

func newChaChaStream(key, iv []byte) *chachaStream {
	s := &chachaStream{key: key}
	for i := range s.words {
		s.words[i] = binary.LittleEndian.Uint32(iv[4*i:])
	}
	s.reset()
	return s
}

func (s *chachaStream) reset() {
	nonce := make([]byte, 12)
	for i := 1; i < 4; i++ {
		binary.LittleEndian.PutUint32(nonce[4*(i-1):], s.words[i])
	}
	s.c, _ = chacha20.NewUnauthenticatedCipher(s.key, nonce)
	s.c.SetCounter(s.words[0])
	s.left = (1<<32 - uint64(s.words[0])) * 64
}

func (s *chachaStream) XORKeyStream(dst, src []byte) {
	for len(src) > 0 {
		n := uint64(len(src))
		if n > s.left {
			n = s.left
		}
		s.c.XORKeyStream(dst[:n], src[:n])
		dst, src, s.left = dst[n:], src[n:], s.left-n
		if s.left == 0 {
			s.words[0] = 0
			s.words[1]++
			s.reset()
		}
	}
}

// newChaChaCtx sets up chacha20, or chacha20-poly1305 as RFC 8439 and
// OpenSSL do: the Poly1305 key is the first 32 bytes of keystream block 0,
// the data starts at block 1, and the MAC is fed as the data goes by.
func newChaChaCtx(spec cipherSpec, key, iv []byte, decrypt bool, tagLen int) *cipherCtx {
	c := &cipherCtx{spec: spec, decrypt: decrypt, padding: true}
	if spec.Name == "chacha20" {
		c.stream = newChaChaStream(key, iv)
		return c
	}
	stream := newChaChaStream(key, append(make([]byte, 4), iv...))
	var macKey [32]byte
	block := make([]byte, 64)
	stream.XORKeyStream(block, block)
	copy(macKey[:], block)
	c.stream, c.mac = stream, poly1305.New(&macKey)
	if tagLen == -1 {
		tagLen = 16
	}
	c.tagLen = tagLen
	return c
}

// padAAD ends a run of AAD with zeros to a 16-byte boundary, as OpenSSL
// does before the next data or at the end.
func (c *cipherCtx) padAAD() {
	if c.inAAD {
		c.mac.Write(make([]byte, (16-c.aadLen%16)%16))
		c.inAAD = false
	}
}

// newCipherCtx sets up a context as Node's CipherBase::Init does: the
// cipher, then the IV, then the key length.
func newCipherCtx(name string, key, iv []byte, hasIV, decrypt bool, tagLen int) (*cipherCtx, error) {
	spec, ok := cipherSpecs[strings.ToLower(name)]
	if !ok {
		return nil, errUnknownCipher
	}
	if spec.Mode == "gcm" {
		if !hasIV || len(iv) == 0 {
			return nil, errInvalidIV
		}
		if tagLen != -1 && !validGCMTagLen(tagLen) {
			return nil, errInvalidAuthTag(tagLen)
		}
	} else if spec.Name == "chacha20-poly1305" {
		if !hasIV || len(iv) != spec.IVLength {
			return nil, errInvalidIV
		}
		if tagLen != -1 && (tagLen < 1 || tagLen > 16) {
			return nil, errInvalidAuthTag(tagLen)
		}
	} else if spec.IVLength == 0 {
		if hasIV && len(iv) != 0 {
			return nil, errInvalidIV
		}
	} else if !hasIV || len(iv) != spec.IVLength {
		return nil, errInvalidIV
	}
	if len(key) != spec.KeyLength {
		return nil, errInvalidKeyLen
	}
	if spec.chacha {
		return newChaChaCtx(spec, key, iv, decrypt, tagLen), nil
	}
	var block cipher.Block
	var err error
	if spec.des {
		k := key
		if len(k) == 16 {
			k = append(append([]byte{}, key...), key[:8]...)
		}
		block, err = des.NewTripleDESCipher(k)
	} else {
		block, err = aes.NewCipher(key)
	}
	if err != nil {
		return nil, errInvalidKeyLen
	}
	c := &cipherCtx{spec: spec, decrypt: decrypt, block: block, padding: true}
	ivCopy := append([]byte{}, iv...)
	switch spec.Mode {
	case "cbc":
		if decrypt {
			c.mode = cipher.NewCBCDecrypter(block, ivCopy)
		} else {
			c.mode = cipher.NewCBCEncrypter(block, ivCopy)
		}
	case "ecb":
		c.mode = ecbMode{block, decrypt}
	case "ctr":
		c.stream = cipher.NewCTR(block, ivCopy)
	case "ofb":
		c.stream = cipher.NewOFB(block, ivCopy)
	case "cfb":
		if decrypt {
			c.stream = cipher.NewCFBDecrypter(block, ivCopy)
		} else {
			c.stream = cipher.NewCFBEncrypter(block, ivCopy)
		}
	case "cfb8":
		c.stream = &cfb8{b: block, reg: ivCopy, out: make([]byte, block.BlockSize()), decrypt: decrypt}
	case "gcm":
		aead, err := cipher.NewGCMWithNonceSize(block, len(iv))
		if err != nil {
			return nil, errInvalidIV
		}
		c.aead, c.nonce = aead, ivCopy
		if tagLen == -1 {
			tagLen = 16
		}
		c.tagLen = tagLen
		// GCM encrypts with CTR from inc32(J0). Sealing one zero block gives
		// E(inc32(J0)); decrypting that gives the counter block itself.
		first := aead.Seal(nil, ivCopy, make([]byte, 16), nil)
		counter := make([]byte, 16)
		block.Decrypt(counter, first[:16])
		c.stream = &gcmCTR{b: block, counter: counter, ks: make([]byte, 16), used: 16}
	}
	return c, nil
}

func (c *cipherCtx) update(data []byte) ([]byte, error) {
	if c.done {
		return nil, errUnsupported
	}
	// Empty updates don't end the AAD phase, as in OpenSSL.
	if len(data) > 0 {
		c.updated = true
	}
	if c.stream != nil {
		out := make([]byte, len(data))
		c.stream.XORKeyStream(out, data)
		if c.mac != nil && len(data) > 0 {
			c.padAAD()
			if c.decrypt {
				c.mac.Write(data)
			} else {
				c.mac.Write(out)
			}
			c.textLen += uint64(len(data))
		}
		if c.aead != nil {
			if c.decrypt {
				c.plain = append(c.plain, out...)
			} else {
				c.plain = append(c.plain, data...)
			}
		}
		return out, nil
	}
	bs := c.block.BlockSize()
	c.pending = append(c.pending, data...)
	n := len(c.pending) / bs * bs
	// Decrypting with padding holds back the last full block for final.
	if c.decrypt && c.padding && n == len(c.pending) && n > 0 {
		n -= bs
	}
	out := make([]byte, n)
	c.mode.CryptBlocks(out, c.pending[:n])
	c.pending = append([]byte{}, c.pending[n:]...)
	return out, nil
}

func (c *cipherCtx) final() ([]byte, error) {
	if c.done {
		return nil, errInvalidState
	}
	c.done = true
	if c.mac != nil {
		c.padAAD()
		c.mac.Write(make([]byte, (16-c.textLen%16)%16))
		var lens [16]byte
		binary.LittleEndian.PutUint64(lens[:8], c.aadLen)
		binary.LittleEndian.PutUint64(lens[8:], c.textLen)
		c.mac.Write(lens[:])
		full := c.mac.Sum(nil)
		if !c.decrypt {
			c.tag = full[:c.tagLen]
			return []byte{}, nil
		}
		if c.tag == nil || subtle.ConstantTimeCompare(c.tag, full[:len(c.tag)]) != 1 {
			return nil, errAuthenticate
		}
		return []byte{}, nil
	}
	if c.aead != nil {
		sealed := c.aead.Seal(nil, c.nonce, c.plain, c.aad)
		full := sealed[len(sealed)-16:]
		if !c.decrypt {
			c.tag = append([]byte{}, full[:c.tagLen]...)
			return []byte{}, nil
		}
		if c.tag == nil || subtle.ConstantTimeCompare(c.tag, full[:len(c.tag)]) != 1 {
			return nil, errAuthenticate
		}
		return []byte{}, nil
	}
	if c.stream != nil {
		return []byte{}, nil
	}
	bs := c.block.BlockSize()
	if !c.decrypt {
		if c.padding {
			p := bs - len(c.pending)%bs
			c.pending = append(c.pending, bytes.Repeat([]byte{byte(p)}, p)...)
		} else if len(c.pending) != 0 {
			return nil, errWrongFinalLen
		}
		out := make([]byte, len(c.pending))
		c.mode.CryptBlocks(out, c.pending)
		return out, nil
	}
	if !c.padding {
		// A block held back while padding was on is decrypted as it is.
		if len(c.pending)%bs != 0 {
			return nil, errWrongFinalLen
		}
		out := make([]byte, len(c.pending))
		c.mode.CryptBlocks(out, c.pending)
		return out, nil
	}
	if len(c.pending) != bs {
		return nil, errWrongFinalLen
	}
	out := make([]byte, bs)
	c.mode.CryptBlocks(out, c.pending)
	p := int(out[bs-1])
	if p == 0 || p > bs {
		return nil, errBadDecrypt
	}
	for _, b := range out[bs-p:] {
		if int(b) != p {
			return nil, errBadDecrypt
		}
	}
	return out[:bs-p], nil
}

// --- the registry and natives ------------------------------------------------

func (c *Crypto) cipherArg(info *v8.FunctionCallbackInfo) (*cipherCtx, *v8.Value) {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return nil, ctx.Throw("cipher: handle is required")
	}
	c.cipherMu.Lock()
	cc := c.cipherCtxs[args[0].Integer()]
	c.cipherMu.Unlock()
	if cc == nil {
		return nil, ctx.Throw("cipher: invalid handle")
	}
	return cc, nil
}

func throwCipherError(ctx *v8.Context, err error) *v8.Value {
	return ctx.Throw(err.Error())
}

func b64Result(ctx *v8.Context, b []byte) *v8.Value {
	v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(b))
	return v
}

func argB64(args []*v8.Value, i int) []byte {
	if i >= len(args) {
		return nil
	}
	b, _ := base64.StdEncoding.DecodeString(args[i].String())
	return b
}

// cipherInitFunc backs _cipherInit(name, keyB64, ivB64 or null, decrypt,
// authTagLength or -1): a handle.
func (c *Crypto) cipherInitFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 5 {
		return ctx.Throw("cipher: missing arguments")
	}
	hasIV := !args[2].IsNull() && !args[2].IsUndefined()
	cc, err := newCipherCtx(args[0].String(), argB64(args, 1), argB64(args, 2), hasIV, args[3].Boolean(), int(args[4].Integer()))
	if err != nil {
		return throwCipherError(ctx, err)
	}
	c.cipherMu.Lock()
	if c.cipherCtxs == nil {
		c.cipherCtxs = map[int64]*cipherCtx{}
	}
	c.cipherSeq++
	id := c.cipherSeq
	c.cipherCtxs[id] = cc
	c.cipherMu.Unlock()
	return ctx.NewNumber(float64(id))
}

func (c *Crypto) cipherUpdateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	out, err := cc.update(argB64(info.Args(), 1))
	if err != nil {
		return throwCipherError(info.Context(), err)
	}
	return b64Result(info.Context(), out)
}

func (c *Crypto) cipherFinalFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	out, err := cc.final()
	if err != nil {
		return throwCipherError(info.Context(), err)
	}
	return b64Result(info.Context(), out)
}

func (c *Crypto) cipherSetAutoPaddingFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	if cc.done {
		return throwCipherError(info.Context(), errStateFor("setAutoPadding"))
	}
	cc.padding = len(info.Args()) < 2 || info.Args()[1].Boolean()
	return info.Context().Undefined()
}

func (c *Crypto) cipherSetAADFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	if cc.mac != nil && !cc.done {
		// OpenSSL takes AAD at any point, padding it when data follows.
		aad := argB64(info.Args(), 1)
		cc.mac.Write(aad)
		cc.aadLen += uint64(len(aad))
		cc.inAAD = true
		return info.Context().Undefined()
	}
	if cc.aead == nil || cc.done || cc.updated {
		return throwCipherError(info.Context(), errStateFor("setAAD"))
	}
	cc.aad = append(cc.aad, argB64(info.Args(), 1)...)
	return info.Context().Undefined()
}

func (c *Crypto) cipherGetAuthTagFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	if (cc.aead == nil && cc.mac == nil) || cc.decrypt || !cc.done || cc.tag == nil {
		return throwCipherError(info.Context(), errStateFor("getAuthTag"))
	}
	return b64Result(info.Context(), cc.tag)
}

func (c *Crypto) cipherSetAuthTagFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	cc, throw := c.cipherArg(info)
	if throw != nil {
		return throw
	}
	tag := argB64(info.Args(), 1)
	if (cc.aead == nil && cc.mac == nil) || !cc.decrypt || cc.done || cc.tag != nil {
		return throwCipherError(info.Context(), errStateFor("setAuthTag"))
	}
	if cc.mac != nil {
		if len(tag) != cc.tagLen {
			return throwCipherError(info.Context(), errInvalidAuthTag(len(tag)))
		}
	} else if !validGCMTagLen(len(tag)) {
		return throwCipherError(info.Context(), errInvalidAuthTag(len(tag)))
	}
	cc.tag = tag
	return info.Context().Undefined()
}

func (c *Crypto) cipherFreeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) > 0 {
		c.cipherMu.Lock()
		delete(c.cipherCtxs, args[0].Integer())
		c.cipherMu.Unlock()
	}
	return info.Context().Undefined()
}

// cipherInfoFunc backs _cipherInfo(name or nid): getCipherInfo's object as JSON,
// or "" for a cipher Orbital doesn't have.
func (c *Crypto) cipherInfoFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	out := ""
	if args := info.Args(); len(args) > 0 {
		var spec cipherSpec
		found := false
		if args[0].IsNumber() {
			nid := int(args[0].Integer())
			for _, sp := range cipherSpecs {
				if sp.NID == nid {
					spec, found = sp, true
					break
				}
			}
		} else {
			spec, found = cipherSpecs[strings.ToLower(args[0].String())]
		}
		if found {
			b, _ := json.Marshal(spec)
			out = string(b)
		}
	}
	v, _ := ctx.NewString(out)
	return v
}

// cipherCountFunc backs _cipherCount(): how many contexts are open, for tests.
func (c *Crypto) cipherCountFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	c.cipherMu.Lock()
	n := len(c.cipherCtxs)
	c.cipherMu.Unlock()
	return info.Context().NewNumber(float64(n))
}
