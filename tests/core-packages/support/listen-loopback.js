// Loaded ahead of Express's specs. supertest starts each app with listen(0), which listens on
// '::', and then connects to 127.0.0.1 on that port. On macOS the kernel can give '::' a
// port that another program holds on 127.0.0.1 alone, and the request then reaches that
// program instead: 426 from a WebSocket server, or a hang-up. Node fails the same way. This
// connects to the loopback address of the family the server listens on, which only it has.
'use strict';
const path = require('path');
const https = require('https');

const Test = require(path.join(process.cwd(), 'node_modules', 'supertest', 'lib', 'test.js'));

Test.prototype.serverAddress = function (app, urlPath) {
  if (!app.address()) this._server = app.listen(0);
  const addr = app.address();
  const host = addr.family === 'IPv6' || addr.family === 6 ? '[::1]' : '127.0.0.1';
  const protocol = app instanceof https.Server ? 'https' : 'http';
  return protocol + '://' + host + ':' + addr.port + urlPath;
};
