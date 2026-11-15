// Package crypto implements the Node.js crypto module.
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"sync"

	"github.com/google/uuid"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

// Crypto provides cryptographic functionality.
type Crypto struct {
	rt *runtime.Runtime

	// keys is this runtime's KeyObject registry: JS KeyObject instances hold an
	// opaque integer handle into this table rather than the key material itself.
	// It is per-Crypto-instance (one per runtime), so separate sandboxes/workers
	// never share key state. Guarded by keyMu; handles are monotonic.
	keyMu  sync.Mutex
	keys   map[int64]*keyEntry
	keySeq int64

	// cipherCtxs holds the contexts behind Cipheriv and Decipheriv objects,
	// by handle, like keys.
	cipherMu   sync.Mutex
	cipherCtxs map[int64]*cipherCtx
	cipherSeq  int64
}

// New creates a new Crypto module.
func New() *Crypto {
	return &Crypto{keys: make(map[int64]*keyEntry)}
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
		"randomBytes":      c.randomBytesFunc,
		"randomUUID":       c.randomUUIDFunc,
		"_randomInt":       c.randomIntFunc,
		"createHash":       c.createHashFunc,
		"createHmac":       c.createHmacFunc,
		"getHashes":        c.getHashesFunc,
		"getCurves":        c.getCurvesFunc,
		"getCiphers":       c.getCiphersFunc,
		"_x509Parse":       c.x509ParseFunc,
		"_x509Check":       c.x509CheckFunc,
		"_timingSafeEqual": c.timingSafeEqualFunc,
		// KeyObject API (native Go; see keyobject.go). These low-level helpers
		// take/return strings + integer handles and are wrapped by the JS
		// KeyObject class installed in keyObjectJS below.
		"_generateKeyPair":     c.generateKeyPairFunc,
		"_createSecretKey":     c.createSecretKeyFunc,
		"_createPrivateKeyJWK": c.createPrivateKeyJWKFunc,
		"_createPublicKeyJWK":  c.createPublicKeyJWKFunc,
		"_createKeyFromPEM":    c.createKeyFromPEMFunc,
		"_createKeyFromDER":    c.createKeyFromDERFunc,
		"_keyInfo":             c.keyInfoFunc,
		"_keyExportJWK":        c.keyExportJWKFunc,
		"_keyExportPEM":        c.keyExportPEMFunc,
		"_keyExportDER":        c.keyExportDERFunc,
		"_keyExportSecret":     c.keyExportSecretFunc,
		"_keyEquals":           c.keyEqualsFunc,

		// Callback forms whose work runs off the JS thread (async.go).
		"_generateKeyPairAsync": c.generateKeyPairAsyncFunc,
		"_randomBytesAsync":     c.randomBytesAsyncFunc,

		// Signatures (sign.go).
		"_sign":   c.signFunc,
		"_verify": c.verifyFunc,

		// Key derivation (kdf.go).
		"_digestSize":  c.digestSizeFunc,
		"_pbkdf2":      c.pbkdf2Func,
		"_scryptCheck": c.scryptCheckFunc,
		"_scrypt":      c.scryptFunc,
		"_hkdf":        c.hkdfFunc,

		// Ciphers (cipher.go).
		"_cipherInit":           c.cipherInitFunc,
		"_cipherUpdate":         c.cipherUpdateFunc,
		"_cipherFinal":          c.cipherFinalFunc,
		"_cipherSetAutoPadding": c.cipherSetAutoPaddingFunc,
		"_cipherSetAAD":         c.cipherSetAADFunc,
		"_cipherGetAuthTag":     c.cipherGetAuthTagFunc,
		"_cipherSetAuthTag":     c.cipherSetAuthTagFunc,
		"_cipherFree":           c.cipherFreeFunc,
		"_cipherInfo":           c.cipherInfoFunc,
		"_cipherCount":          c.cipherCountFunc,
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

	// Bytes cross into Go as base64. A latin1 "wire-string" cannot carry them:
	// Value.String() reads a C string, so it stops at the first NUL byte.

	// viewBytes copies the bytes a TypedArray or DataView actually spans.
	// Buffer.from(view) is not equivalent: it narrows each element of a wider
	// TypedArray to one byte and reads a DataView as zeros. The copy also means
	// later changes to the caller's buffer cannot alter the digest.
	function viewBytes(view) {
		return Buffer.from(new Uint8Array(view.buffer, view.byteOffset, view.byteLength));
	}

	function dataBytes(data, inputEncoding) {
		if (typeof data === 'string') {
			return Buffer.from(data, inputEncoding || 'utf8');
		}
		if (ArrayBuffer.isView(data)) {
			return viewBytes(data);
		}
		const err = new TypeError('The "data" argument must be of type string or an instance of Buffer, TypedArray, or DataView.');
		err.code = 'ERR_INVALID_ARG_TYPE';
		throw err;
	}

	function keyBytes(key) {
		if (typeof key === 'string') {
			return Buffer.from(key, 'utf8');
		}
		if (ArrayBuffer.isView(key)) {
			return viewBytes(key);
		}
		// Buffer.from(arrayBuffer) shares its memory; copy so the key is fixed
		// when the Hmac is made, as in Node.
		if (key instanceof ArrayBuffer) {
			return Buffer.from(new Uint8Array(key));
		}
		return Buffer.from(key);
	}

	function encodeDigest(hexDigest, encoding) {
		const digest = Buffer.from(hexDigest, 'hex');
		return encoding && Buffer.isEncoding(encoding) ? digest.toString(encoding) : digest;
	}

	// Node's description of a rejected value, as in its ERR_INVALID_ARG_TYPE.
	function describeReceived(value) {
		if (value === null || value === undefined) return 'Received ' + value;
		if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
		if (typeof value === 'object') {
			if (value.constructor && value.constructor.name) {
				return 'Received an instance of ' + value.constructor.name;
			}
			return 'Received ' + String(value);
		}
		let shown;
		if (typeof value === 'string') {
			shown = "'" + (value.length > 28 ? value.slice(0, 25) + '...' : value) + "'";
		} else if (typeof value === 'bigint') {
			shown = String(value) + 'n';
		} else {
			shown = Object.is(value, -0) ? '-0' : String(value);
		}
		return 'Received type ' + typeof value + ' (' + shown + ')';
	}

	function checkDigestName(value, name) {
		if (typeof value !== 'string') {
			const err = new TypeError('The "' + name + '" argument must be of type string. ' + describeReceived(value));
			err.code = 'ERR_INVALID_ARG_TYPE';
			throw err;
		}
		return crypto._digestSize(value) > 0;
	}

	// SHAKE128 and SHAKE256 are extendable-output functions: createHash reads
	// them to options.outputLength, and HMAC refuses them as OpenSSL does.
	const isXOF = (name) => /^shake-?(128|256)$/i.test(name);

	function outputLengthOption(options) {
		const n = options !== null && typeof options === 'object' ? options.outputLength : undefined;
		if (n === undefined) return -1;
		if (typeof n !== 'number') {
			const err = new TypeError('The "options.outputLength" property must be of type number. ' + describeReceived(n));
			err.code = 'ERR_INVALID_ARG_TYPE';
			throw err;
		}
		if (!Number.isInteger(n) || n < 0 || n > 4294967295) {
			const err = new RangeError('The value of "options.outputLength" is out of range. It must be ' +
				(Number.isInteger(n) ? '>= 0 && <= 4294967295' : 'an integer') + '. Received ' + String(n));
			err.code = 'ERR_OUT_OF_RANGE';
			throw err;
		}
		return n;
	}

	// Hash class
	class Hash {
		constructor(algorithm, options) {
			if (typeof algorithm === 'string') outputLengthOption(options);
			if (!checkDigestName(algorithm, 'algorithm')) {
				throw new Error('Digest method not supported');
			}
			const outputLength = outputLengthOption(options);
			if (outputLength !== -1 && !isXOF(algorithm) && outputLength !== crypto._digestSize(algorithm)) {
				const err = new Error('error:030000B2:digital envelope routines::not XOF or invalid length');
				err.code = 'ERR_OSSL_EVP_NOT_XOF_OR_INVALID_LENGTH';
				throw err;
			}
			this._algorithm = algorithm;
			this._outputLength = isXOF(algorithm) ? outputLength : -1;
			this._data = [];
		}

		update(data, inputEncoding) {
			this._data.push(dataBytes(data, inputEncoding));
			return this;
		}

		digest(encoding) {
			const data = Buffer.concat(this._data).toString('base64');
			return encodeDigest(crypto._hashDigest(this._algorithm, data, this._outputLength), encoding);
		}

		copy(options) {
			const h = new Hash(this._algorithm, options);
			h._data = this._data.slice();
			return h;
		}
	}

	// Hmac class
	class Hmac {
		constructor(algorithm, key) {
			if (!checkDigestName(algorithm, 'hmac')) {
				const err = new TypeError('Invalid digest: ' + algorithm);
				err.code = 'ERR_CRYPTO_INVALID_DIGEST';
				throw err;
			}
			if (isXOF(algorithm)) throw new Error('error:00000000:lib(0)::reason(0)');
			this._algorithm = algorithm;
			this._key = keyBytes(key);
			this._data = [];
		}

		update(data, inputEncoding) {
			this._data.push(dataBytes(data, inputEncoding));
			return this;
		}

		digest(encoding) {
			const key = this._key.toString('base64');
			const data = Buffer.concat(this._data).toString('base64');
			return encodeDigest(crypto._hmacDigest(this._algorithm, key, data), encoding);
		}
	}

	function timingSafeBytes(value, name) {
		if (ArrayBuffer.isView(value)) {
			return viewBytes(value);
		}
		if (value instanceof ArrayBuffer) {
			return Buffer.from(value);
		}
		const err = new TypeError('The "' + name + '" argument must be an instance of ArrayBuffer, Buffer, TypedArray, or DataView.');
		err.code = 'ERR_INVALID_ARG_TYPE';
		throw err;
	}

	crypto.timingSafeEqual = function(a, b) {
		const bytesA = timingSafeBytes(a, 'buf1');
		const bytesB = timingSafeBytes(b, 'buf2');
		if (bytesA.length !== bytesB.length) {
			const err = new RangeError('Input buffers must have the same byte length');
			err.code = 'ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH';
			throw err;
		}
		return crypto._timingSafeEqual(bytesA.toString('base64'), bytesB.toString('base64'));
	};

	// Wrap createHash to return Hash instance
	const originalCreateHash = crypto.createHash;
	crypto.createHash = function(algorithm, options) {
		return new Hash(algorithm, options);
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

	// Install the KeyObject class + generateKeyPair/createSecretKey/... wrappers
	// on top of the native helpers registered above.
	if _, err = rt.RunScript(keyObjectJS, "crypto_keyobject.js"); err != nil {
		return err
	}

	// createCipheriv and createDecipheriv over the cipher natives.
	if _, err = rt.RunScript(cipherJS, "crypto_cipher.js"); err != nil {
		return err
	}

	// randomBytes argument checks and its off-thread callback form.
	if _, err = rt.RunScript(randomBytesJS, "crypto_randombytes.js"); err != nil {
		return err
	}

	// crypto.randomInt over the native _randomInt.
	if _, err = rt.RunScript(randomIntJS, "crypto_randomint.js"); err != nil {
		return err
	}

	// X509Certificate over _x509Parse and _x509Check.
	if _, err = rt.RunScript(x509JS, "crypto_x509.js"); err != nil {
		return err
	}

	// createSign, createVerify, sign and verify over _sign and _verify.
	if _, err = rt.RunScript(signJS, "crypto_sign.js"); err != nil {
		return err
	}

	// pbkdf2, scrypt and hkdf over the natives above.
	if _, err = rt.RunScript(kdfJS, "crypto_kdf.js"); err != nil {
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

	// The callback form is randomBytesAsyncFunc (async.go).
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

func (c *Crypto) createHashFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is replaced by JS wrapper, but kept for reference
	return nil
}

func (c *Crypto) createHmacFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	// This is replaced by JS wrapper, but kept for reference
	return nil
}

func (c *Crypto) getHashesFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	return stringArray(info.Context(), supportedHashes())
}

