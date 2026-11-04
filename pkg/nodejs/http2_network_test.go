package nodejs

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/runtime"
)

func itoa(n int) string { return strconv.Itoa(n) }

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// h2Server is a real HTTP/2 TLS server that counts connections.
type h2Server struct {
	srv       *httptest.Server
	addr      string
	port      int
	conns     atomic.Int64
	requests  atomic.Int64
	cancelled chan string
	mu        sync.Mutex
	hosts     []string
}

func newH2Server(t *testing.T, cert tls.Certificate) *h2Server {
	t.Helper()
	s := &h2Server{cancelled: make(chan string, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.mu.Lock()
		s.hosts = append(s.hosts, r.Host)
		s.mu.Unlock()
		w.Header().Set("X-Proto", r.Proto)
		fmt.Fprint(w, "hello")
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n])
				w.(http.Flusher).Flush()
			}
			if err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		s.cancelled <- r.URL.Path
	})
	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 256)
		for i := range buf {
			buf[i] = byte(i)
		}
		w.Write(buf)
	})
	s.srv = httptest.NewUnstartedServer(mux)
	s.srv.EnableHTTP2 = true
	s.srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	s.srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.conns.Add(1)
		}
	}
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
	s.addr = s.srv.Listener.Addr().String()
	s.port = s.srv.Listener.Addr().(*net.TCPAddr).Port
	return s
}

// getScript issues one request and reports the outcome through done().
func getScript(authority, options string) string {
	return fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q, %s);
