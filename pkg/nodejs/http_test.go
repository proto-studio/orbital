package nodejs

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

const httpPrelude = `
const http = require('http');
const serve = (handler, options) => new Promise((resolve) => {
  const server = options ? http.createServer(options, handler) : http.createServer(handler);
  server.listen(0, '127.0.0.1', () => resolve(server));
});
const target = (server, extra) => ({ port: server.address().port, host: '127.0.0.1', agent: false, ...extra });
const read = async (message) => { const c = []; for await (const d of message) c.push(d); return Buffer.concat(c); };
const get = (options) => new Promise((resolve, reject) => http.get(options, resolve).on('error', reject));
const raw = (server, text) => new Promise((resolve) => {
  const c = net.connect(server.address().port, '127.0.0.1', () => c.write(text));
  const chunks = [];
  c.on('data', (d) => chunks.push(d));
  c.on('close', () => resolve(Buffer.concat(chunks).toString('latin1')));
  c.on('error', () => {});
  setTimeout(() => c.destroy(), 300);
});
const noDate = (s) => s.replace(/Date: [^\r]+\r\n/g, '');
`

func runHTTP(t *testing.T, cfg *runtime.Config, body string) map[string]any {
	t.Helper()
	return runScript(t, cfg, httpPrelude+body)
}

func TestHTTP_Wire(t *testing.T) {
	result := runHTTP(t, &runtime.Config{}, `
const on = async (handler, text) => {
  const s = await serve(handler);
  const out = noDate(await raw(s, text));
  s.close();
  s.closeAllConnections();
  return out;
};
const close = 'GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n';
done({
  end: await on((q, p) => p.end('hi'), close),
  keepAlive: await on((q, p) => p.end('hi'), 'GET / HTTP/1.1\r\nHost: x\r\n\r\n'),
  chunked: await on((q, p) => { p.write('ab'); p.write('c'); p.end(); }, close),
  http10: await on((q, p) => { p.write('ab'); p.end(); }, 'GET / HTTP/1.0\r\n\r\n'),
  head: await on((q, p) => p.end('ab'), 'HEAD / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n'),
  noContent: await on((q, p) => { p.statusCode = 204; p.end(); }, close),
  notModified: await on((q, p) => { p.writeHead(304, { ETag: 'x' }); p.end(); }, close),
  trailers: await on((q, p) => { p.writeHead(200, { Trailer: 'X-T' }); p.write('a'); p.addTrailers({ 'X-T': 'v' }); p.end(); }, close),
  binary: Buffer.from((await on((q, p) => p.end(Buffer.from([0, 255, 128, 10])), close)).slice(-4), 'latin1').toString('hex'),
  pipelined: (await on((q, p) => setTimeout(() => p.end(q.url), q.url === '/a' ? 30 : 0),
    'GET /a HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n')).match(/\r\n\r\n\/[ab]/g),
  post: (await on(async (q, p) => p.end(q.method + ':' + (await read(q)) + ':' + q.complete),
    'POST / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nContent-Length: 3\r\n\r\nabc')).split('\r\n\r\n')[1],
  chunkedPost: (await on(async (q, p) => p.end(String(await read(q)) + JSON.stringify(q.trailers)),
    'POST / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n1;ext=1\r\nd\r\n0\r\nX-A: b\r\n\r\n')).split('\r\n\r\n')[1],
});`)
	wantJSON(t, result, map[string]any{
		"end":         "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 2\r\n\r\nhi",
		"keepAlive":   "HTTP/1.1 200 OK\r\nConnection: keep-alive\r\nKeep-Alive: timeout=5\r\nContent-Length: 2\r\n\r\nhi",
		"chunked":     "HTTP/1.1 200 OK\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nab\r\n1\r\nc\r\n0\r\n\r\n",
		"http10":      "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nab",
		"head":        "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n",
		"noContent":   "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n",
		"notModified": "HTTP/1.1 304 Not Modified\r\nETag: x\r\nConnection: close\r\n\r\n",
		"trailers":    "HTTP/1.1 200 OK\r\nTrailer: X-T\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n1\r\na\r\n0\r\nX-T: v\r\n\r\n",
		"binary":      "00ff800a",
		"pipelined":   []any{"\r\n\r\n/a", "\r\n\r\n/b"},
		"post":        "POST:abc:true",
		"chunkedPost": `abcd{"x-a":"b"}`,
	})
}

