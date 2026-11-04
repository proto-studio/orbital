package nodejs

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
	"proto.zip/studio/orbital/pkg/runtime"
)

// dnsFixture is a local DNS server.
type dnsFixture struct {
	addr    string
	queries atomic.Int32
}

func newDNSFixture(t *testing.T) *dnsFixture {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fixture := &dnsFixture{addr: conn.LocalAddr().String()}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			fixture.queries.Add(1)
			var query dnsmessage.Message
			if err := query.Unpack(buf[:n]); err != nil || len(query.Questions) != 1 {
				continue
			}
			reply, err := answerDNS(query)
			if err != nil {
				continue
			}
			conn.WriteTo(reply, from)
		}
	}()
	return fixture
}

func answerDNS(query dnsmessage.Message) ([]byte, error) {
	question := query.Questions[0]
	name := strings.TrimSuffix(question.Name.String(), ".")
	header := dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 60}
	response := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: query.ID, Response: true},
		Questions: query.Questions,
	}
	add := func(rrtype dnsmessage.Type, body dnsmessage.ResourceBody) {
		if question.Type != rrtype && question.Type != dnsmessage.TypeALL {
			return
		}
		h := header
		h.Type = rrtype
		response.Answers = append(response.Answers, dnsmessage.Resource{Header: h, Body: body})
	}
	target := dnsmessage.MustNewName("target.test.")
	switch name {
	case "nope.test":
		response.RCode = dnsmessage.RCodeNameError
	case "fail.test":
		response.RCode = dnsmessage.RCodeServerFailure
	case "a.test":
		add(dnsmessage.TypeA, &dnsmessage.AResource{A: [4]byte{1, 2, 3, 4}})
		add(dnsmessage.TypeAAAA, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 15: 1}})
		add(dnsmessage.TypeMX, &dnsmessage.MXResource{Pref: 10, MX: target})
		add(dnsmessage.TypeTXT, &dnsmessage.TXTResource{TXT: []string{"ab", "cd"}})
		add(dnsmessage.TypeNS, &dnsmessage.NSResource{NS: target})
		add(dnsmessage.TypeCNAME, &dnsmessage.CNAMEResource{CNAME: target})
		add(dnsmessage.TypeSRV, &dnsmessage.SRVResource{Priority: 1, Weight: 2, Port: 443, Target: target})
		add(dnsmessage.TypeSOA, &dnsmessage.SOAResource{NS: target, MBox: target, Serial: 1, Refresh: 2, Retry: 3, Expire: 4, MinTTL: 5})
		add(dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: target})
		add(dnsmessage.Type(257), &dnsmessage.UnknownResource{Type: 257, Data: append([]byte{0, 5}, "issueca.test"...)})
	case "4.3.2.1.in-addr.arpa":
		add(dnsmessage.TypePTR, &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("rev.test.")})
	}
	return response.Pack()
}

const dnsPrelude = `
const dns = require('dns');
const ask = (fn, ...args) => new Promise((resolve) => fn(...args, (e, ...v) => resolve(e ? [e.code, e.syscall, e.hostname] : v)));
`

func TestDNS_RecordsFromAServer(t *testing.T) {
	fixture := newDNSFixture(t)
	result := runScript(t, &runtime.Config{}, dnsPrelude+fmt.Sprintf(`
dns.setServers([%q]);
done({
  a: await ask(dns.resolve4, 'a.test'),
  ttl: await ask(dns.resolve4, 'a.test', { ttl: true }),
  aaaa: await ask(dns.resolve6, 'a.test'),
  mx: await ask(dns.resolveMx, 'a.test'),
  txt: await ask(dns.resolveTxt, 'a.test'),
  ns: await ask(dns.resolveNs, 'a.test'),
  cname: await ask(dns.resolveCname, 'a.test'),
  srv: await ask(dns.resolveSrv, 'a.test'),
  soa: await ask(dns.resolveSoa, 'a.test'),
  ptr: await ask(dns.resolvePtr, 'a.test'),
  caa: await ask(dns.resolveCaa, 'a.test'),
  generic: await ask(dns.resolve, 'a.test', 'MX'),
  reverse: await ask(dns.reverse, '1.2.3.4'),
  promise: await dns.promises.resolve4('a.test'),
  notFound: await ask(dns.resolve4, 'nope.test'),
  noData: await ask(dns.resolve4, 'empty.test'),
  serverFailure: await ask(dns.resolve4, 'fail.test'),
  servers: dns.getServers().length,
});`, fixture.addr))
	wantJSON(t, result, map[string]any{
		"a":             []any{[]any{"1.2.3.4"}},
		"ttl":           []any{[]any{map[string]any{"address": "1.2.3.4", "ttl": float64(60)}}},
		"aaaa":          []any{[]any{"2001::1"}},
		"mx":            []any{[]any{map[string]any{"exchange": "target.test", "priority": float64(10)}}},
		"txt":           []any{[]any{[]any{"ab", "cd"}}},
		"ns":            []any{[]any{"target.test"}},
		"cname":         []any{[]any{"target.test"}},
		"srv":           []any{[]any{map[string]any{"name": "target.test", "port": float64(443), "priority": float64(1), "weight": float64(2)}}},
		"soa":           []any{map[string]any{"nsname": "target.test", "hostmaster": "target.test", "serial": float64(1), "refresh": float64(2), "retry": float64(3), "expire": float64(4), "minttl": float64(5)}},
		"ptr":           []any{[]any{"target.test"}},
		"caa":           []any{[]any{map[string]any{"critical": float64(0), "issue": "ca.test"}}},
		"generic":       []any{[]any{map[string]any{"exchange": "target.test", "priority": float64(10)}}},
		"reverse":       []any{[]any{"rev.test"}},
		"promise":       []any{"1.2.3.4"},
		"notFound":      []any{"ENOTFOUND", "queryA", "nope.test"},
		"noData":        []any{"ENODATA", "queryA", "empty.test"},
		"serverFailure": []any{"ESERVFAIL", "queryA", "fail.test"},
		"servers":       float64(1),
	})
}

