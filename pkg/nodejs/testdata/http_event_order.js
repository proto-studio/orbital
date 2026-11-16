// Runs unchanged in Node and in Orbital. http_event_order.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestHTTPEventOrder_MatchesNode'
//
// The order of a request's 'end' and 'close' and its response's 'finish' and 'close' on the
// server, for handlers that end the response at different points relative to reading the
// request, and when a request's end is parsed relative to the handler's callbacks.
const http = require('http');
const net = require('net');

const handlers = {
  endThenResume: (req, res) => { res.end('x'); req.resume(); },
  resumeThenEnd: (req, res) => { req.resume(); res.end('x'); },
  endAfterData: (req, res) => { req.on('data', () => {}); setImmediate(() => res.end('x')); },
  endOnRequestEnd: (req, res) => { req.on('end', () => res.end('x')); req.resume(); },
  endInImmediate: (req, res) => { req.resume(); setImmediate(() => res.end('x')); },
  writeThenEnd: (req, res) => { req.resume(); res.write('a'); res.write('b'); res.end('c'); },
  endInTick: (req, res) => { req.resume(); process.nextTick(() => res.end('x')); },
};

// Orbital's writes for a request with no body still finish a turn after Node's (see
// docs/known-limitations.md), so that case is only run with a body.
const methods = { writeThenEnd: ['POST'] };

// Two requests with bodies in one packet: when each handler runs, and when each request
// is complete, relative to the callbacks the handler queued.
async function pipelined() {
  const log = [];
  const server = http.createServer((req, res) => {
    const url = req.url;
    log.push(`handler ${url} complete=${req.complete}`);
    req.on('data', () => log.push(`data ${url} complete=${req.complete}`));
    req.on('end', () => log.push(`end ${url}`));
    process.nextTick(() => log.push(`tick ${url} complete=${req.complete}`));
    Promise.resolve().then(() => log.push(`microtask ${url} complete=${req.complete}`));
    setImmediate(() => log.push(`immediate ${url} complete=${req.complete}`));
    setTimeout(() => res.end(), 30);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  await new Promise((resolve) => {
    const c = net.connect(server.address().port, '127.0.0.1', () => c.write(
      'POST /a HTTP/1.1\r\nHost: x\r\nContent-Length: 4\r\n\r\nbody' +
      'POST /b HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\nConnection: close\r\n\r\nzz'));
    c.resume();
    c.on('end', resolve);
  });
  server.close();
  return log;
}

async function main() {
  const results = {};
  let current;
  const server = http.createServer((req, res) => {
    const log = current;
    req.on('end', () => log.push('req end'));
    req.on('close', () => log.push('req close'));
    res.on('finish', () => log.push('res finish'));
    res.on('close', () => log.push('res close'));
    handlers[req.url.slice(1)](req, res);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  for (const name of Object.keys(handlers)) {
    for (const method of methods[name] || ['GET', 'POST']) {
      current = [];
      await new Promise((resolve, reject) => {
        const req = http.request({ host: '127.0.0.1', port, path: '/' + name, method, agent: false }, (res) => {
          res.resume();
          res.on('end', resolve);
        });
        req.on('error', reject);
        req.end(method === 'POST' ? 'body' : undefined);
      });
      await new Promise((resolve) => setTimeout(resolve, 20));
      results[`${method} ${name}`] = current;
    }
  }
  server.close();
  results.pipelined = await pipelined();
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
