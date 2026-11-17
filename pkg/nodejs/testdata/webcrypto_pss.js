// Runs unchanged in Node and in Orbital. webcrypto_pss.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestWebCryptoPSS_MatchesNode'
// The key and the signatures (empty salt and 32-byte salt) were made by Node for this test.
// Signing with saltLength 0 isn't offered (see TestWebCryptoPSS_SignWithEmptySalt), so only
// verification is compared here.
const { subtle } = globalThis.crypto;
const fx = {
 "jwk": {
  "key_ops": [
   "sign"
  ],
  "ext": true,
  "kty": "RSA",
  "n": "6eNNl08vVLTmTkq2su2lmDJWztjFAGflWjYd3X-eR9ssqL_pRXT6q_z2i3c2FyvldiSm2PnNpWdxC3nthoMJPL37SiGadye8T9bNsiPpmepjMD22t8DXnpdyl4Uka6MwdaxP1JQjeKJMLZtzGo8sqqI4yYEtK793FJc5kD1eEfk",
  "e": "AQAB",
  "d": "hWiYK4SJVOHStDm9uJYBbKzaAyZ2SGL62dggdOWEXSxRbvf4kRvGmC-0WZzoB5OMUTVazky-1mgIfSlRKPa5Xwr-cdd2IDov_CFEqVYpZMGPN220CpXQxzu_7lfTIPg5_S4winQ7b12Cem3Tm8hPOmNHmcbGzBv9EG3eCEfbk9k",
  "p": "_3H4T80E-ffmXL0vsZ_Ccs4FuxS0Pg9wgXuVw8hNWDG-UIT6GrQG5jfbe4VNAuqJ5D44ALCSiTd4VCr3F91scw",
  "q": "6mVY2LEqPjOVco2tVojTRPz975BwcIdqhkgFRudWTVScUhHg8o261vrxFVriTG0PukfkJMH-AyOZLIJxDst44w",
  "dp": "U-AJR7Kq8qUKd-MK2Qj7IRUy3oXi1QneyDVbLKkFdHMI6YlwUfpQ1aD7qWhQrCd01YP0gY144CNdmE1vSt_Crw",
  "dq": "Vv4EKnAeUHWOmrRQR9SCLJGEoVvHD0yC0ZF7O-Rx2EN1vNPb28mUNWNvBFD1ug2kD6vM1YOWBY45tBY5ZZdIpw",
  "qi": "Wnq46LpNe7yQeAeMwc8l961YTMA84W9kIfkPsrSOg_IiFl9cKFIHVZUstxzEDdqPnyPro8Pb6ge3OkqYbideZg",
  "alg": "PS256"
 },
 "s0": "Qcz5CqIvUzaOEHysn6A9hWXdIuLt7ZNBBctDE8LxGNsCPLX0IH5JbcCxBvrEPaOLJG5LKTFdHnAWjlRHLu7/ln8IMhZ1xDdJw6MFX43tYhUwNOwjEScuTARY8giSRLGOEGqf4N3tZkQ/f+h0a3mG56rLUzvOulFrb/t1trtWK2k=",
 "s32": "TUnnILFeAdmoxBTmIWSMMZYpxy4Oi8KAlsmdpvnCYcaUJJYuZaxdJscM+Am1+vkYjFt8KXTN99Z1PsYZ6Nq25iWhiczh5iiiWLXMoc+bH/DUXsovChf3l1AhkFDQxHQy09l5qi1+6+lAgwV81tbSp2EGUTeUuRNlL2Q1nzHU3Bg="
};
const data = new TextEncoder().encode('message to sign');
const hex = (b) => Buffer.from(b).toString('hex');

async function outcome(f) {
  try {
    const v = await f();
    return { value: v instanceof ArrayBuffer ? hex(v) : v };
  } catch (e) {
    return { name: e.name, message: e.message };
  }
}

async function main() {
  const algo = { name: 'RSA-PSS', hash: 'SHA-256' };
  const priv = await subtle.importKey('jwk', fx.jwk, algo, true, ['sign']);
  const { d, p, q, dp, dq, qi, ...pubJwk } = fx.jwk;
  const pub = await subtle.importKey('jwk', { ...pubJwk, key_ops: ['verify'] }, algo, true, ['verify']);
  const salted = Buffer.from(fx.s32, 'base64');
  const empty = Buffer.from(fx.s0, 'base64');
  const r = {};
  r.verifyEmptyWith0 = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: 0 }, pub, empty, data));
  r.verifyEmptyWith32 = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: 32 }, pub, empty, data));
  r.verifySaltedWith0 = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: 0 }, pub, salted, data));
  r.verifySaltedWith32 = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: 32 }, pub, salted, data));
  r.verifyOtherData = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: 0 }, pub, empty, new TextEncoder().encode('other')));
  r.saltString = await outcome(() => subtle.verify({ name: 'RSA-PSS', saltLength: '0' }, pub, empty, data));
  r.sign32 = await outcome(async () => subtle.verify({ name: 'RSA-PSS', saltLength: 32 }, pub, await subtle.sign({ name: 'RSA-PSS', saltLength: 32 }, priv, data), data));
  r.missingSalt = await outcome(() => subtle.sign({ name: 'RSA-PSS' }, priv, data));
  r.negativeSalt = await outcome(() => subtle.sign({ name: 'RSA-PSS', saltLength: -1 }, priv, data));
  r.missingSaltVerify = await outcome(() => subtle.verify({ name: 'RSA-PSS' }, pub, salted, data));
  return r;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
});
