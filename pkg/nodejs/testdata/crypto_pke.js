// Runs unchanged in Node and in Orbital. crypto_pke.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoPublicEncrypt_MatchesNode'
// The keys and the ciphertexts in fx were made by Node for this test.
const crypto = require('crypto');
const fx = {
 "rsaPriv": "-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAMHSniUMs+N7TqgE\ny8bZ+rkpnq0MkDXrOJNfSosU4CWuwhyz15IXipuoJ7yOPccJ+OWktUkh38Cd84tY\nsaSLzcQrhI4Gl9B1oRKjlllW+muqtfN1QPNmEIsFNXZuC+j1r0jksFhIMPfMkZ7M\ntdtaIZALlPrWymtF5hMhMAp1jf/BAgMBAAECgYEAk7E9ZrgGP5qPdkKpUO57Ois3\nyV+gaCXEZ7AeKlsO7kf7jEmSQ7WCnlgFZ43wD0JPEhZvOboBOVJi6dT9EIijkrm3\nl+Bpg45JnEf8n5BclO4AW0q25//dzZb36UGKxX/DuUQBB4mW9n8M8dex0HMMC46D\nnsFdNk8CDXRdo/dMPT0CQQD33d+3pp5/clyPNi6poowHXbBE7w6gx4IL1xVvWKy/\nEcE0WxHHh0IONlsCOj8xZtXtoljI9js1HtUN6fUf56P7AkEAyC7DtzALDz0eRIRM\ngV6XtWcxmFdAhYFlWGGXhpOUk7Os4ERm8Ye84Rb5ML+NeRyViH2uaIsQPEm/2XGq\nQKgicwJAOnS5Lja9apEuAxhkKN2M6K/PwnIeoQxsPx6aj4f6NZWaWx6fMs9TZMas\n7/mMrbny+fgvuDVaAU0kMgPbK4sVwQJAQva5y7bLjE5yqJLlEMW21BzwDzjowYA2\niakjygXmRr5tDxxyn0s72MQZmcAKMEBqmR0J0bjrNm+pOLD4NebIlwJAdWap4921\n1eGrS2dtW+w+hoDdfmdyGdBzmfSw5T9IKuR2tzxhdBKSFW2sxukXiWhMTRrUC6gj\nL8dQb/yyzT1Ahg==\n-----END PRIVATE KEY-----\n",
 "rsaPub": "-----BEGIN PUBLIC KEY-----\nMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDB0p4lDLPje06oBMvG2fq5KZ6t\nDJA16ziTX0qLFOAlrsIcs9eSF4qbqCe8jj3HCfjlpLVJId/AnfOLWLGki83EK4SO\nBpfQdaESo5ZZVvprqrXzdUDzZhCLBTV2bgvo9a9I5LBYSDD3zJGezLXbWiGQC5T6\n1sprReYTITAKdY3/wQIDAQAB\n-----END PUBLIC KEY-----\n",
 "ecPub": "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEghDdsoDqZIADcqHkyyE5ouLyms/U\n+If7x+I2wJGsZQo1MKvu79+fe71lxh/6ttI2RrTwDYxVc51T+ubyz8uUwA==\n-----END PUBLIC KEY-----\n",
 "oaep": "rNA9RegmY9HlPLU6o9PahhPsagwSSzE9zLKpYKjob//Vkg7mE0LcECSn6LzMVAXl/WaPPZcZ0TgRx2hMrqF4JVyd5+NB79Gmew14jLmzopoi7YmNUvDnGZEyeiWdpFO6DPPZ2yk2gqog14QOnPpCCmYRri8WGn2JT7n/5J43izU=",
 "oaep256": "qwvdh1uqe9fP/saAhGX9T1rlSinnAYwlyRcRNpQkQ+drEf74aldqYvxxy4KNgO6dlHUvwFJx4k6TExkicNfQ1Qdwwbpp24R8Bgyp9gB92S9PSIttvFPktDs9+fwgOzghYInOcBfMftB5aF72SSRHwOgTri23jOXMCnfOdKdK7uE=",
 "oaepLabel": "XAZo2TOf4tu5wo/rYyPScrvg6K6tEPLrYvybNv5lLaJItGAC5yKXeEiW1ysldp5RNcx9ibDWNkiiuQ4PsjXiNHI35kIEzKUElzHnGVCTJn5jgt1Ahg3F/wo+/ENr4eMNXmhr2j7P3Ok6fpP+PpIgAEsh6EwZM7F31Fm2zMkDaDA="
};
const results = {};
const msg = Buffer.from('secret message');
const priv = crypto.createPrivateKey(fx.rsaPriv);
const pub = crypto.createPublicKey(fx.rsaPub);
const { RSA_PKCS1_PADDING, RSA_NO_PADDING, RSA_PKCS1_OAEP_PADDING, RSA_PKCS1_PSS_PADDING } = { RSA_PKCS1_PADDING: 1, RSA_NO_PADDING: 3, RSA_PKCS1_OAEP_PADDING: 4, RSA_PKCS1_PSS_PADDING: 6 };

