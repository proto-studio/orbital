// Runs unchanged in Node and in Orbital. crypto_x509.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoX509_MatchesNode'
// The certificates were made with Go's crypto/x509 for this test: a CA, a leaf it issued
// with most extensions, a self-signed P-384 certificate with an unusual subject, and an
// Ed25519 certificate with alt names that need quoting.
const certs = {
 "ca": "-----BEGIN CERTIFICATE-----\nMIID6DCCAtCgAwIBAgICAQIwDQYJKoZIhvcNAQELBQAwgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTAg\nFw0yNDAxMDUwMzA0MDVaGA8yMDYwMDEwNTAzMDQwNVowgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTCC\nASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALyTBq5JpNxzLtVaJs8mrgkQ\njt2YNpnoNAEsZ1uPqmrULxKGKyVDDK3UQe7DcfAY54G90TVmw0oFL5Fh+QWndZFJ\nmvQIr819dYKdviH1W06/TkwpDy2WKMnfZ1wAKcl3VmVDgU7rEOqiobbcNkborFQH\n1HMfeSBJf15nrKznipPZ2bj7fsrcLK14bTNNcpzDpGN6Sh1ijMEIKkZPVhCdNCdj\noVxX8fNwipvpwcV69RnWB2EOdqZWlaLDlfKzp+Oqvq8l94GqiaND5l5d898rl5RS\nn7CsPwV3Q7cs8Ehy0HZf9dPDB2qfwHnL3fLPJtTH8wlKYVOIVdXkZie44p9+fKUC\nAwEAAaMyMDAwDgYDVR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wDQYDVR0O\nBAYEBAECAwQwDQYJKoZIhvcNAQELBQADggEBAJTBx3yRc2DI3ABXfP8mvRYImg8s\nDPhHwORZtZatcHGt5IsO55c0Oe2aTjTHLVp1GD7eG/8MKDyLjM12a7qYK31przNG\nknTui1mLL8CYuSoa7rh3aqCSMeo8U3tBUfc2k5EtZn63B8LMO7/M2QMZxfqto0mL\nPq2yZOJwuR9eXQhrp8+SbryA83gvPEJDKurEfmcoYXHush5CdI7lb+M0YJtbFsXD\nx6lMrvbq/eOb0A4T2+BL7rQ+Cry5jLz0OgozZBxFM9e126YuneK76ZSeZYEaTRQ+\n7LmKxcUP5V5NDwOrWe9RpotXyBHIX4eI7rDzt4aL/ngT+6P00JFPi04AZhQ=\n-----END CERTIFICATE-----\n",
 "caKey": "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQC8kwauSaTccy7V\nWibPJq4JEI7dmDaZ6DQBLGdbj6pq1C8ShislQwyt1EHuw3HwGOeBvdE1ZsNKBS+R\nYfkFp3WRSZr0CK/NfXWCnb4h9VtOv05MKQ8tlijJ32dcACnJd1ZlQ4FO6xDqoqG2\n3DZG6KxUB9RzH3kgSX9eZ6ys54qT2dm4+37K3CyteG0zTXKcw6RjekodYozBCCpG\nT1YQnTQnY6FcV/HzcIqb6cHFevUZ1gdhDnamVpWiw5Xys6fjqr6vJfeBqomjQ+Ze\nXfPfK5eUUp+wrD8Fd0O3LPBIctB2X/XTwwdqn8B5y93yzybUx/MJSmFTiFXV5GYn\nuOKffnylAgMBAAECggEABrXUUfKOGAfOQGp9QTepuj9ZV3J8QOpLIsk4eADgVNMD\nm3e5mdFtZlMhsdQec2eDd3Gwe+NnVXSNSTGwuZOIWA5D4hAGvNEh08R2uKvL3xJo\n1XDMQ42Zb846wJft5LzRzs0skQka2JVHHA7GggFIvpMVFi/7cLDE2ZsRVolvvDaU\nz3H1VsJU2xo2UwdXeUYsMhxwGjfsyuycmRBHueNU53N+Il62xdbdoIB55Cjkm5Sc\ndLB4eb37RLMep8xzqlEF3UhmboIqgNeP1/prHfcSb/R5NurKjpxc5oAp9+bN4zsF\nKm6huNCdJ1ea6CT0NbD2/AaRJkbedrLaPSJ4ocuOWQKBgQDACF9NRC65d5Ctqj9W\nkBq8ZpPS4cnQY475K2BCa3WCNnHbDK9Z6Z8lcmrToWNHJ65k+471FOBWgSXEc3jG\nHIgIPF09mg34g3MlVFvkUbkh5yxTEVEEtzklD3pMz9KnMgGIHKmFuYgMWlg3pI2y\nS0kbPXewFNj/bN+r0CvPnpwIbQKBgQD7Y71N+quzCFeKtUQsLGLt3b8iaCJRHXVw\nVWRt0H2kje5odgp6P1XeF3j6RjgR3FKR6BvSvYgHWMDOXkbzqOQH23gsyH1FaEuN\nLRHwoKoBpKuXShI46rJG2NUery4tA4ooNz7QAvb589frFnXjTO1kgg8abLSLLRyX\nlPk3mhISGQKBgQCLEsUqaTHJoj0GeODMKSsjvMpcFKebIy89Z1ucXsTIm/OTJ7mq\nzVYzwYx6XkxPEP7tJX5gIlFidL/MabBTEgC2rjj8zyBFFbYDl/fBACqq0gg41Lav\nSp65J190V6KA8edIEzH2pepQv/FZwvPX50w3zSlsPXz2Z3wJq4a/I2hvdQKBgQCQ\nN1bZjm3D5ow6IeU7JjeW2DiXZ4AQYsMFr6dOrGX36giUWuuF2CHNqE+Z3IC8FlY0\nKCEWE+8er9bkTQeNLx2qOEjIADiFgeCd2/ZHwhPln29k+nRbvKCLrkcNNFhcMISo\nr5DUSm8fU7lRv2/f9zjVCRva//PPSBD4ENmsATJOuQKBgC0OUjuK3ScltEcdxzRE\nz2TXGNDfRk0Ec5su2kGFtRdC/vZheztJVSujXMIu7A5ci4NTNkMc9GfO8KXuU6os\n/XSINwwyWPEAcX9tIux8KHDES79GsizP96Nlslfs5e+tpcMqQ4cA/AI4YIiaagCE\nqUlCITamQq19aBbSEDMwV4NB\n-----END PRIVATE KEY-----\n",
 "ed": "-----BEGIN CERTIFICATE-----\nMIIBRzCB+qADAgECAgEAMAUGAytlcDAXMRUwEwYDVQQDEwxlZDI1NTE5IHRlc3Qw\nHhcNMjQwMTA1MDMwNDA1WhcNMjQwMjA0MDMwNDA1WjAXMRUwEwYDVQQDEwxlZDI1\nNTE5IHRlc3QwKjAFBgMrZXADIQA5xo6Ufmi+uqhP1siKwAhLOOJwT49cAxvPAerp\nTNBctKNrMGkwDgYDVR0PAQH/BAQDAgeAMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0O\nBBYEFINd42PVAtOHk5HElYSxMwBj6StDMCcGA1UdEQQgMB6CC2EsYi5leGFtcGxl\ngg9xdW90ZSJkLmV4YW1wbGUwBQYDK2VwA0EAZ77pPe9cW4BKi1h0q43L2W7/zPAS\ndPN1iBi9j/sSUamUReGHJ5WWHZ1mD93LDGjYg1awRaKW05GrgBd0ns/yAA==\n-----END CERTIFICATE-----\n",
 "leaf": "-----BEGIN CERTIFICATE-----\nMIIDzDCCArSgAwIBAgIFAI8BAgMwDQYJKoZIhvcNAQELBQAwgZsxCzAJBgNVBAYT\nAlVTMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNV\nBAoTCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UE\nAxMPRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNv\nbTAeFw0yNDAxMDUwMzA0MDVaFw0yNDAyMDkxMzE0MTVaMEMxCzAJBgNVBAYTAlVT\nMRowGAYDVQQKDBFFeGFtcGxlLCAiQ28iIDx4PjEYMBYGA1UEAxMPd3d3LmV4YW1w\nbGUuY29tMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE/3De3WWgeDTNtwK6JZUg\ngfBPA4t2bVensjeDRWcpkCOw7oDXahaF782oFSTfD2itWB/M/luNE2IeH/daJi4A\nT6OCATcwggEzMA4GA1UdDwEB/wQEAwIHgDAdBgNVHSUEFjAUBggrBgEFBQcDAQYI\nKwYBBQUHAwIwDwYDVR0jBAgwBoAEAQIDBDBdBggrBgEFBQcBAQRRME8wIwYIKwYB\nBQUHMAGGF2h0dHA6Ly9vY3NwLmV4YW1wbGUuY29tMCgGCCsGAQUFBzAChhxodHRw\nOi8vY2EuZXhhbXBsZS5jb20vY2EuY3J0MIGRBgNVHREEgYkwgYaCD3d3dy5leGFt\ncGxlLmNvbYINKi5leGFtcGxlLm9yZ4IQZm9vKi5leGFtcGxlLm5ldIERYWRtaW5A\nZXhhbXBsZS5jb22HBH8AAAGHEAAAAAAAAAAAAAAAAAAAAAGHECABDbgAAAAAAAAA\nAAAAAAWGFWh0dHBzOi8vZXhhbXBsZS5jb20veDANBgkqhkiG9w0BAQsFAAOCAQEA\nqJYS1lMnjJ9eSztyzdU1BdA/kAa1JgexpN4vY7Q1gfF8ia7oVFxQofss8Yoctu7p\nTj+7JynhKAuTkfoF+6/a2ej4Ey3Hj+6qKVFQbu9VE1QBE9FHJmaXOqOBHyc6ZZHP\no7GSriR2g0wmNK2I0rmI48NhETEv18WUebUxYHA1kROId4LMKKZWpdqGWpuhr541\n5PO+OG6c/Wbd47nnbnUFp5D1M4DH0jlXg8RGFFKWlqmTlqqAKc/NDZbhSGPEC2Si\npY73+VLH7VNZJ5ohcCvg/VdCeOMK/5rdnwXYFNE9Ji7TADQSzZXcRk7SRi9LhCbf\noMjwQA77lcPZHLIqvscUJA==\n-----END CERTIFICATE-----\n",
 "leafKey": "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgDcGqlw9AG2gCsHob\n9C958+5Ge/wuevpIxBBfIRHRLR2hRANCAAT/cN7dZaB4NM23ArollSCB8E8Di3Zt\nV6eyN4NFZymQI7DugNdqFoXvzagVJN8PaK1YH8z+W40TYh4f91omLgBP\n-----END PRIVATE KEY-----\n",
 "odd": "-----BEGIN CERTIFICATE-----\nMIICGTCCAZ6gAwIBAgIBBzAKBggqhkjOPQQDAzB2MRkwFwYDVQQDExBob3N0LmV4\nYW1wbGUuY29tMR4wHAYDVQQKDBVaw7xyaWNoICsgU8O2aG5lOyAjMSAxFzAVBgoJ\nkiaJk/IsZAEZEwdleGFtcGxlMQ4wDAYDVQQFEwVTTi00MjEQMA4GBCoDBAUTBmN1\nc3RvbTAgFw0yNDAxMDUwMzA0MDVaGA8yMDUxMDYwNzA4MDkxMFowdjEZMBcGA1UE\nAxMQaG9zdC5leGFtcGxlLmNvbTEeMBwGA1UECgwVWsO8cmljaCArIFPDtmhuZTsg\nIzEgMRcwFQYKCZImiZPyLGQBGRMHZXhhbXBsZTEOMAwGA1UEBRMFU04tNDIxEDAO\nBgQqAwQFEwZjdXN0b20wdjAQBgcqhkjOPQIBBgUrgQQAIgNiAARmKfxhF841WodS\nJtAcK4qFJCLxsWq/iNV3M3Aae++Rc8mS5uwd+o2z8NWqf33UTW38sAxsKCb7uwvC\nhepRNucizPYRkh7Vt9l5+Ej8HOQzmunYpyg5rlCTb/M5QYmmMvowCgYIKoZIzj0E\nAwMDaQAwZgIxAP5KxIuAd8EZQKVA1eVX4zHRqx8EcOd+QLxTP+uBLm8fHHSUEWjm\nQvce7B/BxiJBUwIxAIHKB4tQq60H8pGAW2W4x1yNdba6TgMbNH9v2fCIBLya5XdA\nv5ohNqxp6s3mEhqHbw==\n-----END CERTIFICATE-----\n"
};

