package nodejs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// newTLSServer starts an echoing TLS server and returns its port.
func newTLSServer(t *testing.T, cert tls.Certificate) int {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 4096)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					if _, err := conn.Write(buf[:n]); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// selfSigned is a certificate that is its own issuer, as Node's depth-zero case needs.
func (ca *testCA) selfSigned() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{ca.cert.Raw}, PrivateKey: ca.key}
}

func (ca *testCA) issueExpired(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsScript connects with the given JavaScript options expression and reports what the
// socket did. prelude runs first, and the script's hook calls are in result.hook.
func tlsScript(prelude, options string) string {
	return `
const tls = require('tls');
const events = [];
const hook = [];
` + prelude + `
let socket;
try {
  socket = tls.connect(` + options + `, () => events.push('callback'));
} catch (e) {
  done({ events: ['throw:' + e.code], hook });
}
if (socket) {
  socket.on('secureConnect', () => { events.push('secureConnect'); socket.destroy(); });
  socket.on('error', (e) => { events.push('error:' + e.code); });
  socket.on('close', (hadError) => {
    events.push('close:' + hadError);
    done({ events, hook, authorized: socket.authorized, authorizationError: socket.authorizationError });
  });
}
`
}

func wantTLS(t *testing.T, result map[string]any, authorized bool, authorizationError any, events ...any) {
	t.Helper()
	if !reflect.DeepEqual(result["events"], events) {
		t.Errorf("events = %v, want %v", result["events"], events)
	}
	if result["authorized"] != authorized {
		t.Errorf("authorized = %v, want %v", result["authorized"], authorized)
	}
	if result["authorizationError"] != authorizationError {
		t.Errorf("authorizationError = %v, want %v", result["authorizationError"], authorizationError)
	}
}

var (
	tlsConnected = []any{"callback", "secureConnect", "close:false"}
)

func tlsFailed(code string) []any { return []any{"error:" + code, "close:true"} }

func TestTLSConnect_VerifiesByDefault(t *testing.T) {
	ca := newTestCA(t)
	cases := map[string]struct {
		cert tls.Certificate
		code string
	}{
		"self-signed": {newTestCA(t).selfSigned(), "DEPTH_ZERO_SELF_SIGNED_CERT"},
		"untrusted":   {newTestCA(t).issue(t, "localhost"), "UNABLE_TO_VERIFY_LEAF_SIGNATURE"},
		"mismatch":    {ca.issue(t, "other.test"), "ERR_TLS_CERT_ALTNAME_INVALID"},
		"expired":     {ca.issueExpired(t, "localhost"), "CERT_HAS_EXPIRED"},
		"chain with untrusted root": {func() tls.Certificate {
			other := newTestCA(t)
			cert := other.issue(t, "localhost")
			cert.Certificate = append(cert.Certificate, other.cert.Raw)
			return cert
		}(), "SELF_SIGNED_CERT_IN_CHAIN"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			port := newTLSServer(t, tc.cert)
			caOption := ""
			// Mismatch and expiry need a trusted chain to reach those checks.
			if name == "mismatch" || name == "expired" {
				caOption = fmt.Sprintf(", ca: %q", ca.pem)
			}
			result := runJS(t, &runtime.Config{}, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d%s}`, port, caOption)))
			wantTLS(t, result, false, tc.code, tlsFailed(tc.code)...)
		})
	}
}

func TestTLSConnect_ReportsChainBeforeHostname(t *testing.T) {
	port := newTLSServer(t, newTestCA(t).selfSigned())
	result := runJS(t, &runtime.Config{}, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, servername: 'other.test'}`, port)))
	wantTLS(t, result, false, "DEPTH_ZERO_SELF_SIGNED_CERT", tlsFailed("DEPTH_ZERO_SELF_SIGNED_CERT")...)
}