// Each of these lets two hops disagree about where a message ends, or carries something
// a handler must never see.
func TestHTTP_RejectsAmbiguousRequests(t *testing.T) {
	cases := map[string]struct {
		request, code string
		// A body is only found to be malformed once the handler has started.
		headersWereValid bool
	}{
		"content-length and transfer-encoding": {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n", code: "HPE_UNEXPECTED_CONTENT_LENGTH"},
		"conflicting lengths":                  {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\nabcd", code: "HPE_UNEXPECTED_CONTENT_LENGTH"},
		"malformed length":                     {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3x\r\n\r\nabc", code: "HPE_INVALID_CONTENT_LENGTH"},
		"negative length":                      {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: -1\r\n\r\n", code: "HPE_INVALID_CONTENT_LENGTH"},
		"final coding not chunked":             {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: gzip\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING"},
		"obfuscated coding":                    {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: xchunked\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING"},
		"space before colon":                   {request: "GET / HTTP/1.1\r\nHost : x\r\n\r\n", code: "HPE_INVALID_HEADER_TOKEN"},
		"bare LF":                              {request: "GET / HTTP/1.1\nHost: x\n\n", code: "HPE_INVALID_HEADER_TOKEN"},
		"folded header":                        {request: "GET / HTTP/1.1\r\nHost: x\r\n y\r\n\r\n", code: "HPE_INVALID_HEADER_TOKEN"},
		"NUL in value":                         {request: "GET / HTTP/1.1\r\nHost: x\x00y\r\n\r\n", code: "HPE_INVALID_HEADER_TOKEN"},
		"unknown method":                       {request: "FOO / HTTP/1.1\r\nHost: x\r\n\r\n", code: "HPE_INVALID_METHOD"},
		"repeated equal lengths":               {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nContent-Length: 3\r\n\r\nabc", code: "HPE_UNEXPECTED_CONTENT_LENGTH"},
		"lengths equal only after rounding":    {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 9007199254740993\r\nContent-Length: 9007199254740992\r\n\r\n", code: "HPE_INVALID_CONTENT_LENGTH"},
		"length overflow":                      {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 18446744073709551616\r\n\r\n", code: "HPE_INVALID_CONTENT_LENGTH"},
		"trailing tab on length":               {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\t\r\n\r\nabc", code: "HPE_INVALID_CONTENT_LENGTH"},
		"tab then space after length":          {request: "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 3\t \r\n\r\nabc", code: "HPE_INVALID_CONTENT_LENGTH"},
		"trailing tab on coding":               {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\t\r\n\r\n0\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING"},
		"chunked twice":                        {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked, chunked\r\n\r\n0\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING"},
		"chunked on two lines":                 {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING"},
		"unknown version":                      {request: "GET / HTTP/1.2\r\nHost: x\r\n\r\n", code: "HPE_INVALID_VERSION"},
		"target without a slash":               {request: "GET x HTTP/1.1\r\nHost: x\r\n\r\n", code: "HPE_INVALID_URL"},
		"DEL in target":                        {request: "GET /\x7f HTTP/1.1\r\nHost: x\r\n\r\n", code: "HPE_INVALID_URL"},
		"space before chunk extension":         {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n1 ;a\r\nx\r\n0\r\n\r\n", code: "HPE_INVALID_CHUNK_SIZE", headersWereValid: true},
		"chunk extension with a space":         {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n1; a\r\nx\r\n0\r\n\r\n", code: "HPE_STRICT", headersWereValid: true},
		"chunk extension with NUL":             {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n1;a\x00\r\nx\r\n0\r\n\r\n", code: "HPE_STRICT", headersWereValid: true},
		"unterminated chunk extension quote":   {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n1;a=\"b\r\nx\r\n0\r\n\r\n", code: "HPE_STRICT", headersWereValid: true},
		"transfer-encoding trailer":            {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nTransfer-Encoding: chunked\r\n\r\n", code: "HPE_INVALID_TRANSFER_ENCODING", headersWereValid: true},
		"malformed content-length trailer":     {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nContent-Length: x\r\n\r\n", code: "HPE_INVALID_CONTENT_LENGTH", headersWereValid: true},
		"bad chunk size":                       {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n", code: "HPE_INVALID_CHUNK_SIZE", headersWereValid: true},
		"chunk without CRLF":                   {request: "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n1\nx\r\n0\r\n\r\n", code: "HPE_CR_EXPECTED", headersWereValid: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := runHTTP(t, &runtime.Config{}, fmt.Sprintf(`
let served = false;
const server = await serve((q, p) => { served = true; p.end('served'); });
const codes = [];
server.on('clientError', (e, socket) => { codes.push(e.code); socket.destroy(); });
await raw(server, %q);
server.close();
server.closeAllConnections();
done({ code: codes[0], served });`, tc.request))
			wantJSON(t, result, map[string]any{"code": tc.code, "served": tc.headersWereValid})
		})
	}

	t.Run("default responses", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const server = await serve((q, p) => p.end('x'));
const invalid = await raw(server, 'NOPE\r\n\r\n');
const tooLarge = await raw(server, 'GET / HTTP/1.1\r\nA: ' + 'b'.repeat(20000) + '\r\n\r\n');
server.close();
server.closeAllConnections();
done({ invalid, tooLarge });`)
		wantJSON(t, result, map[string]any{
			"invalid":  "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n",
			"tooLarge": "HTTP/1.1 431 Request Header Fields Too Large\r\nConnection: close\r\n\r\n",
		})
	})

	t.Run("responses cannot be split", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const v = [];
for (const f of [(p) => p.setHeader('X', 'a\r\nb'), (p) => p.setHeader('X y', 'a'), (p) => p.writeHead(200, 'a\r\nb'), (p) => p.addTrailers({ a: 'b\nc' })]) {
  const server = await serve((q, p) => { try { f(p); v.push('ok'); } catch (e) { v.push(e.code); } p.destroy(); });
  await new Promise((r) => { const c = http.get(target(server)); c.on('error', r); c.on('close', r); });
  server.close();
}
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"ERR_INVALID_CHAR", "ERR_INVALID_HTTP_TOKEN", "ERR_INVALID_CHAR", "ERR_INVALID_CHAR"}})
	})

	t.Run("client rejects bad options", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const v = [];
for (const f of [() => http.request({ protocol: 'ftp:' }), () => http.request({ path: '/a b' }), () => http.request({ method: 'BA D' }),
  () => http.request({ agent: 5 }), () => http.request({ headers: { 'a b': 'c' } }), () => http.request({ headers: { a: 'b\nc' } }),
  () => http.request({ headers: ['a', 'b\r\nInjected: 1'] }), () => http.request({ headers: ['a b', 'c'] })]) {
  try { const q = f(); q.on('error', () => {}); q.destroy(); v.push('ok'); } catch (e) { v.push(e.code); }
}
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"ERR_INVALID_PROTOCOL", "ERR_UNESCAPED_CHARACTERS", "ERR_INVALID_HTTP_TOKEN", "ERR_INVALID_ARG_TYPE", "ERR_INVALID_HTTP_TOKEN", "ERR_INVALID_CHAR", "ERR_INVALID_CHAR", "ERR_INVALID_HTTP_TOKEN"}})
	})
}

// Found by fuzzing against Node: where one message ends, and what follows it.
func TestHTTP_MessageBoundaries(t *testing.T) {
	t.Run("a rejected message is final", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const seen = [];
const server = await serve((q, p) => { seen.push(q.url); q.resume(); p.end('ok'); });
// This listener answers and leaves the socket open, so later bytes still arrive.
server.on('clientError', (e, s) => { seen.push(e.code); s.write('HTTP/1.1 400 Bad\r\n\r\n'); });
const c = net.connect(server.address().port, '127.0.0.1');
c.on('error', () => {});
const wait = () => new Promise((r) => setTimeout(r, 30));
await new Promise((r) => c.once('connect', r));
c.write('GET /a HTTP/1.1\r\nHost: x\r\nBad Header: y\r\n\r\n');
await wait();
c.write('GET /after HTTP/1.1\r\nHost: x\r\n\r\n');
await wait();
c.destroy();
server.close();
server.closeAllConnections();
done({ seen });`)
		wantJSON(t, result, map[string]any{"seen": []any{"HPE_INVALID_HEADER_TOKEN", "HPE_INVALID_HEADER_TOKEN"}})
	})

	t.Run("nothing is read after the last request", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const second = 'GET /second HTTP/1.1\r\nHost: x\r\n\r\n';
const urls = async (first, sameWrite) => {
  const seen = [];
  const server = await serve((q, p) => { seen.push(q.url); q.resume(); setTimeout(() => p.end('ok'), 40); });
  const c = net.connect(server.address().port, '127.0.0.1');
  c.on('error', () => {});
  await new Promise((r) => c.once('connect', r));
  if (sameWrite) {
    c.write(first + second);
  } else {
    c.write(first);
    await new Promise((r) => setTimeout(r, 10));
    c.write(second);
  }
  await new Promise((r) => setTimeout(r, 80));
  c.destroy();
  server.close();
  server.closeAllConnections();
  return seen;
};
const close = 'GET /first HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n';
const old = 'GET /first HTTP/1.0\r\n\r\n';
const oldKeepAlive = 'GET /first HTTP/1.0\r\nConnection: keep-alive\r\n\r\n';
done({
  closeSame: await urls(close, true), closeNext: await urls(close, false),
  oldSame: await urls(old, true), oldNext: await urls(old, false),
  keepAlive: await urls(oldKeepAlive, true),
  contradictory: await urls('GET /first HTTP/1.0\r\nConnection: keep-alive, close\r\n\r\n', true),
});`)
		only := []any{"/first"}
		wantJSON(t, result, map[string]any{
			"closeSame": only, "closeNext": only, "oldSame": only, "oldNext": only,
			"keepAlive":     []any{"/first", "/second"},
			"contradictory": only,
		})
	})

	t.Run("HTTP/1.1 requires Host", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const urls = async (options, text) => {
  const seen = [];
  const server = await serve((q, p) => { seen.push(q.url); p.end('ok'); }, options);
  const reply = noDate(await raw(server, text));
  server.close();
  server.closeAllConnections();
  return { seen, status: reply.split('\r\n')[0] };
};
const next = 'GET /next HTTP/1.1\r\nHost: x\r\n\r\n';
done({
  missing: await urls(undefined, 'GET /a HTTP/1.1\r\n\r\n' + next),
  allowed: await urls({ requireHostHeader: false }, 'GET /a HTTP/1.1\r\nConnection: close\r\n\r\n'),
  old: await urls(undefined, 'GET /a HTTP/1.0\r\n\r\n'),
});`)
		wantJSON(t, result, map[string]any{
			"missing": map[string]any{"seen": []any{}, "status": "HTTP/1.1 400 Bad Request"},
			"allowed": map[string]any{"seen": []any{"/a"}, "status": "HTTP/1.1 200 OK"},
			"old":     map[string]any{"seen": []any{"/a"}, "status": "HTTP/1.1 200 OK"},
		})
	})

	t.Run("header names cannot reach Object.prototype", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
let out;
const server = await serve((q, p) => {
  out = {
    constructor: q.headers.constructor,
    proto: Object.getPrototypeOf(q.headers) === Object.prototype,
    distinct: q.headersDistinct.constructor,
    distinctProto: Object.getPrototypeOf(q.headersDistinct) === Object.prototype,
    polluted: ({}).evil === undefined,
  };
  p.end();
});
await raw(server, 'GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nconstructor: evil\r\n__proto__: evil\r\n\r\n');
server.close();
server.closeAllConnections();
done(out);`)
		wantJSON(t, result, map[string]any{
			"constructor": "evil", "proto": true,
			"distinct": []any{"evil"}, "distinctProto": true, "polluted": true,
		})
	})

	t.Run("a message cut off by EOF is reported", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const codes = [];
const server = await serve((q, p) => p.end('ok'));
server.on('clientError', (e, s) => { codes.push(e.code); s.destroy(); });
const c = net.connect(server.address().port, '127.0.0.1', () => c.end('GET / HTTP/1.1\r\nHo'));
c.on('error', () => {});
c.resume();
await new Promise((r) => c.on('close', r));
server.close();
server.closeAllConnections();
done({ codes });`)
		wantJSON(t, result, map[string]any{"codes": []any{"HPE_INVALID_EOF_STATE"}})
	})

	t.Run("valid chunk extensions are accepted", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const server = await serve(async (q, p) => p.end(String(await read(q))));
const body = async (ext) => (await raw(server, 'POST / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n1' + ext + '\r\nx\r\n0\r\n\r\n')).split('\r\n\r\n')[1];
const v = [await body(';a'), await body(';a=b'), await body(';a="b c;d"'), await body(';a=b;c=d'), await body(';a="\\""')];
server.close();
server.closeAllConnections();
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"x", "x", "x", "x", "x"}})
	})

	t.Run("long zero padding in a chunk size is still that size", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const server = await serve(async (q, p) => p.end(String(await read(q))));
const reply = await raw(server, 'POST / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n00000000000001\r\nx\r\n0000000000000000\r\n\r\n');
server.close();
server.closeAllConnections();
done({ body: reply.split('\r\n\r\n')[1] });`)
		wantJSON(t, result, map[string]any{"body": "x"})
	})

	t.Run("client rejects ambiguous responses", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const errors = async (text) => {
  const server = net.createServer((s) => { s.on('error', () => {}); s.once('data', () => s.end(text)); });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const seen = [];
  await new Promise((resolve) => {
    const q = http.get(target(server), (res) => { res.on('error', () => {}); res.resume(); });
    q.on('error', (e) => seen.push(e.code));
    q.on('close', () => setTimeout(resolve, 20));
  });
  server.close();
  return seen;
};
done({
  repeated: await errors('HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\nx'),
  both: await errors('HTTP/1.1 200 OK\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n'),
  overflow: await errors('HTTP/1.1 200 OK\r\nContent-Length: 18446744073709551616\r\n\r\n'),
  tab: await errors('HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\t\r\n\r\n0\r\n\r\n'),
  version: await errors('HTTP/3.1 200 OK\r\nContent-Length: 0\r\n\r\n'),
  hangUp: await errors('HTTP/1.1 200 OK\r\nX: y'),
});`)
		wantJSON(t, result, map[string]any{
			"repeated": []any{"HPE_UNEXPECTED_CONTENT_LENGTH"},
			"both":     []any{"HPE_UNEXPECTED_CONTENT_LENGTH"},
			"overflow": []any{"HPE_INVALID_CONTENT_LENGTH"},
			"tab":      []any{"HPE_INVALID_TRANSFER_ENCODING"},
			"version":  []any{"HPE_INVALID_VERSION"},
			"hangUp":   []any{"ECONNRESET"},
		})
	})
}

func TestHTTP_Client(t *testing.T) {
	t.Run("streams in both directions", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
let first = null;
let release;
const server = await serve((q, p) => {
  q.once('data', (d) => { first = String(d); p.write('one'); release = () => { q.resume(); p.end('two'); }; });
});
const got = [];
await new Promise((resolve) => {
  const q = http.request(target(server, { method: 'POST' }), (p) => {
    p.on('data', (d) => { got.push(String(d)); if (got.length === 1) { q.end(); release(); } });
    p.on('end', resolve);
  });
  q.write('early');
});
server.close();
done({ first, got });`)
		wantJSON(t, result, map[string]any{"first": "early", "got": []any{"one", "two"}})
	})

	t.Run("binary responses", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const s = net.createServer((c) => c.on('data', () => c.end(Buffer.concat([Buffer.from('HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\n'), Buffer.from([0, 255, 128, 10])]))));
await new Promise((r) => s.listen(0, '127.0.0.1', r));
const res = await get({ port: s.address().port, host: '127.0.0.1', agent: false });
const hex = (await read(res)).toString('hex');
s.close();
done({ hex });`)
		wantJSON(t, result, map[string]any{"hex": "00ff800a"})
	})

	t.Run("event order", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const server = await serve((q, p) => p.end('x'));
const seen = await new Promise((resolve) => {
  const ev = [];
  const q = http.get(target(server));
  for (const e of ['socket', 'finish', 'response', 'close', 'abort', 'error']) q.on(e, () => ev.push(e));
  q.on('response', (p) => {
    p.on('data', () => ev.push('data'));
    p.on('end', () => ev.push('end'));
    p.on('close', () => ev.push('res close'));
  });
  q.on('close', () => setTimeout(() => resolve(ev), 20));
});
server.close();
done({ seen });`)
		wantJSON(t, result, map[string]any{"seen": []any{"socket", "finish", "response", "data", "end", "res close", "close"}})
	})

	t.Run("a handler's exception stays the handler's", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const server = await serve((q, p) => p.end('x'));
const seen = [];
const q = http.get(target(server), () => { throw new Error('mine'); });
q.on('error', (e) => seen.push('error:' + e.message));
setTimeout(() => { server.close(); server.closeAllConnections(); done({ seen }); }, 50);`)
		wantJSON(t, result, map[string]any{"seen": []any{}})
	})

	t.Run("failures", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const out = {};
out.refused = await new Promise((r) => http.get({ port: 1, host: '127.0.0.1', agent: false }).on('error', (e) => r([e.code, e.syscall, e.port])));

const hangup = net.createServer((c) => { c.on('data', () => c.end()); });
await new Promise((r) => hangup.listen(0, '127.0.0.1', r));
out.hangup = await new Promise((r) => http.get({ port: hangup.address().port, host: '127.0.0.1', agent: false }).on('error', (e) => r([e.code, e.message])));
hangup.close();

const truncated = net.createServer((c) => c.on('data', () => { c.write('HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc'); setTimeout(() => c.destroy(), 10); }));
await new Promise((r) => truncated.listen(0, '127.0.0.1', r));
out.truncated = await new Promise((resolve) => {
  const ev = [];
  http.get({ port: truncated.address().port, host: '127.0.0.1', agent: false }, (p) => {
    p.on('data', (d) => ev.push(String(d)));
    p.on('aborted', () => ev.push('aborted'));
    p.on('error', (e) => ev.push(e.code));
    p.on('end', () => ev.push('end'));
    p.on('close', () => { ev.push(p.complete); resolve(ev); });
  });
});
truncated.close();

const garbage = net.createServer((c) => c.on('data', () => c.end('garbage\r\n\r\n')));
await new Promise((r) => garbage.listen(0, '127.0.0.1', r));
out.garbage = await new Promise((r) => http.get({ port: garbage.address().port, host: '127.0.0.1', agent: false }).on('error', (e) => r(e.code)));
garbage.close();

const quiet = await serve(() => {});
out.destroyed = await new Promise((resolve) => {
  const ev = [];
  const q = http.get(target(quiet));
  q.on('error', (e) => ev.push(e.code));
  q.on('close', () => resolve([ev, q.destroyed]));
  setTimeout(() => q.destroy(), 20);
});
const controller = new AbortController();
out.signal = await new Promise((resolve) => {
  http.get(target(quiet, { signal: controller.signal })).on('error', (e) => resolve([e.code, e.name]));
  setTimeout(() => controller.abort(), 20);
});
out.timeout = await new Promise((resolve) => {
  const q = http.get(target(quiet));
  q.on('error', () => {});
  q.setTimeout(30, () => { q.destroy(); resolve('fired'); });
});
quiet.close();
quiet.closeAllConnections();
done(out);`)
		wantJSON(t, result, map[string]any{
			"refused":   []any{"ECONNREFUSED", "connect", float64(1)},
			"hangup":    []any{"ECONNRESET", "socket hang up"},
			"truncated": []any{"abc", "aborted", "ECONNRESET", false},
			"garbage":   "HPE_INVALID_CONSTANT",
			"destroyed": []any{[]any{"ECONNRESET"}, true},
			"signal":    []any{"ABORT_ERR", "AbortError"},
			"timeout":   "fired",
		})
	})

	t.Run("protocol switches", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const out = {};
const server = await serve((q, p) => p.end('plain'));
out.unhandled = String(await read(await new Promise((r) => {
  const q = http.request(target(server, { headers: { Connection: 'Upgrade', Upgrade: 'x' } }), r);
  q.end();
})));
server.on('upgrade', (q, c, head) => {
  c.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: x\r\nConnection: Upgrade\r\n\r\nhead');
  c.on('data', (d) => c.end(String(d).toUpperCase()));
});
out.upgrade = await new Promise((resolve) => {
  const q = http.request(target(server, { headers: { Connection: 'Upgrade', Upgrade: 'x' } }));
  q.on('upgrade', (p, c, head) => {
    c.write('ping');
    c.on('data', (d) => { c.destroy(); resolve([p.statusCode, String(head), String(d)]); });
  });
  q.end();
});
server.on('connect', (q, c) => { c.write('HTTP/1.1 200 OK\r\n\r\n'); c.on('data', (d) => c.end(d)); });
out.connect = await new Promise((resolve) => {
  const q = http.request(target(server, { method: 'CONNECT', path: 'a:1' }));
  q.on('connect', (p, c) => {
    c.write('t');
    c.on('data', (d) => { c.destroy(); resolve([p.statusCode, String(d)]); });
  });
  q.end();
});
server.close();
server.closeAllConnections();
done(out);`)
		wantJSON(t, result, map[string]any{
			"unhandled": "plain",
			"upgrade":   []any{float64(101), "head", "PING"},
			"connect":   []any{float64(200), "t"},
		})
	})

	t.Run("continue and information", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const out = {};
const s = await serve((q, p) => { q.resume(); q.on('end', () => p.end('k')); });
out.proceed = await new Promise((resolve) => {
  const ev = [];
  const q = http.request(target(s, { method: 'POST', headers: { Expect: '100-continue' } }));
  q.on('continue', () => { ev.push('continue'); q.end('b'); });
  q.on('response', (p) => { p.resume(); ev.push(p.statusCode); resolve(ev); });
});
s.close();
const refusing = await serve(() => {});
refusing.on('checkContinue', (q, p) => { p.writeHead(403); p.end(); });
out.refused = await new Promise((resolve) => {
  const q = http.request(target(refusing, { method: 'POST', headers: { Expect: '100-continue' } }), (p) => { p.resume(); resolve(p.statusCode); });
  q.end();
});
refusing.close();
const expect = await serve((q, p) => p.end());
out.unknownExpect = await new Promise((resolve) => {
  const q = http.request(target(expect, { headers: { Expect: 'nope' } }), (p) => { p.resume(); resolve(p.statusCode); });
  q.end();
});
expect.close();
const hints = await serve((q, p) => { p.writeProcessing(); p.writeEarlyHints({ link: '</a>' }); p.end(); });
out.information = await new Promise((resolve) => {
  const seen = [];
  const q = http.get(target(hints), (p) => { p.resume(); resolve([seen, p.statusCode]); });
  q.on('information', (i) => seen.push(i.statusCode));
});
hints.close();
done(out);`)
		wantJSON(t, result, map[string]any{
			"proceed":       []any{"continue", float64(200)},
			"refused":       float64(403),
			"unknownExpect": float64(417),
			"information":   []any{[]any{float64(102), float64(103)}, float64(200)},
		})
	})

	t.Run("agents", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{}, `
const out = {};
const ports = new Set();
let active = 0;
let peak = 0;
const server = await serve((q, p) => {
  ports.add(q.socket.remotePort);
  active++;
  peak = Math.max(peak, active);
  setTimeout(() => { active--; p.end(q.headers.connection); }, 5);
});
const agent = new http.Agent({ keepAlive: true, maxSockets: 1 });
const go = (a) => new Promise((resolve) => {
  const q = http.get({ port: server.address().port, host: '127.0.0.1', agent: a }, (p) => {
    p.resume();
    p.on('end', () => setImmediate(() => resolve(q.reusedSocket)));
  });
});
out.reused = [await go(agent), await go(agent), await go(agent)];
out.connections = ports.size;
out.free = Object.keys(agent.freeSockets).length;
agent.destroy();
const limited = new http.Agent({ keepAlive: true, maxSockets: 2 });
peak = 0;
await Promise.all([go(limited), go(limited), go(limited), go(limited), go(limited)]);
out.peak = peak;
limited.destroy();
out.closeHeader = String(await read(await get(target(server))));
out.custom = await new Promise((resolve) => {
  let used = 0;
  http.get({ port: server.address().port, host: '127.0.0.1', createConnection: (o) => { used++; return net.connect(o.port, o.host); } }, (p) => {
    p.resume();
    p.on('end', () => resolve(used));
  });
});
server.close();
server.closeAllConnections();
done(out);`)
		wantJSON(t, result, map[string]any{
			"reused":      []any{false, true, true},
			"connections": float64(1),
			"free":        float64(1),
			"peak":        float64(2),
			"closeHeader": "close",
			"custom":      float64(1),
		})
	})
}

func TestHTTP_Server(t *testing.T) {
	result := runHTTP(t, &runtime.Config{}, `
const out = {};
out.types = [http.createServer() instanceof net.Server, http.Server() instanceof http.Server,
  new http.IncomingMessage(new net.Socket()) instanceof stream.Readable];

const props = await serve(() => {});
out.props = [props.keepAliveTimeout, props.headersTimeout, props.requestTimeout, props.timeout];
props.close();

out.events = await new Promise(async (resolve) => {
  const ev = [];
  const s = await serve((q, p) => {
    q.on('end', () => ev.push('req end'));
    q.on('close', () => ev.push('req close'));
    p.on('close', () => { ev.push('res close'); setTimeout(() => { s.close(); resolve(ev.filter((e) => e !== 'res finish')); }, 15); });
    q.resume();
    p.end('x');
  });
  http.get(target(s), (p) => p.resume());
});

out.disconnect = await new Promise(async (resolve) => {
  const ev = [];
  const s = await serve((q, p) => {
    q.on('aborted', () => ev.push('aborted'));
    q.on('error', (e) => ev.push(e.code));
    p.on('close', () => { ev.push('res close:' + p.writableFinished); setTimeout(() => { s.close(); resolve(ev); }, 15); });
    k.destroy();
  });
  const k = http.get(target(s));
  k.on('error', () => {});
});

out.closeIdle = await new Promise(async (resolve) => {
  const s = await serve((q, p) => p.end('x'));
  const agent = new http.Agent({ keepAlive: true });
  const res = await get({ port: s.address().port, host: '127.0.0.1', agent });
  res.resume();
  await new Promise((r) => res.on('end', r));
  await new Promise((r) => setTimeout(r, 10));
  s.close(() => { agent.destroy(); resolve('closed'); });
});

out.closeAll = await new Promise(async (resolve) => {
  const s = await serve(() => {});
  http.get(target(s)).on('error', (e) => resolve(e.code));
  setTimeout(() => { s.closeAllConnections(); s.close(); }, 20);
});

out.keepAliveTimeout = await new Promise(async (resolve) => {
  const s = await serve((q, p) => p.end('x'), { keepAliveTimeout: 30 });
  const c = net.connect(s.address().port, '127.0.0.1', () => c.write('GET / HTTP/1.1\r\nHost: x\r\n\r\n'));
  c.resume();
  c.on('close', () => { s.close(); resolve('closed'); });
});

out.setTimeout = await new Promise(async (resolve) => {
  const s = await serve(() => {});
  s.setTimeout(30, (c) => { c.destroy(); s.close(); resolve('fired'); });
  http.get(target(s)).on('error', () => {});
});

class Req extends http.IncomingMessage {}
class Res extends http.ServerResponse { hi() { this.end('hi'); } }
const custom = http.createServer({ IncomingMessage: Req, ServerResponse: Res }, (q, p) => p.hi());
await new Promise((r) => custom.listen(0, '127.0.0.1', r));
out.custom = String(await read(await get(target(custom))));
custom.close();

const echo = await serve((q, p) => q.pipe(p));
out.pipe = (await read(await new Promise((r) => { const q = http.request(target(echo, { method: 'POST' }), r); q.end(Buffer.alloc(200000, 1)); }))).length;
echo.close();

const big = await serve((q, p) => p.end(Buffer.alloc(2000000, 2)));
let total = 0;
for await (const d of await get(target(big))) { total += d.length; await new Promise((r) => setImmediate(r)); }
out.slowReader = total;
big.close();
done(out);`)
	wantJSON(t, result, map[string]any{
		"types":            []any{true, true, true},
		"props":            []any{float64(5000), float64(60000), float64(300000), float64(0)},
		"events":           []any{"req end", "res close", "req close"},
		"disconnect":       []any{"aborted", "res close:false", "ECONNRESET"},
		"closeIdle":        "closed",
		"closeAll":         "ECONNRESET",
		"keepAliveTimeout": "closed",
		"setTimeout":       "fired",
		"custom":           "hi",
		"pipe":             float64(200000),
		"slowReader":       float64(2000000),
	})
}

// recordingHTTPClient is an embedder's own client, which must see every request.
type recordingHTTPClient struct {
	runtime.RealHTTPClient
	mu       sync.Mutex
	requests []*runtime.Request
	reply    func(*runtime.Request) (*runtime.Response, error)
}

func (c *recordingHTTPClient) Do(_ context.Context, req *runtime.Request) (*runtime.Response, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()
	return c.reply(req)
}

func TestHTTP_EmbedderClientStaysInControl(t *testing.T) {
	newClient := func() *recordingHTTPClient {
		return &recordingHTTPClient{reply: func(req *runtime.Request) (*runtime.Response, error) {
			return &runtime.Response{
				StatusCode: 201,
				Status:     "201 Made",
				Headers:    http.Header{"X-Reply": {"yes"}, "Set-Cookie": {"a=1", "b=2"}},
				Body:       []byte{0, 255, 128},
			}, nil
		}}
	}

	t.Run("receives whole requests", func(t *testing.T) {
		client := newClient()
		factory := &recordingFactory{}
		result := runHTTP(t, &runtime.Config{HTTPClient: client, SocketFactory: factory}, `
const res = await new Promise((resolve, reject) => {
  const q = http.request({ host: 'api.test', port: 8080, path: '/p?q=1', method: 'POST', headers: { 'X-Mine': 'v' } }, resolve);
  q.on('error', reject);
  q.write(Buffer.from([0, 1]));
  q.end(Buffer.from([255]));
});
const body = await read(res);
done({ status: res.statusCode, message: res.statusMessage, reply: res.headers['x-reply'], cookies: res.headers['set-cookie'].length, hex: body.toString('hex') });`)
		wantJSON(t, result, map[string]any{"status": float64(201), "message": "Made", "reply": "yes", "cookies": float64(2), "hex": "00ff80"})
		if len(client.requests) != 1 {
			t.Fatalf("client saw %d requests", len(client.requests))
		}
		req := client.requests[0]
		if req.Method != "POST" || req.URL != "http://api.test:8080/p?q=1" || string(req.Body) != "\x00\x01\xff" || !req.NoFollowRedirects {
			t.Errorf("request = %+v", req)
		}
		if got := req.Headers["X-Mine"]; len(got) != 1 || got[0] != "v" {
			t.Errorf("headers = %v", req.Headers)
		}
		if got := factory.dialed(); len(got) != 0 {
			t.Errorf("opened sockets: %v", got)
		}
	})

	t.Run("cannot be bypassed by protocol switches", func(t *testing.T) {
		client := newClient()
		factory := &recordingFactory{}
		result := runHTTP(t, &runtime.Config{HTTPClient: client, SocketFactory: factory}, `
const attempt = (options) => new Promise((resolve) => {
  const q = http.request(options, () => resolve('response'));
  q.on('error', (e) => resolve(e.code));
  q.on('connect', () => resolve('tunnel'));
  q.on('upgrade', () => resolve('upgraded'));
  q.end();
});
done({
  connect: await attempt({ host: 'internal.test', method: 'CONNECT', path: 'internal.test:22' }),
  upgrade: await attempt({ host: 'internal.test', headers: { Connection: 'Upgrade', Upgrade: 'x' } }),
});`)
		wantJSON(t, result, map[string]any{"connect": "ERR_HTTP_TRANSPORT_UNAVAILABLE", "upgrade": "ERR_HTTP_TRANSPORT_UNAVAILABLE"})
		if got := factory.dialed(); len(got) != 0 {
			t.Errorf("opened sockets: %v", got)
		}
		if len(client.requests) != 0 {
			t.Errorf("client saw %d requests", len(client.requests))
		}
	})

	t.Run("cannot be bypassed by a custom connection", func(t *testing.T) {
		client := newClient()
		factory := &recordingFactory{}
		result := runHTTP(t, &runtime.Config{HTTPClient: client, SocketFactory: factory}, `
const https = require('https');
const net = require('net');
let created = 0;
const createConnection = (o) => { created++; return net.connect(80, 'internal.test'); };
const attempt = (mod, options) => new Promise((resolve) => {
  const q = mod.request({ host: 'internal.test', createConnection, ...options }, () => resolve('response'));
  q.on('error', (e) => resolve(e.code));
  q.end();
});
done({
  http: await attempt(http, {}),
  noAgent: await attempt(http, { agent: false }),
  https: await attempt(https, {}),
  created,
});`)
		const code = "ERR_HTTP_TRANSPORT_UNAVAILABLE"
		wantJSON(t, result, map[string]any{"http": code, "noAgent": code, "https": code, "created": float64(0)})
		if got := factory.dialed(); len(got) != 0 {
			t.Errorf("opened sockets: %v", got)
		}
		if len(client.requests) != 0 {
			t.Errorf("client saw %d requests", len(client.requests))
		}
	})

	t.Run("cannot be given a host that means two things", func(t *testing.T) {
		client := newClient()
		result := runHTTP(t, &runtime.Config{HTTPClient: client}, `
const v = [];
for (const host of ['allowed.test@evil.test', 'allowed.test/x', 'allowed.test#', 'allowed.test?y', 'a b']) {
  v.push(await new Promise((resolve) => {
    http.get({ host, path: '/' }, () => resolve('response')).on('error', (e) => resolve(e.code));
  }));
}
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"ENOTFOUND", "ENOTFOUND", "ENOTFOUND", "ENOTFOUND", "ENOTFOUND"}})
		if len(client.requests) != 0 {
			t.Errorf("client saw %v", client.requests[0].URL)
		}
	})

	t.Run("reports its errors", func(t *testing.T) {
		client := &recordingHTTPClient{reply: func(*runtime.Request) (*runtime.Response, error) {
			return nil, runtime.ErrRequestBlocked
		}}
		result := runHTTP(t, &runtime.Config{HTTPClient: client}, `
const v = await new Promise((resolve) => http.get({ host: 'api.test', path: '/' }, () => resolve('response')).on('error', (e) => resolve(e.code)));
done({ v });`)
		wantJSON(t, result, map[string]any{"v": "ENETUNREACH"})
	})
}