const crypto = require('crypto');
const { X509Certificate } = crypto;
const results = {};

function show(value) {
  if (Buffer.isBuffer(value)) return 'Buffer ' + value.toString('hex');
  if (value === undefined) return 'undefined';
  if (value instanceof crypto.KeyObject) return 'KeyObject ' + value.type + ' ' + value.asymmetricKeyType;
  return value;
}

function outcome(f) {
  try {
    return { value: show(f()) };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

const parsed = {};
for (const name of ['ca', 'leaf', 'odd', 'ed']) {
  const c = parsed[name] = new X509Certificate(certs[name]);
  const r = {};
  for (const k of ['ca', 'fingerprint', 'fingerprint256', 'fingerprint512', 'issuer', 'infoAccess', 'keyUsage',
    'serialNumber', 'subject', 'subjectAltName', 'validFrom', 'validTo', 'issuerCertificate', 'publicKey', 'raw']) {
    r[k] = outcome(() => c[k]);
  }
  r.toString = c.toString() === certs[name];
  r.toJSON = c.toJSON() === certs[name];
  r.legacy = outcome(() => {
    const l = c.toLegacyObject();
    const o = {};
    for (const [k, v] of Object.entries(l)) o[k] = show(v);
    return o;
  });
  results[name] = r;
}

const { ca, leaf, odd, ed } = parsed;
results.fromDer = outcome(() => new X509Certificate(leaf.raw).fingerprint256 === leaf.fingerprint256);
results.fromUint8Array = outcome(() => new X509Certificate(new Uint8Array(Buffer.from(certs.leaf))).serialNumber);
results.withLeadingText = outcome(() => new X509Certificate('junk\n' + certs.leaf).serialNumber);
results.bad = [outcome(() => new X509Certificate('nope')), outcome(() => new X509Certificate(Buffer.from('xx'))),
  outcome(() => new X509Certificate(5)), outcome(() => new X509Certificate()),
  // OpenSSL's reason for bad DER depends on where it fails; only the class is compared.
  (() => { try { new X509Certificate(certs.leaf.replace('CERTIFICATE-----\n', 'CERTIFICATE-----\nAAAA')); return 'parsed'; } catch (e) { return e.name; } })()];

results.checkIssued = [leaf.checkIssued(ca), ca.checkIssued(leaf), ca.checkIssued(ca), odd.checkIssued(odd), leaf.checkIssued(odd), ed.checkIssued(ed)];
results.checkIssuedBad = outcome(() => leaf.checkIssued('x'));
results.verify = [leaf.verify(ca.publicKey), leaf.verify(leaf.publicKey), ca.verify(ca.publicKey), ed.verify(ed.publicKey), odd.verify(odd.publicKey)];
// The message for a key of the wrong type quotes util.inspect of the key, which differs.
const codeOnly = (o) => (o.code ? { name: o.name, code: o.code } : o);
results.verifyBad = [outcome(() => leaf.verify('x')), codeOnly(outcome(() => leaf.verify(crypto.createPrivateKey(certs.caKey))))];
results.checkPrivateKey = [leaf.checkPrivateKey(crypto.createPrivateKey(certs.leafKey)), leaf.checkPrivateKey(crypto.createPrivateKey(certs.caKey)),
  ca.checkPrivateKey(crypto.createPrivateKey(certs.caKey))];
results.checkPrivateKeyBad = [outcome(() => leaf.checkPrivateKey('x')), codeOnly(outcome(() => leaf.checkPrivateKey(ca.publicKey)))];

const hosts = ['www.example.com', 'WWW.Example.COM', 'a.example.org', 'b.a.example.org', 'example.org', 'example.com',
  'foo1.example.net', 'foo.example.net', 'x.example.net', 'host.example.com', 'Example Root CA', '127.0.0.1', '', 'www.example.com.'];
results.checkHost = {};
for (const h of hosts) {
  results.checkHost[h] = [leaf, odd, ca, ed].map((c) => show(c.checkHost(h)));
}
results.checkHostOptions = {
  noWildcards: show(leaf.checkHost('a.example.org', { wildcards: false })),
  noPartial: show(leaf.checkHost('foo1.example.net', { partialWildcards: false })),
  multiLabel: show(leaf.checkHost('b.a.example.org', { multiLabelWildcards: true })),
  subjectAlways: show(leaf.checkHost('www.example.com', { subject: 'always' })),
  subjectNever: show(odd.checkHost('host.example.com', { subject: 'never' })),
  subjectAlwaysOdd: show(odd.checkHost('host.example.com', { subject: 'always' })),
  singleLabel: show(leaf.checkHost('.example.org', { singleLabelSubdomains: true })),
};
results.checkHostBad = [outcome(() => leaf.checkHost(5)), outcome(() => leaf.checkHost('x', { subject: 'bogus' })),
  outcome(() => leaf.checkHost('x', { wildcards: 'yes' })), outcome(() => leaf.checkHost('x', 5))];
results.checkEmail = ['admin@example.com', 'ADMIN@EXAMPLE.COM', 'Admin@example.com', 'other@example.com', 'ca@example.com']
  .map((e) => [leaf, ca].map((c) => show(c.checkEmail(e))));
results.checkEmailSubject = [show(ca.checkEmail('ca@example.com', { subject: 'always' })), show(ca.checkEmail('ca@example.com', { subject: 'never' }))];
results.checkEmailBad = outcome(() => leaf.checkEmail(5));
results.checkIP = ['127.0.0.1', '::1', '0:0:0:0:0:0:0:1', '2001:db8::5', '2001:DB8:0::5', '1.2.3.4', '::ffff:127.0.0.1']
  .map((ip) => show(leaf.checkIP(ip)));
results.checkIPBad = [outcome(() => leaf.checkIP('not an ip')), outcome(() => leaf.checkIP(5)), outcome(() => leaf.checkIP('127.0.0.1', {}))];

// Orbital lists the part of Node's lists it implements; each name it lists must work.
results.lists = {
  hashesInclude: ['md5', 'sha1', 'sha224', 'sha256', 'sha384', 'sha512', 'sha512-224', 'sha512-256', 'sha3-256',
    'RSA-SHA256', 'sha256WithRSAEncryption', 'id-rsassa-pkcs1-v1_5-with-sha3-512'].filter((h) => !crypto.getHashes().includes(h)),
  // Node lists shake128/256, which createHmac refuses.
  hashesWork: crypto.getHashes().filter((h) => !h.startsWith('shake')).filter((h) => {
    try { crypto.createHash(h).update('x').digest(); crypto.createHmac(h, 'k').update('x').digest(); return false; } catch { return true; }
  }),
  hashesSorted: crypto.getHashes().join() === crypto.getHashes().slice().sort().join(),
  curvesInclude: ['prime256v1', 'secp384r1', 'secp521r1'].filter((c) => !crypto.getCurves().includes(c)),
  curvesWork: crypto.getCurves().filter((c) => {
    try { crypto.generateKeyPairSync('ec', { namedCurve: c }); return false; } catch { return true; }
  }),
  ciphers: Array.isArray(crypto.getCiphers()),
};

// createHash and createHmac take Node's digest names.
const digests = {};
for (const name of ['sha256', 'SHA256', 'RSA-SHA256', 'sha-256', 'sha224', 'sha512-224', 'sha512-256', 'sha3-224', 'sha3-512', 'md5WithRSAEncryption', 'ssl3-sha1']) {
  digests[name] = [crypto.createHash(name).update('abc').digest('hex'), crypto.createHmac(name, 'key').update('abc').digest('hex')];
}
results.digests = digests;
results.digestErrors = [outcome(() => crypto.createHash('nope')), outcome(() => crypto.createHash(5)),
  outcome(() => crypto.createHmac('nope', 'k')), outcome(() => crypto.createHmac(5, 'k'))];
results.prototype = Object.getOwnPropertyNames(X509Certificate.prototype).sort();

if (globalThis.done) done(results);
else process.stdout.write(JSON.stringify(results));
