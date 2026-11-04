package crypto

import (
	"testing"
)

// binaryParityCases pairs a JS expression with the result real Node produces for
// it. Every expected value was captured by evaluating the expression on Node
// v20.18.1; do not edit them by hand. Results are normalized the same way on both
// sides: strings as JSON (so control bytes survive being read back), other values
// via String(), and thrown errors as "name code".
var binaryParityCases = []struct {
	expr string
	want string
}{
	// leading NUL
	{"crypto.createHash('sha256').update(Buffer.from([0x00])).digest('hex')", "\"6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"950cba8026f8ccdab454c8295d20c1821d5798750e859ca46f9c3d3b62a2c848\""},
	{"crypto.createHash('md5').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"d5aafaf745d53f3699f1315114fd6730\""},
	{"crypto.createHash('sha1').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"05a7629ddf7bc3a08aca287a27e5081c981fcc5b\""},
	{"crypto.createHash('sha384').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"bdf1d1c6940d4996c92300b45e958245cb0a1e97dcaf4417a0419e6cc68949df535d921e07ec37432a775e6ad12f2507\""},
	{"crypto.createHash('sha512').update(Buffer.from([0x00, 0x41])).digest('hex')", "\"64784a444ac9ace3e90922d53cc3cfd9d0b14e8caa36a81fd51337bacb183e29e48f383909c5891bf7c9e44fcbdbfe6991e1cf4f0d46101d2b40725e1a30c254\""},
	// embedded and trailing NUL
	{"crypto.createHash('sha256').update(Buffer.from([0x41, 0x00, 0x42])).digest('hex')", "\"76fe3925c7167317f2df68454339f5ec3650e4062178b4f2be219b105a507907\""},
	{"crypto.createHash('sha256').update(Buffer.from([0x41, 0x00])).digest('hex')", "\"e61c21ca716b3b1aefb7d1198f83679c4ca4d596e5792275dd6203b49216237d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0x80, 0x00, 0xff])).digest('hex')", "\"f192456d81c614b7586a985db70856924a5e0bbfba3838ebfa3198ceab2e70aa\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.from([0x41, 0x00, 0x42])).digest('hex')", "\"1a4df418a9eed0f9d4cae951ca9cc140cdcde390a5ca09a54002a06dc2309efa\""},
	{"crypto.createHash('sha256').update(Buffer.alloc(32)).digest('hex')", "\"66687aadf862bd776c8fc18b8e9f8e20089714856ee233b3902a591d0d5f2925\""},
	// no NUL
	{"crypto.createHash('sha256').update(Buffer.alloc(0)).digest('hex')", "\"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\""},
	{"crypto.createHash('sha256').update(Buffer.from([0xff])).digest('hex')", "\"a8100ae6aa1940d0b663bb31cd466142ebbdbd5187131b92d93818987832eb89\""},
	{"crypto.createHash('sha256').update(Buffer.from([0x41, 0xff, 0x42])).digest('hex')", "\"0f618ee4d2a04e92342e16bfecfcc6e4029a52b2993ceb8bb092650795ce7810\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.from([0x41, 0xff, 0x42])).digest('hex')", "\"390d371d9e47e7fb1b64a7463093924bb236a7a1a8a2ead5d5f8c181e4e7f225\""},
	// every byte value and large input
	{"crypto.createHash('sha256').update(Buffer.from(Array.from({length: 256}, (_, i) => i))).digest('hex')", "\"40aff2e9d2d8922e47afd4648e6967497158785fbd1da870e7110266bf944880\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.from(Array.from({length: 256}, (_, i) => i))).digest('hex')", "\"6ad0a89813f79e827359742225b46dc811d35e920192cfdf60f4955f14a93680\""},
	{"crypto.createHash('sha256').update(Buffer.alloc(100000, 0x00)).digest('hex')", "\"9192c25b734fcbadbe32dadc28089c60db0e39f90cc20ce2e5733f57261acc0c\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.alloc(100000, 0x00)).digest('hex')", "\"c3560ed44057af073fee41457325f00d0d8642da92a82535694f325798766476\""},
	// repeated update
	{"crypto.createHash('sha256').update(Buffer.from([0x00])).update(Buffer.from([0x41])).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0x41])).update(Buffer.from([0x00])).update(Buffer.from([0x42])).digest('hex')", "\"76fe3925c7167317f2df68454339f5ec3650e4062178b4f2be219b105a507907\""},
	{"crypto.createHash('sha256').update('a').update(Buffer.from([0x00, 0xff])).update('é').digest('hex')", "\"1180bd4ef836437f81a9e4e97b72a5787bfe7f1407fef52818668d0d40d1cc39\""},
	{"crypto.createHmac('sha256', 'key').update(Buffer.from([0x41])).update(Buffer.from([0x00])).update(Buffer.from([0x42])).digest('hex')", "\"1a4df418a9eed0f9d4cae951ca9cc140cdcde390a5ca09a54002a06dc2309efa\""},
	{"(() => { const h = crypto.createHash('sha256').update(Buffer.from([0x00, 0x01])); const c = h.copy().update(Buffer.from([0x02])); return h.digest('hex') + ' ' + c.digest('hex'); })()", "\"b413f47d13ee2fe6c845b2ee141af81de858df4ec549a58b7970bb96645bc8d2 ae4b3280e56e2faf83f414a6e3dabe9d5fbe18976544c05fed121accb85b53fc\""},
	{"crypto.createHash('sha256').digest('hex')", "\"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\""},
	// string input
	{"crypto.createHash('sha256').update('a\\0b').digest('hex')", "\"59b271ae1bbcb1d31d41929817f4b16fb439eb4f31520b5ad1d5ce98920a7138\""},
	{"crypto.createHash('sha256').update('\\0').digest('hex')", "\"6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d\""},
	{"crypto.createHash('sha256').update('0041', 'hex').digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update('AEE=', 'base64').digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update('\\u0000\\u00ff', 'latin1').digest('hex')", "\"06eb7d6a69ee19e5fbdf749018d3d2abfa04bcbd1365db312eb86dc7169389b8\""},
	{"crypto.createHash('sha256').update('ab', 'utf16le').digest('hex')", "\"8fb20ef63ced4145fc2e983ffe597d1dcff39154c3bf21f0fa9dde6a0c50fdc9\""},
	{"crypto.createHash('sha256').update('héllo→😀').digest('hex')", "\"38ca742227cf16af4e224c497af64930de9e0d22c4ce3be6d55d53a3f988778b\""},
	// hmac key
	{"crypto.createHmac('sha256', Buffer.from([0x00, 0x41])).update('msg').digest('hex')", "\"32bbc524622004fad8a688fda7c6c707e072e88f1ab383da6982729d01038f42\""},
	{"crypto.createHmac('sha256', Buffer.from([0x41, 0x00, 0x42])).update('msg').digest('hex')", "\"1cd82d6d17fe4335f06f2f64aa4b3234cb588520ea34042a85b06a4fc8b82ba0\""},
	{"crypto.createHmac('sha256', 'a\\0b').update('msg').digest('hex')", "\"d056a5d043fa6f38932b1af43d07a5609dc8da00065fb7b48facf1055acaef97\""},
	{"crypto.createHmac('sha256', 'kéy').update('msg').digest('hex')", "\"dfb31df97b5f271684bddd1ef8c3fee9898d7b4e4129c0e04c6f8f02a801a382\""},
	{"crypto.createHmac('sha256', Buffer.from(Array.from({length: 256}, (_, i) => i))).update('msg').digest('hex')", "\"bd197b003af605485a0d37857111370a383c31b0d6194f4d9553eff356da3063\""},
	{"crypto.createHmac('sha256', Buffer.alloc(100000, 0x00)).update('msg').digest('hex')", "\"f33579a635d792f251af173eebb302bf238aaad3865d6200f6238595f3e7caa7\""},
	{"crypto.createHmac('sha256', new Uint16Array([0x0100, 0xff00])).update('msg').digest('hex')", "\"27aa624b063091efdf80cb931c0de35f36913f4cd4bc18f7500646f832d66abd\""},
	{"crypto.createHmac('sha256', new DataView(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).update('msg').digest('hex')", "\"32bbc524622004fad8a688fda7c6c707e072e88f1ab383da6982729d01038f42\""},
	{"crypto.createHmac('sha256', new Uint8Array(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).update('msg').digest('hex')", "\"32bbc524622004fad8a688fda7c6c707e072e88f1ab383da6982729d01038f42\""},
	{"crypto.createHmac('sha256', new Uint8Array([0, 1]).buffer).update('msg').digest('hex')", "\"9355f12eb0fc4600d188edc1045d20051349732e4ca6cdcb5e07ba583539fb3b\""},
	// views
	{"crypto.createHash('sha256').update(new Uint8Array([0, 0x41])).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(Buffer.from([9, 0, 0x41, 9]).subarray(1, 3)).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(new Uint8Array(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(new Uint16Array([0x0100, 0xff00])).digest('hex')", "\"35405bce5dc7cacedd9c4373e68d01b369da4b5da34ecf90d0db265416797f71\""},
	{"crypto.createHash('sha256').update(new Int8Array([0, -1])).digest('hex')", "\"06eb7d6a69ee19e5fbdf749018d3d2abfa04bcbd1365db312eb86dc7169389b8\""},
	{"crypto.createHash('sha256').update(new Float64Array([1.5])).digest('hex')", "\"e163f8cb0f7067a7fc78ca859a77f849aea3214f38fb75b884e4a16be725c905\""},
	{"crypto.createHash('sha256').update(new DataView(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).digest('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHmac('sha256', 'key').update(new Uint16Array([0x0100, 0xff00])).digest('hex')", "\"b853642a5de60aebeb1e4a00534da36352bc3f14663f4688e8cbb21024f9b07c\""},
	{"crypto.createHmac('sha256', 'key').update(new DataView(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).digest('hex')", "\"950cba8026f8ccdab454c8295d20c1821d5798750e859ca46f9c3d3b62a2c848\""},
	{"(() => { const b = Buffer.from([0, 1]); const h = crypto.createHash('sha256').update(b); b[1] = 2; return h.digest('hex'); })()", "\"b413f47d13ee2fe6c845b2ee141af81de858df4ec549a58b7970bb96645bc8d2\""},
	// invalid update input
	{"crypto.createHash('sha256').update(5)", "TypeError ERR_INVALID_ARG_TYPE"},
	{"crypto.createHash('sha256').update(new ArrayBuffer(2))", "TypeError ERR_INVALID_ARG_TYPE"},
	{"crypto.createHash('sha256').update(undefined)", "TypeError ERR_INVALID_ARG_TYPE"},
	{"crypto.createHmac('sha256', 'k').update(5)", "TypeError ERR_INVALID_ARG_TYPE"},
	// digest encoding
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest().toString('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('buffer').toString('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('bogus').toString('hex')", "\"c00b4d3c929cb5cc316691ed4636f634576f2c9b2954767234c5274e9dde185d\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('base64')", "\"wAtNPJKctcwxZpHtRjb2NFdvLJspVHZyNMUnTp3eGF0=\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('base64url')", "\"wAtNPJKctcwxZpHtRjb2NFdvLJspVHZyNMUnTp3eGF0\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('latin1')", "\"À\\u000bM<µÌ1fíF6ö4Wo,)Tvr4Å'NÞ\\u0018]\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('binary')", "\"À\\u000bM<µÌ1fíF6ö4Wo,)Tvr4Å'NÞ\\u0018]\""},
	{"crypto.createHash('sha256').update(Buffer.from([0, 0x41])).digest('ucs2')", "\"ீ㱍鲒첵昱㙆㓶潗鬬吩牶씴丧\\ude9d崘\""},
	{"crypto.createHmac('sha256', 'k').update(Buffer.from([0, 0x41])).digest('base64')", "\"pbw+bujpPVjvIcbO8fuqoiK8SwXfV5p9YdmmZe9zHP0=\""},
	{"crypto.createHmac('sha256', 'k').update(Buffer.from([0, 0x41])).digest('base64url')", "\"pbw-bujpPVjvIcbO8fuqoiK8SwXfV5p9YdmmZe9zHP0\""},
	{"crypto.createHmac('sha256', 'k').update(Buffer.from([0, 0x41])).digest('latin1')", "\"¥¼>nèé=Xï!ÆÎñûª¢\\\"¼K\\u0005ßW}aÙ¦eïs\\u001cý\""},
	// timingSafeEqual
	{"crypto.timingSafeEqual(Buffer.from([0, 1]), Buffer.from([0, 1]))", "true"},
	{"crypto.timingSafeEqual(Buffer.from([0, 0xff]), Buffer.from([0, 0xfe]))", "false"},
	{"crypto.timingSafeEqual(new Uint8Array([1, 2]), new Uint8Array([1, 2]))", "true"},
	{"crypto.timingSafeEqual(new Uint8Array([1, 2]), new Uint8Array([3, 4]))", "false"},
	{"crypto.timingSafeEqual(new Uint16Array([1]), new Uint16Array([2]))", "false"},
	{"crypto.timingSafeEqual(new DataView(new ArrayBuffer(1)), new DataView(new Uint8Array([1]).buffer))", "false"},
	{"crypto.timingSafeEqual(new Uint8Array([1]).buffer, new Uint8Array([2]).buffer)", "false"},
	{"crypto.timingSafeEqual(new Uint8Array(new Uint8Array([9, 1, 9]).buffer, 1, 1), new Uint8Array(new Uint8Array([9, 2, 9]).buffer, 1, 1))", "false"},
	{"crypto.timingSafeEqual(new Uint16Array([0x0201]), new Uint8Array([1, 2]))", "true"},
	{"crypto.timingSafeEqual(Buffer.alloc(0), Buffer.alloc(0))", "true"},
	{"crypto.timingSafeEqual(Buffer.from([1]), Buffer.from([1, 2]))", "RangeError ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH"},
	{"crypto.timingSafeEqual('a\\0b', 'a\\0c')", "TypeError ERR_INVALID_ARG_TYPE"},
	{"crypto.timingSafeEqual(undefined, undefined)", "TypeError ERR_INVALID_ARG_TYPE"},
	// createSecretKey
	{"crypto.createSecretKey(Buffer.from([0, 0xff, 0])).export().toString('hex')", "\"00ff00\""},
	{"crypto.createSecretKey(new Uint16Array([0x0100])).export().toString('hex')", "\"0001\""},
	{"crypto.createSecretKey(new DataView(new Uint8Array([9, 0, 0x41, 9]).buffer, 1, 2)).export().toString('hex')", "\"0041\""},
	{"crypto.createSecretKey(Buffer.from([0, 1])).equals(crypto.createSecretKey(Buffer.from([0, 2])))", "false"},
}

func TestCrypto_BinaryInputMatchesNode(t *testing.T) {
	rt := setupRuntime(t)
	defer rt.Dispose()

	for _, tc := range binaryParityCases {
		t.Run(tc.expr, func(t *testing.T) {
			script := `(function (crypto) {
				try {
					const r = (` + tc.expr + `);
					return typeof r === 'string' ? JSON.stringify(r) : String(r);
				} catch (e) {
					return e.name + ' ' + e.code;
				}
			})(__crypto_module)`
			result, err := rt.RunScript(script, "test.js")
			if err != nil {
				t.Fatalf("RunScript failed: %v", err)
			}
			if got := result.String(); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