// A script choosing its DNS server would otherwise send traffic the policy never saw.
func TestDNS_ScriptServersNeedRealSockets(t *testing.T) {
	fixture := newDNSFixture(t)
	policed := runtime.NewFilteredSocketFactory(nil, runtime.NewDenyAllPolicy())
	result := runScript(t, &runtime.Config{SocketFactory: policed, DNSResolver: &recordingResolver{hosts: map[string][]string{"a.test": {"9.9.9.9"}}}}, dnsPrelude+fmt.Sprintf(`
dns.setServers([%q]);
done({ a: await ask(dns.resolve4, 'a.test') });`, fixture.addr))
	wantJSON(t, result, map[string]any{"a": []any{[]any{"9.9.9.9"}}})
	if got := fixture.queries.Load(); got != 0 {
		t.Errorf("script reached its own DNS server %d times", got)
	}
}

// embedsRealResolver restricts lookups by overriding them. It must not inherit a way of
// asking DNS servers directly.
type embedsRealResolver struct {
	runtime.RealResolver
}

func (embedsRealResolver) LookupIP(context.Context, string, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("9.9.9.9")}, nil
}

func TestDNS_EmbeddingTheRealResolverInheritsNoRawQueries(t *testing.T) {
	fixture := newDNSFixture(t)
	result := runScript(t, &runtime.Config{DNSResolver: &embedsRealResolver{}}, dnsPrelude+fmt.Sprintf(`
dns.setServers([%q]);
done({ a: await ask(dns.resolve4, 'a.test'), soa: await ask(dns.resolveSoa, 'a.test') });`, fixture.addr))
	wantJSON(t, result, map[string]any{"a": []any{[]any{"9.9.9.9"}}, "soa": []any{"ENOTIMP", "querySoa", "a.test"}})
	if got := fixture.queries.Load(); got != 0 {
		t.Errorf("script reached its own DNS server %d times", got)
	}
}

func TestDNS_RealResolverIgnoresScriptServersBehindAPolicy(t *testing.T) {
	fixture := newDNSFixture(t)
	policed := runtime.NewFilteredSocketFactory(nil, runtime.NewAllowAllPolicy())
	result := runScript(t, &runtime.Config{SocketFactory: policed}, dnsPrelude+fmt.Sprintf(`
dns.setServers([%q]);
await ask(dns.resolve4, 'a.test');
done({ ok: true });`, fixture.addr))
	wantJSON(t, result, map[string]any{"ok": true})
	if got := fixture.queries.Load(); got != 0 {
		t.Errorf("script reached its own DNS server %d times", got)
	}
}