const result = {};
session.on('error', (e) => { result.sessionError = errInfo(e); });
session.on('close', () => done(result));
const req = session.request({ ':path': '/hello' });
req.on('error', (e) => { result.streamError = errInfo(e); });
req.on('response', (h) => { result.status = h[':status']; result.proto = h['x-proto']; });
let body = '';
req.on('data', (d) => { body += d; });
req.on('end', () => { result.body = body; });
req.on('close', () => session.close());
`, authority, options)
}

func wantCode(t *testing.T, result map[string]any, code string) {
	t.Helper()
	info, ok := result["sessionError"].(map[string]any)
	if !ok {
		t.Fatalf("no session error, result = %v", result)
	}
	if info["code"] != code {
		t.Fatalf("session error code = %v (%v), want %s", info["code"], info["message"], code)
	}
	stream, ok := result["streamError"].(map[string]any)
	if !ok || stream["code"] != "ERR_HTTP2_STREAM_CANCEL" {
		t.Fatalf("pending stream error = %v, want ERR_HTTP2_STREAM_CANCEL", result["streamError"])
	}
	if _, got := result["body"]; got {
		t.Fatalf("a denied request produced a response: %v", result)
	}
}

func TestHTTP2UsesInjectedControls(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "api.test"))
	resolver := &recordingResolver{hosts: map[string][]string{"api.test": {"203.0.113.10"}}}
	factory := &recordingFactory{redirect: map[string]string{"203.0.113.10": srv.addr}}

	result := runJS(t, &runtime.Config{
		DNSResolver:     resolver,
		SocketFactory:   factory,
		ClientTLSConfig: &tls.Config{RootCAs: ca.pool},
	}, getScript(fmt.Sprintf("https://api.test:%d", srv.port), "{}"))

	if result["body"] != "hello" || result["proto"] != "HTTP/2.0" {
		t.Fatalf("result = %v", result)
	}
	if got := strings.Join(resolver.lookups, ","); got != "api.test" {
		t.Fatalf("resolver lookups = %q, want the injected resolver to be used", got)
	}
	want := fmt.Sprintf("203.0.113.10:%d", srv.port)
	if got := strings.Join(factory.dialed(), ","); got != want {
		t.Fatalf("socket dials = %q, want %q", got, want)
	}
	if srv.hosts[0] != fmt.Sprintf("api.test:%d", srv.port) {
		t.Fatalf(":authority = %q", srv.hosts[0])
	}
}

func TestHTTP2DeniedDestinationsCannotBypassPolicy(t *testing.T) {
	cases := []struct {
		name      string
		authority string
		dns       []string
	}{
		{name: "loopback", authority: "https://127.0.0.1:443"},
		{name: "loopback other", authority: "https://127.1.2.3:443"},
		{name: "loopback ipv6", authority: "https://[::1]:443"},
		{name: "mapped loopback", authority: "https://[::ffff:127.0.0.1]:443"},
		{name: "unspecified", authority: "https://0.0.0.0:443"},
		{name: "private 10", authority: "https://10.1.2.3:443"},
		{name: "private 172", authority: "https://172.16.0.1:443"},
		{name: "private 192", authority: "https://192.168.1.1:443"},
		{name: "private ipv6", authority: "https://[fd00::1]:443"},
		{name: "cgnat", authority: "https://100.64.0.1:443"},
		{name: "metadata", authority: "https://169.254.169.254:443"},
		{name: "mapped metadata", authority: "https://[::ffff:a9fe:a9fe]:443"},
		{name: "link-local ipv6", authority: "https://[fe80::1]:443"},
		{name: "name to loopback", authority: "https://rebind.test:443", dns: []string{"127.0.0.1"}},
		{name: "name to metadata", authority: "https://rebind.test:443", dns: []string{"169.254.169.254"}},
		{name: "name to mapped private", authority: "https://rebind.test:443", dns: []string{"::ffff:10.0.0.1"}},
		{name: "plaintext to private", authority: "http://10.0.0.1:80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			underlying := &recordingFactory{}
			result := runJS(t, &runtime.Config{
				DNSResolver:   &recordingResolver{hosts: map[string][]string{"rebind.test": tc.dns}},
				SocketFactory: runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks()),
			}, getScript(tc.authority, "{}"))
			wantCode(t, result, "EACCES")
			if dials := underlying.dialed(); len(dials) != 0 {
				t.Fatalf("policy was bypassed; dials = %v", dials)
			}
		})
	}
}

// Orbital's URL parser leaves these as names rather than normalizing them to an
// address, so they take the DNS path and are checked after resolution.
func TestHTTP2NonCanonicalAddressLiteralsAreNotDialed(t *testing.T) {
	for _, host := range []string{"2130706433", "0x7f.0.0.1", "0177.0.0.1", "127.1"} {
		t.Run(host, func(t *testing.T) {
			underlying := &recordingFactory{}
			result := runJS(t, &runtime.Config{
				DNSResolver:   &recordingResolver{hosts: map[string][]string{host: {"127.0.0.1"}}},
				SocketFactory: runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks()),
			}, getScript("https://"+host+":443", "{}"))
			if result["sessionError"] == nil {
				t.Fatalf("result = %v", result)
			}
			if dials := underlying.dialed(); len(dials) != 0 {
				t.Fatalf("policy was bypassed; dials = %v", dials)
			}
		})
	}
}

// The part of an address literal after "%" is guest-chosen text. It must not satisfy
// a wildcard allow rule while the connection goes to the address.
func TestHTTP2AddressZoneCannotMatchAllowRules(t *testing.T) {
	rules := runtime.NewRuleBasedPolicy()
	rules.AllowHost(runtime.ProtocolTCP, runtime.DirectionOutbound, "*.allowed.com")
	policies := map[string]runtime.NetworkPolicy{
		"rule based": rules,
		"host list":  runtime.NewHostListPolicy([]string{"*.allowed.com"}, nil),
	}
	authorities := map[string]string{
		"mapped metadata url": `'https://[::ffff:169.254.169.254%x.allowed.com]/'`,
		"mapped private h2c":  `{ protocol: 'http:', hostname: '::ffff:10.0.0.5%x.allowed.com', port: 8080 }`,
		"global ipv6 object":  `{ protocol: 'https:', hostname: '2001:db8::1%x.allowed.com', port: 443 }`,
		"ipv4 with zone text": `{ protocol: 'http:', hostname: '10.0.0.5%x.allowed.com', port: 8080 }`,
		"colon in a name":     `{ protocol: 'http:', hostname: '10.0.0.5:x.allowed.com', port: 8080 }`,
	}
	for policyName, policy := range policies {
		for name, authority := range authorities {
			t.Run(policyName+"/"+name, func(t *testing.T) {
				underlying := &recordingFactory{}
				resolver := &recordingResolver{}
				result := runJS(t, &runtime.Config{
					DNSResolver:   resolver,
					SocketFactory: runtime.NewFilteredSocketFactory(underlying, policy),
				}, fmt.Sprintf(`