func (c *Crypto) hashDigestFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 2 {
		return nil
	}

	algorithm := args[0].String()
	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return ctx.Throw("crypto: invalid digest input: " + err.Error())
	}

	newHash := nodeDigest(algorithm)
	if newHash == nil {
		return ctx.Throw("crypto: unsupported digest " + algorithm)
	}
	h := newHash()

	h.Write(data)
	var sum []byte
	if x, ok := h.(*shake); ok && len(args) > 2 && args[2].Integer() >= 0 {
		// An extendable-output function read to options.outputLength.
		sum = make([]byte, args[2].Integer())
		x.s.Read(sum)
	} else {
		sum = h.Sum(nil)
	}
	result := hex.EncodeToString(sum)

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
	key, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return ctx.Throw("crypto: invalid hmac key: " + err.Error())
	}
	data, err := base64.StdEncoding.DecodeString(args[2].String())
	if err != nil {
		return ctx.Throw("crypto: invalid digest input: " + err.Error())
	}

	h := nodeDigest(algorithm)
	if h == nil {
		return ctx.Throw("crypto: unsupported digest " + algorithm)
	}

	mac := hmac.New(h, key)
	mac.Write(data)
	result := hex.EncodeToString(mac.Sum(nil))

	val, _ := ctx.NewString(result)
	return val
}

// timingSafeEqualFunc compares two base64-encoded byte strings. The JS wrapper
// validates the arguments, including that their lengths match.
func (c *Crypto) timingSafeEqualFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()

	if len(args) < 2 {
		return ctx.False()
	}

	a, err := base64.StdEncoding.DecodeString(args[0].String())
	if err != nil {
		return ctx.Throw("timingSafeEqual: " + err.Error())
	}
	b, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		return ctx.Throw("timingSafeEqual: " + err.Error())
	}

	if subtleConstEq(a, b) {
		return ctx.True()
	}
	return ctx.False()
}
