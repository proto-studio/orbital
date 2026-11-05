package nodejs

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// netPrelude gives scripts a server helper.
const netPrelude = `
const net = require('net');
const stream = require('stream');
const listen = (handler, options) => new Promise((resolve) => {
  const server = net.createServer(options || {}, handler);
  server.listen(0, '127.0.0.1', () => resolve(server));
});
const fail = (e) => done({ failed: String(e && e.stack || e) });
`

func runScript(t *testing.T, cfg *runtime.Config, body string) map[string]any {
	t.Helper()
	return runJS(t, cfg, netPrelude+`(async () => {`+body+`})().catch(fail);`)
}

func wantJSON(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for key := range want {
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("%s = %v, want %v", key, got[key], want[key])
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected %s = %v", key, got[key])
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestStream_NodeSemantics(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `
const { Readable, Writable, Duplex, Transform, PassThrough, pipeline, finished } = stream;
const events = require('events');
const out = {};
out.types = [new Duplex() instanceof Readable, new Duplex() instanceof Writable,
  new Readable() instanceof Writable, new Readable() instanceof events];

out.order = await new Promise((resolve) => {
  const seen = [];
  const r = new Readable({ read() {} });
  r.on('data', (d) => seen.push('data:' + d));
  r.on('end', () => seen.push('end'));
  r.on('close', () => { seen.push('close'); resolve(seen); });
  r.push('a');
  r.push(null);
});

out.bufferJoin = await new Promise((resolve) => {
  const r = new Readable({ read() {} });
  r.push(Buffer.from([0, 255]));
  r.push(Buffer.from([1]));
  r.once('readable', () => resolve(r.read().toString('hex')));
});

out.split = await new Promise((resolve) => {
  const r = new Readable({ read() {} });
  r.setEncoding('utf8');
  const euro = Buffer.from('€');
  r.push(euro.subarray(0, 1));
  r.push(euro.subarray(1));
  r.push(null);
  let text = '';
  r.on('data', (d) => { text += d; });
  r.on('end', () => resolve(text.charCodeAt(0)));
});

out.drain = await new Promise((resolve) => {
  const w = new Writable({ highWaterMark: 3, write(c, e, cb) { setTimeout(cb, 1); } });
  const first = w.write('abc');
  w.on('drain', () => resolve([first, 'drain']));
});

out.writeAfterEnd = await new Promise((resolve) => {
  const w = new Writable({ write(c, e, cb) { cb(); } });
  w.on('error', (e) => resolve(e.code));
  w.end();
  w.write('x');
});

out.iterate = await (async () => {
  let text = '';
  for await (const c of Readable.from(['x', 'y'])) text += c;
  return text;
})();

out.pipelineError = await new Promise((resolve) => {
  const src = new Readable({ read() { this.destroy(new Error('source')); } });
  const dst = new PassThrough();
  pipeline(src, dst, (e) => setTimeout(() => resolve([e.message, dst.destroyed]), 5));
});

out.premature = await new Promise((resolve) => {
  const r = new Readable({ read() {} });
  finished(r, (e) => resolve(e.code));
  r.destroy();
});

out.pipeBackpressure = await new Promise((resolve) => {
  let produced = 0;
  const src = new Readable({ highWaterMark: 1, read() { this.push(produced++ < 5 ? 'x' : null); } });
  let written = 0;
  const dst = new Writable({ highWaterMark: 1, write(c, e, cb) { written++; setTimeout(cb, 1); } });
  dst.on('finish', () => resolve(written));
  src.pipe(dst);
});
done(out);`)
	wantJSON(t, result, map[string]any{
		"types":            []any{true, true, false, true},
		"order":            []any{"data:a", "end", "close"},
		"bufferJoin":       "00ff01",
		"split":            float64(0x20ac),
		"drain":            []any{false, "drain"},
		"writeAfterEnd":    "ERR_STREAM_WRITE_AFTER_END",
		"iterate":          "xy",
		"pipelineError":    []any{"source", true},
		"premature":        "ERR_STREAM_PREMATURE_CLOSE",
		"pipeBackpressure": float64(5),
	})
}

func TestTimers_TimeoutObjects(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `
const timer = setTimeout(() => {}, 1000);
const out = [typeof timer, timer.hasRef(), timer.unref() === timer, timer.hasRef(), typeof +timer];
clearTimeout(timer);
let fired = false;
const byNumber = setTimeout(() => { fired = true; }, 5);
clearTimeout(+byNumber);
await new Promise((r) => setTimeout(r, 30));
out.push(fired);
let count = 0;
const refreshed = setTimeout(() => { count++; }, 20);
await new Promise((r) => setTimeout(r, 10));
refreshed.refresh();
await new Promise((r) => setTimeout(r, 15));
out.push(count);
done({ out });`)
	wantJSON(t, result, map[string]any{"out": []any{"object", true, true, false, "number", false, float64(0)}})
}

