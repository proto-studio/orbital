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
	password, salt := argBytes(args, 0), argBytes(args, 1)
	iter, keyLen := args[2].Integer(), args[3].Integer()
	h := nodeDigest(args[4].String())
	// kdf.js checks these, but scripts can reach the native directly.
	if keyLen < 0 || keyLen > maxNativeBytes {
		return ctx.Throw(outOfRange("keylen", 0, maxNativeBytes, keyLen))
	}
	if h == nil || iter < 1 || iter > math.MaxInt32 {
		return ctx.Throw("pbkdf2: invalid arguments")
	}
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

// scryptCheckFunc backs _scryptCheck(N, r, p, maxmem): "" when OpenSSL would
// accept the parameters, else the reason it gives, which may be empty, after
// a "!" so an empty reason still reads as a failure.
func (c *Crypto) scryptCheckFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("scrypt: missing arguments")
	}
	out := ""
	if err := scryptCheck(uint64(args[0].Number()), uint64(args[1].Number()), uint64(args[2].Number()), uint64(args[3].Number())); err != nil {
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
	password, salt := argBytes(args, 0), argBytes(args, 1)
	n, r, p, maxmem := args[2].Number(), args[3].Number(), args[4].Number(), args[5].Number()
	keyLen := args[6].Integer()
	if keyLen < 0 || keyLen > maxNativeBytes {
		return ctx.Throw(outOfRange("keylen", 0, maxNativeBytes, keyLen))
	}
	if n < 0 || r < 0 || p < 0 || maxmem < 1 || maxmem > 1<<53 ||
		scryptCheck(uint64(n), uint64(r), uint64(p), uint64(maxmem)) != nil || keyLen < 0 || keyLen > math.MaxInt32 {
		return ctx.Throw("scrypt: invalid parameters")
	}
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
	h := nodeDigest(args[0].String())
	ikm, salt, infoBytes := argBytes(args, 1), argBytes(args, 2), argBytes(args, 3)
	keyLen := int(args[4].Integer())
	if h == nil || keyLen < 0 || keyLen > 255*h().Size() {
		return ctx.Throw("hkdf: invalid arguments")
	}
	xof := isXOF(args[0].String())
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
