package crypto

import (
	"crypto/hkdf"
	"crypto/pbkdf2"
	_ "embed"
	"encoding/base64"
	"errors"
	"hash"
	"math"

	"golang.org/x/crypto/scrypt"
	"proto.zip/studio/orbital/pkg/v8"
)

// Native halves of pbkdf2, scrypt and hkdf. kdf.js checks the arguments as
// Node does and passes bytes as base64; these derive the key, on the JS
// thread for the Sync forms and on a goroutine when given a callback.

//go:embed kdf.js
var kdfJS string

// deriveKey runs work now, returning the key as base64, or, when callback is
// a function, on the runtime's thread pool, calling callback(undefined, base64) or
// callback(message) on the JS thread later, as Node runs these on its
// threadpool.
func (c *Crypto) deriveKey(ctx *v8.Context, callback *v8.Value, work func() ([]byte, error)) *v8.Value {
	if callback == nil || !callback.IsFunction() {
		key, err := work()
		if err != nil {
			return ctx.Throw(err.Error())
		}
		v, _ := ctx.NewString(base64.StdEncoding.EncodeToString(key))
		return v
	}
	loop := c.rt.EventLoop()
	jsCtx := c.rt.Context()
	loop.AddPendingWork()
	c.rt.RunInThreadPool(func() {
		defer loop.DonePendingWork()
		key, err := work()
		loop.EnqueueMicrotask(func() {
			if err != nil {
				msg, _ := jsCtx.NewString(err.Error())
				c.rt.MakeCallback(callback, nil, msg)
				return
			}
			data, _ := jsCtx.NewString(base64.StdEncoding.EncodeToString(key))
			c.rt.MakeCallback(callback, nil, jsCtx.Undefined(), data)
		})
	})
	return ctx.Undefined()
}

// errDerivingBits is Node's error when OpenSSL's PBKDF2 or HKDF fails, as it
// does for a SHAKE digest.
var errDerivingBits = errors.New("Deriving bits failed")

func argBytes(args []*v8.Value, i int) []byte {
	if i >= len(args) {
		return nil
	}
	b, _ := base64.StdEncoding.DecodeString(args[i].String())
	return b
}

func argCallback(args []*v8.Value, i int) *v8.Value {
	if i < len(args) {
		return args[i]
	}
	return nil
}

// digestSizeFunc backs _digestSize(name): the digest's output size in bytes,
// or 0 when Node's name is unknown here.
func (c *Crypto) digestSizeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.NewNumber(0)
	}
	h := nodeDigest(args[0].String())
	if h == nil {
		return ctx.NewNumber(0)
	}
	return ctx.NewNumber(float64(h().Size()))
}

// pbkdf2Func backs _pbkdf2(passwordB64, saltB64, iterations, keylen, digest[, callback]).
func (c *Crypto) pbkdf2Func(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 5 {
		return ctx.Throw("pbkdf2: missing arguments")
	}
	iter, keyLen := args[2].Integer(), args[3].Integer()
	h := nodeDigest(args[4].String())
	// kdf.js checks these, but scripts can reach the native directly. The
	// limit is checked before the inputs are decoded.
	if keyLen < 0 || keyLen > maxNativeBytes {
		return ctx.Throw(outOfRange("keylen", 0, maxNativeBytes, keyLen))
	}
	inputs, ok := c.inputsCost(args, 0, 1)
	if !ok || h == nil || iter < 1 || iter > math.MaxInt32 || !c.keyLenFits(keyLen, inputs) {
		return ctx.Throw("pbkdf2: invalid arguments")
	}
	password, salt := argBytes(args, 0), argBytes(args, 1)
	xof := isXOF(args[4].String())
	return c.deriveKey(ctx, argCallback(args, 5), func() ([]byte, error) {
		if xof {
			return nil, errDerivingBits
		}
		if keyLen == 0 {
			return []byte{}, nil
		}
		return pbkdf2.Key(h, string(password), salt, int(iter), int(keyLen))
	})
}