func TestDNS_InjectedResolver(t *testing.T) {
	resolver := &recordingResolver{hosts: map[string][]string{"both.test": {"::1", "1.2.3.4", "5.6.7.8"}}}
	result := runScript(t, &runtime.Config{DNSResolver: resolver}, dnsPrelude+`
done({
  first: await ask(dns.lookup, 'both.test'),
  family4: await ask(dns.lookup, 'both.test', { family: 4 }),
  family6: await ask(dns.lookup, 'both.test', 6),
  all: await ask(dns.lookup, 'both.test', { all: true }),
  ipv4first: await ask(dns.lookup, 'both.test', { all: true, order: 'ipv4first' }),
  verbatimFalse: await ask(dns.lookup, 'both.test', { verbatim: false }),
  literal: await ask(dns.lookup, '1.2.3.4', { all: true }),
  empty: await ask(dns.lookup, ''),
  missing: await ask(dns.lookup, 'nope.test'),
  promise: await dns.promises.lookup('both.test', { family: 4 }),
  promiseAll: await dns.promises.lookup('both.test', { family: 4, all: true }),
  notAvailable: await ask(dns.resolveSoa, 'both.test'),
  resolve4: await ask(dns.resolve4, 'both.test'),
  orders: (() => { dns.setDefaultResultOrder('ipv4first'); const v = dns.getDefaultResultOrder(); dns.setDefaultResultOrder('verbatim'); return v; })(),
  badFamily: (() => { try { dns.lookup('x', { family: 5 }, () => {}); } catch (e) { return e.code; } })(),
  badServers: (() => { try { dns.setServers(['nope']); } catch (e) { return e.code; } })(),
});`)
	v4 := map[string]any{"address": "1.2.3.4", "family": float64(4)}
	v4b := map[string]any{"address": "5.6.7.8", "family": float64(4)}
	v6 := map[string]any{"address": "::1", "family": float64(6)}
	wantJSON(t, result, map[string]any{
		"first":         []any{"::1", float64(6)},
		"family4":       []any{"1.2.3.4", float64(4)},
		"family6":       []any{"::1", float64(6)},
		"all":           []any{[]any{v6, v4, v4b}},
		"ipv4first":     []any{[]any{v4, v4b, v6}},
		"verbatimFalse": []any{"1.2.3.4", float64(4)},
		"literal":       []any{[]any{v4}},
		"empty":         []any{nil, float64(4)},
		"missing":       []any{"ENOTFOUND", "getaddrinfo", "nope.test"},
		"promise":       v4,
		"promiseAll":    []any{v4, v4b},
		"notAvailable":  []any{"ENOTIMP", "querySoa", "both.test"},
		"resolve4":      []any{[]any{"1.2.3.4", "5.6.7.8"}},
		"orders":        "ipv4first",
		"badFamily":     "ERR_INVALID_ARG_VALUE",
		"badServers":    "ERR_INVALID_IP_ADDRESS",
	})
}

func TestDgram_NodeBehavior(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `
const dgram = require('dgram');
const bound = () => new Promise((r) => { const s = dgram.createSocket('udp4'); s.bind(0, '127.0.0.1', () => r(s)); });
const out = {};
const a = await bound();
const port = a.address().port;
out.binary = await new Promise((resolve) => {
  a.once('message', (m, i) => resolve([m.toString('hex'), i.family, i.size]));
  const b = dgram.createSocket('udp4');
  b.send(Buffer.from([0, 255, 128, 0, 65]), port, '127.0.0.1', () => b.close());
});
out.connected = await new Promise((resolve) => {
  const b = dgram.createSocket('udp4');
  b.connect(port, '127.0.0.1', () => {
    const remote = b.remoteAddress();
    b.send('c');
    a.once('message', (m) => {
      const v = [String(m), remote.address, remote.family, remote.port === port];
      try { b.connect(1); } catch (e) { v.push(e.code); }
      b.disconnect();
      try { b.remoteAddress(); } catch (e) { v.push(e.code); }
      b.close();
      resolve(v);
    });
  });
});
out.filtered = await new Promise(async (resolve) => {
  const stranger = await bound();
  const b = dgram.createSocket('udp4');
  const got = [];
  b.on('message', (m) => got.push(String(m)));
  b.connect(port, '127.0.0.1', () => {
    const p = b.address().port;
    stranger.send('stranger', p, '127.0.0.1');
    setTimeout(() => a.send('peer', p, '127.0.0.1'), 15);
    setTimeout(() => { stranger.close(); b.close(); resolve(got); }, 50);
  });
});
out.options = [a.setTTL(5), a.setMulticastTTL(6), a.setMulticastLoopback(true), a.getRecvBufferSize() > 0, a.getSendBufferSize() > 0, a.getSendQueueCount()];
a.setRecvBufferSize(100000);
a.setSendBufferSize(100000);
try { a.remoteAddress(); } catch (e) { out.notConnected = e.code; }
try { a.bind(0); } catch (e) { out.alreadyBound = e.code; }
a.close();
try { a.close(); } catch (e) { out.notRunning = e.code; }
try { dgram.createSocket('nope'); } catch (e) { out.badType = e.code; }
done(out);`)
	wantJSON(t, result, map[string]any{
		"binary":       []any{"00ff800041", "IPv4", float64(5)},
		"connected":    []any{"c", "127.0.0.1", "IPv4", true, "ERR_SOCKET_DGRAM_IS_CONNECTED", "ERR_SOCKET_DGRAM_NOT_CONNECTED"},
		"filtered":     []any{"peer"},
		"options":      []any{float64(5), float64(6), true, true, true, float64(0)},
		"notConnected": "ERR_SOCKET_DGRAM_NOT_CONNECTED",
		"alreadyBound": "ERR_SOCKET_ALREADY_BOUND",
		"notRunning":   "ERR_SOCKET_DGRAM_NOT_RUNNING",
		"badType":      "ERR_SOCKET_BAD_TYPE",
	})

	t.Run("a name is resolved and checked", func(t *testing.T) {
		dns := &recordingResolver{hosts: map[string][]string{"private.test": {"10.0.0.5"}}}
		factory := runtime.NewFilteredSocketFactory(nil, runtime.CommonPolicies.NoPrivateNetworks())
		result := runScript(t, &runtime.Config{DNSResolver: dns, SocketFactory: factory}, `
const s = require('dgram').createSocket('udp4');
const code = await new Promise((resolve) => s.send('x', 53, 'private.test', (e) => resolve(e ? e.code : 'sent')));
s.close();
done({ code });`)
		wantJSON(t, result, map[string]any{"code": "EACCES"})
	})
}

