'use strict';
// Exercise axios on Orbital against the REAL network — the whole point of having
// axios as a core package is validating Orbital's outbound HTTP/HTTPS client
// stack (TLS, DNS, redirects, streaming, JSON handling) with a real-world
// consumer. It hits stable public endpoints over both https and http, follows a
// cross-scheme redirect, and checks error handling on a real 404.
//
// Run from the checked-out axios repo after `npm run build` (cwd == checkout);
// the built CommonJS bundle lives at dist/node/axios.cjs.
const path = require('path');
const assert = require('assert');

const axios = require(path.join(process.cwd(), 'dist', 'node', 'axios.cjs'));

const UA = { 'User-Agent': 'orbital-core-package-test' };
const TIMEOUT = 20000;

// --- static / config surface (no network) ---
assert.strictEqual(typeof axios, 'function', 'axios should be callable');
assert.strictEqual(typeof axios.create, 'function');
assert.strictEqual(typeof axios.get, 'function');
assert.strictEqual(typeof axios.isAxiosError, 'function');
assert.ok(axios.AxiosError, 'AxiosError export');
assert.ok(axios.AxiosHeaders, 'AxiosHeaders export');
console.log('axios static surface OK');

async function main() {
  // 1. HTTPS: real TLS handshake + real DNS against the canonical, stable,
  //    rate-limit-free endpoint.
  const ex = await axios.get('https://example.com', { headers: UA, timeout: TIMEOUT });
  assert.strictEqual(ex.status, 200, 'example.com should return 200');
  assert.ok(/Example Domain/.test(ex.data || ''), 'expected the example.com body');
  console.log('axios HTTPS GET https://example.com OK (200)');

  // 2. HTTPS + JSON: exercises JSON response parsing on a real API.
  const gh = await axios.get('https://api.github.com', { headers: UA, timeout: TIMEOUT });
  assert.strictEqual(gh.status, 200, 'api.github.com should return 200');
  assert.ok(
    /application\/json/.test(gh.headers['content-type'] || ''),
    'api.github.com should return JSON'
  );
  assert.ok(gh.data && typeof gh.data.current_user_url === 'string', 'expected GitHub API JSON body');
  console.log('axios HTTPS GET https://api.github.com OK (200, JSON)');

  // 3. Plain HTTP over port 80 (httpforever.com is designed to stay HTTP).
  const hf = await axios.get('http://httpforever.com', { headers: UA, timeout: TIMEOUT });
  assert.strictEqual(hf.status, 200, 'httpforever.com should return 200');
  assert.ok((hf.data || '').length > 0, 'expected a non-empty HTTP body');
  console.log('axios HTTP GET http://httpforever.com OK (200)');

  // 4. Cross-scheme redirect following: http://cloudflare.com -> https 200.
  const cf = await axios.get('http://cloudflare.com', { headers: UA, timeout: TIMEOUT, maxRedirects: 5 });
  assert.strictEqual(cf.status, 200, 'cloudflare.com should end at 200 after redirect');
  console.log('axios redirect follow http://cloudflare.com -> 200 OK');

  // 5. Real error handling: a 404 from a real server should reject with an
  //    AxiosError carrying the response.
  let threw = false;
  try {
    await axios.get('https://api.github.com/orbital-does-not-exist-xyz', { headers: UA, timeout: TIMEOUT });
  } catch (e) {
    threw = true;
    assert.ok(axios.isAxiosError(e), 'error should be an AxiosError');
    assert.ok(e.response && e.response.status === 404, 'should carry the real 404 response');
  }
  assert.ok(threw, 'expected a rejection for the real 404');
  console.log('axios real 404 rejection OK');

  console.log('ALL AXIOS NETWORK TESTS PASSED');
}

main().catch((e) => {
  console.error('AXIOS TEST FAILED:', (e && (e.code || e.message)) || e);
  if (e && e.stack) console.error(e.stack.split('\n').slice(0, 5).join('\n'));
  process.exitCode = 1;
});
