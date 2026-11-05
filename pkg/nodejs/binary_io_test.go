package nodejs

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// Value.String() stops at the first NUL and goes through UTF-8, so bytes must not cross
// from JavaScript to Go as strings. These tests send arbitrary bytes through each module
// to a real Go peer, so nothing on the receiving side is Orbital's own code.
//
// wireCases holds the JavaScript arguments of a write or end call and the bytes Node
// v20.18.1 put on the wire for them.
var wireCases = []struct {
	name, args, want string
}{
	{"buffer", `Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43])`, "410042ff0043"},
	{"leading NUL", `Buffer.from([0, 0, 0, 1])`, "00000001"},
	{"only NUL", `Buffer.from([0])`, "00"},
	{"all bytes", `Buffer.from(Array.from({ length: 256 }, (_, i) => i))`, allBytesHex()},
	{"Uint8Array", `new Uint8Array([1, 0, 2, 255, 0])`, "010002ff00"},
	{"utf8 string", `'A\0BÿC€'`, "410042c3bf43e282ac"},
	{"latin1 string", `'A\0B\xffC', 'latin1'`, "410042ff43"},
	{"binary string", `'A\0B\xffC', 'binary'`, "410042ff43"},
	{"hex string", `'410042ff0043', 'hex'`, "410042ff0043"},
	{"utf16le string", `'A\0B', 'utf16le'`, "410000004200"},
}

func allBytesHex() string {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	return hex.EncodeToString(all)
}

// wireCasesJS renders wireCases as a JavaScript array of argument lists.
func wireCasesJS() string {
	var sb strings.Builder
	sb.WriteString("[")
	for _, c := range wireCases {
		sb.WriteString("[" + c.args + "],")
	}
	sb.WriteString("]")
	return sb.String()
}

func wantWireCases(t *testing.T, got []string) {
	t.Helper()
	if len(got) != len(wireCases) {
		t.Fatalf("peer saw %d exchanges, want %d: %q", len(got), len(wireCases), got)
	}
	for i, c := range wireCases {
		if got[i] != c.want {
			t.Errorf("%s: peer received %s, want %s", c.name, got[i], c.want)
		}
	}
}

// recordConnections reads each connection to its end, in turn, and returns what it held.
// It stops once want connections have been read.
func recordConnections(t *testing.T, ln net.Listener, want int) <-chan []string {
	t.Helper()
	out := make(chan []string, 1)
	go func() {
		var got []string
		defer func() { out <- got }()
		for len(got) < want {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			data, _ := io.ReadAll(conn)
			conn.Close()
			got = append(got, hex.EncodeToString(data))
		}
	}()
	return out
}

func listenLocal(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln, ln.Addr().(*net.TCPAddr).Port
}