func TestProcess_ReportsLinkedV8(t *testing.T) {
	result := runScript(t, &runtime.Config{}, `done({ v8: process.versions.v8 });`)
	if v, _ := result["v8"].(string); v == "" || strings.HasPrefix(v, "12.9.") || v == "unknown" {
		t.Fatalf("process.versions.v8 = %q", result["v8"])
	}
}

func TestFetch_NodeBehavior(t *testing.T) {
	result := runHTTP(t, &runtime.Config{}, `
const out = {};
const server = await serve((q, p) => {
  if (q.url === '/redirect') { p.writeHead(302, { Location: '/there' }); p.end(); return; }
  if (q.url === '/there') { p.end('there'); return; }
  const c = [];
  q.on('data', (d) => c.push(d));
  q.on('end', () => p.end(Buffer.concat(c).toString('hex')));
});
const base = 'http://127.0.0.1:' + server.address().port;
const manual = await fetch(base + '/redirect', { redirect: 'manual' });
out.manual = [manual.status, manual.headers.get('location')];
out.error = await fetch(base + '/redirect', { redirect: 'error' }).catch((e) => [e.name, e.message, e.cause.message]);
out.follow = await (await fetch(base + '/redirect')).text();
out.binary = await (await fetch(base, { method: 'POST', body: new Uint8Array([0, 255, 128]) })).text();
out.text = await (await fetch(base, { method: 'POST', body: '€' })).text();
const port = server.address().port;
server.close();
server.closeAllConnections();
await new Promise((r) => setTimeout(r, 10));
out.refused = await fetch('http://127.0.0.1:' + port + '/').catch((e) => [e.name, e.message, e.cause.code, e.cause.syscall, e.cause.address, e.cause.port === port]);
const urlencoded = await new Response('a=1&b=x%20y', { headers: { 'content-type': 'application/x-www-form-urlencoded' } }).formData();
out.urlencoded = [urlencoded.get('a'), urlencoded.get('b')];
const multipart = await new Response('--B\r\nContent-Disposition: form-data; name="a"\r\n\r\n1\r\n--B\r\nContent-Disposition: form-data; name="f"; filename="n.txt"\r\nContent-Type: text/plain\r\n\r\nhello\r\n--B--\r\n', {
  headers: { 'content-type': 'multipart/form-data; boundary=B' },
}).formData();
const file = multipart.get('f');
out.multipart = [multipart.get('a'), file.name, file.type, await file.text()];
out.badForm = await new Response('x', { headers: { 'content-type': 'text/plain' } }).formData().catch((e) => e.name);
done(out);`)
	wantJSON(t, result, map[string]any{
		"manual":     []any{float64(302), "/there"},
		"error":      []any{"TypeError", "fetch failed", "unexpected redirect"},
		"follow":     "there",
		"binary":     "00ff80",
		"text":       "e282ac",
		"refused":    []any{"TypeError", "fetch failed", "ECONNREFUSED", "connect", "127.0.0.1", true},
		"urlencoded": []any{"1", "x y"},
		"multipart":  []any{"1", "n.txt", "text/plain", "hello"},
		"badForm":    "TypeError",
	})
}
