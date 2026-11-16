// Runs unchanged in Node and in Orbital. crypto_kdf.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoKDF_MatchesNode'
const crypto = require('crypto');
const results = {};

function show(value) {
  if (Buffer.isBuffer(value)) return 'Buffer ' + value.toString('hex');
  if (value instanceof ArrayBuffer) return 'ArrayBuffer ' + Buffer.from(value).toString('hex');
  return String(value);
}

function outcome(f) {
  try {
    return { value: show(f()) };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

const pbkdf2 = {
  sha256: ['pw', 'salt', 1, 32, 'sha256'],
  iterations2: ['pw', 'salt', 2, 20, 'sha1'],
  rfc6070: ['password', 'salt', 4096, 20, 'sha1'],
  upperDigest: ['pw', 'salt', 1, 32, 'SHA256'],
  rsaAlias: ['pw', 'salt', 1, 32, 'RSA-SHA256'],
  dashAlias: ['pw', 'salt', 1, 16, 'sha-512'],
  sha224: ['pw', 'salt', 1, 32, 'sha224'],
  sha512_256: ['pw', 'salt', 1, 32, 'sha512-256'],
  sha3_256: ['pw', 'salt', 1, 32, 'sha3-256'],
  md5: ['pw', 'salt', 1, 16, 'md5'],
  ripemd160: ['pw', 'salt', 2, 20, 'ripemd160'],
  rmd160Alias: ['pw', 'salt', 1, 20, 'RMD160'],
  blake2b512: ['pw', 'salt', 2, 70, 'blake2b512'],
  blake2s256: ['pw', 'salt', 2, 40, 'BLAKE2S-256'],
  md5Sha1: ['pw', 'salt', 2, 40, 'md5-sha1'],
  shake128: ['pw', 'salt', 1, 16, 'shake128'],
  shake256ZeroKeylen: ['pw', 'salt', 1, 0, 'SHAKE256'],
  zeroKeylen: ['pw', 'salt', 1, 0, 'sha256'],
  longKey: ['pw', 'salt', 3, 100, 'sha512'],
  nulBytes: [Buffer.from([0, 1, 0]), new Uint8Array([0, 0]), 2, 16, 'sha1'],
  otherViews: [new Uint16Array([1, 256]).buffer, new DataView(new ArrayBuffer(2)), 2, 16, 'sha256'],
  utf8: ['pässwörd', 'sälz', 1, 16, 'sha256'],
  zeroIterations: ['pw', 'salt', 0, 32, 'sha256'],
  fractionIterations: ['pw', 'salt', 1.5, 32, 'sha256'],
  stringIterations: ['pw', 'salt', '1', 32, 'sha256'],
  tooManyIterations: ['pw', 'salt', 2 ** 31, 32, 'sha256'],
  negativeKeylen: ['pw', 'salt', 1, -1, 'sha256'],
  unknownDigest: ['pw', 'salt', 1, 32, 'nope'],
  noDigest: ['pw', 'salt', 1, 32],
  numberDigest: ['pw', 'salt', 1, 32, 5],
  numberPassword: [5, 'salt', 1, 32, 'sha256'],
  nullSalt: ['pw', null, 1, 32, 'sha256'],
};
for (const [name, args] of Object.entries(pbkdf2)) results['pbkdf2Sync_' + name] = outcome(() => crypto.pbkdf2Sync(...args));
results.pbkdf2_noCallback = outcome(() => crypto.pbkdf2('pw', 'salt', 1, 32, 'sha256'));
results.pbkdf2_callbackAsDigest = outcome(() => crypto.pbkdf2('pw', 'salt', 1, 32, () => {}));
results.pbkdf2_unknownDigestThrows = outcome(() => crypto.pbkdf2('pw', 'salt', 1, 32, 'nope', () => {}));
// Before the scrypt cases below, which leave an OpenSSL error that Node would add here.
results.hkdfSync_shake128 = outcome(() => crypto.hkdfSync('shake128', 'key', 'salt', 'info', 16));

// Node leaves OpenSSL's last error queued, so after a memory-limit failure it adds that
// message to later parameter errors too. These run first so that doesn't show.
results.scryptSync_notPowerOfTwo = outcome(() => crypto.scryptSync('pw', 'salt', 16, { N: 1000 }));
results.scryptSync_N1 = outcome(() => crypto.scryptSync('pw', 'salt', 16, { N: 1 }));
results.scrypt_badParamsThrow = outcome(() => crypto.scrypt('pw', 'salt', 16, { N: 3 }, () => {}));

const scrypt = {
  defaults: ['pw', 'salt', 16],
  nullOptions: ['pw', 'salt', 16, null],
  N1024: ['pw', 'salt', 16, { N: 1024 }],
  longNames: ['pw', 'salt', 16, { cost: 1024, blockSize: 8, parallelization: 1 }],
  rfc7914: ['password', 'NaCl', 64, { N: 1024, r: 8, p: 16 }],
  rfc7914Empty: ['', '', 64, { N: 16, r: 1, p: 1 }],
  zeroMeansDefault: ['pw', 'salt', 8, { N: 0, r: 0, p: 0, maxmem: 0 }],
  N4: ['pw', 'salt', 8, { N: 4 }],
  maxmemExact: ['pw', 's', 8, { N: 4, r: 1, p: 1, maxmem: 896 }],
  maxmemShort: ['pw', 's', 8, { N: 4, r: 1, p: 1, maxmem: 895 }],
  defaultMaxmemTooSmall: ['pw', 'salt', 16, { N: 2 ** 15 }],
  NTooBigForR: ['pw', 's', 8, { N: 2 ** 16, r: 1, maxmem: 2 ** 40 }],
  hugeP: ['pw', 's', 8, { N: 16, r: 1, p: 2 ** 30 }],
  bothNAndCost: ['pw', 'salt', 16, { N: 1024, cost: 1024 }],
  bothRAndBlockSize: ['pw', 'salt', 16, { r: 8, blockSize: 8 }],
  bothPAndParallelization: ['pw', 'salt', 16, { p: 1, parallelization: 1 }],
  stringN: ['pw', 'salt', 16, { N: '1024' }],
  NTooLarge: ['pw', 'salt', 16, { N: 2 ** 32 }],
  negativeP: ['pw', 'salt', 16, { p: -1 }],
  negativeMaxmem: ['pw', 'salt', 16, { maxmem: -1 }],
  fractionMaxmem: ['pw', 'salt', 16, { maxmem: 1.5 }],
  negativeKeylen: ['pw', 'salt', -1],
  zeroKeylen: ['pw', 'salt', 0],
  bufferInputs: [Buffer.from([0, 255]), new Uint8Array([0]), 16, { N: 16, r: 1 }],
};
for (const [name, args] of Object.entries(scrypt)) results['scryptSync_' + name] = outcome(() => crypto.scryptSync(...args));
results.scrypt_noCallback = outcome(() => crypto.scrypt('pw', 'salt', 16));

const hkdf = {
  sha256: ['sha256', 'key', 'salt', 'info', 32],
  zeroLength: ['sha256', 'key', 'salt', 'info', 0],
  longest: ['sha256', 'key', 'salt', 'info', 255 * 32],
  tooLong: ['sha256', 'key', 'salt', 'info', 255 * 32 + 1],
  upperDigest: ['SHA512', 'key', 'salt', 'info', 16],
  ripemd160: ['ripemd160', 'key', 'salt', 'info', 30],
  blake2b512: ['blake2b512', 'key', 'salt', 'info', 70],
  blake2s256: ['blake2s256', 'key', 'salt', 'info', 40],
  md5Sha1: ['MD5-SHA1', 'key', 'salt', 'info', 40],
  shake128TooLong: ['shake128', 'key', 'salt', 'info', 255 * 16 + 1],
  emptyInputs: ['sha256', '', '', '', 16],
  rfc5869Case1: ['sha256', Buffer.from('0b'.repeat(22), 'hex'), Buffer.from('000102030405060708090a0b0c', 'hex'), Buffer.from('f0f1f2f3f4f5f6f7f8f9', 'hex'), 42],
  info1024: ['sha256', 'key', 'salt', 'x'.repeat(1024), 16],
  info1025: ['sha256', 'key', 'salt', 'x'.repeat(1025), 16],
  unknownDigest: ['nope', 'key', 'salt', 'info', 32],
  numberDigest: [5, 'key', 'salt', 'info', 32],
  numberKey: ['sha256', 5, 'salt', 'info', 32],
  numberSalt: ['sha256', 'key', 5, 'info', 32],
  negativeLength: ['sha256', 'key', 'salt', 'info', -1],
  stringLength: ['sha256', 'key', 'salt', 'info', '32'],
  views: ['sha1', new Uint16Array([1, 2]), new DataView(new ArrayBuffer(3)), new Uint8Array([0, 1]).buffer, 20],
};
for (const [name, args] of Object.entries(hkdf)) results['hkdfSync_' + name] = outcome(() => crypto.hkdfSync(...args));
results.hkdfSync_secretKeyObject = outcome(() => crypto.hkdfSync('sha256', crypto.createSecretKey(Buffer.from('key')), 'salt', 'info', 16));
results.hkdf_noCallback = outcome(() => crypto.hkdf('sha256', 'key', 'salt', 'info', 16));

// The callback forms return undefined and call back later.
function callback(start) {
  return new Promise((resolve) => {
    const order = [];
    const returned = start((err, value) => {
      order.push('callback');
      resolve({ err: String(err), value: show(value), returned: String(returned), order });
    });
    order.push('sync');
  });
}

async function main() {
  results.pbkdf2Callback = await callback((cb) => crypto.pbkdf2('pw', 'salt', 1, 16, 'sha256', cb));
  results.scryptCallback = await callback((cb) => crypto.scrypt('pw', 'salt', 16, cb));
  results.scryptOptionsCallback = await callback((cb) => crypto.scrypt('pw', 'salt', 16, { N: 1024 }, cb));
  results.hkdfCallback = await callback((cb) => crypto.hkdf('sha256', 'key', 'salt', 'info', 16, cb));
  results.pbkdf2ShakeCallback = await callback((cb) => crypto.pbkdf2('pw', 'salt', 1, 16, 'shake256', cb));
  results.hkdfShakeCallback = await callback((cb) => crypto.hkdf('shake128', 'key', 'salt', 'info', 16, cb));

  // Callbacks run in the AsyncLocalStorage context they were started in.
  const { AsyncLocalStorage } = require('async_hooks');
  const als = new AsyncLocalStorage();
  const seen = await Promise.all([
    (cb) => crypto.pbkdf2('pw', 'salt', 1, 16, 'sha256', cb),
    (cb) => crypto.scrypt('pw', 'salt', 16, { N: 16, r: 1 }, cb),
    (cb) => crypto.hkdf('sha256', 'key', 'salt', 'info', 16, cb),
  ].map((start, i) => new Promise((resolve) => {
    als.run('request ' + i, () => start(() => resolve(als.getStore())));
  })));
  results.asyncContext = seen;

  // Many derivations at once all finish.
  const many = await Promise.all(Array.from({ length: 12 }, (_, i) => new Promise((resolve) => {
    crypto.scrypt('pw', 'salt' + i, 8, { N: 1024 }, (err, key) => resolve(err ? String(err) : key.length));
  })));
  results.manyAtOnce = many;
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