// OpenSSL checks scrypt's parameters before deriving and reports a memory
// limit as below; Node shows its message. golang.org/x/crypto/scrypt derives.
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

// keyOutputCost is the memory a derived key of keyLen bytes takes on its way
// to JavaScript, counted against Config.CryptoMemoryLimit: the key, its
// base64 form (4/3 of it), the C copy of that string and the V8 string made
// from it (4/3 each), and the Buffer decoded from it: six times its length.
func keyOutputCost(keyLen int64) uint64 {
	return 6 * uint64(keyLen)
}

// inputCharCost is what each character of a derivation's base64 input
// strings counts against Config.CryptoMemoryLimit. Copying a string out of V8
// holds up to three UTF-8 copies of it at once, at up to three bytes a
// character, and the decoded bytes (and any copy the derivation makes of
// them) take less than another one. The length is read from V8 before any
// copy is made.
const inputCharCost = 10

// inputCost is the memory the inputs of a derivation take, given the total
// length of the base64 strings they arrive as.
func inputCost(chars int64) uint64 {
	if chars <= 0 {
		return 0
	}
	return uint64(chars) * inputCharCost
}

// StringLength returns a function that reports a value's length in UTF-16
// code units, without copying it out of V8, or -1 when it isn't a string. A
// native checks the length of an input against a memory limit with it before
// the input is copied, and it rules out objects whose conversion to a string
// could differ from one call to the next.
func StringLength(ctx *v8.Context) (func(*v8.Value) int64, error) {
	fn, err := ctx.RunScript("(s) => typeof s === 'string' ? s.length : -1", "crypto-string-length.js")
	if err != nil {
		return nil, err
	}
	return func(v *v8.Value) int64 {
		n, err := fn.Call(ctx.Undefined(), v)
		if err != nil || n == nil {
			return -1
		}
		return n.Integer()
	}, nil
}

// inputsCost is inputCost for the string arguments at the given indexes, read
// before they're copied; ok is false when one of them isn't a string.
func (c *Crypto) inputsCost(args []*v8.Value, idx ...int) (cost uint64, ok bool) {
	var chars int64
	for _, i := range idx {
		if i >= len(args) {
			continue
		}
		n := c.strLen(args[i])
		if n < 0 {
			return 0, false
		}
		chars += n
	}
	return inputCost(chars), true
}

// kdfMaxKeyLen is the longest key a derivation whose decoded inputs take
// inputs bytes may return under Config.CryptoMemoryLimit, or -1 when no limit
// is set.
func (c *Crypto) kdfMaxKeyLen(inputs uint64) int64 {
	limit := c.rt.CryptoMemoryLimit()
	if limit <= 0 {
		return -1
	}
	if inputs >= uint64(limit) {
		return 0
	}
	return (limit - int64(inputs)) / 6
}

// keyLenFits reports whether a key of keyLen bytes, from inputs taking
// inputs bytes, fits Config.CryptoMemoryLimit.
func (c *Crypto) keyLenFits(keyLen int64, inputs uint64) bool {
	limit := c.rt.CryptoMemoryLimit()
	return limit <= 0 || keyOutputCost(keyLen)+inputs <= uint64(limit)
}

// scryptMaxmem is the memory limit an scrypt call's workspace is checked
// against: the script's maxmem, lowered when Config.CryptoMemoryLimit is set
// to what the limit leaves after the derived key's output and the inputs.
func (c *Crypto) scryptMaxmem(maxmem float64, keyLen int64, inputs uint64) uint64 {
	m := uint64(maxmem)
	if limit := c.rt.CryptoMemoryLimit(); limit > 0 {
		left := uint64(0)
		if cost := keyOutputCost(keyLen) + inputs; cost < uint64(limit) {
			left = uint64(limit) - cost
		}
		if left < m {
			m = left
		}
		// scryptCheck reads 0 as OpenSSL's default; nothing is left here.
		if m == 0 {
			m = 1
		}
	}
	return m
}