// exitsPromptly fails if the script's handles keep the event loop running.
func exitsPromptly(t *testing.T, script string) {
	t.Helper()
	start := time.Now()
	result := runScript(t, &runtime.Config{Timeout: 10 * time.Second}, script)
	if result["ok"] != true {
		t.Fatalf("result = %v", result)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("event loop stayed alive for %s", elapsed)
	}
}

func TestEventLoop_UnrefLetsTheLoopExit(t *testing.T) {
	t.Run("timer", func(t *testing.T) {
		exitsPromptly(t, `setTimeout(() => {}, 8000).unref(); done({ ok: true });`)
	})
	t.Run("server", func(t *testing.T) {
		exitsPromptly(t, `const s = await listen(() => {}); s.unref(); done({ ok: true });`)
	})
	t.Run("client", func(t *testing.T) {
		exitsPromptly(t, `
const server = net.createServer((c) => { c.unref(); });
await new Promise((r) => server.listen(0, '127.0.0.1', r));
server.unref();
await new Promise((r) => { const c = net.connect(server.address().port, '127.0.0.1', () => { c.unref(); r(); }); });
done({ ok: true });`)
	})
	t.Run("dgram", func(t *testing.T) {
		exitsPromptly(t, `
const s = require('dgram').createSocket('udp4');
await new Promise((r) => s.bind(0, '127.0.0.1', r));
s.unref();
done({ ok: true });`)
	})
	t.Run("idle keep-alive agent", func(t *testing.T) {
		exitsPromptly(t, `
const http = require('http');
const server = http.createServer((q, p) => p.end('x'));
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const res = await new Promise((r) => http.get({ port: server.address().port, host: '127.0.0.1' }, r));
res.resume();
await new Promise((r) => res.on('end', r));
await new Promise((r) => setTimeout(r, 10));
server.close();
server.closeAllConnections();
done({ ok: true });`)
	})
}

// The dial finishes during the spin, so its result is queued before either callback.
func TestEventLoop_CallbacksRunBeforeFinishedIO(t *testing.T) {
	_, port, err := net.SplitHostPort(newEchoListener(t))
	if err != nil {
		t.Fatal(err)
	}
	result := runScript(t, &runtime.Config{}, fmt.Sprintf(`
const seen = [];
await new Promise((resolve) => {
  const s = net.connect(%s, '127.0.0.1', () => { seen.push('connect'); s.destroy(); resolve(); });
  const end = Date.now() + 50;
  while (Date.now() < end);
  process.nextTick(() => seen.push('nextTick'));
  queueMicrotask(() => seen.push('queueMicrotask'));
});
done({ seen });`, port))
	wantJSON(t, result, map[string]any{"seen": []any{"nextTick", "queueMicrotask", "connect"}})
}

