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
  // Each side runs on its own, so their events are kept apart.
  const server = [];
  const client = [];
  const serverSocket = new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) });
  serverSocket.on('secure', () => server.push('secure'));
  serverSocket.on('data', (d) => { server.push('got ' + d); serverSocket.end('pong'); });
  const socket = tls.connect({ socket: a, servername: 'localhost', ca: cert }, () => {
    client.push('secure ' + socket.authorized + ' ' + socket.getProtocol() + ' encrypted=' + socket.encrypted);
    socket.write('ping');
  });
  let got = '';
  socket.on('data', (d) => { got += d; });
  await new Promise((r) => socket.on('end', r));
  client.push('got ' + got);
  return { client, server };
}

async function slowStream() {
  // A stream that takes its time with every write still gets everything, in order, before
  // the TLS socket closes.
  const [a, b] = pair();
  const slow = new Duplex({
    read() {},
    write(c, e, cb) { setTimeout(() => a.write(c, cb), 5); },
    final(cb) { setTimeout(() => { a.end(); cb(); }, 5); },
  });
  a.on('data', (d) => slow.push(d));
  a.on('end', () => slow.push(null));
  const serverSocket = new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) });
  let received = 0;
  // The server finishes first, so the client closes as soon as its own writes are done.
  serverSocket.on('secure', () => serverSocket.end());
  serverSocket.on('data', (d) => { received += d.length; });
  const ended = new Promise((r) => serverSocket.on('end', r));
  const socket = tls.connect({ socket: slow, servername: 'localhost', ca: cert }, () => {
    for (let i = 0; i < 20; i++) socket.write(Buffer.alloc(1000, i));
    socket.end();
  });
  await ended;
  socket.destroy();
  serverSocket.destroy();
  return received;
}

async function halfOpenThenClosed() {
  // A half-open TLS socket still closes when its stream does.
  const [a, b] = pair();
  const serverSocket = new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) });
  serverSocket.on('secure', () => serverSocket.end('bye'));
  const events = [];
  const socket = tls.connect({ socket: a, servername: 'localhost', ca: cert, allowHalfOpen: true });
  socket.on('data', (d) => events.push('data ' + d));
  socket.on('end', () => { events.push('end'); setImmediate(() => a.destroy()); });
  await new Promise((r) => socket.on('close', r));
  events.push('close');
  serverSocket.destroy();
  return events;
}

async function pausedReader() {
  // While the TLS socket is paused, the stream under it is paused too, rather than its data
  // piling up below the TLS socket.
  const [a, b] = pair();
  const serverSocket = new tls.TLSSocket(b, { isServer: true, secureContext: tls.createSecureContext({ cert, key }) });
  serverSocket.on('secure', () => { for (let i = 0; i < 64; i++) serverSocket.write(Buffer.alloc(16384, i)); });
  let received = 0;
  const socket = tls.connect({ socket: a, servername: 'localhost', ca: cert }, () => socket.pause());
  socket.on('data', (d) => { received += d.length; });
  await new Promise((r) => setTimeout(r, 300));
  const whilePaused = { streamPaused: a.isPaused(), streamBuffered: a.readableLength > 0 };
  socket.resume();
  await new Promise((r) => { const check = () => (received === 64 * 16384 ? r() : setTimeout(check, 10)); check(); });
  socket.destroy();
  serverSocket.destroy();
  return { ...whilePaused, received };
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
  const out = { inMemory: await inMemory(), slowStream: await slowStream(), halfOpenThenClosed: await halfOpenThenClosed(), pausedReader: await pausedReader(), overNetSocket: await overNetSocket(), badCert: await badCert() };
  if (globalThis.done) done(out);
  else process.stdout.write(JSON.stringify(out));
})();
