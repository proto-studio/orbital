// Runs unchanged in Node and in Orbital. webcrypto_jwk.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestWebCryptoJWKImport_MatchesNode'
const s = globalThis.crypto.subtle;
const results = {};

async function main() {
  const ec = await s.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
  const ecPub = await s.exportKey('jwk', ec.publicKey), ecPriv = await s.exportKey('jwk', ec.privateKey);
  const rsa = await s.generateKey({ name: 'RSASSA-PKCS1-v1_5', modulusLength: 1024, publicExponent: new Uint8Array([1,0,1]), hash: 'SHA-256' }, true, ['sign', 'verify']);
  const rsaPub = await s.exportKey('jwk', rsa.publicKey), rsaPriv = await s.exportKey('jwk', rsa.privateKey);
  const ed = await s.generateKey({ name: 'Ed25519' }, true, ['sign', 'verify']);
  const edPub = await s.exportKey('jwk', ed.publicKey), edPriv = await s.exportKey('jwk', ed.privateKey);
  const x = await s.generateKey({ name: 'X25519' }, true, ['deriveBits']);
  const xPub = await s.exportKey('jwk', x.publicKey);
  const b = (n) => Buffer.alloc(n, 7).toString('base64url');
  const strip = (j, ...f) => { const o = { ...j }; for (const k of f) delete o[k]; return o; };
  const ECDSA = { name: 'ECDSA', namedCurve: 'P-256' }, RS = { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' };
  const ec2 = await s.generateKey({ name: 'ECDH', namedCurve: 'P-384' }, true, ['deriveBits']);
  const ec2Pub = await s.exportKey('jwk', ec2.publicKey), ec2Priv = await s.exportKey('jwk', ec2.privateKey);
  const ed2 = await s.generateKey({ name: 'Ed25519' }, true, ['sign', 'verify']);
  const ed2Pub = await s.exportKey('jwk', ed2.publicKey);
  const k16 = b(16), k32 = b(32);
  const ECDH = { name: 'ECDH', namedCurve: 'P-384' };
  const cases = {
    ec_ok: [ecPub, ECDSA, ['verify'], true],
    ec_x31: [{ ...ecPub, x: b(31) }, ECDSA, ['verify'], true],
    ec_x33: [{ ...ecPub, x: b(33) }, ECDSA, ['verify'], true],
    ec_offcurve: [{ ...ecPub, x: b(32), y: b(32) }, ECDSA, ['verify'], true],
    ec_skippedchars: [{ ...ecPub, x: ecPub.x.slice(0, 10) + '!*' + ecPub.x.slice(10) }, ECDSA, ['verify'], true],
    ec_padded: [{ ...ecPub, x: ecPub.x + '=' }, ECDSA, ['verify'], true],
    ec_crv_mismatch: [{ ...ecPub, crv: 'P-384' }, ECDSA, ['verify'], true],
    ec_crv_bad: [{ ...ecPub, crv: 'P-999' }, ECDSA, ['verify'], true],
    ec_no_y: [strip(ecPub, 'y'), ECDSA, ['verify'], true],
    ec_d31: [{ ...ecPriv, d: b(31) }, ECDSA, ['sign'], true],
    ec_d_mismatch: [{ ...ecPriv, d: b(32) }, ECDSA, ['sign'], true],
    ec_kty_rsa: [rsaPub, ECDSA, ['verify'], true],
    ec_kty_missing: [strip(ecPub, 'kty'), ECDSA, ['verify'], true],
    ec_kty_bad: [{ ...ecPub, kty: 'XX' }, ECDSA, ['verify'], true],
    ec_use_enc: [{ ...ecPub, use: 'enc' }, ECDSA, ['verify'], true],
    ec_use_sig: [{ ...ecPub, use: 'sig' }, ECDSA, ['verify'], true],
    ec_keyops_mismatch: [{ ...ecPub, key_ops: ['encrypt'] }, ECDSA, ['verify'], true],
    ec_ext_false: [{ ...ecPub, ext: false }, ECDSA, ['verify'], true],
    ec_alg_bad: [{ ...ecPub, alg: 'ES384' }, ECDSA, ['verify'], true],
    ec_x_number: [{ ...ecPub, x: 5 }, ECDSA, ['verify'], true],
    rsa_ok: [rsaPub, RS, ['verify'], true],
    rsa_n_badchars: [{ ...rsaPub, n: rsaPub.n.slice(0, -2) + '**' }, RS, ['verify'], true],
    rsa_e_empty: [{ ...rsaPub, e: '' }, RS, ['verify'], true],
    rsa_no_e: [strip(rsaPub, 'e'), RS, ['verify'], true],
    rsa_priv_no_p: [strip(rsaPriv, 'p'), RS, ['sign'], true],
    rsa_priv_only_d: [strip(rsaPriv, 'p', 'q', 'dp', 'dq', 'qi'), RS, ['sign'], true],
    rsa_priv_bad_d: [{ ...rsaPriv, d: b(128) }, RS, ['sign'], true],
    rsa_alg_mismatch: [{ ...rsaPub, alg: 'RS384' }, RS, ['verify'], true],
    rsa_alg_ok: [{ ...rsaPub, alg: 'RS256' }, RS, ['verify'], true],
    rsa_small_n: [{ ...rsaPub, n: b(8) }, RS, ['verify'], true],
    ed_ok: [edPub, 'Ed25519', ['verify'], true],
    ed_x31: [{ ...edPub, x: b(31) }, 'Ed25519', ['verify'], true],
    ed_crv_x25519: [{ ...edPub, crv: 'X25519' }, 'Ed25519', ['verify'], true],
    ed_d31: [{ ...edPriv, d: b(31) }, 'Ed25519', ['sign'], true],
    ed_d_mismatch: [{ ...edPriv, d: b(32) }, 'Ed25519', ['sign'], true],
    ed_kty_ec: [{ ...edPub, kty: 'EC' }, 'Ed25519', ['verify'], true],
    x_ok: [xPub, 'X25519', [], true],
    x_x33: [{ ...xPub, x: b(33) }, 'X25519', [], true],
    hmac_ok: [{ kty: 'oct', k: b(32) }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    hmac_badchars: [{ kty: 'oct', k: 'ab+/' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    hmac_empty: [{ kty: 'oct', k: '' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    hmac_alg_mismatch: [{ kty: 'oct', k: b(32), alg: 'HS384' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    hmac_length_mismatch: [{ kty: 'oct', k: b(32) }, { name: 'HMAC', hash: 'SHA-256', length: 128 }, ['sign'], true],
    aes_ok: [{ kty: 'oct', k: b(16) }, 'AES-GCM', ['encrypt'], true],
    aes_len15: [{ kty: 'oct', k: b(15) }, 'AES-GCM', ['encrypt'], true],
    aes_alg_mismatch: [{ kty: 'oct', k: b(16), alg: 'A256GCM' }, 'AES-GCM', ['encrypt'], true],
    aes_kty_rsa: [rsaPub, 'AES-GCM', ['encrypt'], true],
    aes_no_k: [{ kty: 'oct' }, 'AES-GCM', ['encrypt'], true],
    ecdh_ok: [ec2Pub, ECDH, [], true],
    ecdh_priv_ok: [ec2Priv, ECDH, ['deriveBits'], true],
    ecdh_alg_any: [{ ...ec2Pub, alg: 'whatever' }, ECDH, [], true],
    ecdh_use_sig_empty_usages: [{ ...ec2Pub, use: 'sig' }, ECDH, [], true],
    ecdh_use_sig_with_usages: [{ ...ec2Priv, use: 'sig' }, ECDH, ['deriveBits'], true],
    ecdh_use_enc_with_usages: [{ ...ec2Priv, use: 'enc' }, ECDH, ['deriveBits'], true],
    ecdh_priv_d_wrong: [{ ...ec2Priv, d: b(48) }, ECDH, ['deriveBits'], true],
    ecdh_priv_d_zero: [{ ...ec2Priv, d: Buffer.alloc(48).toString('base64url') }, ECDH, ['deriveBits'], true],
    ecdsa384_alg_ok: [{ ...ec2Pub, alg: 'ES384' }, { name: 'ECDSA', namedCurve: 'P-384' }, ['verify'], true],
    keyops_dup: [{ kty: 'oct', k: k16, key_ops: ['encrypt', 'encrypt'] }, 'AES-GCM', ['encrypt'], true],
    keyops_notarray: [{ kty: 'oct', k: k16, key_ops: 'encrypt' }, 'AES-GCM', ['encrypt'], true],
    keyops_superset: [{ kty: 'oct', k: k16, key_ops: ['encrypt', 'decrypt'] }, 'AES-GCM', ['encrypt'], true],
    keyops_bogus: [{ kty: 'oct', k: k16, key_ops: ['encrypt', 'bogus'] }, 'AES-GCM', ['encrypt'], true],
    ext_false_nonextract: [{ kty: 'oct', k: k16, ext: false }, 'AES-GCM', ['encrypt'], false],
    ext_string: [{ kty: 'oct', k: k16, ext: 'false' }, 'AES-GCM', ['encrypt'], true],
    use_aes_sig: [{ kty: 'oct', k: k16, use: 'sig' }, 'AES-GCM', ['encrypt'], true],
    use_hmac_enc: [{ kty: 'oct', k: k16, use: 'enc' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    use_bogus: [{ kty: 'oct', k: k16, use: 'xyz' }, 'AES-GCM', ['encrypt'], true],
    aes_a128gcm: [{ kty: 'oct', k: k16, alg: 'A128GCM' }, 'AES-GCM', ['encrypt'], true],
    aes_a128cbc: [{ kty: 'oct', k: k16, alg: 'A128CBC' }, 'AES-CBC', ['encrypt'], true],
    aes_a128ctr: [{ kty: 'oct', k: k16, alg: 'A128CTR' }, 'AES-CTR', ['encrypt'], true],
    aes_a128kw: [{ kty: 'oct', k: k16, alg: 'A128KW' }, 'AES-KW', ['wrapKey'], true],
    aes_cbc_gcmalg: [{ kty: 'oct', k: k16, alg: 'A128GCM' }, 'AES-CBC', ['encrypt'], true],
    aes_len_param: [{ kty: 'oct', k: k16 }, { name: 'AES-GCM', length: 256 }, ['encrypt'], true],
    aes_badchars: [{ kty: 'oct', k: k16.slice(0, -2) + '!!' }, 'AES-GCM', ['encrypt'], true],
    aes_k_number: [{ kty: 'oct', k: 5 }, 'AES-GCM', ['encrypt'], true],
    hmac_hs256: [{ kty: 'oct', k: k32, alg: 'HS256' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    hmac_hs1: [{ kty: 'oct', k: k32, alg: 'HS1' }, { name: 'HMAC', hash: 'SHA-1' }, ['sign'], true],
    hmac_hs512: [{ kty: 'oct', k: k32, alg: 'HS512' }, { name: 'HMAC', hash: 'SHA-512' }, ['sign'], true],
    hmac_len_param: [{ kty: 'oct', k: k32 }, { name: 'HMAC', hash: 'SHA-256', length: 256 }, ['sign'], true],
    hmac_no_k: [{ kty: 'oct' }, { name: 'HMAC', hash: 'SHA-256' }, ['sign'], true],
    rsa_oaep_alg: [{ kty: 'RSA', n: k32, e: 'AQAB', alg: 'RSA-OAEP-256' }, { name: 'RSA-OAEP', hash: 'SHA-256' }, ['encrypt'], true],
    rsa_oaep_alg1: [{ kty: 'RSA', n: k32, e: 'AQAB', alg: 'RSA-OAEP' }, { name: 'RSA-OAEP', hash: 'SHA-1' }, ['encrypt'], true],
    rsa_pss_alg: [{ kty: 'RSA', n: k32, e: 'AQAB', alg: 'PS384' }, { name: 'RSA-PSS', hash: 'SHA-384' }, ['verify'], true],
    rsa_rs1: [{ kty: 'RSA', n: k32, e: 'AQAB', alg: 'RS1' }, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-1' }, ['verify'], true],
    rsa_n_empty: [{ kty: 'RSA', n: '', e: 'AQAB' }, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-1' }, ['verify'], true],
    rsa_n_number: [{ kty: 'RSA', n: 5, e: 'AQAB' }, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-1' }, ['verify'], true],
    rsa_use_enc_sign: [{ kty: 'RSA', n: k32, e: 'AQAB', use: 'enc' }, { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-1' }, ['verify'], true],
    ed_alg_eddsa: [{ ...ed2Pub, alg: 'EdDSA' }, 'Ed25519', ['verify'], true],
    ed_alg_bad: [{ ...ed2Pub, alg: 'ES256' }, 'Ed25519', ['verify'], true],
    ed_use_enc: [{ ...ed2Pub, use: 'enc' }, 'Ed25519', ['verify'], true],
    ed_no_crv: [{ kty: 'OKP', x: ed2Pub.x }, 'Ed25519', ['verify'], true],
    jwk_string: ['abc', 'AES-GCM', ['encrypt'], true],
    jwk_null: [null, 'AES-GCM', ['encrypt'], true],
    order_kty_vs_ext: [{ kty: 'RSA', ext: false }, 'AES-GCM', ['encrypt'], true],
    order_alg_vs_len: [{ kty: 'oct', k: b(15), alg: 'A256GCM' }, 'AES-GCM', ['encrypt'], true],
    order_keyops_vs_use: [{ kty: 'oct', k: k16, use: 'sig', key_ops: ['decrypt'] }, 'AES-GCM', ['encrypt'], true],
    order_ext_vs_keyops: [{ kty: 'oct', k: k16, ext: false, key_ops: ['decrypt'] }, 'AES-GCM', ['encrypt'], true],
    order_ext_vs_alg: [{ kty: 'oct', k: k16, ext: false, alg: 'A256GCM' }, 'AES-GCM', ['encrypt'], true],
    order_crv_vs_use: [{ ...ec2Pub, crv: 'P-256', use: 'enc' }, { name: 'ECDSA', namedCurve: 'P-384' }, ['verify'], true],
    order_ec_alg_vs_x: [{ ...ec2Pub, x: b(3), alg: 'ES256' }, { name: 'ECDSA', namedCurve: 'P-384' }, ['verify'], true],
  };
  for (const [name, [jwk, algorithm, usages, extractable]] of Object.entries(cases)) {
    try {
      const key = await s.importKey('jwk', jwk, algorithm, extractable, usages);
      results[name] = { type: key.type };
    } catch (e) {
      results[name] = { name: e.name, message: e.message };
    }
  }

  // Members are stored as Node would re-encode them.
  const padded = await s.importKey('jwk', { ...ecPub, x: ecPub.x + '=' }, ECDSA, true, ['verify']);
  results.paddedExportsCanonical = (await s.exportKey('jwk', padded)).x === ecPub.x;
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