func TestNet_Socket(t *testing.T) {
	t.Run("is a Duplex", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const s = new net.Socket();
done({ v: [s instanceof stream.Duplex, s instanceof stream.Writable, s instanceof require('events'), s.readyState, s.pending, s.connecting] });`)
		wantJSON(t, result, map[string]any{"v": []any{true, true, true, "open", true, false}})
	})

	t.Run("every connect form calls back", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen(() => {});
const port = server.address().port;
const attempt = (...args) => new Promise((resolve) => {
  const c = net.connect(...args, () => { c.destroy(); resolve(1); });
  c.on('error', (e) => resolve(e.code));
});
const v = [await attempt(port, '127.0.0.1'), await attempt(port), await attempt({ port, host: '127.0.0.1' }), await attempt(String(port), '127.0.0.1')];
server.close();
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{float64(1), float64(1), float64(1), float64(1)}})
	})

	t.Run("invalid arguments", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const v = [];
for (const args of [[], [{}], [70000], [-1], [{ port: 1, host: 5 }]]) {
  try { const c = net.connect(...args); c.on('error', () => {}); c.destroy(); v.push('ok'); } catch (e) { v.push(e.code); }
}
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"ERR_MISSING_ARGS", "ERR_MISSING_ARGS", "ERR_SOCKET_BAD_PORT", "ERR_SOCKET_BAD_PORT", "ERR_INVALID_ARG_TYPE"}})
	})

	t.Run("connection errors carry details", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const v = await new Promise((resolve) => {
  const c = net.connect(1, '127.0.0.1');
  const seen = [];
  c.on('error', (e) => seen.push(e.code, e.syscall, e.address, e.port, e.errno < 0, e.message));
  c.on('close', (hadError) => resolve([...seen, hadError, c.destroyed]));
});
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"ECONNREFUSED", "connect", "127.0.0.1", float64(1), true, "connect ECONNREFUSED 127.0.0.1:1", true, true}})
	})

	t.Run("name failures report getaddrinfo", func(t *testing.T) {
		dns := &recordingResolver{hosts: map[string][]string{}}
		result := runScript(t, &runtime.Config{DNSResolver: dns}, `
const v = await new Promise((resolve) => {
  const c = net.connect(1, 'nope.test');
  const seen = [];
  c.on('lookup', (e, a, f, h) => seen.push('lookup', e && e.code, h));
  c.on('error', (e) => seen.push(e.code, e.syscall, e.hostname));
  c.on('close', (hadError) => resolve([...seen, hadError]));
});
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"lookup", "ENOTFOUND", "nope.test", "ENOTFOUND", "getaddrinfo", "nope.test", true}})
	})

	t.Run("binary data is unchanged", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => c.pipe(c));
const bytes = Buffer.from([0, 1, 255, 254, 128, 65, 0]);
const hex = await new Promise((resolve) => {
  const c = net.connect(server.address().port, '127.0.0.1', () => c.write(bytes));
  const chunks = [];
  c.on('data', (d) => {
    chunks.push(d);
    if (Buffer.concat(chunks).length >= bytes.length) { c.destroy(); resolve(Buffer.concat(chunks).toString('hex')); }
  });
});
server.close();
done({ hex });`)
		wantJSON(t, result, map[string]any{"hex": "0001fffe804100"})
	})

	t.Run("backpressure and drain", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => c.resume());
const v = await new Promise((resolve) => {
  const c = net.connect(server.address().port, '127.0.0.1', () => {
    const ok = c.write(Buffer.alloc(1024 * 1024));
    c.once('drain', () => { c.destroy(); resolve([ok, 'drain']); });
  });
});
server.close();
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{false, "drain"}})
	})

	t.Run("pause stops data", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => { c.write('a'); setTimeout(() => c.write('b'), 30); });
const v = await new Promise((resolve) => {
  const c = net.connect(server.address().port, '127.0.0.1');
  const got = [];
  c.on('data', (d) => {
    got.push(String(d));
    c.pause();
    setTimeout(() => {
      const whilePaused = got.join('');
      c.resume();
      setTimeout(() => { c.destroy(); resolve([whilePaused, got.join('')]); }, 40);
    }, 100);
  });
});
server.close();
done({ v });`)
		wantJSON(t, result, map[string]any{"v": []any{"a", "ab"}})
	})

	t.Run("stream reading APIs", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => c.end('€'));