// freePort picks a port for a JavaScript server that a Go client dials.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// retryUntilListening runs dial until the JavaScript server is up.
func retryUntilListening[T any](dial func() (T, error)) (T, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		value, err := dial()
		if err == nil || time.Now().After(deadline) {
			return value, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBinaryWire_NetClient(t *testing.T) {
	// Writes made before the connection is up must wait for it, not vanish.
	for name, send := range map[string]string{
		"written before connect": `socket.write(...args); socket.end();`,
		"ended before connect":   `socket.end(...args);`,
		"written after connect":  `socket.on('connect', () => socket.end(...args));`,
	} {
		t.Run(name, func(t *testing.T) {
			ln, port := listenLocal(t)
			got := recordConnections(t, ln, len(wireCases))
			runScript(t, &runtime.Config{}, fmt.Sprintf(`
for (const args of %s) {
  await new Promise((resolve, reject) => {
    const socket = net.connect(%d, '127.0.0.1');
    socket.on('error', reject);
    socket.on('close', resolve);
    socket.resume();
    %s
  });
}
done({});`, wireCasesJS(), port, send))
			wantWireCases(t, <-got)
		})
	}
}

func TestBinaryWire_NetServer(t *testing.T) {
	port := freePort(t)
	got := make(chan []string, 1)
	go func() {
		var out []string
		defer func() { got <- out }()
		for range wireCases {
			conn, err := retryUntilListening(func() (net.Conn, error) {
				return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			})
			if err != nil {
				return
			}
			data, _ := io.ReadAll(conn)
			conn.Close()
			out = append(out, hex.EncodeToString(data))
		}
	}()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const cases = %s;
let next = 0;
const server = net.createServer((socket) => {
  socket.on('error', () => {});
  socket.resume();
  socket.end(...cases[next++]);
  if (next === cases.length) server.close(() => done({}));
});
server.listen(%d, '127.0.0.1');`, wireCasesJS(), port))
	wantWireCases(t, <-got)
}

// The case that first exposed the bug: the body of an http.createServer response.
func TestBinaryWire_HTTPServer(t *testing.T) {
	const bigSize = 200000
	port := freePort(t)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	get := func() ([]byte, error) {
		resp, err := retryUntilListening(func() (*http.Response, error) {
			return client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		})
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	type result struct {
		bodies []string
		big    []byte
		err    error
	}
	got := make(chan result, 1)
	go func() {
		var r result
		defer func() { got <- r }()
		// One extra exchange each for two writes, a set Content-Length and a large body.
		for range len(wireCases) + 3 {
			body, err := get()
			if err != nil {
				r.err = err
				return
			}
			r.bodies = append(r.bodies, hex.EncodeToString(body))
		}
		r.big, r.err = get()
	}()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const http = require('http');
const cases = %s;
const bin = Buffer.from([0x41, 0, 0x42, 0xff, 0, 0x43]);
const responders = cases.map((args) => (res) => res.end(...args));
responders.push((res) => { res.write(bin); res.end(bin); });
responders.push((res) => { res.setHeader('Content-Length', bin.length); res.end(bin); });
responders.push((res) => { res.writeHead(200, { 'X-A': 'b' }); res.end('\0'); });
responders.push((res) => res.end(Buffer.alloc(%d)));
let next = 0;
const server = http.createServer((req, res) => {
  const respond = responders[next++];
  if (next === responders.length) {
    res.on('finish', () => server.close(() => done({})));
  }
  respond(res);
});
server.listen(%d, '127.0.0.1');`, wireCasesJS(), bigSize, port))
	r := <-got
	if r.err != nil {
		t.Fatal(r.err)
	}
	n := len(wireCases)
	if len(r.bodies) != n+3 {
		t.Fatalf("got %d bodies", len(r.bodies))
	}
	wantWireCases(t, r.bodies[:n])
	for i, want := range []string{"410042ff0043410042ff0043", "410042ff0043", "00"} {
		if r.bodies[n+i] != want {
			t.Errorf("extra case %d = %s, want %s", i, r.bodies[n+i], want)
		}
	}
	if !bytes.Equal(r.big, make([]byte, bigSize)) {
		t.Errorf("large body has %d bytes, want %d NUL bytes", len(r.big), bigSize)
	}
}

// bodyRecorder is an HTTP handler that records request bodies.
type bodyRecorder struct {
	mu     sync.Mutex
	bodies []string
	protos []string
}

func (b *bodyRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.bodies = append(b.bodies, hex.EncodeToString(data))
	b.protos = append(b.protos, r.Proto)
	b.mu.Unlock()
}

func TestBinaryWire_HTTPClient(t *testing.T) {
	recorder := &bodyRecorder{}
	srv := httptest.NewServer(recorder)
	defer srv.Close()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const http = require('http');
for (const args of %s) {
  await new Promise((resolve, reject) => {
    const req = http.request(%q, { method: 'POST' }, (res) => {
      res.resume();
      res.on('end', resolve);
    });
    req.on('error', reject);
    req.end(...args);
  });
}
done({});`, wireCasesJS(), srv.URL))
	wantWireCases(t, recorder.bodies)
}

func TestBinaryWire_HTTP2Client(t *testing.T) {
	recorder := &bodyRecorder{}
	srv := httptest.NewUnstartedServer(recorder)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const session = require('http2').connect(%q, { rejectUnauthorized: false });
for (const args of %s) {
  await new Promise((resolve, reject) => {
    const req = session.request({ ':method': 'POST', ':path': '/' });
    req.on('error', reject);
    req.on('close', resolve);
    req.resume();
    req.end(...args);
  });
}
session.close(() => done({}));`, srv.URL, wireCasesJS()))
	wantWireCases(t, recorder.bodies)
	for _, proto := range recorder.protos {
		if proto != "HTTP/2.0" {
			t.Fatalf("request used %s", proto)
		}
	}
}

