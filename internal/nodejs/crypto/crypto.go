// Package crypto implements the Node.js crypto module.
package crypto

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"

	"github.com/google/uuid"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// Crypto provides cryptographic functionality.
type Crypto struct {
	rt *runtime.Runtime
}

// New creates a new Crypto module.
func New() *Crypto {
	return &Crypto{}
}

// Name returns the module name.
func (c *Crypto) Name() string {
	return "crypto"
}

// Register sets up the crypto module.
func (c *Crypto) Register(rt *runtime.Runtime) error {
	c.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	// Create crypto object
	cryptoObj, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"randomBytes":     c.randomBytesFunc,
		"randomUUID":      c.randomUUIDFunc,
		"randomInt":       c.randomIntFunc,
		"createHash":      c.createHashFunc,
		"createHmac":      c.createHmacFunc,
		"getHashes":       c.getHashesFunc,
		"timingSafeEqual": c.timingSafeEqualFunc,
	}

	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := cryptoObj.Set(name, val); err != nil {
			return err
		}
	}

	// Set as global
	if err := rt.SetGlobal("__crypto_module", cryptoObj); err != nil {
		return err
	}

	// Set up Hash and Hmac classes via JS
	jsSetup := `
(function() {
	const crypto = __crypto_module;

	// Hash class
	class Hash {
		constructor(algorithm) {
			this._algorithm = algorithm;
			this._data = [];
		}

		update(data, inputEncoding) {
			if (typeof data === 'string') {
				this._data.push(data);
			} else if (data instanceof Uint8Array || data instanceof Buffer) {
				this._data.push(Array.from(data).map(b => String.fromCharCode(b)).join(''));
			}
			return this;
		}

		digest(encoding) {
			const result = crypto._hashDigest(this._algorithm, this._data.join(''));
			if (!encoding || encoding === 'buffer') {
				return Buffer.from(result, 'hex');
			}
			if (encoding === 'hex') {
				return result;
			}
			if (encoding === 'base64') {
				return Buffer.from(result, 'hex').toString('base64');
			}
			return result;
		}

		copy() {
			const h = new Hash(this._algorithm);
			h._data = this._data.slice();
			return h;
		}
	}

	// Hmac class
	class Hmac {
		constructor(algorithm, key) {
			this._algorithm = algorithm;
			this._key = typeof key === 'string' ? key : Array.from(key).map(b => String.fromCharCode(b)).join('');
			this._data = [];
		}

		update(data, inputEncoding) {
			if (typeof data === 'string') {
				this._data.push(data);
			} else if (data instanceof Uint8Array || data instanceof Buffer) {
				this._data.push(Array.from(data).map(b => String.fromCharCode(b)).join(''));
			}
			return this;
		}

		digest(encoding) {
			const result = crypto._hmacDigest(this._algorithm, this._key, this._data.join(''));
			if (!encoding || encoding === 'buffer') {
				return Buffer.from(result, 'hex');
			}
			if (encoding === 'hex') {
				return result;
			}
			if (encoding === 'base64') {
				return Buffer.from(result, 'hex').toString('base64');
			}
			return result;
		}
	}

	// Wrap createHash to return Hash instance
	const originalCreateHash = crypto.createHash;
	crypto.createHash = function(algorithm) {
		return new Hash(algorithm);
	};

	// Wrap createHmac to return Hmac instance
	const originalCreateHmac = crypto.createHmac;
	crypto.createHmac = function(algorithm, key) {
		return new Hmac(algorithm, key);
	};

	crypto.Hash = Hash;
	crypto.Hmac = Hmac;

	// Constants
	crypto.constants = {
		SSL_OP_ALL: 0x80000BFF,
		SSL_OP_NO_SSLv2: 0x01000000,
		SSL_OP_NO_SSLv3: 0x02000000,
		SSL_OP_NO_TLSv1: 0x04000000,
		SSL_OP_NO_TLSv1_1: 0x10000000,
		SSL_OP_NO_TLSv1_2: 0x08000000,
		SSL_OP_NO_TLSv1_3: 0x20000000
	};

	globalThis.__crypto_module = crypto;
})();
`
	_, err = rt.RunScript(jsSetup, "crypto_setup.js")
	if err != nil {
		return err
	}

	// Add internal functions for Hash/Hmac digest
	hashDigestFn, err := iso.NewFunctionTemplate(c.hashDigestFunc)
	if err != nil {
		return err
	}
	hashDigestVal, err := hashDigestFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	cryptoObj.Set("_hashDigest", hashDigestVal)

	hmacDigestFn, err := iso.NewFunctionTemplate(c.hmacDigestFunc)
	if err != nil {
		return err
	}
	hmacDigestVal, err := hmacDigestFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	cryptoObj.Set("_hmacDigest", hmacDigestVal)

	return nil
}

