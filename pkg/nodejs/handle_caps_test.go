package nodejs

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// The expected errors below are what Node 20.18.1 gives at the OS's descriptor limit
// (captured with ulimit -n), which the caps reproduce.

// runCapped runs source with exactly the caps cfg sets, unlike runJS, which opens them,
// and with the host access runJS gives.
func runCapped(t *testing.T, cfg *runtime.Config, source string) map[string]any {
	t.Helper()
	cfg.EnableConsole, cfg.EnableTimers = true, true
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	inst, err := New(hostAccess(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	return runIn(t, inst, `const describe = (e) => e ? [e.name, e.code, e.errno, e.syscall, e.address, e.port, e.path, e.dest, e.message].map(String).join('|') : 'none';
`+source)
}

func TestCaps_ConnectionsOverTheCapFailAsInNode(t *testing.T) {
	host, port, _ := net.SplitHostPort(newEchoListener(t))
	got := runCapped(t, &runtime.Config{MaxOpenSockets: 2}, `
const net = require('net');
const connect = () => new Promise((resolve) => {
  const s = net.connect(`+port+`, '`+host+`');
  s.on('connect', () => resolve({ s }));
  s.on('error', (e) => resolve({ e }));
});
(async () => {
  const a = await connect(), b = await connect(), c = await connect();
  a.s.destroy();
  await new Promise((r) => setTimeout(r, 50));
  const d = await connect();
  done({ a: !!a.s, b: !!b.s, c: describe(c.e), d: d.e ? describe(d.e) : 'connected' });
  b.s.destroy(); if (d.s) d.s.destroy();
})();
`)
	wantJSON(t, got, map[string]any{
		"a": true, "b": true,
		"c": "Error|EMFILE|-24|open|undefined|undefined|undefined|undefined|open EMFILE",
		"d": "connected",
	})
}

func TestCaps_ListenOverTheCapFailsAsInNode(t *testing.T) {
	got := runCapped(t, &runtime.Config{}, `
const net = require('net');
const listen = (...args) => new Promise((resolve) => {
  const s = net.createServer();
  s.on('error', resolve);
  s.listen(...args, () => resolve(null));
});
(async () => {
  done({ anyPort: describe(await listen(0, '127.0.0.1')), port: describe(await listen(45678, '127.0.0.1')) });
})();
`)
	wantJSON(t, got, map[string]any{
		"anyPort": "Error|EMFILE|-24|listen|127.0.0.1|undefined|undefined|undefined|listen EMFILE: too many open files 127.0.0.1",
		"port":    "Error|EMFILE|-24|listen|127.0.0.1|45678|undefined|undefined|listen EMFILE: too many open files 127.0.0.1:45678",
	})
}

// A connection a server accepts over the cap is closed unseen, as libuv drops one it
// accepts at the descriptor limit. Here the listener and the client use the two places,
// so the server's side of the connection is the third.
func TestCaps_AcceptOverTheCapIsDropped(t *testing.T) {
	got := runCapped(t, &runtime.Config{MaxOpenSockets: 2}, `
const net = require('net');
let connections = 0;
const server = net.createServer(() => { connections++; }).listen(0, '127.0.0.1', () => {
  const client = net.connect(server.address().port, '127.0.0.1');
  client.on('error', () => {});
  client.on('close', () => {
    server.close();
    done({ clientClosed: true, connections });
  });
});
`)
	wantJSON(t, got, map[string]any{"clientClosed": true, "connections": float64(0)})
}

func TestCaps_DgramOverTheCapFailsAsInNode(t *testing.T) {
	got := runCapped(t, &runtime.Config{}, `
const dgram = require('dgram');
const results = {};
const bound = dgram.createSocket('udp4');
bound.on('error', (e) => {
  results.bind = describe(e);
  const unbound = dgram.createSocket('udp4');
  let called = false;
  unbound.on('error', (e) => {
    results.implicitBind = describe(e);
    setTimeout(() => { results.sendCallback = called; done(results); }, 20);
  });
  unbound.send('x', 9999, '127.0.0.1', () => { called = true; });
});
bound.bind(0, '127.0.0.1');
`)
	wantJSON(t, got, map[string]any{
		"bind":         "Error|EMFILE|-24|bind|127.0.0.1|undefined|undefined|undefined|bind EMFILE 127.0.0.1",
		"implicitBind": "Error|EMFILE|-24|bind|0.0.0.0|undefined|undefined|undefined|bind EMFILE 0.0.0.0",
		"sendCallback": false,
	})
}

func TestCaps_RequestsOverTheCapFailAsInNode(t *testing.T) {
	got := runCapped(t, &runtime.Config{}, `
const results = {};
fetch('http://127.0.0.1:1/').then(() => 'fetched', (e) => describe(e) + '|cause:' + String(e.cause)).then((outcome) => {
  results.fetch = outcome;
  const req = require('http').get('http://127.0.0.1:1/');
  req.on('error', (e) => {
    results.http = describe(e);
    const session = require('http2').connect('http://127.0.0.1:45679');
    session.on('error', (e) => { results.http2 = describe(e); done(results); });
  });
});
`)
	wantJSON(t, got, map[string]any{
		"fetch": "Error|EMFILE|-24|open|undefined|undefined|undefined|undefined|open EMFILE|cause:undefined",
		"http":  "Error|EMFILE|-24|open|undefined|undefined|undefined|undefined|open EMFILE",
		"http2": "Error|EMFILE|-24|connect|127.0.0.1|45679|undefined|undefined|connect EMFILE 127.0.0.1:45679 - Local (undefined:undefined)",
	})
}

// A worker counts its sockets against its parent's cap.
func TestCaps_WorkersShareTheParentsSockets(t *testing.T) {
	addr := newEchoListener(t)
	host, port, _ := net.SplitHostPort(addr)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker.js"), []byte(`
const { parentPort } = require('worker_threads');
const s = require('net').connect(`+port+`, '`+host+`');
s.on('connect', () => { parentPort.postMessage('connected'); s.destroy(); });
s.on('error', (e) => parentPort.postMessage(e.code));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCapped(t, &runtime.Config{MaxOpenSockets: 1, MaxOpenFiles: -1, MaxWorkers: -1}, `
const held = require('net').connect(`+port+`, '`+host+`');
held.on('connect', () => {
  const { Worker } = require('worker_threads');
  const w = new Worker(`+strconv.Quote(filepath.Join(dir, "worker.js"))+`);
  w.on('message', (m) => { done({ worker: m }); held.destroy(); });
});
`)
	wantJSON(t, got, map[string]any{"worker": "EMFILE"})
}

func TestCaps_FilesOverTheCapFailAsInNode(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, c := filepath.Join(dir, "b.txt"), filepath.Join(dir, "c.txt")
	q := strconv.Quote
	got := runCapped(t, &runtime.Config{MaxOpenFiles: 2}, `
const fs = require('fs');
const attempt = (f) => { try { f(); return 'ok'; } catch (e) { return describe(e); } };
const results = {};
const fd1 = fs.openSync(`+q(a)+`, 'r'), fd2 = fs.openSync(`+q(a)+`, 'r');
results.open = attempt(() => fs.openSync(`+q(a)+`, 'r'));
results.readFile = attempt(() => fs.readFileSync(`+q(a)+`));
results.writeFile = attempt(() => fs.writeFileSync(`+q(c)+`, 'x'));
results.readdir = attempt(() => fs.readdirSync(`+q(dir)+`));
fs.closeSync(fd1);
results.copyFileOneFree = attempt(() => fs.copyFileSync(`+q(a)+`, `+q(b)+`));
results.readFileOneFree = attempt(() => fs.readFileSync(`+q(a)+`));
fs.closeSync(fd2);
results.copyFile = attempt(() => fs.copyFileSync(`+q(a)+`, `+q(b)+`));
const held1 = fs.openSync(`+q(a)+`, 'r'), held2 = fs.openSync(`+q(a)+`, 'r');
fs.createReadStream(`+q(a)+`).on('error', (e) => {
  results.readStream = describe(e);
  fs.readFile(`+q(a)+`, (e) => {
    results.readFileCallback = describe(e);
    fs.closeSync(held1); fs.closeSync(held2);
    done(results);
  });
});
`)
	open := func(p string) string {
		return "Error|EMFILE|-24|open|undefined|undefined|" + p + "|undefined|EMFILE: too many open files, open '" + p + "'"
	}
	wantJSON(t, got, map[string]any{
		"open":             open(a),
		"readFile":         open(a),
		"writeFile":        open(c),
		"readdir":          "Error|EMFILE|-24|scandir|undefined|undefined|" + dir + "|undefined|EMFILE: too many open files, scandir '" + dir + "'",
		"copyFileOneFree":  "Error|EMFILE|-24|copyfile|undefined|undefined|" + a + "|" + b + "|EMFILE: too many open files, copyfile '" + a + "' -> '" + b + "'",
		"readFileOneFree":  "ok",
		"copyFile":         "ok",
		"readStream":       open(a),
		"readFileCallback": open(a),
	})
}

// With no files allowed, scripts still load: loading modules isn't the script opening files.
func TestCaps_ModulesLoadWithNoFilesAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dep.js"), []byte(`module.exports = 'dep';`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCapped(t, &runtime.Config{}, `
let read;
try { require('fs').readFileSync(`+strconv.Quote(filepath.Join(dir, "dep.js"))+`); read = 'read'; } catch (e) { read = e.code; }
done({ dep: require(`+strconv.Quote(filepath.Join(dir, "dep.js"))+`), read });
`)
	wantJSON(t, got, map[string]any{"dep": "dep", "read": "EMFILE"})
}

// Files a worker leaves open close when it exits, giving their places back to the parent.
func TestCaps_WorkerExitGivesBackItsFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worker.js"), []byte(`
require('fs').openSync(`+strconv.Quote(file)+`, 'r');
require('worker_threads').parentPort.postMessage('opened');
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runCapped(t, &runtime.Config{MaxOpenFiles: 1, MaxWorkers: -1}, `
const fs = require('fs');
const { Worker } = require('worker_threads');
const w = new Worker(`+strconv.Quote(filepath.Join(dir, "worker.js"))+`);
w.on('exit', () => {
  let after;
  try { fs.closeSync(fs.openSync(`+strconv.Quote(file)+`, 'r')); after = 'opened'; } catch (e) { after = e.code; }
  done({ after });
});
`)
	wantJSON(t, got, map[string]any{"after": "opened"})
}

// A server that drops a connection over the cap keeps accepting: once a place frees, the
// next connection reaches the script.
func TestCaps_ServerKeepsAcceptingAfterADrop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	firstDropped := make(chan bool, 1)
	go func() {
		addr := "127.0.0.1:" + strconv.Itoa(port)
		var conn net.Conn
		for i := 0; i < 100; i++ {
			if conn, err = net.Dial("tcp", addr); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if conn == nil {
			firstDropped <- false
			return
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, rerr := conn.Read(make([]byte, 1))
		conn.Close()
		firstDropped <- rerr != nil
		time.Sleep(600 * time.Millisecond)
		if second, err := net.Dial("tcp", addr); err == nil {
			t.Cleanup(func() { second.Close() })
		}
	}()

	got := runCapped(t, &runtime.Config{MaxOpenSockets: 2, Timeout: 10 * time.Second}, `
const net = require('net'), dgram = require('dgram');
const udp = dgram.createSocket('udp4');
udp.bind(0, '127.0.0.1', () => {
  // The listener and the UDP socket fill the cap; the first connection is dropped.
  const server = net.createServer((socket) => {
    socket.destroy();
    server.close();
    done({ accepted: true });
  }).listen(`+strconv.Itoa(port)+`, '127.0.0.1');
  setTimeout(() => udp.close(), 300);
});
`)
	wantJSON(t, got, map[string]any{"accepted": true})
	if !<-firstDropped {
		t.Error("the first connection was not dropped")
	}
}

// Each socket or listener made from an inherited descriptor wraps a duplicate of it, so
// it counts against the cap like any other, and closing it frees its place.
func TestCaps_InheritedDescriptorsCount(t *testing.T) {
	t.Run("sockets", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		fd := int(r.Fd())
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedDescriptors(fd)
		got := runCapped(t, &runtime.Config{SocketFactory: factory, MaxOpenSockets: 1}, `
const net = require('net');
const wrap = () => { try { return { s: new net.Socket({ fd: `+strconv.Itoa(fd)+` }) }; } catch (e) { return { e }; } };
const a = wrap(), b = wrap();
a.s.destroy();
setTimeout(() => {
  const c = wrap();
  done({ a: !!a.s, b: describe(b.e), c: c.s ? 'wrapped' : describe(c.e) });
  if (c.s) c.s.destroy();
}, 50);
`)
		wantJSON(t, got, map[string]any{
			"a": true,
			"b": "Error|EMFILE|-24|open|undefined|undefined|undefined|undefined|open EMFILE",
			"c": "wrapped",
		})
	})
	t.Run("listeners", func(t *testing.T) {
		fd, _ := inheritedListener(t)
		factory := runtime.NewRealSocketFactory()
		factory.AllowInheritedDescriptors(fd)
		got := runCapped(t, &runtime.Config{SocketFactory: factory, MaxOpenSockets: 1}, `
const net = require('net');
const listen = () => new Promise((resolve) => {
  const server = net.createServer();
  server.on('error', (e) => resolve({ e }));
  server.listen({ fd: `+strconv.Itoa(fd)+` }, () => resolve({ server }));
});
(async () => {
  const a = await listen(), b = await listen();
  await new Promise((r) => a.server.close(r));
  const c = await listen();
  done({ a: !!a.server, b: describe(b.e), c: c.server ? 'listening' : describe(c.e) });
  if (c.server) c.server.close();
})();
`)
		wantJSON(t, got, map[string]any{
			"a": true,
			"b": "Error|EMFILE|-24|listen|null|undefined|undefined|undefined|listen EMFILE: too many open files",
			"c": "listening",
		})
	})
}

// A UDP bind that fails before opening a socket, explicitly or on a first send, gives
// its place back, so failed binds don't use up the cap even if the script never closes
// the socket.
func TestCaps_FailedDgramBindsGiveBackTheirPlace(t *testing.T) {
	taken, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := strconv.Itoa(taken.LocalAddr().(*net.UDPAddr).Port)
	got := runCapped(t, &runtime.Config{MaxOpenSockets: 1}, `
const dgram = require('dgram');
const results = {};
const attempt = (start) => new Promise((resolve) => {
  const s = dgram.createSocket('udp4');
  s.on('error', (e) => resolve(e.code));
  start(s, () => resolve('ok'));
});
(async () => {
  results.send = await attempt((s) => s.send('x', 9999, 'host.invalid'));
  results.bind = await attempt((s) => s.bind(`+port+`, '127.0.0.1'));
  const ok = dgram.createSocket('udp4');
  ok.on('error', (e) => done({ ...results, after: e.code }));
  ok.bind(0, '127.0.0.1', () => { ok.close(); done({ ...results, after: 'bound' }); });
})();
`)
	if got["send"] == "EMFILE" || got["bind"] != "EADDRINUSE" || got["after"] != "bound" {
		t.Fatalf("got %v, want a failed send and an EADDRINUSE bind, then a bind that works", got)
	}
}

// slowUDPFactory makes real UDP sockets whose sends to 127.0.0.2 wait first, so a test can
// have another send fail, or the script close the socket, while one is still going.
type slowUDPFactory struct {
	*runtime.RealSocketFactory
	mu      sync.Mutex
	sockets []runtime.UDPSocket
}

type slowUDPSocket struct{ runtime.UDPSocket }

func (s slowUDPSocket) Send(data []byte, address string, port int) (int, error) {
	if address == "127.0.0.2" {
		time.Sleep(150 * time.Millisecond)
	}
	return s.UDPSocket.Send(data, address, port)
}

func (f *slowUDPFactory) CreateUDPSocket(socketType string) runtime.UDPSocket {
	s := f.RealSocketFactory.CreateUDPSocket(socketType)
	f.mu.Lock()
	f.sockets = append(f.sockets, s)
	f.mu.Unlock()
	return slowUDPSocket{s}
}

// A send that fails while another on the same socket is still going leaves the socket's
// place held, since the other one may yet open it.
func TestCaps_DgramPlaceStaysWhileASendIsGoing(t *testing.T) {
	factory := &slowUDPFactory{RealSocketFactory: runtime.NewRealSocketFactory()}
	got := runCapped(t, &runtime.Config{SocketFactory: factory, MaxOpenSockets: 1}, `
const dgram = require('dgram');
const s = dgram.createSocket('udp4');
s.on('error', () => {});
const results = {};
const other = () => new Promise((resolve) => {
  const o = dgram.createSocket('udp4');
  o.on('error', (e) => resolve(e.code));
  o.bind(0, '127.0.0.1', () => { o.close(); resolve('bound'); });
});
s.send('x', 9, '127.0.0.2', async (e) => {
  results.slow = e ? e.code : 'sent';
  results.whileBound = await other();
  s.close();
  setTimeout(async () => { results.afterClose = await other(); done(results); }, 20);
});
s.send('x', 9, 'host.invalid', (e) => { results.fast = e ? e.code : 'sent'; });
`)
	if got["fast"] == "sent" || got["slow"] != "sent" || got["whileBound"] != "EMFILE" || got["afterClose"] != "bound" {
		t.Fatalf("got %v, want the slow send to bind while the fast one fails, and its place held until close", got)
	}
}

// A send still going when the script closes the socket doesn't leave a descriptor open.
func TestCaps_DgramSendAfterCloseLeavesNothingOpen(t *testing.T) {
	factory := &slowUDPFactory{RealSocketFactory: runtime.NewRealSocketFactory()}
	runCapped(t, &runtime.Config{SocketFactory: factory, MaxOpenSockets: 1}, `
const dgram = require('dgram');
const s = dgram.createSocket('udp4');
s.on('error', () => {});
s.send('x', 9, '127.0.0.2');
s.close();
setTimeout(() => done({}), 300);
`)
	factory.mu.Lock()
	defer factory.mu.Unlock()
	for _, s := range factory.sockets {
		if addr := s.LocalAddr(); addr != nil {
			t.Fatalf("socket still open on %v after close", addr)
		}
	}
}
