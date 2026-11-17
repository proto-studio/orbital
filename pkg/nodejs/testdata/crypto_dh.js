// Runs unchanged in Node and in Orbital. crypto_dh.node.json is this script's output under
// Node v20.18.1. Regenerate the output with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoDH_MatchesNode'
// The keys in pems were made by Node for this test.
const crypto = require('crypto');
const pems = {
 "x1": "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VuBCIEIGCdyKcAm0dvwdOe4g0MuokD7F2J1mr5mT220FrzwQNs\n-----END PRIVATE KEY-----\n",
 "x2pub": "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VuAyEAbVZwSyydE868OidLKp5IGWX2XMuVG+3qv3Z2aXWaJHk=\n-----END PUBLIC KEY-----\n",
 "x2": "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VuBCIEIGDX0qxEyOuX7rdgsRcm2z9XunZgYSWpOBlBo2cD7v1H\n-----END PRIVATE KEY-----\n",
 "e1": "-----BEGIN PRIVATE KEY-----\nMIG2AgEAMBAGByqGSM49AgEGBSuBBAAiBIGeMIGbAgEBBDBzBFaWLi2gh1aKjPzk\nkN9OKgKTym6WnV9wW+qC7Llz4PiCHzGr0fLsvJWwQaPBVMGhZANiAAQGhfYkAqx2\n74ZdSjIiPavyu0frO83pyI23MLWmRIkTL6GisLkYMdL6xlEVCD3z67mQvEzT5i/+\nd9PPrIg/47x6HMY0mTp6iA/5OObUodnIz/vM++zA25cS3YVvaq4RXd8=\n-----END PRIVATE KEY-----\n",
 "e2pub": "-----BEGIN PUBLIC KEY-----\nMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAEeh1kXMiYYbt01oOyhi5k+BgMPMq8inAb\nfXGtx/qL2MvMUoEQE4caBamYdm0m3dPdQNH9YtlmtDQd+tuz3woaBSKbeVXKC8go\nJSLosPWt0uU5dEk1jl090r0q8g9Kb3qD\n-----END PUBLIC KEY-----\n",
 "e3pub": "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEEyoWGdbHAX1RbUi7CkgA03qeAcal\nJukucmTmsiHxyG8xeKq/q8gex/fly4qqblhj/tWHRfujzEItjv76fVm4fA==\n-----END PUBLIC KEY-----\n"
};
const results = {};