// kdfMaxKeyLenFunc backs _kdfMaxKeyLen(inputB64Length): kdfMaxKeyLen for
// inputs whose base64 strings total inputB64Length characters.
func (c *Crypto) kdfMaxKeyLenFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	var inputs uint64
	if args := info.Args(); len(args) > 0 && args[0].Integer() > 0 {
		inputs = inputCost(args[0].Integer())
	}
	return info.Context().NewNumber(float64(c.kdfMaxKeyLen(inputs)))
}

// scryptCheckFunc backs _scryptCheck(N, r, p, maxmem, keylen, inputB64Length): "" when OpenSSL would
// accept the parameters, else the reason it gives, which may be empty, after
// a "!" so an empty reason still reads as a failure.
func (c *Crypto) scryptCheckFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("scrypt: missing arguments")
	}
	out := ""
	var keyLen int64
	var inputs uint64
	if len(args) > 4 {
		keyLen = args[4].Integer()
	}
	if len(args) > 5 && args[5].Integer() > 0 {
		inputs = inputCost(args[5].Integer())
	}
	if err := scryptCheck(uint64(args[0].Number()), uint64(args[1].Number()), uint64(args[2].Number()), c.scryptMaxmem(args[3].Number(), keyLen, inputs)); err != nil {
		out = "!" + err.Error()
	}
	v, _ := ctx.NewString(out)
	return v
}

// scryptFunc backs _scrypt(passwordB64, saltB64, N, r, p, maxmem, keylen[,
// callback]). The parameters are checked again here against maxmem.
func (c *Crypto) scryptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 7 {
		return ctx.Throw("scrypt: missing arguments")
	}
	n, r, p, maxmem := args[2].Number(), args[3].Number(), args[4].Number(), args[5].Number()
	inputs, ok := c.inputsCost(args, 0, 1)
	keyLen := args[6].Integer()
	if keyLen < 0 || keyLen > maxNativeBytes {
		return ctx.Throw(outOfRange("keylen", 0, maxNativeBytes, keyLen))
	}
	if n < 0 || r < 0 || p < 0 || maxmem < 1 || maxmem > 1<<53 ||
		keyLen < 0 || keyLen > math.MaxInt32 || scryptCheck(uint64(n), uint64(r), uint64(p), c.scryptMaxmem(maxmem, keyLen, inputs)) != nil {
		return ctx.Throw("scrypt: invalid parameters")
	}
	if !ok {
		return ctx.Throw("scrypt: invalid parameters")
	}
	password, salt := argBytes(args, 0), argBytes(args, 1)
	return c.deriveKey(ctx, argCallback(args, 7), func() ([]byte, error) {
		if keyLen == 0 {
			return []byte{}, nil
		}
		return scrypt.Key(password, salt, int(n), int(r), int(p), int(keyLen))
	})
}

// hkdfFunc backs _hkdf(digest, ikmB64, saltB64, infoB64, keylen[, callback]).
func (c *Crypto) hkdfFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 5 {
		return ctx.Throw("hkdf: missing arguments")
	}
	digest := args[0].String()
	h := nodeDigest(digest)
	keyLen := int(args[4].Integer())
	inputs, ok := c.inputsCost(args, 1, 2, 3)
	if !ok || h == nil || keyLen < 0 || keyLen > 255*h().Size() || !c.keyLenFits(int64(keyLen), inputs) {
		return ctx.Throw("hkdf: invalid arguments")
	}
	ikm, salt, infoBytes := argBytes(args, 1), argBytes(args, 2), argBytes(args, 3)
	xof := isXOF(digest)
	return c.deriveKey(ctx, argCallback(args, 5), func() ([]byte, error) {
		if xof {
			return nil, errDerivingBits
		}
		if keyLen == 0 {
			return []byte{}, nil
		}
		return hkdf.Key(func() hash.Hash { return h() }, ikm, salt, string(infoBytes), keyLen)
	})
}
