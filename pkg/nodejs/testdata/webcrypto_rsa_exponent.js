// Runs unchanged in Node and in Orbital. webcrypto_rsa_exponent.node.json is this script's
// output under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestWebCryptoRSAExponent_MatchesNode'
const subtle = globalThis.crypto.subtle;
const results = {};

const exponents = {
  three: new Uint8Array([3]), f4: new Uint8Array([1, 0, 1]), leadingZero: new Uint8Array([0, 1, 0, 1]),
  seventeen: new Uint8Array([17]), int32Max: new Uint8Array([0x7f, 0xff, 0xff, 0xff]), buffer: Buffer.from([3]),
  two: new Uint8Array([2]), one: new Uint8Array([1]), zero: new Uint8Array([0]), empty: new Uint8Array([]),
  fiveBytes: new Uint8Array([1, 0, 0, 0, 1]), array: [1, 0, 1], missing: undefined,
  uint16: new Uint16Array([1]), arrayBuffer: new ArrayBuffer(1),
};

async function main() {
  for (const [label, pe] of Object.entries(exponents)) {
    for (const name of ['RSASSA-PKCS1-v1_5', 'RSA-PSS', 'RSA-OAEP']) {
      const usages = name === 'RSA-OAEP' ? ['encrypt', 'decrypt'] : ['sign', 'verify'];
      try {
        const { publicKey, privateKey } = await subtle.generateKey(
          { name, modulusLength: 1024, publicExponent: pe, hash: 'SHA-256' }, true, usages);
        const alg = publicKey.algorithm.publicExponent;
        results[label + ' ' + name] = [(await subtle.exportKey('jwk', publicKey)).e, (await subtle.exportKey('jwk', privateKey)).e,
          Array.from(alg), alg.constructor.name, alg !== pe];
      } catch (e) {
        results[label + ' ' + name] = { name: e.name, message: e.message };
      }
    }
  }
  // A key made with exponent 3 signs and verifies.
  const { publicKey, privateKey } = await subtle.generateKey(
    { name: 'RSASSA-PKCS1-v1_5', modulusLength: 1024, publicExponent: new Uint8Array([3]), hash: 'SHA-256' }, true, ['sign', 'verify']);
  const sig = await subtle.sign('RSASSA-PKCS1-v1_5', privateKey, Buffer.from('message'));
  results.signVerify = await subtle.verify('RSASSA-PKCS1-v1_5', publicKey, sig, Buffer.from('message'));
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