func TestHTTP_PolicyApplies(t *testing.T) {
	policy := runtime.NewHostListPolicy([]string{"allowed.test"}, nil)
	dns := func() *recordingResolver {
		return &recordingResolver{hosts: map[string][]string{"allowed.test": {"203.0.113.7"}, "other.test": {"203.0.113.8"}}}
	}
	ln := newEchoListener(t)
	request := func(host string) string {
		return fmt.Sprintf(`
const v = await new Promise((resolve) => {
  const q = http.get({ host: %q, path: '/', agent: false });
  q.on('error', (e) => resolve(e.code));
  q.setTimeout(100, () => { q.destroy(); });
  q.on('socket', (s) => s.on('connect', () => { resolve('connected'); q.destroy(); }));
});
done({ v });`, host)
	}

	t.Run("sockets when client and factory share a built-in policy", func(t *testing.T) {
		underlying := &recordingFactory{redirect: map[string]string{"203.0.113.7": ln}}
		cfg := &runtime.Config{
			HTTPClient:    runtime.NewFilteredHTTPClient(runtime.NewRealHTTPClient(), policy),
			SocketFactory: runtime.NewFilteredSocketFactory(underlying, policy),
			DNSResolver:   dns(),
		}
		wantJSON(t, runHTTP(t, cfg, request("allowed.test")), map[string]any{"v": "connected"})
		wantJSON(t, runHTTP(t, &runtime.Config{
			HTTPClient:    runtime.NewFilteredHTTPClient(runtime.NewRealHTTPClient(), policy),
			SocketFactory: runtime.NewFilteredSocketFactory(underlying, policy),
			DNSResolver:   dns(),
		}, request("other.test")), map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 1 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a stricter client keeps seeing requests", func(t *testing.T) {
		underlying := &recordingFactory{redirect: map[string]string{"203.0.113.7": ln, "203.0.113.8": ln}}
		cfg := &runtime.Config{
			// The client's policy is not the socket factory's, so sockets would skip it.
			HTTPClient:    runtime.NewFilteredHTTPClient(runtime.NewRealHTTPClient(), runtime.NewDenyAllPolicy()),
			SocketFactory: runtime.NewFilteredSocketFactory(underlying, runtime.NewAllowAllPolicy()),
			DNSResolver:   dns(),
		}
		wantJSON(t, runHTTP(t, cfg, request("allowed.test")), map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a resolved private address is denied", func(t *testing.T) {
		deny := runtime.CommonPolicies.NoPrivateNetworks()
		underlying := &recordingFactory{}
		cfg := &runtime.Config{
			SocketFactory: runtime.NewFilteredSocketFactory(underlying, deny),
			DNSResolver:   &recordingResolver{hosts: map[string][]string{"rebind.test": {"10.1.2.3"}}},
		}
		wantJSON(t, runHTTP(t, cfg, request("rebind.test")), map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})
}

// A script can only listen for 'connect' once the request hands it the socket, so that must
// happen first however quickly the connection opens. The server echoes, so a listener that
// misses 'connect' gets a parse error from the echoed request instead.
func TestHTTP_SocketEventBeforeConnect(t *testing.T) {
	underlying := &recordingFactory{redirect: map[string]string{"203.0.113.7": newEchoListener(t)}}
	cfg := &runtime.Config{
		SocketFactory: underlying,
		DNSResolver:   &recordingResolver{hosts: map[string][]string{"slow.test": {"203.0.113.7"}}},
	}
	result := runHTTP(t, cfg, `
const seen = [];
const agent = new http.Agent();
const create = agent.createConnection;
agent.createConnection = function (...args) {
  const socket = create.apply(this, args);
  socket.on('connect', () => seen.push('connect'));
  // Stay on the script's thread until the dial has finished in the background.
  const end = Date.now() + 50;
  while (Date.now() < end);
  return socket;
};
const v = await new Promise((resolve) => {
  const q = http.get({ host: 'slow.test', path: '/', agent });
  q.on('error', (e) => resolve(e.code));
  q.on('socket', (s) => {
    seen.push('socket');
    s.on('connect', () => { resolve('connected'); q.destroy(); });
  });
});
done({ v, seen });`)
	wantJSON(t, result, map[string]any{"v": "connected", "seen": []any{"socket", "connect"}})
}

func TestHTTPS(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issuePEM(t, "localhost", "127.0.0.1")
	selfCA := newTestCA(t)
	selfCertPEM, selfKeyPEM := selfCA.selfSignedPEM(t)
	prelude := fmt.Sprintf(`
const https = require('https');
const tls = require('tls');
const CA = %q, CERT = %q, KEY = %q, SELF_CERT = %q, SELF_KEY = %q;
const secure = async (handler, cert, key) => {
  const server = https.createServer({ cert: cert || CERT, key: key || KEY }, handler);
  server.on('tlsClientError', () => {});
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  return server;
};
const fetchTLS = (options) => new Promise((resolve, reject) => https.get(options, resolve).on('error', reject));
`, ca.pem, certPEM, keyPEM, selfCertPEM, selfKeyPEM)

	result := runHTTP(t, &runtime.Config{}, prelude+`
const out = {};
out.types = [https.createServer({ cert: CERT, key: KEY }) instanceof tls.Server, https.globalAgent instanceof http.Agent, https.globalAgent.defaultPort, https.globalAgent.protocol];

const server = await secure((q, p) => p.end(JSON.stringify([q.socket.encrypted, q.socket.alpnProtocol, q.url])));
const port = server.address().port;
const ok = await fetchTLS({ port, host: 'localhost', ca: CA, path: '/x', agent: false });
out.ok = [ok.statusCode, JSON.parse(await read(ok)), ok.socket === null ? null : ok.socket.encrypted];

out.untrusted = await fetchTLS({ port, host: 'localhost', agent: false }).catch((e) => e.code);
out.mismatch = await fetchTLS({ port, host: 'localhost', servername: 'other.test', ca: CA, agent: false }).catch((e) => e.code);
server.close();
server.closeAllConnections();

const self = await secure((q, p) => p.end('x'), SELF_CERT, SELF_KEY);
const selfPort = self.address().port;
const optOut = await fetchTLS({ port: selfPort, host: 'localhost', rejectUnauthorized: false, agent: false });
out.optOut = [String(await read(optOut)), optOut.statusCode];

// A connection opened with verification off must not serve a request that wants it on.
const agent = new https.Agent({ keepAlive: true });
const lax = await fetchTLS({ port: selfPort, host: 'localhost', rejectUnauthorized: false, agent });
lax.resume();
await new Promise((r) => lax.on('end', r));
await new Promise((r) => setTimeout(r, 10));
out.pooled = await fetchTLS({ port: selfPort, host: 'localhost', agent }).then(() => 'reused', (e) => e.code);
agent.destroy();
self.close();
self.closeAllConnections();
try { http.request('https://x/'); } catch (e) { out.wrongProtocol = e.code; }
done(out);`)
	wantJSON(t, result, map[string]any{
		"types":         []any{true, true, float64(443), "https:"},
		"ok":            []any{float64(200), []any{true, false, "/x"}, true},
		"untrusted":     "UNABLE_TO_VERIFY_LEAF_SIGNATURE",
		"mismatch":      "ERR_TLS_CERT_ALTNAME_INVALID",
		"optOut":        []any{"x", float64(200)},
		"pooled":        "DEPTH_ZERO_SELF_SIGNED_CERT",
		"wrongProtocol": "ERR_INVALID_PROTOCOL",
	})

	t.Run("ClientTLSConfig is authoritative", func(t *testing.T) {
		result := runHTTP(t, &runtime.Config{ClientTLSConfig: &tls.Config{RootCAs: ca.pool}}, prelude+`
const self = await secure((q, p) => p.end('x'), SELF_CERT, SELF_KEY);
const code = await fetchTLS({ port: self.address().port, host: 'localhost', rejectUnauthorized: false, agent: false }).then(() => 'connected', (e) => e.code);
self.close();
done({ code });`)
		wantJSON(t, result, map[string]any{"code": "DEPTH_ZERO_SELF_SIGNED_CERT"})
	})
}