const port = server.address().port;
const encoded = await new Promise((resolve) => {
  const c = net.connect(port, '127.0.0.1');
  c.setEncoding('utf8');
  c.on('data', (d) => resolve([typeof d, d.charCodeAt(0)]));
});
let iterated = 0;
for await (const chunk of net.connect(port, '127.0.0.1')) iterated += chunk.length;
const viaReadable = await new Promise((resolve) => {
  const c = net.connect(port, '127.0.0.1');
  let n = 0;
  c.on('readable', () => { let x; while ((x = c.read()) !== null) n += x.length; });
  c.on('end', () => resolve(n));
});
server.close();
done({ encoded, iterated, viaReadable });`)
		wantJSON(t, result, map[string]any{"encoded": []any{"string", float64(0x20ac)}, "iterated": float64(3), "viaReadable": float64(3)})
	})

	t.Run("timeouts", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => {
  const interval = setInterval(() => c.write('x'), 15);
  c.on('close', () => clearInterval(interval));
});
const port = server.address().port;
const idle = await new Promise((resolve) => {
  const quiet = net.createServer(() => {});
  quiet.listen(0, '127.0.0.1', () => {
    const c = net.connect(quiet.address().port, '127.0.0.1');
    c.setTimeout(30, () => { const v = c.timeout; c.destroy(); quiet.close(); resolve(v); });
  });
});
const active = await new Promise((resolve) => {
  const c = net.connect(port, '127.0.0.1');
  c.resume();
  let fired = false;
  c.setTimeout(60, () => { fired = true; });
  setTimeout(() => { c.destroy(); resolve(fired); }, 150);
});
const cleared = await new Promise((resolve) => {
  const c = net.connect(port, '127.0.0.1');
  c.resume();
  let fired = false;
  c.setTimeout(10, () => { fired = true; });
  c.setTimeout(0);
  setTimeout(() => { c.destroy(); resolve(fired); }, 50);
});
server.close();
done({ idle, active, cleared });`)
		wantJSON(t, result, map[string]any{"idle": float64(30), "active": false, "cleared": false})
	})

	t.Run("half-open and event order", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const order = await new Promise(async (resolve) => {
  const server = await listen((c) => c.end('x'));
  const seen = [];
  const c = net.connect(server.address().port, '127.0.0.1');
  for (const name of ['connect', 'ready', 'data', 'end', 'finish']) c.on(name, () => seen.push(name));
  c.on('close', (hadError) => { seen.push('close:' + hadError); server.close(); resolve(seen); });
});
const halfOpen = await new Promise(async (resolve) => {
  const seen = [];
  const server = await listen((c) => {
    c.on('data', () => {});
    c.on('end', () => { seen.push(c.writable); setTimeout(() => c.end('late'), 10); });
  }, { allowHalfOpen: true });
  let got = '';
  const c = net.connect(server.address().port, '127.0.0.1', () => c.end('hi'));
  c.on('data', (d) => { got += d; });
  c.on('close', () => { server.close(); resolve([seen[0], got]); });
});
done({ order, halfOpen });`)
		wantJSON(t, result, map[string]any{
			"order":    []any{"connect", "ready", "data", "end", "finish", "close:false"},
			"halfOpen": []any{true, "late"},
		})
	})

	t.Run("reconnect and destroy", func(t *testing.T) {
		result := runScript(t, &runtime.Config{}, `
const server = await listen((c) => c.end('x'));
const port = server.address().port;
const reconnected = await new Promise((resolve) => {
  const c = new net.Socket();
  let closes = 0;
  c.on('data', () => {});
  c.on('close', () => { if (++closes === 2) resolve(closes); else c.connect(port, '127.0.0.1'); });
  c.connect(port, '127.0.0.1');
});
const midConnect = await new Promise((resolve) => {
  const seen = [];
  const c = net.connect(port, '127.0.0.1');
  c.on('connect', () => seen.push('connect'));
  c.on('close', (e) => { seen.push('close:' + e); setTimeout(() => resolve(seen), 20); });
  c.destroy();
});
const withError = await new Promise((resolve) => {
  const seen = [];
  const c = net.connect(port, '127.0.0.1', () => c.destroy(Object.assign(new Error('x'), { code: 'MINE' })));
  c.on('error', (e) => seen.push(e.code));
  c.on('close', (e) => { seen.push(e); resolve(seen); });
});
server.close();
done({ reconnected, midConnect, withError });`)
		wantJSON(t, result, map[string]any{
			"reconnected": float64(2),
			"midConnect":  []any{"close:false"},
			"withError":   []any{"MINE", true},
		})
	})
}

func TestNet_Server(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `
const out = {};
const server = await listen(() => {});
const addr = server.address();
out.addr = [addr.address, addr.family, typeof addr.port, server.listening];

out.inUse = await new Promise((resolve) => {
  const other = net.createServer();
  other.on('error', (e) => resolve([e.code, e.syscall, e.address, e.port === addr.port]));
  other.listen(addr.port, '127.0.0.1');
});
try { server.listen(0); } catch (e) { out.twice = e.code; }
try { net.createServer().listen(70000); } catch (e) { out.badPort = e.code; }
await new Promise((r) => server.close(r));
out.closed = [server.listening, server.address()];
out.notRunning = await new Promise((r) => net.createServer().close((e) => r(e.code)));

out.wildcards = await new Promise((resolve) => {
  const v4 = net.createServer();
  v4.listen(0, '0.0.0.0', () => {
    const a = v4.address();
    const v6 = net.createServer();
    v6.on('error', (e) => { v4.close(); resolve([a.address, a.family, e.code]); });
    v6.listen(a.port, '0.0.0.0');
  });
});

out.closeWaits = await new Promise(async (resolve) => {
  const seen = [];
  const s = await listen((c) => {
    s.close(() => seen.push('closed'));
    setTimeout(() => { seen.push('destroy'); c.destroy(); }, 20);
  });
  const c = net.connect(s.address().port, '127.0.0.1');
  c.resume();
  c.on('close', () => setTimeout(() => resolve(seen), 20));
});

out.drop = await new Promise(async (resolve) => {
  const s = net.createServer(() => {});
  s.maxConnections = 1;
  s.on('drop', (d) => { a.destroy(); b.destroy(); s.close(); resolve(typeof d.remotePort); });
  await new Promise((r) => s.listen(0, '127.0.0.1', r));
  const a = net.connect(s.address().port, '127.0.0.1');
  a.on('error', () => {});
  const b = net.connect(s.address().port, '127.0.0.1');
  b.on('error', () => {});
});

out.pauseOnConnect = await new Promise(async (resolve) => {
  const s = await listen((c) => {
    const paused = c.isPaused();
    c.on('data', (d) => { c.destroy(); s.close(); k.destroy(); resolve([paused, String(d)]); });
    setTimeout(() => c.resume(), 20);
  }, { pauseOnConnect: true });
  const k = net.connect(s.address().port, '127.0.0.1', () => k.write('z'));
  k.on('error', () => {});
});
done(out);`)
	wantJSON(t, result, map[string]any{
		"addr":           []any{"127.0.0.1", "IPv4", "number", true},
		"inUse":          []any{"EADDRINUSE", "listen", "127.0.0.1", true},
		"twice":          "ERR_SERVER_ALREADY_LISTEN",
		"badPort":        "ERR_SOCKET_BAD_PORT",
		"closed":         []any{false, nil},
		"notRunning":     "ERR_SERVER_NOT_RUNNING",
		"wildcards":      []any{"0.0.0.0", "IPv4", "EADDRINUSE"},
		"closeWaits":     []any{"destroy", "closed"},
		"drop":           "number",
		"pauseOnConnect": []any{true, "z"},
	})
}

func TestNet_Utilities(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `
const list = new net.BlockList();
list.addAddress('1.1.1.1');
list.addRange('10.0.0.1', '10.0.0.10');
list.addSubnet('8592:757c:efae:4e45::', 64, 'ipv6');
const errors = [];
for (const f of [() => list.addAddress('nope'), () => list.addSubnet('1.1.1.1', 40), () => list.addAddress('1.1.1.1', 'x')]) {
  try { f(); errors.push('ok'); } catch (e) { errors.push(e.code); }
}
const address = new net.SocketAddress({ address: '1.2.3.4', port: 80 });
done({
  isIP: [net.isIP('::1'), net.isIP('1.2.3.256'), net.isIP('fe80::1%en0'), net.isIPv4('01.1.1.1'), net.isIP('::ffff:1.2.3.4'), net.isIP('1::2::3')],
  checks: [list.check('1.1.1.1'), list.check('1.1.1.2'), list.check('10.0.0.5'), list.check('10.0.0.11'),
    list.check('::ffff:1.1.1.1', 'ipv6'), list.check('8592:757c:efae:4e45:fb5d:d62a:0d00:8e17', 'ipv6')],
  errors,
  address: [address.address, address.port, address.family, net.SocketAddress.isSocketAddress(address)],
});`)
	wantJSON(t, result, map[string]any{
		"isIP":    []any{float64(6), float64(0), float64(6), false, float64(6), float64(0)},
		"checks":  []any{true, false, true, false, true, true},
		"errors":  []any{"ERR_INVALID_ADDRESS", "ERR_OUT_OF_RANGE", "ERR_INVALID_ARG_VALUE"},
		"address": []any{"1.2.3.4", float64(80), "ipv4", true},
	})
}

func TestNet_UnixSockets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	script := fmt.Sprintf(`
const path = %q;
const v = await new Promise((resolve) => {
  const s = net.createServer((c) => c.on('data', (d) => c.end(d)));
  s.on('error', (e) => resolve(['listen', e.code]));
  s.listen(path, () => {
    const addr = s.address();
    const c = net.connect(path);
    c.on('connect', () => c.write('u'));
    let got = '';
    c.on('data', (d) => { got += d; });
    c.on('error', (e) => resolve(['connect', e.code]));
    c.on('close', () => s.close(() => resolve([addr === path, got, require('fs').existsSync(path)])));
  });
});
done({ v });`, path)

	t.Run("work with real sockets and an unrestricted filesystem", func(t *testing.T) {
		wantJSON(t, runScript(t, &runtime.Config{}, script), map[string]any{"v": []any{true, "u", false}})
	})
	t.Run("are denied through a policy", func(t *testing.T) {
		factory := runtime.NewFilteredSocketFactory(nil, runtime.NewAllowAllPolicy())
		wantJSON(t, runScript(t, &runtime.Config{SocketFactory: factory}, script), map[string]any{"v": []any{"listen", "EACCES"}})
	})
	t.Run("are denied with a sandboxed filesystem", func(t *testing.T) {
		root := t.TempDir()
		cfg := &runtime.Config{Filesystem: runtime.NewLocalFilesystem(root)}
		result := runScript(t, cfg, fmt.Sprintf(`
const v = await new Promise((resolve) => {
  const c = net.connect(%q);
  c.on('error', (e) => resolve(e.code));
  c.on('connect', () => resolve('connected'));
});
done({ v });`, path))
		wantJSON(t, result, map[string]any{"v": "EACCES"})
	})
	t.Run("cannot reach a real socket through a no-op factory", func(t *testing.T) {
		wantJSON(t, runScript(t, &runtime.Config{SocketFactory: &runtime.NoOpSocketFactory{}}, script), map[string]any{"v": []any{"listen", "EACCES"}})
	})
	t.Run("are not inherited by embedding the real factory", func(t *testing.T) {
		wantJSON(t, runScript(t, &runtime.Config{SocketFactory: &recordingFactory{}}, script), map[string]any{"v": []any{"listen", "EACCES"}})
	})
	if _, err := os.Stat(path); err == nil {
		t.Errorf("socket file left behind")
	}
}

// Policies decide on the address actually dialed, however it was chosen.
func TestNet_InjectedControls(t *testing.T) {
	dial := func(options string) string {
		return fmt.Sprintf(`
const v = await new Promise((resolve) => {
  const c = net.connect(%s);
  c.on('connect', () => { c.destroy(); resolve('connected'); });
  c.on('error', (e) => resolve(e.code));
});
done({ v });`, options)
	}

	t.Run("goes through the injected resolver and factory", func(t *testing.T) {
		underlying := &recordingFactory{}
		ln := newEchoListener(t)
		underlying.redirect = map[string]string{"203.0.113.5": ln}
		dns := &recordingResolver{hosts: map[string][]string{"api.test": {"203.0.113.5"}}}
		result := runScript(t, &runtime.Config{SocketFactory: underlying, DNSResolver: dns}, dial(`{ host: 'api.test', port: 80 }`))
		wantJSON(t, result, map[string]any{"v": "connected"})
		if got := underlying.dialed(); !reflect.DeepEqual(got, []string{"203.0.113.5:80"}) {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a name that resolves to a denied address is denied", func(t *testing.T) {
		underlying := &recordingFactory{}
		factory := runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks())
		dns := &recordingResolver{hosts: map[string][]string{"rebind.test": {"10.0.0.5"}}}
		result := runScript(t, &runtime.Config{SocketFactory: factory, DNSResolver: dns}, dial(`{ host: 'rebind.test', port: 80 }`))
		wantJSON(t, result, map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a script's lookup cannot choose a denied address", func(t *testing.T) {
		underlying := &recordingFactory{}
		factory := runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks())
		result := runScript(t, &runtime.Config{SocketFactory: factory}, dial(`{ host: 'public.test', port: 80, lookup: (h, o, cb) => cb(null, [{ address: '10.0.0.5', family: 4 }]) }`))
		wantJSON(t, result, map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a script's lookup cannot borrow an allowed name", func(t *testing.T) {
		underlying := &recordingFactory{}
		factory := runtime.NewFilteredSocketFactory(underlying, runtime.NewHostListPolicy([]string{"allowed.test"}, nil))
		result := runScript(t, &runtime.Config{SocketFactory: factory}, dial(`{ host: 'allowed.test', port: 80, lookup: (h, o, cb) => cb(null, '203.0.113.9', 4) }`))
		wantJSON(t, result, map[string]any{"v": "EACCES"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("a zone cannot match a wildcard rule", func(t *testing.T) {
		underlying := &recordingFactory{}
		factory := runtime.NewFilteredSocketFactory(underlying, runtime.NewHostListPolicy([]string{"*.allowed.test"}, nil))
		result := runScript(t, &runtime.Config{SocketFactory: factory}, dial(`{ host: '::ffff:10.0.0.5%x.allowed.test', port: 80 }`))
		wantJSON(t, result, map[string]any{"v": "ENOTFOUND"})
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("dialed %v", got)
		}
	})

	t.Run("no-op factory", func(t *testing.T) {
		result := runScript(t, &runtime.Config{SocketFactory: &runtime.NoOpSocketFactory{}, DNSResolver: &recordingResolver{hosts: map[string][]string{"api.test": {"203.0.113.5"}}}}, dial(`{ host: 'api.test', port: 80 }`))
		wantJSON(t, result, map[string]any{"v": "ENETUNREACH"})
	})
}
