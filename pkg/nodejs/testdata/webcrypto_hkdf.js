// Runs unchanged in Node and in Orbital. webcrypto_hkdf.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestWebCryptoHKDF_MatchesNode'
const subtle = globalThis.crypto.subtle;
const results = {};
const hex = (b) => Buffer.from(b).toString('hex');

async function outcome(f) {
  try {
    const value = await f();
    return { value: value instanceof ArrayBuffer ? hex(value) : value };
  } catch (e) {
    return { name: e.name, message: e.message };
  }
}

async function main() {
  const ikm = new Uint8Array([1, 2, 3, 0, 5]);
  const key = await subtle.importKey('raw', ikm, 'HKDF', false, ['deriveBits', 'deriveKey']);
  results.keyShape = [key.type, key.extractable, key.algorithm.name, key.usages.join(',')];

  const p = (extra) => Object.assign({ name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(1), info: new Uint8Array(1) }, extra);
  const derive = {
    sha256: [p(), 128],
    sha1EmptySaltInfo: [p({ hash: 'SHA-1', salt: new Uint8Array(0), info: new Uint8Array(0) }), 160],
    sha512HashObject: [p({ hash: { name: 'SHA-512' }, salt: Buffer.from('salt'), info: Buffer.from('info') }), 512],
    sha384OtherViews: [p({ hash: 'SHA-384', salt: new Uint8Array(4).buffer, info: new DataView(new ArrayBuffer(3)) }), 8],
    lowercaseNames: [p({ name: 'hkdf', hash: 'sha-256' }), 128],
    nulInSaltAndInfo: [p({ salt: new Uint8Array([0, 1, 0]), info: new Uint8Array([0, 0, 2]) }), 256],
    longest: [p(), 255 * 256],
    tooLong: [p(), 255 * 256 + 8],
    zero: [p(), 0],
    notByteMultiple: [p(), 12],
    nullLength: [p(), null],
    undefinedLength: [p(), undefined],
    negative: [p(), -8],
    negativeOne: [p(), -1],
    fraction: [p(), 8.9],
    numericString: [p(), '16'],
    nonNumericString: [p(), 'abc'],
    wrapsPast2To32: [p(), 2 ** 32 + 8],
    exactly2To32: [p(), 2 ** 32],
    nanLength: [p(), NaN],
    infiniteLength: [p(), Infinity],
    bigintLength: [p(), 16n],
    noSalt: [p({ salt: undefined }), 128],
    noInfo: [p({ info: undefined }), 128],
    noHash: [p({ hash: undefined }), 128],
    unknownHash: [p({ hash: 'MD5' }), 128],
    stringSalt: [p({ salt: 'abc' }), 128],
    arrayInfo: [p({ info: [1, 2] }), 128],
  };
  for (const [name, [algo, length]] of Object.entries(derive)) {
    results['deriveBits_' + name] = await outcome(() => subtle.deriveBits(algo, key, length));
  }

  // A known answer: RFC 5869 test case 1.
  const rfcKey = await subtle.importKey('raw', Buffer.from('0b'.repeat(22), 'hex'), 'HKDF', false, ['deriveBits']);
  results.rfc5869Case1 = await outcome(() => subtle.deriveBits(
    { name: 'HKDF', hash: 'SHA-256', salt: Buffer.from('000102030405060708090a0b0c', 'hex'), info: Buffer.from('f0f1f2f3f4f5f6f7f8f9', 'hex') },
    rfcKey, 42 * 8));

  results.deriveKeyAesGcm = await outcome(async () => {
    const k = await subtle.deriveKey(p({ salt: new Uint8Array(2), info: new Uint8Array(2) }), key, { name: 'AES-GCM', length: 256 }, true, ['encrypt']);
    return [k.algorithm.name, k.algorithm.length, hex(await subtle.exportKey('raw', k))];
  });

  const onlyDeriveKey = await subtle.importKey('raw', ikm, 'HKDF', false, ['deriveKey']);
  results.deriveBitsWithoutUsage = await outcome(() => subtle.deriveBits(p(), onlyDeriveKey, 128));
  results.deriveKeyWithOnlyDeriveKey = await outcome(async () => {
    const k = await subtle.deriveKey(p(), onlyDeriveKey, { name: 'AES-GCM', length: 128 }, true, ['encrypt']);
    return hex(await subtle.exportKey('raw', k));
  });
  const onlyDeriveBits = await subtle.importKey('raw', ikm, 'HKDF', false, ['deriveBits']);
  results.deriveKeyWithoutUsage = await outcome(() => subtle.deriveKey(p(), onlyDeriveBits, { name: 'AES-GCM', length: 128 }, true, ['encrypt']));
  const pbkdf2Key = await subtle.importKey('raw', ikm, 'PBKDF2', false, ['deriveBits']);
  results.keyAlgorithmMismatch = await outcome(() => subtle.deriveBits(p(), pbkdf2Key, 128));

  results.importExtractable = await outcome(() => subtle.importKey('raw', ikm, 'HKDF', true, ['deriveBits']));
  results.importJwk = await outcome(() => subtle.importKey('jwk', { kty: 'oct', k: 'AAAA' }, 'HKDF', false, ['deriveBits']));
  results.importSignUsage = await outcome(() => subtle.importKey('raw', ikm, 'HKDF', false, ['sign']));
  results.importLowercase = await outcome(async () => (await subtle.importKey('raw', ikm, { name: 'hkdf' }, false, ['deriveBits'])).algorithm.name);
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