const session = require('http2').connect(%s, { rejectUnauthorized: false });
const result = {};
session.on('error', (e) => { result.sessionError = errInfo(e); });
session.on('close', () => done(result));
`, authority))
				info, _ := result["sessionError"].(map[string]any)
				if info == nil || info["code"] != "ERR_INVALID_URL" {
					t.Fatalf("result = %v", result)
				}
				if dials := underlying.dialed(); len(dials) != 0 {
					t.Fatalf("allow rule was bypassed; dials = %v", dials)
				}
				if len(resolver.lookups) != 0 {
					t.Fatalf("lookups = %v", resolver.lookups)
				}
			})
		}
	}
}

// A literal is only ever checked as an address, however it is spelled.
func TestHTTP2LiteralSpellingsAreCheckedAsAddresses(t *testing.T) {
	rules := runtime.NewRuleBasedPolicy()
	rules.AllowHost(runtime.ProtocolTCP, runtime.DirectionOutbound, "*:ffff:*", "0:0:*")
	underlying := &recordingFactory{}
	result := runJS(t, &runtime.Config{
		SocketFactory: runtime.NewFilteredSocketFactory(underlying, rules),
	}, getScript("https://[0:0:0:0:0:ffff:a9fe:a9fe]:443", "{}"))
	wantCode(t, result, "EACCES")
	if dials := underlying.dialed(); len(dials) != 0 {
		t.Fatalf("dials = %v", dials)
	}
}

func TestHTTP2SkipsDeniedAddressesOfAName(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "mixed.test"))
	underlying := &recordingFactory{redirect: map[string]string{"203.0.113.20": srv.addr}}
	result := runJS(t, &runtime.Config{
		DNSResolver:     &recordingResolver{hosts: map[string][]string{"mixed.test": {"127.0.0.1", "203.0.113.20"}}},
		SocketFactory:   runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks()),
		ClientTLSConfig: &tls.Config{RootCAs: ca.pool},
	}, getScript(fmt.Sprintf("https://mixed.test:%d", srv.port), "{}"))
	if result["body"] != "hello" {
		t.Fatalf("result = %v", result)
	}
	want := fmt.Sprintf("203.0.113.20:%d", srv.port)
	if got := strings.Join(underlying.dialed(), ","); got != want {
		t.Fatalf("dials = %q, want only the allowed address %q", got, want)
	}
}

func TestHTTP2AllowedPublicDestinationsWork(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "public.test", "203.0.113.30"))

	policies := map[string]runtime.NetworkPolicy{
		"deny private, allow rest": runtime.CommonPolicies.NoPrivateNetworks(),
		"host allowlist": func() runtime.NetworkPolicy {
			p := runtime.NewRuleBasedPolicy()
			p.AllowHost(runtime.ProtocolTCP, runtime.DirectionOutbound, "public.test")
			return p
		}(),
		"host list":    runtime.NewHostListPolicy([]string{"public.test"}, []string{"10.0.0.1"}),
		"cidr allowed": runtime.NewHostListPolicy([]string{"203.0.113.30"}, nil),
		"allow all":    runtime.NewAllowAllPolicy(),
	}
	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			underlying := &recordingFactory{redirect: map[string]string{"203.0.113.30": srv.addr}}
			result := runJS(t, &runtime.Config{
				DNSResolver:     &recordingResolver{hosts: map[string][]string{"public.test": {"203.0.113.30"}}},
				SocketFactory:   runtime.NewFilteredSocketFactory(underlying, policy),
				ClientTLSConfig: &tls.Config{RootCAs: ca.pool},
			}, getScript(fmt.Sprintf("https://public.test:%d", srv.port), "{}"))
			if result["body"] != "hello" || result["status"] != float64(200) {
				t.Fatalf("result = %v", result)
			}
		})
	}
}

func TestHTTP2HostAllowlistStillDeniesOtherHosts(t *testing.T) {
	p := runtime.NewRuleBasedPolicy()
	p.AllowHost(runtime.ProtocolTCP, runtime.DirectionOutbound, "public.test")
	underlying := &recordingFactory{}
	result := runJS(t, &runtime.Config{
		DNSResolver:   &recordingResolver{hosts: map[string][]string{"other.test": {"203.0.113.30"}}},
		SocketFactory: runtime.NewFilteredSocketFactory(underlying, p),
	}, getScript("https://other.test:443", "{}"))
	wantCode(t, result, "EACCES")
	if dials := underlying.dialed(); len(dials) != 0 {
		t.Fatalf("dials = %v", dials)
	}
}

// A custom policy with no knowledge of resolved connections must allow both the
// name and the IP.
type literalOnlyPolicy struct{ *runtime.AllowAllPolicy }

func (literalOnlyPolicy) CheckConnection(_ runtime.Protocol, _ runtime.Direction, host string, _ int) error {
	if host == "169.254.169.254" {
		return runtime.ErrConnectionDenied
	}
	return nil
}

func TestHTTP2CustomPolicySeesResolvedIP(t *testing.T) {
	underlying := &recordingFactory{}
	result := runJS(t, &runtime.Config{
		DNSResolver:   &recordingResolver{hosts: map[string][]string{"rebind.test": {"169.254.169.254"}}},
		SocketFactory: runtime.NewFilteredSocketFactory(underlying, literalOnlyPolicy{}),
	}, getScript("https://rebind.test:443", "{}"))
	wantCode(t, result, "EACCES")
	if dials := underlying.dialed(); len(dials) != 0 {
		t.Fatalf("dials = %v", dials)
	}
}

// Sockets without ResolvedTCPSocket are given the resolved IP, so an embedder's
// check inside Connect applies to the real destination.
func TestHTTP2SocketFactoryWithoutResolvedSupportGetsIP(t *testing.T) {
	underlying := &recordingFactory{}
	result := runJS(t, &runtime.Config{
		DNSResolver:   &recordingResolver{hosts: map[string][]string{"name.test": {"127.0.0.1"}}},
		SocketFactory: underlying,
	}, getScript("https://name.test:1", "{}"))
	if _, ok := result["sessionError"]; !ok {
		t.Fatalf("result = %v", result)
	}
	if got := strings.Join(underlying.dialed(), ","); got != "127.0.0.1:1" {
		t.Fatalf("dials = %q", got)
	}
}

func TestHTTP2NoSilentFallback(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "127.0.0.1"))
	authority := fmt.Sprintf("https://127.0.0.1:%d", srv.port)

	t.Run("restricted client without socket factory", func(t *testing.T) {
		result := runJS(t, &runtime.Config{HTTPClient: runtime.NewNoOpHTTPClient()}, getScript(authority, "{}"))
		wantCode(t, result, "ERR_HTTP2_TRANSPORT_UNAVAILABLE")
	})
	t.Run("filtered client with explicit real sockets", func(t *testing.T) {
		result := runJS(t, &runtime.Config{
			HTTPClient:    runtime.NewFilteredHTTPClient(nil, runtime.NewDenyAllPolicy()),
			SocketFactory: runtime.NewRealSocketFactory(),
		}, getScript(authority, "{}"))
		wantCode(t, result, "ERR_HTTP2_TRANSPORT_UNAVAILABLE")
	})
	t.Run("no-op sockets", func(t *testing.T) {
		result := runJS(t, &runtime.Config{SocketFactory: runtime.NewNoOpSocketFactory()}, getScript(authority, "{}"))
		wantCode(t, result, "ENETUNREACH")
	})
	t.Run("deny-all sockets", func(t *testing.T) {
		result := runJS(t, &runtime.Config{
			SocketFactory: runtime.NewFilteredSocketFactory(nil, runtime.NewDenyAllPolicy()),
		}, getScript(authority, "{}"))
		wantCode(t, result, "EACCES")
	})
	if got := srv.conns.Load(); got != 0 {
		t.Fatalf("%d connections reached the server", got)
	}
}

func TestHTTP2CertificateVerification(t *testing.T) {
	ca := newTestCA(t)
	good := newH2Server(t, ca.issue(t, "localhost", "127.0.0.1"))
	wrong := newH2Server(t, ca.issue(t, "wrong.example"))
	caOption := fmt.Sprintf("{ ca: %q }", ca.pem)

	t.Run("untrusted fails by default", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", good.port), "{}"))
		wantCode(t, result, "UNABLE_TO_VERIFY_LEAF_SIGNATURE")
	})
	t.Run("hostname mismatch fails", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", wrong.port), caOption))
		wantCode(t, result, "ERR_TLS_CERT_ALTNAME_INVALID")
	})
	t.Run("ip mismatch fails", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://127.0.0.1:%d", wrong.port), caOption))
		wantCode(t, result, "ERR_TLS_CERT_ALTNAME_INVALID")
	})
	t.Run("trusted ca works", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", good.port), caOption))
		if result["body"] != "hello" {
			t.Fatalf("result = %v", result)
		}
	})
	t.Run("embedder roots work", func(t *testing.T) {
		result := runJS(t, &runtime.Config{ClientTLSConfig: &tls.Config{RootCAs: ca.pool}},
			getScript(fmt.Sprintf("https://localhost:%d", good.port), "{}"))
		if result["body"] != "hello" {
			t.Fatalf("result = %v", result)
		}
	})
	t.Run("rejectUnauthorized false is honored without a locked config", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", wrong.port), "{ rejectUnauthorized: false }"))
		if result["body"] != "hello" {
			t.Fatalf("result = %v", result)
		}
	})

	// Guest code cannot weaken an embedder's TLS configuration.
	for name, option := range map[string]string{
		"rejectUnauthorized": "{ rejectUnauthorized: false }",
		"guest ca":           caOption,
		"guest servername":   fmt.Sprintf("{ ca: %q, servername: 'wrong.example' }", ca.pem),
	} {
		t.Run("locked config ignores "+name, func(t *testing.T) {
			result := runJS(t, &runtime.Config{ClientTLSConfig: &tls.Config{RootCAs: newTestCA(t).pool}},
				getScript(fmt.Sprintf("https://localhost:%d", wrong.port), option))
			if _, ok := result["sessionError"]; !ok {
				t.Fatalf("expected a certificate failure, result = %v", result)
			}
		})
	}
	t.Run("locked config ignores env override", func(t *testing.T) {
		result := runJS(t, &runtime.Config{
			ClientTLSConfig: &tls.Config{RootCAs: newTestCA(t).pool},
			Environment:     runtime.NewSandboxedEnvironment(map[string]string{"NODE_TLS_REJECT_UNAUTHORIZED": "0"}),
		}, getScript(fmt.Sprintf("https://localhost:%d", wrong.port), "{}"))
		if _, ok := result["sessionError"]; !ok {
			t.Fatalf("expected a certificate failure, result = %v", result)
		}
	})
	t.Run("server without a common protocol is an error", func(t *testing.T) {
		srv := httptest.NewUnstartedServer(http.NotFoundHandler())
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "localhost")}}
		srv.StartTLS()
		defer srv.Close()
		port := srv.Listener.Addr().(*net.TCPAddr).Port
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", port), caOption))
		wantCode(t, result, "ERR_SSL_TLSV1_ALERT_NO_APPLICATION_PROTOCOL")
	})
	t.Run("server that does not negotiate h2 is an error", func(t *testing.T) {
		listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{ca.issue(t, "localhost")}})
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					conn.(*tls.Conn).Handshake()
					io.Copy(io.Discard, conn)
					conn.Close()
				}()
			}
		}()
		port := listener.Addr().(*net.TCPAddr).Port
		result := runJS(t, &runtime.Config{}, getScript(fmt.Sprintf("https://localhost:%d", port), caOption))
		wantCode(t, result, "ERR_HTTP2_ERROR")
	})
}

func secureConfig(ca *testCA) *runtime.Config {
	return &runtime.Config{ClientTLSConfig: &tls.Config{RootCAs: ca.pool}}
}

func waitCancelled(t *testing.T, srv *h2Server) {
	t.Helper()
	select {
	case <-srv.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not observe the cancellation")
	}
}

func TestHTTP2Cancellation(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "localhost"))
	authority := fmt.Sprintf("https://localhost:%d", srv.port)

	for name, action := range map[string]string{
		"stream.close":    `req.close(http2.constants.NGHTTP2_CANCEL);`,
		"stream.destroy":  `req.destroy();`,
		"session.destroy": `session.destroy();`,
		"abort signal":    `controller.abort();`,
	} {
		t.Run(name, func(t *testing.T) {
			result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const controller = new AbortController();
const req = session.request({ ':path': '/stall' }, { signal: controller.signal });
const events = [];
req.on('error', (e) => events.push(e.code || e.message));
req.on('close', () => { events.push('close'); session.close(); });
session.on('close', () => done({ events, destroyed: req.destroyed }));
req.once('data', () => { %s });
`, authority, action))
			if result["destroyed"] != true {
				t.Fatalf("result = %v", result)
			}
			waitCancelled(t, srv)
		})
	}

	t.Run("disposing the runtime closes the connection", func(t *testing.T) {
		cfg := secureConfig(ca)
		cfg.EnableTimers = true
		inst, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		// Drive the loop only until the response has started; the session stays open.
		if _, err := inst.Runtime.RunScript(fmt.Sprintf(`
const http2 = require('http2');
globalThis.session = http2.connect(%q);
globalThis.req = session.request({ ':path': '/stall' });
req.on('data', () => { globalThis.gotData = true; });
req.on('error', () => {});
session.on('error', () => {});
`, authority), "test.js"); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			inst.Runtime.EventLoop().RunOnce()
			if v, _ := inst.Runtime.GetGlobal("gotData"); v != nil && v.Boolean() {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		inst.Runtime.Dispose()
		waitCancelled(t, srv)
	})

	t.Run("execution timeout ends an open session", func(t *testing.T) {
		cfg := secureConfig(ca)
		cfg.Timeout = 300 * time.Millisecond
		inst, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer inst.Runtime.Dispose()
		start := time.Now()
		if _, err := inst.Runtime.Run(fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
session.on('error', () => {});
const req = session.request({ ':path': '/stall' });
req.on('error', () => {});
req.resume();
`, authority), "test.js"); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatalf("timeout took %s", time.Since(start))
		}
		waitCancelled(t, srv)
	})
}

func TestHTTP2AbortedRequestEmitsAborted(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "localhost"))
	result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect('https://localhost:%d');
const req = session.request({ ':path': '/stall', ':method': 'POST' });
const events = [];
req.on('aborted', () => events.push('aborted'));
req.on('close', () => { session.close(); done({ events, aborted: req.aborted }); });
req.once('data', () => req.destroy());
`, srv.port))
	if fmt.Sprint(result["events"]) != "[aborted]" || result["aborted"] != true {
		t.Fatalf("result = %v", result)
	}
	waitCancelled(t, srv)
}

func TestHTTP2Timeouts(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "localhost"))
	authority := fmt.Sprintf("https://localhost:%d", srv.port)

	t.Run("stream", func(t *testing.T) {
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/stall' });
const events = [];
req.on('timeout', () => events.push('event'));
req.setTimeout(50, () => { events.push('callback'); req.close(http2.constants.NGHTTP2_CANCEL); });
req.resume();
req.on('close', () => { session.close(); done({ events, rst: req.rstCode }); });
`, authority))
		if fmt.Sprint(result["events"]) != "[event callback]" || result["rst"] != float64(8) {
			t.Fatalf("result = %v", result)
		}
		waitCancelled(t, srv)
	})
	t.Run("session", func(t *testing.T) {
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/stall' });
req.resume();
let fired = 0;
session.setTimeout(50, () => { fired++; session.destroy(); });
session.on('close', () => done({ fired }));
`, authority))
		if result["fired"] != float64(1) {
			t.Fatalf("result = %v", result)
		}
		waitCancelled(t, srv)
	})
	t.Run("disabled timeout does not fire", func(t *testing.T) {
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/hello' });
let fired = 0;
req.setTimeout(10, () => fired++);
req.setTimeout(0);
req.resume();
req.on('close', () => setTimeout(() => { session.close(); done({ fired }); }, 50));
`, authority))
		if result["fired"] != float64(0) {
			t.Fatalf("result = %v", result)
		}
	})
	t.Run("an idle timer does not keep the process alive", func(t *testing.T) {
		start := time.Now()
		runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/hello' });
req.setTimeout(60000);
session.setTimeout(60000);
req.resume();
req.on('close', () => { session.close(); done({}); });
`, authority))
		if time.Since(start) > 5*time.Second {
			t.Fatalf("took %s", time.Since(start))
		}
	})
}

