// Runs unchanged in Node and in Orbital. http_event_order.node.json is this script's output
// under Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestHTTPEventOrder_MatchesNode'
//
// The order of a request's 'end' and its response's 'finish' on the server, for handlers
// that end the response at different points relative to reading the request.
const http = require('http');

const handlers = {
  endThenResume: (req, res) => { res.end('x'); req.resume(); },
  resumeThenEnd: (req, res) => { req.resume(); res.end('x'); },
  endAfterData: (req, res) => { req.on('data', () => {}); setImmediate(() => res.end('x')); },
  endOnRequestEnd: (req, res) => { req.on('end', () => res.end('x')); req.resume(); },
  endInImmediate: (req, res) => { req.resume(); setImmediate(() => res.end('x')); },
};

async function main() {
  const results = {};
  let current;
  const server = http.createServer((req, res) => {
    const log = current;
    req.on('end', () => log.push('req end'));
    res.on('finish', () => log.push('res finish'));
    handlers[req.url.slice(1)](req, res);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  for (const name of Object.keys(handlers)) {
    for (const method of ['GET', 'POST']) {
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
  return results;
}

main().then((value) => {
  if (globalThis.done) done(value);
  else process.stdout.write(JSON.stringify(value));
}, (err) => {
  if (globalThis.done) done({ failed: String(err && err.stack || err) });
  else throw err;
});
