// Runs unchanged in Node and in Orbital. crypto_sign.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoSign_MatchesNode'
// The keys, and the signatures in nodeSigs, were made by Node for this test.
const crypto = require('crypto');
const fixtures = {
 "keys": {
  "rsaPriv": "-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAMHSniUMs+N7TqgE\ny8bZ+rkpnq0MkDXrOJNfSosU4CWuwhyz15IXipuoJ7yOPccJ+OWktUkh38Cd84tY\nsaSLzcQrhI4Gl9B1oRKjlllW+muqtfN1QPNmEIsFNXZuC+j1r0jksFhIMPfMkZ7M\ntdtaIZALlPrWymtF5hMhMAp1jf/BAgMBAAECgYEAk7E9ZrgGP5qPdkKpUO57Ois3\nyV+gaCXEZ7AeKlsO7kf7jEmSQ7WCnlgFZ43wD0JPEhZvOboBOVJi6dT9EIijkrm3\nl+Bpg45JnEf8n5BclO4AW0q25//dzZb36UGKxX/DuUQBB4mW9n8M8dex0HMMC46D\nnsFdNk8CDXRdo/dMPT0CQQD33d+3pp5/clyPNi6poowHXbBE7w6gx4IL1xVvWKy/\nEcE0WxHHh0IONlsCOj8xZtXtoljI9js1HtUN6fUf56P7AkEAyC7DtzALDz0eRIRM\ngV6XtWcxmFdAhYFlWGGXhpOUk7Os4ERm8Ye84Rb5ML+NeRyViH2uaIsQPEm/2XGq\nQKgicwJAOnS5Lja9apEuAxhkKN2M6K/PwnIeoQxsPx6aj4f6NZWaWx6fMs9TZMas\n7/mMrbny+fgvuDVaAU0kMgPbK4sVwQJAQva5y7bLjE5yqJLlEMW21BzwDzjowYA2\niakjygXmRr5tDxxyn0s72MQZmcAKMEBqmR0J0bjrNm+pOLD4NebIlwJAdWap4921\n1eGrS2dtW+w+hoDdfmdyGdBzmfSw5T9IKuR2tzxhdBKSFW2sxukXiWhMTRrUC6gj\nL8dQb/yyzT1Ahg==\n-----END PRIVATE KEY-----\n",
  "rsaPub": "-----BEGIN PUBLIC KEY-----\nMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDB0p4lDLPje06oBMvG2fq5KZ6t\nDJA16ziTX0qLFOAlrsIcs9eSF4qbqCe8jj3HCfjlpLVJId/AnfOLWLGki83EK4SO\nBpfQdaESo5ZZVvprqrXzdUDzZhCLBTV2bgvo9a9I5LBYSDD3zJGezLXbWiGQC5T6\n1sprReYTITAKdY3/wQIDAQAB\n-----END PUBLIC KEY-----\n",
  "ecPriv": "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg9nRbpARaESE2ObzF\nytBWGdCPg0reOCeD4g9nLhST5zWhRANCAASCEN2ygOpkgANyoeTLITmi4vKaz9T4\nh/vH4jbAkaxlCjUwq+7v3597vWXGH/q20jZGtPANjFVznVP65vLPy5TA\n-----END PRIVATE KEY-----\n",
  "ecPub": "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEghDdsoDqZIADcqHkyyE5ouLyms/U\n+If7x+I2wJGsZQo1MKvu79+fe71lxh/6ttI2RrTwDYxVc51T+ubyz8uUwA==\n-----END PUBLIC KEY-----\n",
  "edPriv": "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEINchJXXS0HGUGPPrL0TmrjXrsDDi+WRbJRcwEPa/7Rpr\n-----END PRIVATE KEY-----\n",
  "edPub": "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAdfJrYnQqOa9xEnrR7pxSVOnlvYOeDUmtwLvDjbEFxPY=\n-----END PUBLIC KEY-----\n",
  "ec384Priv": "-----BEGIN PRIVATE KEY-----\nMIG2AgEAMBAGByqGSM49AgEGBSuBBAAiBIGeMIGbAgEBBDAcIrTW3rIVbDk566r4\naeCoSyFY9fnCYLQMD2C22wmBkhGlJj80T0Y/CGSnCWKZTVKhZANiAASDpfXqSY3L\nkdk//1MLmPrdQHNyumOgn+9g7BFS2cZo24ozkDcx7teYb/qOX0tKv7xARWXcUOsc\nSf+E42TEr1qkbP8lvqVx0UKk1ThuuQ3d2DLgHqiUu4za5qo2cYyv9qc=\n-----END PRIVATE KEY-----\n",
  "ec384Pub": "-----BEGIN PUBLIC KEY-----\nMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAEg6X16kmNy5HZP/9TC5j63UBzcrpjoJ/v\nYOwRUtnGaNuKM5A3Me7XmG/6jl9LSr+8QEVl3FDrHEn/hONkxK9apGz/Jb6lcdFC\npNU4brkN3dgy4B6olLuM2uaqNnGMr/an\n-----END PUBLIC KEY-----\n"
 },
 "nodeSigs": {
  "ecDer": "MEYCIQCc4zuMgQ7Z16RuE4jx47FeYdj6NHYDfBzSEyW4cLdwUAIhAJUIWFqV8/T7XPAlfEpyoinKkrpnAIi0r+3rTT4cQwZi",
  "ecP1363": "Lgu1TD3qCkHqcrGTzUjnnXbwbcGsdgual4wPddNybChpcFZGDbegGCf4DHlJBx9HPp2qhf9Xun+fzwHvbmM0QA==",
  "ec384": "MGYCMQDs3ChuLEHFIAB9Mwbvs6KU3ankSa9wrEIO3xvUu8f0VGvX7qo+wUEN4od6ep2yr5QCMQCNI2j9x1+xolZsLXSjSbJS1XGbes7iFVlwtWAzK+QrEpC1v+20zXl9qM7tAyOvCiQ=",
  "pss": "s/3nlvz9fBqV4bwQ+Z3M/6I/RwhiAuVeWIMNH/vzWOezUj3TyAygcqiz3+HOiHjDqvAriguT9msw2bYP4qJk3W+rzmBd7udVh6cN1teEQ0o56swo4H2HATlElBwR5WmZtyOFTHddlVR0ELsXOcuRSiJPOtKfK9LDvyK+Bl9g5kU=",
  "pssMax": "ukPOTLh3PIDQyFSjRHDGA2+L7GHEnfoAWYynhKiKUFYir7bpEBI3naGaFC0vEMOlwQ+hod4/PUCn3KHW7YYHvVOTc1FNQQ2ZQxJn88GtznjFLGvceL/BwfIjaWPW6pQOuBsDZ8uCLNxsLhr7A4FZjsJPI4SGl/b6y6td/SiCPUw="
 }
};
const { keys, nodeSigs } = fixtures;
const results = {};
const msg = Buffer.from('message to sign');