func (c *Crypto) randomBytesFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 1 {
		return nil
	}

	size := int(args[0].Integer())
	if size < 0 || size > 2147483647 {
		return nil
	}

	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return nil
	}

	// Check for callback (async mode)
	if len(args) >= 2 && args[1].IsFunction() {
		callback := args[1]
		// For simplicity, execute synchronously but call callback
		c.rt.EventLoop().EnqueueMicrotask(func() {
			// Create Buffer from bytes
			hexStr := hex.EncodeToString(bytes)
			code := `Buffer.from('` + hexStr + `', 'hex')`
			bufVal, _ := c.rt.RunScript(code, "crypto_buffer")
			callback.Call(nil, ctx.Null(), bufVal)
		})
		return ctx.Undefined()
	}

	// Sync mode - return Buffer
	hexStr := hex.EncodeToString(bytes)
	code := `Buffer.from('` + hexStr + `', 'hex')`
	bufVal, _ := c.rt.RunScript(code, "crypto_buffer")
	return bufVal
}

func (c *Crypto) randomUUIDFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	id := uuid.New().String()
	val, _ := ctx.NewString(id)
	return val
}

func (c *Crypto) randomIntFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	var min, max int64
	if len(args) == 1 {
		min = 0
		max = args[0].Integer()
	} else if len(args) >= 2 {
		min = args[0].Integer()
		max = args[1].Integer()
	} else {
		return nil
	}

	if min >= max {
		return nil
	}

	// Generate random number in range [min, max)
	rangeSize := max - min
	var randomBytes [8]byte
	rand.Read(randomBytes[:])

	// Convert to uint64 and scale to range
	var n uint64
	for i := 0; i < 8; i++ {
		n = (n << 8) | uint64(randomBytes[i])
	}

	result := min + int64(n%uint64(rangeSize))
	return ctx.NewNumber(float64(result))
}

func (c *Crypto) createHashFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is replaced by JS wrapper, but kept for reference
	return nil
}

func (c *Crypto) createHmacFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is replaced by JS wrapper, but kept for reference
	return nil
}

func (c *Crypto) getHashesFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	hashes := []string{"md5", "sha1", "sha256", "sha384", "sha512"}

	arr, _ := ctx.NewArray(len(hashes))
	for i, h := range hashes {
		val, _ := ctx.NewString(h)
		arr.SetIndex(i, val)
	}
	return arr
}

func (c *Crypto) hashDigestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 2 {
		return nil
	}

	algorithm := args[0].String()
	data := args[1].String()

	var h hash.Hash
	switch algorithm {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	case "sha384":
		h = sha512.New384()
	case "sha512":
		h = sha512.New()
	default:
		return nil
	}

	h.Write([]byte(data))
	result := hex.EncodeToString(h.Sum(nil))

	val, _ := ctx.NewString(result)
	return val
}

func (c *Crypto) hmacDigestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 3 {
		return nil
	}

	algorithm := args[0].String()
	key := args[1].String()
	data := args[2].String()

	var h func() hash.Hash
	switch algorithm {
	case "md5":
		h = md5.New
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	case "sha384":
		h = sha512.New384
	case "sha512":
		h = sha512.New
	default:
		return nil
	}

	mac := hmac.New(h, []byte(key))
	mac.Write([]byte(data))
	result := hex.EncodeToString(mac.Sum(nil))

	val, _ := ctx.NewString(result)
	return val
}

func (c *Crypto) timingSafeEqualFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 2 {
		return ctx.False()
	}

	// Get data from both arguments
	var a, b []byte

	if args[0].IsString() {
		a = []byte(args[0].String())
	} else {
		// Assume Buffer/Uint8Array - get as hex string
		hexA, _ := args[0].Get("toString")
		if hexA != nil && hexA.IsFunction() {
			hexArg, _ := ctx.NewString("hex")
			hexVal, _ := hexA.Call(args[0], hexArg)
			if hexVal != nil {
				a, _ = hex.DecodeString(hexVal.String())
			}
		}
	}

	if args[1].IsString() {
		b = []byte(args[1].String())
	} else {
		hexB, _ := args[1].Get("toString")
		if hexB != nil && hexB.IsFunction() {
			hexArg, _ := ctx.NewString("hex")
			hexVal, _ := hexB.Call(args[1], hexArg)
			if hexVal != nil {
				b, _ = hex.DecodeString(hexVal.String())
			}
		}
	}

	if len(a) != len(b) {
		return ctx.False()
	}

	// Constant-time comparison
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}

	if diff == 0 {
		return ctx.True()
	}
	return ctx.False()
}