func TestTLSConnect_Options(t *testing.T) {
	ca := newTestCA(t)
	good := newTLSServer(t, ca.issue(t, "localhost", "127.0.0.1"))
	other := newTLSServer(t, ca.issue(t, "other.test"))
	self := newTLSServer(t, newTestCA(t).selfSigned())
	const selfCode = "DEPTH_ZERO_SELF_SIGNED_CERT"
	const leafCode = "UNABLE_TO_VERIFY_LEAF_SIGNATURE"
	const nameCode = "ERR_TLS_CERT_ALTNAME_INVALID"

	cases := []struct {
		name       string
		prelude    string
		options    string
		authorized bool
		authErr    any
		events     []any
	}{
		{name: "ca string", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "ca Buffer", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: Buffer.from(%q)}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "ca array", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: [%q]}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "secureContext ca", options: fmt.Sprintf(`{host: 'localhost', port: %d, secureContext: tls.createSecureContext({ca: %q})}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "ca replaces roots", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q}`, good, newTestCA(t).pem), authErr: leafCode, events: tlsFailed(leafCode)},
		{name: "garbage ca", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: 'not a pem'}`, good), authErr: leafCode, events: tlsFailed(leafCode)},
		{name: "empty ca array", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: []}`, good), authErr: leafCode, events: tlsFailed(leafCode)},
		{name: "IP in SAN", options: fmt.Sprintf(`{host: '127.0.0.1', port: %d, ca: %q}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "IP not in SAN", options: fmt.Sprintf(`{host: '127.0.0.1', port: %d, ca: %q}`, other, ca.pem), authErr: nameCode, events: tlsFailed(nameCode)},
		{name: "servername selects the identity", options: fmt.Sprintf(`{host: '127.0.0.1', port: %d, ca: %q, servername: 'other.test'}`, other, ca.pem), authorized: true, events: tlsConnected},
		{name: "servername mismatch", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q, servername: 'nope.test'}`, good, ca.pem), authErr: nameCode, events: tlsFailed(nameCode)},
		{name: "port, options", options: fmt.Sprintf(`%d, {ca: %q}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "port, host, options", options: fmt.Sprintf(`%d, 'localhost', {ca: %q}`, good, ca.pem), authorized: true, events: tlsConnected},

		{name: "rejectUnauthorized false, untrusted", options: fmt.Sprintf(`{host: 'localhost', port: %d, rejectUnauthorized: false}`, self), authErr: selfCode, events: tlsConnected},
		{name: "rejectUnauthorized false, mismatch", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q, rejectUnauthorized: false}`, other, ca.pem), authErr: nameCode, events: tlsConnected},
		{name: "rejectUnauthorized false, valid", options: fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q, rejectUnauthorized: false}`, good, ca.pem), authorized: true, events: tlsConnected},
		{name: "rejectUnauthorized undefined", options: fmt.Sprintf(`{host: 'localhost', port: %d, rejectUnauthorized: undefined}`, self), authErr: selfCode, events: tlsFailed(selfCode)},
		{name: "rejectUnauthorized 0", options: fmt.Sprintf(`{host: 'localhost', port: %d, rejectUnauthorized: 0}`, self), authErr: selfCode, events: tlsFailed(selfCode)},

		{name: "env 0", prelude: `process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';`, options: fmt.Sprintf(`{host: 'localhost', port: %d}`, self), authErr: selfCode, events: tlsConnected},
		{name: "env 1", prelude: `process.env.NODE_TLS_REJECT_UNAUTHORIZED = '1';`, options: fmt.Sprintf(`{host: 'localhost', port: %d}`, self), authErr: selfCode, events: tlsFailed(selfCode)},
		{name: "option beats env", prelude: `process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';`, options: fmt.Sprintf(`{host: 'localhost', port: %d, rejectUnauthorized: true}`, self), authErr: selfCode, events: tlsFailed(selfCode)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NODE_TLS_REJECT_UNAUTHORIZED", "")
			result := runJS(t, &runtime.Config{}, tlsScript(tc.prelude, tc.options))
			wantTLS(t, result, tc.authorized, tc.authErr, tc.events...)
		})
	}

	t.Run("mismatched key pair throws", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, cert: 'x', key: 'y'}`, good)))
		if want := []any{"throw:ERR_OSSL_X509_KEY_VALUES_MISMATCH"}; !reflect.DeepEqual(result["events"], want) {
			t.Fatalf("events = %v", result["events"])
		}
	})
}

func TestTLSConnect_CheckServerIdentity(t *testing.T) {
	ca := newTestCA(t)
	good := newTLSServer(t, ca.issue(t, "localhost"))
	other := newTLSServer(t, ca.issue(t, "other.test"))
	self := newTLSServer(t, newTestCA(t).selfSigned())

	conn := func(port int, extra string) string {
		return fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q, %s}`, port, ca.pem, extra)
	}
	const pin = `checkServerIdentity: (host, cert) => { hook.push(host, cert.subject.CN); const e = new Error('pin'); e.code = 'PIN_FAIL'; return e; }`

	t.Run("can accept a mismatch", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", conn(other, `checkServerIdentity: (host, cert) => { hook.push(host, cert.subject.CN); }`)))
		wantTLS(t, result, true, nil, tlsConnected...)
		if want := []any{"localhost", "other.test"}; !reflect.DeepEqual(result["hook"], want) {
			t.Errorf("hook = %v", result["hook"])
		}
	})
	t.Run("rejection closes a valid connection", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", conn(good, pin)))
		wantTLS(t, result, false, "PIN_FAIL", tlsFailed("PIN_FAIL")...)
	})
	t.Run("rejection with rejectUnauthorized false is recorded", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", conn(good, pin+`, rejectUnauthorized: false`)))
		wantTLS(t, result, false, "PIN_FAIL", tlsConnected...)
	})
	t.Run("not called on a bad chain", func(t *testing.T) {
		for _, extra := range []string{"", ", rejectUnauthorized: false"} {
			result := runJS(t, &runtime.Config{}, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, checkServerIdentity: () => { hook.push('called'); }%s}`, self, extra)))
			if len(result["hook"].([]any)) != 0 {
				t.Errorf("hook called with options %q", extra)
			}
			if result["authorizationError"] != "DEPTH_ZERO_SELF_SIGNED_CERT" {
				t.Errorf("authorizationError = %v", result["authorizationError"])
			}
		}
	})
	t.Run("default check can be delegated to", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", conn(other, `checkServerIdentity: (host, cert) => tls.checkServerIdentity(host, cert)`)))
		wantTLS(t, result, false, "ERR_TLS_CERT_ALTNAME_INVALID", tlsFailed("ERR_TLS_CERT_ALTNAME_INVALID")...)
	})
	t.Run("a non-function keeps the default check", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsScript("", conn(other, `checkServerIdentity: true`)))
		wantTLS(t, result, false, "ERR_TLS_CERT_ALTNAME_INVALID", tlsFailed("ERR_TLS_CERT_ALTNAME_INVALID")...)
	})
}

func TestTLSCheckServerIdentity(t *testing.T) {
	result := runJS(t, &runtime.Config{}, `
const tls = require('tls');
const cert = { subject: { CN: 'a.example.com' }, subjectaltname: 'DNS:a.example.com, DNS:*.w.example.com, IP Address:10.0.0.1' };
const check = (host, c) => { const e = tls.checkServerIdentity(host, c); return e ? e.code : 'ok'; };
done({
  exact: check('a.example.com', cert),
  upper: check('A.EXAMPLE.COM', cert),
  dot: check('a.example.com.', cert),
  other: check('b.example.com', cert),
  wildcard: check('x.w.example.com', cert),
  deep: check('x.y.w.example.com', cert),
  apex: check('w.example.com', cert),
  ip: check('10.0.0.1', cert),
  otherIP: check('10.0.0.2', cert),
  cn: check('cn.test', { subject: { CN: 'cn.test' } }),
  cnIgnoredWithSAN: check('a.example.com', { subject: { CN: 'a.example.com' }, subjectaltname: 'DNS:b.example.com' }),
  cnIsNotAnIP: check('10.0.0.1', { subject: { CN: '10.0.0.1' } }),
  shortWildcard: check('foo.com', { subject: { CN: '*.com' } }),
  empty: check('cn.test', {}),
});`)
	const bad = "ERR_TLS_CERT_ALTNAME_INVALID"
	want := map[string]any{
		"exact": "ok", "upper": "ok", "dot": "ok", "other": bad, "wildcard": "ok", "deep": bad, "apex": bad,
		"ip": "ok", "otherIP": bad, "cn": "ok", "cnIgnoredWithSAN": bad, "cnIsNotAnIP": bad, "shortWildcard": bad, "empty": bad,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("got %v, want %v", result, want)
	}
}

func TestTLSConnect_PeerCertificate(t *testing.T) {
	ca := newTestCA(t)
	port := newTLSServer(t, ca.issue(t, "localhost", "127.0.0.1"))
	result := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const tls = require('tls');
const socket = tls.connect({host: 'localhost', port: %d, ca: %q}, () => {
  const cert = socket.getPeerCertificate();
  socket.destroy();
  done({
    cn: cert.subject.CN,
    issuer: cert.issuer.CN,
    san: cert.subjectaltname,
    fingerprint: /^([0-9A-F]{2}:){31}[0-9A-F]{2}$/.test(cert.fingerprint256),
    raw: Buffer.isBuffer(cert.raw),
    validTo: !isNaN(Date.parse(cert.valid_to)),
  });
});
socket.on('error', (e) => done({ error: e.code }));`, port, ca.pem))
	want := map[string]any{
		"cn": "localhost", "issuer": "Orbital Test CA", "san": "DNS:localhost, IP Address:127.0.0.1",
		"fingerprint": true, "raw": true, "validTo": true,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("got %v, want %v", result, want)
	}
}