function outcome(f) {
  try {
    const v = f();
    return { value: Buffer.isBuffer(v) ? v.toString('hex') : v };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

// Finite-field DH with fixed private keys is deterministic.
const modp2 = crypto.getDiffieHellman('modp2');
results.groups = ['modp1', 'modp2', 'modp5', 'modp14', 'modp15', 'modp16', 'modp17', 'modp18']
  .map((n) => { const g = crypto.getDiffieHellman(n); return [g.getPrime().length, g.getGenerator('hex'), g.verifyError]; });
results.groupShape = [typeof modp2.setPrivateKey, typeof modp2.setPublicKey, crypto.createDiffieHellmanGroup('modp1').getPrime().length];
results.fixedKeys = outcome(() => {
  const a = crypto.createDiffieHellman(modp2.getPrime(), modp2.getGenerator());
  const b = crypto.createDiffieHellman(modp2.getPrime('hex'), 'hex', 2);
  a.setPrivateKey(Buffer.from('0102030405060708', 'hex'));
  b.setPrivateKey('99887766', 'hex');
  const pa = a.generateKeys('hex');
  const pb = b.generateKeys();
  return [pa, pb.toString('hex'), a.computeSecret(pb).toString('hex'), b.computeSecret(pa, 'hex', 'base64'),
    a.getPrivateKey('hex'), a.getPublicKey().length, a.verifyError];
});
results.randomKeys = outcome(() => {
  const a = crypto.getDiffieHellman('modp14');
  const b = crypto.getDiffieHellman('modp14');
  a.generateKeys();
  b.generateKeys();
  return [a.computeSecret(b.getPublicKey()).equals(b.computeSecret(a.getPublicKey())), a.computeSecret(b.getPublicKey()).length,
    a.getPublicKey().length <= 256];
});
results.generated = outcome(() => {
  const g = crypto.createDiffieHellman(512);
  return [g.getPrime().length, g.getGenerator('hex'), g.verifyError, g.getPrime()[0] >= 0x80];
});
results.verifyErrors = [
  outcome(() => crypto.createDiffieHellman(Buffer.from([23]), 5).verifyError),
  outcome(() => crypto.createDiffieHellman(Buffer.from([24])).verifyError),
  outcome(() => crypto.createDiffieHellman(modp2.getPrime(), Buffer.from([1])).verifyError),
  outcome(() => crypto.createDiffieHellman(Buffer.concat([modp2.getPrime().subarray(0, 127), Buffer.from([0xfd])])).verifyError),
];
// The buffers DiffieHellman returns are the caller's: changing one leaves the
// object's keys alone.
results.dhReturnedBuffers = (() => {
  const a = crypto.createDiffieHellman(modp2.getPrime(), 2);
  a.setPrivateKey(Buffer.from([6]));
  const pub = a.generateKeys();
  const before = a.getPublicKey('hex');
  pub.fill(0);
  a.getPublicKey().fill(0);
  a.getPrivateKey().fill(0);
  a.getPrime().fill(0);
  return [before, a.getPublicKey('hex'), a.getPrivateKey('hex'), a.getPrime().equals(modp2.getPrime())];
})();
results.dhErrors = {
  sizeTooSmall: outcome(() => crypto.createDiffieHellman(1)),
  badSize: outcome(() => crypto.createDiffieHellman(true)),
  badGenerator: outcome(() => crypto.createDiffieHellman(Buffer.from([23]), {})),
  smallModulusKeys: outcome(() => crypto.createDiffieHellman(Buffer.from([23]), 5).generateKeys()),
  unknownGroup: outcome(() => crypto.getDiffieHellman('modp99')),
  noPublicKey: outcome(() => crypto.createDiffieHellman(modp2.getPrime()).getPublicKey()),
  noPrivateKey: outcome(() => crypto.createDiffieHellman(modp2.getPrime()).getPrivateKey()),
  otherKeyOne: outcome(() => { const a = crypto.getDiffieHellman('modp2'); a.generateKeys(); return a.computeSecret(Buffer.from([1])); }),
  otherKeyZero: outcome(() => { const a = crypto.getDiffieHellman('modp2'); a.generateKeys(); return a.computeSecret(Buffer.from([0])); }),
  otherKeyTooLarge: outcome(() => { const a = crypto.getDiffieHellman('modp2'); a.generateKeys(); return a.computeSecret(modp2.getPrime()); }),
  // The modulus's value counts, not its buffer's length.
  primeOne: outcome(() => crypto.createDiffieHellman(Buffer.concat([Buffer.alloc(63), Buffer.from([1])])).generateKeys()),
  primeTwo: outcome(() => crypto.createDiffieHellman(Buffer.concat([Buffer.alloc(63), Buffer.from([2])])).generateKeys()),
  evenPrime: outcome(() => crypto.createDiffieHellman(Buffer.concat([Buffer.alloc(63, 0xff), Buffer.from([0xfe])])).generateKeys()),
  primeTooLarge: outcome(() => crypto.createDiffieHellman(Buffer.alloc(1251, 0xff)).generateKeys()),
  secretTooLarge: outcome(() => { const a = crypto.createDiffieHellman(Buffer.alloc(1251, 0xff)); a.setPrivateKey(Buffer.from([3])); return a.computeSecret(Buffer.from([5])); }),
  largestPrime: outcome(() => { const a = crypto.createDiffieHellman(Buffer.concat([Buffer.alloc(1249, 0xff), Buffer.from([0xfd])])); a.setPrivateKey(Buffer.from([3])); return [a.generateKeys('hex'), a.computeSecret(Buffer.from([5])).length]; }),
  sizeTooLarge: outcome(() => crypto.createDiffieHellman(10001)),
};

// ECDH with fixed private keys is deterministic.
const ec = {};
for (const curve of ['prime256v1', 'secp384r1', 'secp521r1']) {
  ec[curve] = outcome(() => {
    const size = { prime256v1: 32, secp384r1: 48, secp521r1: 66 }[curve];
    const a = crypto.createECDH(curve);
    const b = crypto.createECDH(curve);
    a.setPrivateKey(Buffer.alloc(size, 1));
    b.setPrivateKey(Buffer.alloc(size, 2).toString('hex'), 'hex');
    const pub = a.getPublicKey();
    return [pub.toString('hex'), a.getPublicKey('hex', 'compressed'), a.getPublicKey('base64', 'hybrid'), a.getPrivateKey('hex'),
      a.computeSecret(b.getPublicKey()).toString('hex'), b.computeSecret(a.getPublicKey(null, 'compressed')).toString('hex'),
      b.computeSecret(a.getPublicKey('hex', 'hybrid'), 'hex', 'hex'),
      crypto.ECDH.convertKey(pub, curve, null, 'hex', 'compressed'), crypto.ECDH.convertKey(a.getPublicKey('hex', 'compressed'), curve, 'hex', 'hex')];
  });
}
results.ecdh = ec;
results.ecdhRandom = outcome(() => {
  const a = crypto.createECDH('prime256v1');
  const b = crypto.createECDH('prime256v1');
  return [a.generateKeys().length, b.generateKeys('hex', 'compressed').length,
    a.computeSecret(b.getPublicKey()).equals(b.computeSecret(a.getPublicKey()))];
});
results.ecdhSetPublicKey = outcome(() => {
  const a = crypto.createECDH('prime256v1');
  a.setPrivateKey(Buffer.alloc(32, 1));
  const b = crypto.createECDH('prime256v1');
  b.setPublicKey(a.getPublicKey('hex', 'compressed'), 'hex');
  return b.getPublicKey('hex');
});
results.ecdhKeyPair = (() => {
  const keys = (n) => { const e = crypto.createECDH('prime256v1'); e.setPrivateKey(Buffer.alloc(32, n)); return e; };
  const other = keys(3).getPublicKey();
  const mismatched = keys(1);
  mismatched.setPublicKey(keys(2).getPublicKey());
  const regenerated = keys(1);
  regenerated.setPublicKey(keys(2).getPublicKey());
  regenerated.generateKeys();
  const publicOnly = () => { const e = crypto.createECDH('prime256v1'); e.setPublicKey(keys(2).getPublicKey()); return e; };
  const ownAgain = keys(1);
  ownAgain.setPublicKey(keys(1).getPublicKey('hex', 'compressed'), 'hex');
  return {
    mismatched: outcome(() => mismatched.computeSecret(other)),
    regenerated: outcome(() => regenerated.computeSecret(other).length),
    publicOnly: outcome(() => publicOnly().computeSecret(other)),
    publicOnlyBadOther: outcome(() => publicOnly().computeSecret(Buffer.alloc(65))),
    ownAgain: outcome(() => ownAgain.computeSecret(other)),
  };
})();
// Private keys are exported without leading zero bytes, as OpenSSL does.
results.ecdhPrivateExport = outcome(() => {
  const a = crypto.createECDH('prime256v1');
  a.setPrivateKey(Buffer.from([1]));
  const b = crypto.createECDH('secp384r1');
  b.setPrivateKey(Buffer.concat([Buffer.alloc(5), Buffer.alloc(43, 7)]));
  const c = crypto.createECDH('prime256v1');
  c.setPrivateKey(Buffer.concat([Buffer.alloc(1), Buffer.alloc(31, 9)]));
  const d = crypto.createECDH('prime256v1');
  d.setPrivateKey(c.getPrivateKey());
  return [a.getPrivateKey('hex'), a.getPublicKey('hex'), b.getPrivateKey().length, c.getPrivateKey('hex'),
    d.computeSecret(a.getPublicKey()).equals(c.computeSecret(a.getPublicKey()))];
});
results.ecdhErrors = {
  unknownCurve: outcome(() => crypto.createECDH('nope')),
  nistName: outcome(() => crypto.createECDH('P-256')),
  numberCurve: outcome(() => crypto.createECDH(5)),
  noPublic: outcome(() => crypto.createECDH('prime256v1').getPublicKey()),
  noPrivate: outcome(() => crypto.createECDH('prime256v1').getPrivateKey()),
  noKeyPair: outcome(() => crypto.createECDH('prime256v1').computeSecret(crypto.createECDH('prime256v1').generateKeys())),
  zeroPrivate: outcome(() => crypto.createECDH('prime256v1').setPrivateKey(Buffer.alloc(32))),
  tooBigPrivate: outcome(() => crypto.createECDH('prime256v1').setPrivateKey(Buffer.alloc(33, 1))),
  badPublic: outcome(() => crypto.createECDH('prime256v1').setPublicKey(Buffer.alloc(65))),
  badOther: outcome(() => { const a = crypto.createECDH('prime256v1'); a.generateKeys(); return a.computeSecret(Buffer.alloc(65)); }),
  shortOther: outcome(() => { const a = crypto.createECDH('prime256v1'); a.generateKeys(); return a.computeSecret(Buffer.alloc(3)); }),
  badFormat: outcome(() => { const a = crypto.createECDH('prime256v1'); return a.generateKeys('hex', 'bogus'); }),
};
results.ecdhShape = [Object.keys(crypto.ECDH.prototype).sort().join(','), Object.keys(crypto.DiffieHellman.prototype).sort().join(','),
  crypto.ECDH('prime256v1') instanceof crypto.ECDH];

// crypto.diffieHellman with KeyObjects.
const k = {};
for (const [name, pem] of Object.entries(pems)) k[name] = name.endsWith('pub') ? crypto.createPublicKey(pem) : crypto.createPrivateKey(pem);
results.keyAgreement = {
  x25519: outcome(() => crypto.diffieHellman({ privateKey: k.x1, publicKey: k.x2pub })),
  x25519Back: outcome(() => crypto.diffieHellman({ privateKey: k.x2, publicKey: crypto.createPublicKey(k.x1) })),
  x25519PrivateAsPublic: outcome(() => crypto.diffieHellman({ privateKey: k.x1, publicKey: k.x2 })),
  ec: outcome(() => crypto.diffieHellman({ privateKey: k.e1, publicKey: k.e2pub })),
  mixed: outcome(() => crypto.diffieHellman({ privateKey: k.e1, publicKey: k.x2pub })),
  publicAsPrivate: outcome(() => crypto.diffieHellman({ privateKey: k.x2pub, publicKey: k.x2pub })),
  missing: outcome(() => crypto.diffieHellman({})),
  notObject: outcome(() => crypto.diffieHellman(5)),
};

// Symmetric keys.
results.generateKeySync = {
  hmac64: outcome(() => crypto.generateKeySync('hmac', { length: 64 }).symmetricKeySize),
  hmac12: outcome(() => crypto.generateKeySync('hmac', { length: 12 }).symmetricKeySize),
  aes128: outcome(() => [crypto.generateKeySync('aes', { length: 128 }).symmetricKeySize, crypto.generateKeySync('aes', { length: 128 }).type]),
  aes100: outcome(() => crypto.generateKeySync('aes', { length: 100 })),
  hmac7: outcome(() => crypto.generateKeySync('hmac', { length: 7 })),
  hmacHuge: outcome(() => crypto.generateKeySync('hmac', { length: 2 ** 31 })),
  hmacString: outcome(() => crypto.generateKeySync('hmac', { length: '8' })),
  unknownType: outcome(() => crypto.generateKeySync('nope', { length: 128 })),
  numberType: outcome(() => crypto.generateKeySync(5, {})),
  noOptions: outcome(() => crypto.generateKeySync('hmac')),
  noCallback: outcome(() => crypto.generateKey('aes', { length: 128 })),
};

async function main() {
  const { AsyncLocalStorage } = require('async_hooks');
  const store = new AsyncLocalStorage();
  results.generateKey = await new Promise((resolve) => {
    const order = [];
    store.run('request', () => crypto.generateKey('aes', { length: 256 }, (err, key) => {
      order.push('callback');
      resolve([String(err), key.symmetricKeySize, order, store.getStore()]);
    }));
    order.push('sync');
  });
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