func TestHTTP2Streaming(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "localhost"))
	authority := fmt.Sprintf("https://localhost:%d", srv.port)

	t.Run("full duplex with binary data", func(t *testing.T) {
		// Each write waits for the previous echo, so this deadlocks if either
		// direction is buffered until the end of the exchange.
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/echo', ':method': 'POST' });
const chunks = [Buffer.from([0, 1, 2]), Buffer.from([255, 128, 0]), Buffer.from('héllo')];
let got = [], next = 0;
req.on('data', (d) => {
  got.push(...d);
  next++;
  if (next < chunks.length) req.write(chunks[next]); else req.end();
});
req.on('end', () => { session.close(); done({ got }); });
req.write(chunks[0]);
`, authority))
		want := "[0 1 2 255 128 0 104 195 169 108 108 111]"
		if fmt.Sprint(result["got"]) != want {
			t.Fatalf("got %v, want %s", result["got"], want)
		}
	})
	t.Run("response bytes survive", func(t *testing.T) {
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
const req = session.request({ ':path': '/binary' });
const parts = [];
req.on('data', (d) => parts.push(d));
req.on('end', () => {
  session.close();
  const all = Buffer.concat(parts);
  let ok = all.length === 256;
  for (let i = 0; i < all.length; i++) ok = ok && all[i] === i;
  done({ ok });
});
`, authority))
		if result["ok"] != true {
			t.Fatalf("result = %v", result)
		}
	})
	t.Run("pause and resume, setEncoding, async iteration", func(t *testing.T) {
		result := runJS(t, secureConfig(ca), fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect(%q);
(async () => {
  const req = session.request({ ':path': '/hello' });
  req.setEncoding('utf8');
  req.pause();
  await new Promise((r) => setTimeout(r, 50));
  req.resume();
  let text = '';
  for await (const chunk of req) text += chunk;
  session.close();
  done({ text });
})();
`, authority))
		if result["text"] != "hello" {
			t.Fatalf("result = %v", result)
		}
	})
}

func TestHTTP2ConnectionReuse(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "reuse.test"))
	factory := &recordingFactory{redirect: map[string]string{"203.0.113.40": srv.addr}}
	cfg := &runtime.Config{
		DNSResolver:     &recordingResolver{hosts: map[string][]string{"reuse.test": {"203.0.113.40"}}},
		SocketFactory:   factory,
		ClientTLSConfig: &tls.Config{RootCAs: ca.pool},
	}
	result := runJS(t, cfg, fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect('https://reuse.test:%d');
const one = () => new Promise((resolve, reject) => {
  const req = session.request({ ':path': '/hello' });
  let body = '';
  req.setEncoding('utf8');
  req.on('data', (d) => { body += d; });
  req.on('error', reject);
  req.on('close', () => resolve(body));
});
Promise.all([one(), one(), one()]).then(one).then((last) => {
  session.close(() => done({ last }));
});
`, srv.port))
	if result["last"] != "hello" {
		t.Fatalf("result = %v", result)
	}
	if got := srv.requests.Load(); got != 4 {
		t.Fatalf("requests = %d", got)
	}
	if got := srv.conns.Load(); got != 1 {
		t.Fatalf("connections = %d, want one connection per session", got)
	}
	if got := len(factory.dialed()); got != 1 {
		t.Fatalf("socket dials = %d", got)
	}
}