func TestBinaryWire_TLSClient(t *testing.T) {
	ca := newTestCA(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "localhost")}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := recordConnections(t, ln, len(wireCases))
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const tls = require('tls');
for (const args of %s) {
  await new Promise((resolve, reject) => {
    const socket = tls.connect({ port: %d, host: '127.0.0.1', rejectUnauthorized: false });
    socket.on('error', reject);
    socket.on('close', resolve);
    socket.resume();
    socket.end(...args);
  });
}
done({});`, wireCasesJS(), ln.Addr().(*net.TCPAddr).Port))
	wantWireCases(t, <-got)
}

func TestBinaryWire_TLSServer(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issuePEM(t, "localhost", "127.0.0.1")
	port := freePort(t)
	got := make(chan []string, 1)
	go func() {
		var out []string
		defer func() { got <- out }()
		for range wireCases {
			conn, err := retryUntilListening(func() (*tls.Conn, error) {
				return tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{RootCAs: ca.pool, ServerName: "localhost"})
			})
			if err != nil {
				return
			}
			conn.CloseWrite()
			data, _ := io.ReadAll(conn)
			conn.Close()
			out = append(out, hex.EncodeToString(data))
		}
	}()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const tls = require('tls');
const cases = %s;
let next = 0;
const server = tls.createServer({ cert: %q, key: %q }, (socket) => {
  socket.on('error', () => {});
  socket.resume();
  socket.end(...cases[next++]);
  if (next === cases.length) server.close(() => done({}));
});
server.listen(%d, '127.0.0.1');`, wireCasesJS(), certPEM, keyPEM, port))
	wantWireCases(t, <-got)
}

func TestBinaryWire_Dgram(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// send() takes no encoding, so only the single-argument cases apply.
	var messages, want []string
	for _, c := range wireCases {
		if !strings.Contains(c.args, "', '") {
			messages = append(messages, c.args)
			want = append(want, c.want)
		}
	}
	got := make(chan []string, 1)
	go func() {
		var out []string
		defer func() { got <- out }()
		buf := make([]byte, 65536)
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		for range messages {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			out = append(out, hex.EncodeToString(buf[:n]))
		}
	}()
	runScript(t, &runtime.Config{}, fmt.Sprintf(`
const socket = require('dgram').createSocket('udp4');
for (const message of [%s]) {
  await new Promise((resolve, reject) => {
    socket.send(message, %d, '127.0.0.1', (err) => (err ? reject(err) : resolve()));
  });
}
socket.close(() => done({}));`, strings.Join(messages, ", "), conn.LocalAddr().(*net.UDPAddr).Port))
	if received := <-got; !reflect.DeepEqual(received, want) {
		t.Errorf("peer received %q, want %q", received, want)
	}
}

// Node keeps a NUL in the label: real Node peers agree on 'a\0b' and on 'é\0', and
// neither matches the label cut at the NUL.
func TestBinaryWire_TLSExportKeyingMaterialLabel(t *testing.T) {
	ca := newTestCA(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "localhost")}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type exports struct{ NUL, Plain, UTF8 string }
	want := make(chan exports, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			want <- exports{}
			return
		}
		defer conn.Close()
		tlsConn := conn.(*tls.Conn)
		if err := tlsConn.Handshake(); err != nil {
			want <- exports{}
			return
		}
		state := tlsConn.ConnectionState()
		export := func(label string) string {
			material, _ := state.ExportKeyingMaterial(label, nil, 16)
			return hex.EncodeToString(material)
		}
		want <- exports{NUL: export("a\x00b"), Plain: export("a"), UTF8: export("é\x00")}
		io.Copy(io.Discard, conn)
	}()
	result := runScript(t, &runtime.Config{}, fmt.Sprintf(`
const socket = require('tls').connect({ port: %d, host: '127.0.0.1', rejectUnauthorized: false }, () => {
  const material = (label) => socket.exportKeyingMaterial(16, label).toString('hex');
  const out = { NUL: material('a\0b'), Plain: material('a'), UTF8: material('é\0') };
  socket.on('close', () => done(out));
  socket.end();
});
socket.resume();`, ln.Addr().(*net.TCPAddr).Port))
	expected := <-want
	if expected.NUL == expected.Plain {
		t.Fatal("fixture: labels should differ")
	}
	wantJSON(t, result, map[string]any{"NUL": expected.NUL, "Plain": expected.Plain, "UTF8": expected.UTF8})
}

func TestBinaryWire_NetRead(t *testing.T) {
	ln, port := listenLocal(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Write([]byte{0x41, 0, 0x42, 0xff, 0, 0x43})
		conn.Close()
	}()
	result := runScript(t, &runtime.Config{}, fmt.Sprintf(`
const socket = net.connect(%d, '127.0.0.1');
const chunks = [];
socket.on('data', (d) => chunks.push(d));
socket.on('close', () => done({ hex: Buffer.concat(chunks).toString('hex') }));`, port))
	wantJSON(t, result, map[string]any{"hex": "410042ff0043"})
}

// The script runs unchanged in Node; its expected output is what Node printed.
func TestBinaryChildProcess_MatchesNode(t *testing.T) {
	script, err := os.ReadFile("testdata/binary_child_process.js")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/binary_child_process.node.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	wantJSON(t, runJS(t, &runtime.Config{}, string(script)), want)
}
