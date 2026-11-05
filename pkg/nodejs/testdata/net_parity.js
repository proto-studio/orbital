// Runs unchanged in Node and in Orbital. net_parity.node.json is this script's output under
// Node v20.18.1; regenerate it with
//   node net_parity.js > net_parity.node.json
// Each case records the events of a socket that nothing reads from.
const net = require('net');

const finish = globalThis.done || ((value) => console.log(JSON.stringify(value, null, 2)));

// Resolves once the server has closed, with the events of the client socket.
const run = (onConnection, start) => new Promise((resolve) => {
  const seen = [];
  let client;
  const server = net.createServer((socket) => onConnection(socket, seen));
  server.listen(0, '127.0.0.1', () => {
    client = net.connect(server.address().port, '127.0.0.1');
    for (const name of ['connect', 'finish', 'end']) client.on(name, () => seen.push(name));
    client.on('error', (err) => seen.push(`error ${err.code}`));
    client.on('close', (hadError) => {
      seen.push(`close ${hadError}`);
      server.close(() => resolve({ seen, destroyed: client.destroyed, readyState: client.readyState }));
    });
    start(client);
  });
  // The process would otherwise exit without a result if a socket never closes.
  setTimeout(() => {
    seen.push('still open');
    client.destroy();
  }, 1000).unref();
});

// The peer reads to its end, so it closes when the client ends.
const drain = (socket) => socket.resume();

(async () => {
  const out = {};

  out.endInConnect = await run(drain, (c) => c.on('connect', () => c.end()));
  out.endWithData = await run(drain, (c) => c.on('connect', () => c.end('bye')));
  out.endBeforeConnect = await run(drain, (c) => c.end());
  out.writeThenEnd = await run(drain, (c) => c.on('connect', () => {
    c.write('a');
    c.end('b');
  }));
  out.peerEndsFirst = await run((s) => {
    s.resume();
    s.end();
  }, () => {});
  out.halfOpen = await new Promise((resolve) => {
    const seen = [];
    const server = net.createServer((s) => {
      s.resume();
      s.end();
    });
    server.listen(0, '127.0.0.1', () => {
      const c = net.connect({ port: server.address().port, host: '127.0.0.1', allowHalfOpen: true });
      for (const name of ['connect', 'finish', 'end']) c.on(name, () => seen.push(name));
      c.on('end', () => setTimeout(() => {
        seen.push('ending now');
        c.end();
      }, 20));
      c.on('close', (hadError) => {
        seen.push(`close ${hadError}`);
        server.close(() => resolve({ seen }));
      });
    });
  });

  // The peer's data is never consumed, so the end stays behind it, and with no socket
  // holding the loop open the process can exit. Nothing here waits for the client.
  out.unreadDataAtEnd = await new Promise((resolve) => {
    const seen = [];
    const server = net.createServer((s) => s.end('unread'));
    server.listen(0, '127.0.0.1', () => {
      const c = net.connect(server.address().port, '127.0.0.1');
      for (const name of ['connect', 'end', 'close']) c.on(name, () => seen.push(name));
      setTimeout(() => {
        c.destroy();
        server.close(() => resolve({ seen, readable: c.readableLength }));
      }, 100);
    });
  });

  // The server's sockets: nothing reads them either.
  out.serverSocket = await new Promise((resolve) => {
    const seen = [];
    const server = net.createServer((s) => {
      for (const name of ['finish', 'end']) s.on(name, () => seen.push(name));
      s.on('close', (hadError) => {
        seen.push(`close ${hadError}`);
        server.close(() => resolve({ seen }));
      });
    });
    server.listen(0, '127.0.0.1', () => {
      net.connect(server.address().port, '127.0.0.1', function connected() {
        this.end();
      });
    });
  });

  finish(out);
})();
