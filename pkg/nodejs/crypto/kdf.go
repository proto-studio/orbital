package crypto

import (
	"crypto/hkdf"
	"crypto/pbkdf2"
	_ "embed"
	"encoding/base64"
	"hash"

	"proto.zip/studio/orbital/pkg/v8"
)

// Native halves of pbkdf2, scrypt and hkdf. kdf.js checks the arguments as
// Node does and passes bytes as base64; these derive the key, on the JS
// thread for the Sync forms and on a goroutine when given a callback.

//go:embed kdf.js
var kdfJS string

// deriveKey runs work now, returning the key as base64, or, when callback is
// a function, on a goroutine, calling callback(undefined, base64) or
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
	go func() {
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
	}()
	return ctx.Undefined()
}

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
	iter, keyLen := int(args[2].Integer()), int(args[3].Integer())
	h := nodeDigest(args[4].String())
	if h == nil || iter < 1 || keyLen < 0 {
		return ctx.Throw("pbkdf2: invalid arguments")
	}
	return c.deriveKey(ctx, argCallback(args, 5), func() ([]byte, error) {
		if keyLen == 0 {
			return []byte{}, nil
		}
		return pbkdf2.Key(h, string(password), salt, iter, keyLen)
	})
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

// scryptFunc backs _scrypt(passwordB64, saltB64, N, r, p, keylen[, callback]).
// kdf.js has run the parameters through _scryptCheck.
func (c *Crypto) scryptFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 6 {
		return ctx.Throw("scrypt: missing arguments")
	}
	password, salt := argBytes(args, 0), argBytes(args, 1)
	n, r, p, keyLen := args[2].Number(), args[3].Number(), args[4].Number(), int(args[5].Integer())
	if scryptCheck(uint64(n), uint64(r), uint64(p), 1<<62) != nil || keyLen < 0 {
		return ctx.Throw("scrypt: invalid parameters")
	}
	return c.deriveKey(ctx, argCallback(args, 6), func() ([]byte, error) {
		return scryptKey(password, salt, int(n), int(r), int(p), keyLen)
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
	return c.deriveKey(ctx, argCallback(args, 5), func() ([]byte, error) {
		if keyLen == 0 {
			return []byte{}, nil
		}
		return hkdf.Key(func() hash.Hash { return h() }, ikm, salt, string(infoBytes), keyLen)
	})
}
