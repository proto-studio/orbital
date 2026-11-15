// Runs unchanged in Node and in Orbital. tls_duplex.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestTLSSocket_WrapsAnyDuplex'
//
// TLS over streams that aren't sockets: an in-memory pair with a TLS server on the other
// end, and a forwarding stream in front of a real connection, as a proxy tunnel is.
const tls = require('tls');
const net = require('net');
const { Duplex } = require('stream');

// A P-256 key and its self-signed certificate for localhost, valid until 2126.
const cert = `-----BEGIN CERTIFICATE-----
MIIBljCCATugAwIBAgIUXF7T2ue8t25rM09Sx40Jy4D1kRAwCgYIKoZIzj0EAwIw
FDESMBAGA1UEAwwJbG9jYWxob3N0MCAXDTI2MTExNTE4NDEzMFoYDzIxMjYxMDIy
MTg0MTMwWjAUMRIwEAYDVQQDDAlsb2NhbGhvc3QwWTATBgcqhkjOPQIBBggqhkjO
PQMBBwNCAAQGfSpCwmwWM9FHBzTxziYE6z1GNg39BIC39/tUYzWw0LHog3SBjJo6
RH0NB50sBrFv6f28fWVYYa1QNLD9Yx3Yo2kwZzAdBgNVHQ4EFgQUyYimuy54M9bY
kG6+66C2G8LN7bUwHwYDVR0jBBgwFoAUyYimuy54M9bYkG6+66C2G8LN7bUwDwYD
VR0TAQH/BAUwAwEB/zAUBgNVHREEDTALgglsb2NhbGhvc3QwCgYIKoZIzj0EAwID
SQAwRgIhALr8XFe2VUyypuYsRxqwPDqHxXnICj60cWNqrLgam8ETAiEAgLmGL2od
r9r9IDpU7sWD/omgBVI62rGpSJdpI+dmeeQ=
-----END CERTIFICATE-----`;
const key = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgj9Rvgs52DuP51mh6
DgzUwWrqXSS9WX3o7KR7pMzp/omhRANCAAQGfSpCwmwWM9FHBzTxziYE6z1GNg39
BIC39/tUYzWw0LHog3SBjJo6RH0NB50sBrFv6f28fWVYYa1QNLD9Yx3Y
-----END PRIVATE KEY-----`;

function pair() {
  let a, b;
  a = new Duplex({ read() {}, write(c, e, cb) { b.push(c); cb(); }, final(cb) { b.push(null); cb(); } });
  b = new Duplex({ read() {}, write(c, e, cb) { a.push(c); cb(); }, final(cb) { a.push(null); cb(); } });
  return [a, b];
}
async function inMemory() {
  const [a, b] = pair();
  const log = [];
  const server = new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) });
  server.on('secure', () => log.push('server secure'));
  server.on('data', (d) => { log.push('server got ' + d); server.end('pong'); });
  const client = tls.connect({ socket: a, servername: 'localhost', ca: cert }, () => {
    log.push('client secure ' + client.authorized + ' ' + client.getProtocol() + ' encrypted=' + client.encrypted);
    client.write('ping');
  });
  let got = '';
  client.on('data', (d) => { got += d; });
  await new Promise((r) => { client.on('end', r); client.on('close', r); });
  log.push('client got ' + got);
  return log;
}
async function overNetSocket() {
  const server = tls.createServer({ cert, key }, (s) => s.end('hello'));
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const raw = net.connect(server.address().port, '127.0.0.1');
  // A stream that only forwards, like a proxy tunnel.
  const tunnel = new Duplex({
    read() { raw.resume(); },
    write(c, e, cb) { raw.write(c, cb); },
    final(cb) { raw.end(); cb(); },
  });
  raw.on('data', (d) => { if (!tunnel.push(d)) raw.pause(); });
  raw.on('end', () => tunnel.push(null));
  const out = await new Promise((resolve) => {
    const c = tls.connect({ socket: tunnel, servername: 'localhost', ca: cert });
    let got = '';
    c.on('secureConnect', () => { got += '[secure ' + c.authorized + ' remote=' + c.remoteAddress + ']'; });
    c.on('data', (d) => { got += d; });
    c.on('end', () => resolve(got));
    c.on('error', (e) => resolve('error ' + e.code));
  });
  server.close();
  raw.destroy();
  return out;
}
async function badCert() {
  const [a, b] = pair();
  new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) }).on('error', () => {});
  return new Promise((resolve) => {
    const c = tls.connect({ socket: a, servername: 'localhost' });
    c.on('secureConnect', () => resolve('connected'));
    c.on('error', (e) => resolve(e.code));
  });
}
(async () => {
  const out = { inMemory: await inMemory(), overNetSocket: await overNetSocket(), badCert: await badCert() };
  if (globalThis.done) done(out);
  else process.stdout.write(JSON.stringify(out));
})();