func TestTLSConnect_ClientTLSConfigIsAuthoritative(t *testing.T) {
	ca := newTestCA(t)
	guestCA := newTestCA(t)
	lock := func() *runtime.Config {
		return &runtime.Config{ClientTLSConfig: &tls.Config{RootCAs: ca.pool}}
	}
	good := newTLSServer(t, ca.issue(t, "localhost"))
	other := newTLSServer(t, ca.issue(t, "other.test"))
	guest := newTLSServer(t, guestCA.issue(t, "localhost"))
	const leafCode = "UNABLE_TO_VERIFY_LEAF_SIGNATURE"
	const nameCode = "ERR_TLS_CERT_ALTNAME_INVALID"

	t.Run("valid", func(t *testing.T) {
		wantTLS(t, runJS(t, lock(), tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, good))), true, nil, tlsConnected...)
	})
	t.Run("rejectUnauthorized", func(t *testing.T) {
		wantTLS(t, runJS(t, lock(), tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, rejectUnauthorized: false}`, guest))), false, leafCode, tlsFailed(leafCode)...)
	})
	t.Run("env", func(t *testing.T) {
		t.Setenv("NODE_TLS_REJECT_UNAUTHORIZED", "")
		wantTLS(t, runJS(t, lock(), tlsScript(`process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';`, fmt.Sprintf(`{host: 'localhost', port: %d}`, guest))), false, leafCode, tlsFailed(leafCode)...)
	})
	t.Run("ca", func(t *testing.T) {
		wantTLS(t, runJS(t, lock(), tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, ca: %q}`, guest, guestCA.pem))), false, leafCode, tlsFailed(leafCode)...)
	})
	t.Run("servername", func(t *testing.T) {
		wantTLS(t, runJS(t, lock(), tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, servername: 'other.test'}`, other))), false, nameCode, tlsFailed(nameCode)...)
	})
	t.Run("checkServerIdentity", func(t *testing.T) {
		result := runJS(t, lock(), tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d, checkServerIdentity: () => { hook.push('called'); }}`, other)))
		wantTLS(t, result, false, nameCode, tlsFailed(nameCode)...)
		if len(result["hook"].([]any)) != 0 {
			t.Error("hook ran under an authoritative configuration")
		}
	})
	t.Run("embedder opt-out is reported", func(t *testing.T) {
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{InsecureSkipVerify: true}}
		result := runJS(t, cfg, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, guest)))
		wantTLS(t, result, false, leafCode, tlsConnected...)
	})
	t.Run("embedder callback still runs", func(t *testing.T) {
		var calls atomic.Int32
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{
			RootCAs: ca.pool,
			VerifyConnection: func(tls.ConnectionState) error {
				calls.Add(1)
				return nil
			},
		}}
		wantTLS(t, runJS(t, cfg, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, good))), true, nil, tlsConnected...)
		if calls.Load() != 1 {
			t.Errorf("VerifyConnection ran %d times", calls.Load())
		}
	})
}

func TestTLSConnect_InjectedSockets(t *testing.T) {
	ca := newTestCA(t)
	port := newTLSServer(t, ca.issue(t, "api.test"))
	target := "127.0.0.1:" + strconv.Itoa(port)
	script := fmt.Sprintf(`{host: 'api.test', port: 443, ca: %q}`, ca.pem)

	t.Run("custom factory carries the handshake", func(t *testing.T) {
		factory := &recordingFactory{redirect: map[string]string{"api.test": target}}
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: factory}, tlsScript("", script)), true, nil, tlsConnected...)
		if got := factory.dialed(); !reflect.DeepEqual(got, []string{"api.test:443"}) {
			t.Errorf("dialed %v", got)
		}
	})
	t.Run("custom factory is verified", func(t *testing.T) {
		factory := &recordingFactory{redirect: map[string]string{"nope.test": target}}
		result := runJS(t, &runtime.Config{SocketFactory: factory}, tlsScript("", fmt.Sprintf(`{host: 'nope.test', port: 443, ca: %q}`, ca.pem)))
		wantTLS(t, result, false, "ERR_TLS_CERT_ALTNAME_INVALID", tlsFailed("ERR_TLS_CERT_ALTNAME_INVALID")...)
	})
	t.Run("filtered factory keeps its policy and verification", func(t *testing.T) {
		underlying := &recordingFactory{redirect: map[string]string{"api.test": target}}
		deny := runtime.NewFilteredSocketFactory(underlying, runtime.NewDenyAllPolicy())
		result := runJS(t, &runtime.Config{SocketFactory: deny}, tlsScript("", script))
		wantTLS(t, result, false, nil, tlsFailed("EACCES")...)
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("denied connection dialed %v", got)
		}

		allow := runtime.NewFilteredSocketFactory(underlying, runtime.NewAllowAllPolicy())
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: allow}, tlsScript("", script)), true, nil, tlsConnected...)
		wrongCA := fmt.Sprintf(`{host: 'api.test', port: 443, ca: %q}`, newTestCA(t).pem)
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: allow}, tlsScript("", wrongCA)), false,
			"UNABLE_TO_VERIFY_LEAF_SIGNATURE", tlsFailed("UNABLE_TO_VERIFY_LEAF_SIGNATURE")...)
	})
	t.Run("no-op factory", func(t *testing.T) {
		result := runJS(t, &runtime.Config{SocketFactory: &runtime.NoOpSocketFactory{}}, tlsScript("", script))
		wantTLS(t, result, false, nil, tlsFailed("ENETUNREACH")...)
	})
	t.Run("factory that ignores the configuration fails closed", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		plainPort := ln.Addr().(*net.TCPAddr).Port
		options := fmt.Sprintf(`{host: '127.0.0.1', port: %d}`, plainPort)

		result := runJS(t, &runtime.Config{SocketFactory: &plaintextFactory{}}, tlsScript("", options))
		wantTLS(t, result, false, nil, tlsFailed("ERR_TLS_CERT_NOT_VERIFIED")...)

		cfg := &runtime.Config{SocketFactory: &plaintextFactory{}}
		cfg.ClientTLSConfig = &tls.Config{InsecureSkipVerify: true}
		wantTLS(t, runJS(t, cfg, tlsScript("", options)), false, nil, tlsConnected...)
	})
}

// plaintextFactory returns the TCP socket unwrapped, so no handshake runs.
type plaintextFactory struct{ runtime.RealSocketFactory }

func (plaintextFactory) CreateTLSSocket(socket runtime.TCPSocket, _ *tls.Config, _ bool) runtime.TCPSocket {
	return socket
}

func TestTLSConnect_DestroyInterruptsHandshake(t *testing.T) {
	// The peer accepts the connection but never answers the ClientHello.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	start := time.Now()
	result := runJS(t, &runtime.Config{Timeout: 10 * time.Second}, fmt.Sprintf(`
const tls = require('tls');
const events = [];
const socket = tls.connect({host: '127.0.0.1', port: %d}, () => events.push('callback'));
socket.on('secureConnect', () => events.push('secureConnect'));
socket.on('error', (e) => events.push('error:' + e.code));
socket.on('close', (hadError) => {
  events.push('close:' + hadError);
  setTimeout(() => done({ events }), 50);
});
setTimeout(() => socket.destroy(), 100);`, port))
	if want := []any{"close:false"}; !reflect.DeepEqual(result["events"], want) {
		t.Errorf("events = %v", result["events"])
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("destroy did not interrupt the handshake (%s)", elapsed)
	}
}

func TestTLSConnect_ConnectionFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	result := runJS(t, &runtime.Config{}, tlsScript("", fmt.Sprintf(`{host: '127.0.0.1', port: %d}`, port)))
	wantTLS(t, result, false, nil, tlsFailed("ECONNREFUSED")...)
}

func TestTLSConnect_Echo(t *testing.T) {
	ca := newTestCA(t)
	port := newTLSServer(t, ca.issue(t, "localhost"))
	result := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const socket = require('tls').connect({host: 'localhost', port: %d, ca: %q}, () => socket.write('hello'));
socket.on('data', (d) => { done({ body: d.toString() }); socket.destroy(); });
socket.on('error', (e) => done({ error: e.code }));`, port, ca.pem))
	if result["body"] != "hello" {
		t.Fatalf("result = %v", result)
	}
}