function outcome(f) {
  try {
    const v = f();
    return { value: Buffer.isBuffer(v) ? v.toString('hex') : v };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

const k = {};
for (const [name, pem] of Object.entries(keys)) {
  k[name] = name.endsWith('Priv') ? crypto.createPrivateKey(pem) : crypto.createPublicKey(pem);
}

// Deterministic signatures, compared byte for byte.
for (const digest of ['sha1', 'sha256', 'SHA384', 'sha512', 'RSA-SHA256', 'sha256WithRSAEncryption', 'sha3-256', 'md5',
  'sha3-512', 'ripemd160', 'RSA-RIPEMD160', 'md5-sha1']) {
  results['rsaPkcs1_' + digest] = outcome(() => crypto.createSign(digest).update(msg).sign(k.rsaPriv));
}
results.rsaOneShotDefaultDigest = outcome(() => crypto.sign(undefined, msg, k.rsaPriv));
results.rsaOneShotSha1 = outcome(() => crypto.sign('sha1', msg, k.rsaPriv));
results.rsaFromPem = outcome(() => crypto.sign('sha256', msg, keys.rsaPriv));
results.rsaFromObject = outcome(() => crypto.sign('sha256', msg, { key: keys.rsaPriv, padding: crypto.constants.RSA_PKCS1_PADDING }));
results.rsaHexOutput = outcome(() => crypto.createSign('sha256').update('message ').update('to sign').sign(k.rsaPriv, 'hex'));
results.ed25519 = outcome(() => crypto.sign(null, msg, k.edPriv));
results.ed25519FromPem = outcome(() => crypto.sign(undefined, msg, keys.edPriv));

// Randomized signatures: lengths, and each side verifies its own and Node's.
const verifies = (algo, pub, sig, opts) => crypto.verify(algo, msg, opts ? { key: pub, ...opts } : pub, sig);
results.ecdsa = outcome(() => {
  const der = crypto.sign('sha256', msg, k.ecPriv);
  const p1363 = crypto.sign('sha256', msg, { key: k.ecPriv, dsaEncoding: 'ieee-p1363' });
  return [p1363.length, verifies('sha256', k.ecPub, der), verifies('sha256', k.ecPub, p1363, { dsaEncoding: 'ieee-p1363' }),
    verifies('sha256', k.ecPub, Buffer.from(nodeSigs.ecDer, 'base64')),
    verifies('sha256', k.ecPub, Buffer.from(nodeSigs.ecP1363, 'base64'), { dsaEncoding: 'ieee-p1363' }),
    verifies('sha256', k.ecPub, Buffer.from(nodeSigs.ecP1363, 'base64')),
    verifies('sha384', k.ec384Pub, Buffer.from(nodeSigs.ec384, 'base64')),
    crypto.createVerify('sha256').update(msg).verify(k.ecPub, der),
    crypto.createSign('sha512').update(msg).sign(k.ecPriv).length > 60];
});
// Digests from golang.org/x/crypto and the SHAKE functions: RSA takes only those with a
// DigestInfo encoding (and MD5-SHA1); ECDSA takes any.
for (const digest of ['ripemd160', 'md5-sha1', 'blake2b512', 'blake2s256', 'shake128', 'shake256']) {
  const pss = { padding: crypto.constants.RSA_PKCS1_PSS_PADDING };
  results['digest_' + digest] = {
    rsaVerify: outcome(() => crypto.verify(digest, msg, k.rsaPub, crypto.sign(digest, msg, k.rsaPriv))),
    rsaVerifyOnly: outcome(() => crypto.verify(digest, msg, k.rsaPub, Buffer.alloc(128))),
    pss: outcome(() => verifies(digest, k.rsaPub, crypto.sign(digest, msg, { key: k.rsaPriv, ...pss }), pss)),
    stream: outcome(() => crypto.createSign(digest).update(msg).sign(k.rsaPriv).length),
    ecdsa: outcome(() => verifies(digest, k.ecPub, crypto.sign(digest, msg, k.ecPriv))),
    ecdsaP1363: outcome(() => crypto.createVerify(digest).update(msg).verify({ key: k.ecPub, dsaEncoding: 'ieee-p1363' },
      crypto.createSign(digest).update(msg).sign({ key: k.ecPriv, dsaEncoding: 'ieee-p1363' }))),
  };
}
results.pss = outcome(() => {
  const pss = { padding: crypto.constants.RSA_PKCS1_PSS_PADDING };
  const own = crypto.sign('sha256', msg, { key: k.rsaPriv, ...pss });
  const salt32 = crypto.sign('sha256', msg, { key: k.rsaPriv, ...pss, saltLength: 32 });
  return [own.length, verifies('sha256', k.rsaPub, own, pss), verifies('sha256', k.rsaPub, salt32, { ...pss, saltLength: 32 }),
    verifies('sha256', k.rsaPub, salt32, { ...pss, saltLength: 20 }),
    verifies('sha256', k.rsaPub, Buffer.from(nodeSigs.pss, 'base64'), pss),
    verifies('sha256', k.rsaPub, Buffer.from(nodeSigs.pss, 'base64'), { ...pss, saltLength: crypto.constants.RSA_PSS_SALTLEN_DIGEST }),
    verifies('sha256', k.rsaPub, Buffer.from(nodeSigs.pssMax, 'base64'), pss),
    verifies('sha256', k.rsaPub, Buffer.from(nodeSigs.pssMax, 'base64'))];
});

// PSS with an explicitly empty salt, made by Node (Orbital doesn't sign with one).
const pssZeroSaltSig = Buffer.from('LlPGdQIvapXcdDBU3DJlpXoRHa0mQoQUtnvCCZco5M/Tvk8ZF6NrDyW964wTBoH4NaiCisNibwlGUqq7PGjaB1mWqS+GC5kREcHfJr8/0bgh33bHml1ezc2lvgPyPHnrb7pXkj5l4163lzz+IK6/28mu2x9bf2O2qleRNeMoSdc=', 'base64');
results.pssZeroSalt = outcome(() => {
  const pss0 = { padding: crypto.constants.RSA_PKCS1_PSS_PADDING, saltLength: 0 };
  return [verifies('sha256', k.rsaPub, pssZeroSaltSig, pss0),
    verifies('sha256', k.rsaPub, pssZeroSaltSig, { padding: 6, saltLength: 32 }), verifies('sha256', k.rsaPub, pssZeroSaltSig, { padding: 6 }),
    verifies('sha256', k.rsaPub, crypto.sign('sha256', msg, { key: k.rsaPriv, padding: 6 }), pss0),
    verifies('sha256', k.rsaPub, Buffer.from(nodeSigs.pss, 'base64'), pss0)];
});

// Verification.
const good = crypto.sign('sha256', msg, k.rsaPriv);
results.verify = {
  good: outcome(() => crypto.createVerify('sha256').update(msg).verify(k.rsaPub, good)),
  privateKey: outcome(() => crypto.createVerify('sha256').update(msg).verify(k.rsaPriv, good)),
  pem: outcome(() => crypto.createVerify('sha256').update(msg).verify(keys.rsaPub, good)),
  hexSignature: outcome(() => crypto.createVerify('sha256').update(msg).verify(k.rsaPub, good.toString('hex'), 'hex')),
  wrongMessage: outcome(() => crypto.createVerify('sha256').update('other').verify(k.rsaPub, good)),
  wrongDigest: outcome(() => crypto.createVerify('sha512').update(msg).verify(k.rsaPub, good)),
  short: outcome(() => crypto.createVerify('sha256').update(msg).verify(k.rsaPub, Buffer.alloc(3))),
  ed25519: outcome(() => crypto.verify(null, msg, k.edPub, crypto.sign(null, msg, k.edPriv))),
  ed25519Wrong: outcome(() => crypto.verify(null, Buffer.from('x'), k.edPub, crypto.sign(null, msg, k.edPriv))),
  stringData: outcome(() => crypto.verify('sha256', 'message to sign', k.rsaPub, good)),
  privatePem: outcome(() => crypto.verify('sha256', msg, keys.rsaPriv, good)),
  privatePemObject: outcome(() => crypto.verify('sha256', msg, { key: keys.rsaPriv }, good)),
  privatePemStream: outcome(() => crypto.createVerify('sha256').update(msg).verify(keys.ecPriv, crypto.sign('sha256', msg, k.ecPriv))),
};

// Errors.
results.errors = {
  unknownDigest: outcome(() => crypto.createSign('nope')),
  numberDigest: outcome(() => crypto.createSign(5)),
  publicKeyToSign: outcome(() => crypto.createSign('sha256').update('x').sign(k.rsaPub)),
  badPem: outcome(() => crypto.createSign('sha256').update('x').sign('nope')),
  ed25519Stream: outcome(() => crypto.createSign('sha256').update('x').sign(k.edPriv)),
  ed25519Digest: outcome(() => crypto.sign('sha256', msg, k.edPriv)),
  badDsaEncoding: outcome(() => crypto.sign('sha256', msg, { key: k.ecPriv, dsaEncoding: 'x' })),
  badPadding: outcome(() => crypto.sign('sha256', msg, { key: k.rsaPriv, padding: 'x' })),
  badSaltLength: outcome(() => crypto.sign('sha256', msg, { key: k.rsaPriv, padding: 6, saltLength: 1.5 })),
  numberData: outcome(() => crypto.sign('sha256', 5, k.rsaPriv)),
  numberAlgorithm: outcome(() => crypto.sign(5, msg, k.rsaPriv)),
  emptyAlgorithm: outcome(() => crypto.sign('', msg, k.rsaPriv)),
  emptyAlgorithmVerify: outcome(() => crypto.verify('', msg, k.rsaPub, Buffer.alloc(128))),
  unknownAlgorithm: outcome(() => crypto.sign('nope', msg, k.rsaPriv)),
  numberUpdate: outcome(() => crypto.createSign('sha256').update(5)),
  secretKey: outcome(() => crypto.sign('sha256', msg, crypto.createSecretKey(Buffer.alloc(16)))),
};
results.states = (() => {
  const s = crypto.createSign('sha256');
  s.update('x');
  s.sign(k.rsaPriv);
  const v = crypto.createVerify('sha256');
  v.update('x');
  v.verify(k.rsaPub, good);
  return [outcome(() => s.sign(k.rsaPriv)), outcome(() => s.update('y')), outcome(() => v.verify(k.rsaPub, good))];
})();
results.shape = [typeof crypto.Sign, typeof crypto.Verify, crypto.createSign('sha256') instanceof require('stream').Writable,
  crypto.Sign('sha256') instanceof crypto.Sign, Object.keys(crypto.Sign.prototype).sort().join(','),
  ['RSA_PKCS1_PADDING', 'RSA_PKCS1_PSS_PADDING', 'RSA_PSS_SALTLEN_DIGEST', 'RSA_PSS_SALTLEN_MAX_SIGN', 'RSA_PSS_SALTLEN_AUTO',
    'POINT_CONVERSION_UNCOMPRESSED'].map((n) => crypto.constants[n]).join(',')];

async function main() {
  results.callback = await new Promise((resolve) => {
    const order = [];
    crypto.sign('sha256', msg, k.rsaPriv, (err, sig) => {
      order.push('callback');
      crypto.verify('sha256', msg, k.rsaPub, sig, (err2, ok) => resolve([String(err), String(err2), ok, order]));
    });
    order.push('sync');
  });
  // WebCrypto CryptoKeys work as keys, directly or as options.key.
  const subtle = globalThis.crypto.subtle;
  const ed = await subtle.generateKey({ name: 'Ed25519' }, true, ['sign', 'verify']);
  const ec = await subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
  const edPub = await subtle.importKey('jwk', k.edPub.export({ format: 'jwk' }), { name: 'Ed25519' }, true, ['verify']);
  results.cryptoKeys = {
    ed: outcome(() => crypto.verify(null, msg, ed.publicKey, crypto.sign(null, msg, ed.privateKey))),
    edOptions: outcome(() => crypto.verify(null, msg, { key: ed.publicKey }, crypto.sign(null, msg, { key: ed.privateKey }))),
    edVerifyWithPrivate: outcome(() => crypto.verify(null, msg, ed.privateKey, crypto.sign(null, msg, ed.privateKey))),
    edImported: outcome(() => crypto.verify(null, msg, edPub, crypto.sign(null, msg, k.edPriv))),
    ecStream: outcome(() => crypto.createVerify('sha256').update(msg).verify(ec.publicKey,
      crypto.createSign('sha256').update(msg).sign(ec.privateKey))),
    ecP1363: outcome(() => crypto.sign('sha256', msg, { key: ec.privateKey, dsaEncoding: 'ieee-p1363' }).length),
    signWithPublic: outcome(() => crypto.sign(null, msg, ed.publicKey)),
  };
  // (Node 20 aborts when the callback form fails, as with an Ed25519 key and a digest,
  // so that case isn't run here.)
  const { AsyncLocalStorage } = require('async_hooks');
  const als = new AsyncLocalStorage();
  results.asyncContext = await Promise.all([
    (cb) => crypto.sign('sha256', msg, k.rsaPriv, cb),
    (cb) => crypto.verify('sha256', msg, k.rsaPub, good, cb),
  ].map((start, i) => new Promise((resolve) => {
    als.run('request ' + i, () => start(() => resolve(als.getStore())));
  })));
  results.stream = await new Promise((resolve) => {
    const s = crypto.createSign('sha256');
    s.write('message ');
    s.end('to sign', () => resolve(s.sign(k.rsaPriv).toString('hex')));
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