// :authority is only a header: it cannot redirect a session to another destination.
func TestHTTP2AuthorityHeaderCannotChangeDestination(t *testing.T) {
	ca := newTestCA(t)
	srv := newH2Server(t, ca.issue(t, "localhost"))
	factory := &recordingFactory{redirect: map[string]string{"203.0.113.50": srv.addr}}
	result := runJS(t, &runtime.Config{
		DNSResolver:     &recordingResolver{hosts: map[string][]string{"localhost": {"203.0.113.50"}}},
		SocketFactory:   factory,
		ClientTLSConfig: &tls.Config{RootCAs: ca.pool},
	}, fmt.Sprintf(`
const http2 = require('http2');
const session = http2.connect('https://localhost:%d');
const req = session.request({ ':path': '/hello', ':authority': '169.254.169.254' });
req.resume();
req.on('end', () => { session.close(); done({ ok: true }); });
`, srv.port))
	if result["ok"] != true {
		t.Fatalf("result = %v", result)
	}
	if got := strings.Join(factory.dialed(), ","); got != fmt.Sprintf("203.0.113.50:%d", srv.port) {
		t.Fatalf("dials = %q", got)
	}
	if srv.hosts[0] != "169.254.169.254" {
		t.Fatalf("server saw authority %q", srv.hosts[0])
	}
}

func TestHTTP2PlaintextUsesInjectedSockets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		h2 := &http2.Server{}
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go h2.ServeConn(conn, &http2.ServeConnOpts{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Proto", r.Proto)
				io.WriteString(w, "hello")
			})})
		}
	}()
	factory := &recordingFactory{redirect: map[string]string{"203.0.113.60": listener.Addr().String()}}
	result := runJS(t, &runtime.Config{SocketFactory: factory}, getScript("http://203.0.113.60:80", "{}"))
	if result["body"] != "hello" || result["proto"] != "HTTP/2.0" {
		t.Fatalf("result = %v", result)
	}
	if got := strings.Join(factory.dialed(), ","); got != "203.0.113.60:80" {
		t.Fatalf("dials = %q", got)
	}
}