function outcome(f) {
  try {
    const v = f();
    return { value: Buffer.isBuffer(v) ? v.toString('hex') : v };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

// Round trips, and Node's ciphertexts decrypted.
const roundTrip = (encOpts, decOpts) => crypto.privateDecrypt({ key: priv, ...decOpts }, crypto.publicEncrypt({ key: pub, ...encOpts }, msg)).toString();
results.oaep = outcome(() => [crypto.publicEncrypt(pub, msg).length, roundTrip({}, {}), crypto.privateDecrypt(priv, Buffer.from(fx.oaep, 'base64')).toString()]);
results.oaep256 = outcome(() => [roundTrip({ oaepHash: 'sha256' }, { oaepHash: 'sha256' }), crypto.privateDecrypt({ key: priv, oaepHash: 'sha256' }, Buffer.from(fx.oaep256, 'base64')).toString()]);
results.oaepLabel = outcome(() => [roundTrip({ oaepHash: 'sha256', oaepLabel: Buffer.from('label') }, { oaepHash: 'sha256', oaepLabel: Buffer.from('label') }),
  crypto.privateDecrypt({ key: priv, oaepHash: 'sha256', oaepLabel: Buffer.from('label') }, Buffer.from(fx.oaepLabel, 'base64')).toString()]);
results.oaepWrongLabel = outcome(() => crypto.privateDecrypt({ key: priv, oaepHash: 'sha256', oaepLabel: Buffer.from('other') }, Buffer.from(fx.oaepLabel, 'base64')));
results.oaepWrongHash = outcome(() => crypto.privateDecrypt(priv, Buffer.from(fx.oaep256, 'base64')));
results.pkcs1Encrypt = outcome(() => crypto.publicEncrypt({ key: pub, padding: RSA_PKCS1_PADDING }, msg).length);
results.pkcs1PrivateDecrypt = outcome(() => crypto.privateDecrypt({ key: priv, padding: RSA_PKCS1_PADDING }, crypto.publicEncrypt({ key: pub, padding: RSA_PKCS1_PADDING }, msg)));
results.withPrivateAsPublic = outcome(() => crypto.privateDecrypt(priv, crypto.publicEncrypt(priv, msg)).toString());
results.fromPem = outcome(() => crypto.privateDecrypt(fx.rsaPriv, crypto.publicEncrypt(fx.rsaPub, msg)).toString());
results.stringData = outcome(() => crypto.privateDecrypt(priv, crypto.publicEncrypt(pub, 'secret message')).toString());

// Deterministic: private encryption and unpadded operations.
results.privateEncrypt = outcome(() => crypto.privateEncrypt(priv, msg));
results.publicDecrypt = outcome(() => crypto.publicDecrypt(pub, crypto.privateEncrypt(priv, msg)).toString());
results.publicDecryptWithPrivate = outcome(() => crypto.publicDecrypt(priv, crypto.privateEncrypt(priv, msg)).toString());
results.noPaddingPrivateEncrypt = outcome(() => crypto.privateEncrypt({ key: priv, padding: RSA_NO_PADDING }, Buffer.alloc(128, 1)));
results.noPaddingPublicEncrypt = outcome(() => crypto.publicEncrypt({ key: pub, padding: RSA_NO_PADDING }, Buffer.alloc(128, 1)));
results.noPaddingRoundTrip = outcome(() => crypto.privateDecrypt({ key: priv, padding: RSA_NO_PADDING },
  crypto.publicEncrypt({ key: pub, padding: RSA_NO_PADDING }, Buffer.alloc(128, 7))).equals(Buffer.alloc(128, 7)));
results.noPaddingShortDecrypt = outcome(() => crypto.privateDecrypt({ key: priv, padding: RSA_NO_PADDING }, Buffer.alloc(127, 1)).length);

// Errors.
results.errors = {
  oaepTooLong: outcome(() => crypto.publicEncrypt(pub, Buffer.alloc(87))),
  oaepLongest: outcome(() => crypto.publicEncrypt(pub, Buffer.alloc(86)).length),
  pkcs1TooLong: outcome(() => crypto.publicEncrypt({ key: pub, padding: RSA_PKCS1_PADDING }, Buffer.alloc(118))),
  privateEncryptTooLong: outcome(() => crypto.privateEncrypt(priv, Buffer.alloc(118))),
  privateEncryptLongest: outcome(() => crypto.privateEncrypt(priv, Buffer.alloc(117)).length),
  noPaddingShort: outcome(() => crypto.privateEncrypt({ key: priv, padding: RSA_NO_PADDING }, msg)),
  noPaddingTooLarge: outcome(() => crypto.publicEncrypt({ key: pub, padding: RSA_NO_PADDING }, Buffer.alloc(128, 0xff))),
  noPaddingDecryptTooLarge: outcome(() => crypto.privateDecrypt({ key: priv, padding: RSA_NO_PADDING }, Buffer.alloc(128, 0xff))),
  oaepGarbage: outcome(() => crypto.privateDecrypt(priv, Buffer.alloc(128))),
  publicDecryptZero: outcome(() => crypto.publicDecrypt(pub, Buffer.alloc(5))),
  publicDecryptRandom: outcome(() => crypto.publicDecrypt(pub, Buffer.alloc(128, 1))),
  publicDecryptTooLarge: outcome(() => crypto.publicDecrypt(pub, Buffer.alloc(128, 0xff))),
  publicKeyToPrivateDecrypt: outcome(() => crypto.privateDecrypt(pub, Buffer.from(fx.oaep, 'base64'))),
  publicKeyToPrivateEncrypt: outcome(() => crypto.privateEncrypt(pub, msg)),
  numberBuffer: outcome(() => crypto.publicEncrypt(pub, 5)),
  badOaepHash: outcome(() => crypto.publicEncrypt({ key: pub, oaepHash: 'nope' }, msg)),
  numberOaepHash: outcome(() => crypto.publicEncrypt({ key: pub, oaepHash: 5 }, msg)),
  pssPadding: outcome(() => crypto.publicEncrypt({ key: pub, padding: RSA_PKCS1_PSS_PADDING }, msg)),
  ecKey: outcome(() => crypto.publicEncrypt(crypto.createPublicKey(fx.ecPub), msg)),
};

if (globalThis.done) done(results);
else process.stdout.write(JSON.stringify(results));
