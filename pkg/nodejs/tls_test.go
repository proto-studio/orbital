package nodejs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	t.Run("embedder callbacks see verified chains", func(t *testing.T) {
		var order []string
		var peerRaw int
		var peerChains, connChains [][]*x509.Certificate
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{
			RootCAs: ca.pool,
			VerifyPeerCertificate: func(raw [][]byte, chains [][]*x509.Certificate) error {
				order = append(order, "peer")
				peerRaw, peerChains = len(raw), chains
				return nil
			},
			VerifyConnection: func(cs tls.ConnectionState) error {
				order = append(order, "connection")
				connChains = cs.VerifiedChains
				return nil
			},
		}}
		wantTLS(t, runJS(t, cfg, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, good))), true, nil, tlsConnected...)
		if !reflect.DeepEqual(order, []string{"peer", "connection"}) {
			t.Fatalf("callbacks ran as %v", order)
		}
		if peerRaw != 1 {
			t.Errorf("VerifyPeerCertificate saw %d raw certificates", peerRaw)
		}
		for name, chains := range map[string][][]*x509.Certificate{"VerifyPeerCertificate": peerChains, "VerifyConnection": connChains} {
			if len(chains) != 1 || !chains[0][len(chains[0])-1].Equal(ca.cert) {
				t.Errorf("%s saw chains %v, want one ending at the root", name, chains)
			}
		}
	})
	t.Run("embedder callbacks can reject", func(t *testing.T) {
		deny := errors.New("denied by embedder")
		for name, base := range map[string]*tls.Config{
			"VerifyPeerCertificate": {RootCAs: ca.pool, VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error { return deny }},
			"VerifyConnection":      {RootCAs: ca.pool, VerifyConnection: func(tls.ConnectionState) error { return deny }},
		} {
			t.Run(name, func(t *testing.T) {
				result := runJS(t, &runtime.Config{ClientTLSConfig: base}, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, good)))
				if events := result["events"].([]any); len(events) == 0 || events[0] == "callback" || events[0] == "secureConnect" {
					t.Errorf("connected anyway: %v", events)
				}
			})
		}
	})
	t.Run("untrusted peer does not reach embedder callbacks", func(t *testing.T) {
		var calls atomic.Int32
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{
			RootCAs:               ca.pool,
			VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error { calls.Add(1); return nil },
			VerifyConnection:      func(tls.ConnectionState) error { calls.Add(1); return nil },
		}}
		wantTLS(t, runJS(t, cfg, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, guest))), false, leafCode, tlsFailed(leafCode)...)
		if calls.Load() != 0 {
			t.Errorf("callbacks ran %d times", calls.Load())
		}
	})
	t.Run("skipping verification leaves chains empty, as crypto/tls does", func(t *testing.T) {
		var peerCalls, connCalls atomic.Int32
		var peerChains, connChains [][]*x509.Certificate
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{
			InsecureSkipVerify: true,
			RootCAs:            ca.pool,
			VerifyPeerCertificate: func(_ [][]byte, chains [][]*x509.Certificate) error {
				peerCalls.Add(1)
				peerChains = chains
				return nil
			},
			VerifyConnection: func(cs tls.ConnectionState) error {
				connCalls.Add(1)
				connChains = cs.VerifiedChains
				return nil
			},
		}}
		wantTLS(t, runJS(t, cfg, tlsScript("", fmt.Sprintf(`{host: 'localhost', port: %d}`, good))), true, nil, tlsConnected...)
		if peerCalls.Load() != 1 || connCalls.Load() != 1 {
			t.Fatalf("callbacks ran %d and %d times", peerCalls.Load(), connCalls.Load())
		}
		if peerChains != nil || connChains != nil {
			t.Errorf("chains = %v and %v, want none", peerChains, connChains)
		}
	})
	t.Run("resumed connections match crypto/tls", func(t *testing.T) {
		var peerCalls, connCalls, resumed atomic.Int32
		var connChains [][][]*x509.Certificate
		cfg := &runtime.Config{ClientTLSConfig: &tls.Config{
			RootCAs:            ca.pool,
			ClientSessionCache: tls.NewLRUClientSessionCache(4),
			VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error {
				peerCalls.Add(1)
				return nil
			},
			VerifyConnection: func(cs tls.ConnectionState) error {
				connCalls.Add(1)
				if cs.DidResume {
					resumed.Add(1)
				}
				connChains = append(connChains, cs.VerifiedChains)
				return nil
			},
		}}
		// Reading the echo is what delivers the session ticket before the next connection.
		result := runJS(t, cfg, fmt.Sprintf(`
const tls = require('tls');
const once = () => new Promise((resolve, reject) => {
  const s = tls.connect({ host: 'localhost', port: %d }, () => s.write('x'));
  s.on('error', reject);
  s.on('data', () => s.destroy());
  s.on('close', () => resolve(s.authorized));
});
(async () => done({ first: await once(), second: await once() }))().catch((e) => done({ error: String(e) }));`, good))
		wantJSON(t, result, map[string]any{"first": true, "second": true})
		if resumed.Load() != 1 {
			t.Fatalf("%d connections resumed, want 1", resumed.Load())
		}
		if peerCalls.Load() != 1 || connCalls.Load() != 2 {
			t.Errorf("VerifyPeerCertificate ran %d times, VerifyConnection %d", peerCalls.Load(), connCalls.Load())
		}
		for i, chains := range connChains {
			if len(chains) == 0 {
				t.Errorf("connection %d had no verified chains", i)
			}
		}
	})
}

func TestTLSConnect_InjectedSockets(t *testing.T) {
	ca := newTestCA(t)
	port := newTLSServer(t, ca.issue(t, "api.test"))
	target := "127.0.0.1:" + strconv.Itoa(port)
	const publicIP = "203.0.113.80"
	resolver := func() *recordingResolver {
		return &recordingResolver{hosts: map[string][]string{"api.test": {publicIP}, "nope.test": {publicIP}}}
	}
	script := fmt.Sprintf(`{host: 'api.test', port: 443, ca: %q}`, ca.pem)

	t.Run("custom factory carries the handshake", func(t *testing.T) {
		factory := &recordingFactory{redirect: map[string]string{publicIP: target}}
		dns := resolver()
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: factory, DNSResolver: dns}, tlsScript("", script)), true, nil, tlsConnected...)
		if got := factory.dialed(); !reflect.DeepEqual(got, []string{publicIP + ":443"}) {
			t.Errorf("dialed %v", got)
		}
		if !reflect.DeepEqual(dns.lookups, []string{"api.test"}) {
			t.Errorf("lookups %v", dns.lookups)
		}
	})
	t.Run("custom factory is verified", func(t *testing.T) {
		factory := &recordingFactory{redirect: map[string]string{publicIP: target}}
		result := runJS(t, &runtime.Config{SocketFactory: factory, DNSResolver: resolver()}, tlsScript("", fmt.Sprintf(`{host: 'nope.test', port: 443, ca: %q}`, ca.pem)))
		wantTLS(t, result, false, "ERR_TLS_CERT_ALTNAME_INVALID", tlsFailed("ERR_TLS_CERT_ALTNAME_INVALID")...)
	})
	t.Run("filtered factory keeps its policy and verification", func(t *testing.T) {
		underlying := &recordingFactory{redirect: map[string]string{publicIP: target}}
		deny := runtime.NewFilteredSocketFactory(underlying, runtime.NewDenyAllPolicy())
		result := runJS(t, &runtime.Config{SocketFactory: deny, DNSResolver: resolver()}, tlsScript("", script))
		wantTLS(t, result, false, nil, tlsFailed("EACCES")...)
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("denied connection dialed %v", got)
		}

		allow := runtime.NewFilteredSocketFactory(underlying, runtime.NewAllowAllPolicy())
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: allow, DNSResolver: resolver()}, tlsScript("", script)), true, nil, tlsConnected...)
		wrongCA := fmt.Sprintf(`{host: 'api.test', port: 443, ca: %q}`, newTestCA(t).pem)
		wantTLS(t, runJS(t, &runtime.Config{SocketFactory: allow, DNSResolver: resolver()}, tlsScript("", wrongCA)), false,
			"UNABLE_TO_VERIFY_LEAF_SIGNATURE", tlsFailed("UNABLE_TO_VERIFY_LEAF_SIGNATURE")...)
	})
	t.Run("policy sees the resolved address", func(t *testing.T) {
		underlying := &recordingFactory{redirect: map[string]string{"10.0.0.9": target}}
		factory := runtime.NewFilteredSocketFactory(underlying, runtime.CommonPolicies.NoPrivateNetworks())
		dns := &recordingResolver{hosts: map[string][]string{"api.test": {"10.0.0.9"}}}
		result := runJS(t, &runtime.Config{SocketFactory: factory, DNSResolver: dns}, tlsScript("", script))
		wantTLS(t, result, false, nil, tlsFailed("EACCES")...)
		if got := underlying.dialed(); len(got) != 0 {
			t.Errorf("denied connection dialed %v", got)
		}
	})
	t.Run("no-op factory", func(t *testing.T) {
		result := runJS(t, &runtime.Config{SocketFactory: &runtime.NoOpSocketFactory{}, DNSResolver: resolver()}, tlsScript("", script))
		wantTLS(t, result, false, nil, tlsFailed("ENETUNREACH")...)
	})
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

// newTLSReplyServer starts a TLS server that reads until the client finishes sending,
// waits, then sends back reply, or what it read if reply is nil, and closes. The wait
// orders the client's finish before its data and end. Each connection's input arrives
// on the returned channel.
func newTLSReplyServer(t *testing.T, cert tls.Certificate, reply []byte) (int, <-chan []byte) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	received := make(chan []byte, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				input, _ := io.ReadAll(conn)
				received <- input
				time.Sleep(100 * time.Millisecond)
				if reply != nil {
					conn.Write(reply)
				} else {
					conn.Write(input)
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, received
}

// tlsDataScript runs body with socket s once connected, or straight away if body
// declares early. It reports events other than data, and the bytes received as hex.
func tlsDataScript(port int, ca, setup, body string) string {
	return fmt.Sprintf(`
const ev = [];
const chunks = [];
const s = require('tls').connect({host: 'localhost', port: %d, ca: %q}, () => {
  ev.push('secureConnect');
  %s
});
s.on('data', (d) => chunks.push(d));
for (const name of ['end', 'finish', 'drain']) s.on(name, () => ev.push(name));
s.on('error', (e) => ev.push('error:' + e.code));
s.on('close', (hadError) => {
  ev.push('close:' + hadError);
  done({ ev, hex: Buffer.concat(chunks).toString('hex') });
});
%s`, port, ca, body, setup)
}

func TestTLSSocket_BinaryDataIsUnchanged(t *testing.T) {
	every := make([]byte, 256)
	for i := range every {
		every[i] = byte(i)
	}
	cases := []struct {
		name  string
		bytes []byte
	}{
		{"NUL and high bytes", []byte{0x00, 0x01, 0xff, 0xfe, 0x80, 0x41}},
		{"leading NUL", []byte{0x00, 0x41}},
		{"every byte value", every},
		{"invalid UTF-8", []byte{0xc3, 0x28, 0xed, 0xa0, 0x80}},
	}
	ca := newTestCA(t)
	for _, tc := range cases {
		want := hex.EncodeToString(tc.bytes)
		t.Run(tc.name+" echoed", func(t *testing.T) {
			port := newTLSServer(t, ca.issue(t, "localhost"))
			result := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const expected = %d;
const chunks = [];
const s = require('tls').connect({host: 'localhost', port: %d, ca: %q}, () => s.write(Buffer.from(%q, 'hex')));
s.on('data', (d) => {
  chunks.push(d);
  if (Buffer.concat(chunks).length >= expected) s.destroy();
});
s.on('error', (e) => done({ error: e.code }));
s.on('close', () => done({ hex: Buffer.concat(chunks).toString('hex') }));
setTimeout(() => s.destroy(), 5000);`, len(tc.bytes), port, ca.pem, want))
			if result["hex"] != want {
				t.Fatalf("result = %v, want %s", result, want)
			}
		})
		t.Run(tc.name+" each direction", func(t *testing.T) {
			// The fixed reply shows that neither direction undoes the other's damage.
			reply := append([]byte{0xee}, tc.bytes...)
			port, received := newTLSReplyServer(t, ca.issue(t, "localhost"), reply)
			result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", fmt.Sprintf(`s.end(Buffer.from(%q, 'hex'));`, want)))
			if got := hex.EncodeToString(<-received); got != want {
				t.Errorf("server received %s, want %s", got, want)
			}
			if result["hex"] != hex.EncodeToString(reply) {
				t.Errorf("client received %v, want %x", result["hex"], reply)
			}
		})
	}
}

func TestTLSSocket_DataTypes(t *testing.T) {
	ca := newTestCA(t)
	port, received := newTLSReplyServer(t, ca.issue(t, "localhost"), nil)
	cases := []struct{ name, call, want string }{
		{"Uint8Array", `s.end(new Uint8Array([0, 200, 255]));`, "00c8ff"},
		{"latin1 string", `s.end('ÿ\u0080', 'latin1');`, "ff80"},
		{"utf8 string", `s.end('héllo €');`, "68c3a96c6c6f20e282ac"},
		{"hex string", `s.write('6869', 'hex'); s.end();`, "6869"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", tc.call))
			if got := hex.EncodeToString(<-received); got != tc.want {
				t.Errorf("server received %s, want %s", got, tc.want)
			}
			if result["hex"] != tc.want {
				t.Errorf("client received %v, want %s", result["hex"], tc.want)
			}
		})
	}
}

// The events and data in this test are what Node 20.18 produces with the same script.
func TestTLSSocket_End(t *testing.T) {
	ca := newTestCA(t)
	port, _ := newTLSReplyServer(t, ca.issue(t, "localhost"), nil)
	clean := []any{"secureConnect", "finish", "end", "close:false"}
	bye := hex.EncodeToString([]byte("bye"))

	cases := []struct {
		name   string
		setup  string
		body   string
		events []any
		data   string
	}{
		{name: "data is delivered", body: `s.end('bye');`, events: clean, data: bye},
		{name: "no data", body: `s.end();`, events: clean},
		{name: "null data", body: `s.end(null);`, events: clean},
		{name: "before the handshake", setup: `s.end('bye');`, events: clean, data: bye},
		{name: "writes before the handshake", setup: `s.write('b'); s.write('y'); s.end('e');`, events: clean, data: bye},
		{name: "twice", body: `s.end('bye'); s.end();`, events: clean, data: bye},
		{
			name:   "callbacks run in order",
			body:   `s.write('b', () => ev.push('writeCb')); s.end('ye', () => ev.push('endCb'));`,
			events: []any{"secureConnect", "writeCb", "endCb", "finish", "end", "close:false"},
			data:   bye,
		},
		{
			name:   "callback alone",
			body:   `s.end(() => ev.push('endCb'));`,
			events: []any{"secureConnect", "endCb", "finish", "end", "close:false"},
		},
		{
			name:   "encoding and callback",
			body:   `s.end('627965', 'hex', () => ev.push('endCb'));`,
			events: []any{"secureConnect", "endCb", "finish", "end", "close:false"},
			data:   bye,
		},
		{
			name:   "data and callback",
			body:   `s.end('bye', () => ev.push('endCb'));`,
			events: []any{"secureConnect", "endCb", "finish", "end", "close:false"},
			data:   bye,
		},
		{
			name: "write after end",
			body: `s.end('a'); ev.push('returned:' + s.write('b', (e) => ev.push('writeCb:' + e.code)));`,
			events: []any{"secureConnect", "returned:false", "writeCb:ERR_STREAM_WRITE_AFTER_END",
				"error:ERR_STREAM_WRITE_AFTER_END", "close:true"},
		},
		{
			name:   "destroy answers the write in progress",
			body:   `s.write('z', (e) => ev.push('writeCb:' + e)); s.destroy();`,
			events: []any{"secureConnect", "writeCb:null", "close:false"},
		},
		{
			name: "destroy cancels what has not started",
			body: `s.write('a', (e) => ev.push('a:' + e));
s.write('b', (e) => ev.push('b:' + e.code));
s.end(() => {});
s.destroy();`,
			events: []any{"secureConnect", "a:null", "b:ERR_STREAM_DESTROYED", "close:false"},
		},
		{
			name:   "write after destroy",
			body:   `s.destroy(); s.write('z', (e) => { ev.push('writeCb:' + e.code); done({ ev, hex: '' }); });`,
			events: []any{"secureConnect", "close:false", "writeCb:ERR_STREAM_DESTROYED"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, tc.setup, tc.body))
			if !reflect.DeepEqual(result["ev"], tc.events) {
				t.Errorf("events = %v, want %v", result["ev"], tc.events)
			}
			if result["hex"] != tc.data {
				t.Errorf("data = %v, want %q", result["hex"], tc.data)
			}
		})
	}
}

func TestTLSSocket_WritesStayInOrder(t *testing.T) {
	ca := newTestCA(t)
	port, received := newTLSReplyServer(t, ca.issue(t, "localhost"), []byte{})
	runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", `
for (let i = 0; i < 200; i++) s.write(String(i % 10).repeat(100));
s.end();`))
	input := <-received
	if len(input) != 20000 {
		t.Fatalf("server received %d bytes", len(input))
	}
	for i, b := range input {
		if want := byte('0' + (i/100)%10); b != want {
			t.Fatalf("byte %d = %c, want %c", i, b, want)
		}
	}
}

func TestTLSSocket_Backpressure(t *testing.T) {
	ca := newTestCA(t)
	port, received := newTLSReplyServer(t, ca.issue(t, "localhost"), []byte{})

	t.Run("small write", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", `ev.push('returned:' + s.write('x')); s.end();`))
		if want := []any{"secureConnect", "returned:true", "finish", "end", "close:false"}; !reflect.DeepEqual(result["ev"], want) {
			t.Errorf("events = %v", result["ev"])
		}
		<-received
	})
	t.Run("large write", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", `
ev.push('returned:' + s.write(Buffer.alloc(1 << 20, 0xab)));
s.once('drain', () => s.end());`))
		if want := []any{"secureConnect", "returned:false", "drain", "finish", "end", "close:false"}; !reflect.DeepEqual(result["ev"], want) {
			t.Errorf("events = %v", result["ev"])
		}
		input := <-received
		if len(input) != 1<<20 || input[0] != 0xab || input[len(input)-1] != 0xab {
			t.Errorf("server received %d bytes", len(input))
		}
	})
}

func TestTLSSocket_LargeEcho(t *testing.T) {
	ca := newTestCA(t)
	port := newTLSServer(t, ca.issue(t, "localhost"))
	result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", `
const big = Buffer.alloc(1 << 20);
for (let i = 0; i < big.length; i++) big[i] = (i * 31) & 0xff;
s.end(big);
s.on('close', () => { done({ ev, match: Buffer.concat(chunks).equals(big) }); });`))
	if result["match"] != true {
		t.Fatalf("result = %v", result)
	}
}

func TestTLSSocket_PeerEndsFirst(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.issue(t, "localhost")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan []byte, 2)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.Write([]byte("hi"))
				conn.(*tls.Conn).CloseWrite()
				input, _ := io.ReadAll(conn)
				received <- input
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	hi := hex.EncodeToString([]byte("hi"))

	t.Run("closes by default", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, tlsDataScript(port, ca.pem, "", ``))
		if want := []any{"secureConnect", "end", "finish", "close:false"}; !reflect.DeepEqual(result["ev"], want) {
			t.Errorf("events = %v", result["ev"])
		}
		if result["hex"] != hi {
			t.Errorf("data = %v", result["hex"])
		}
		<-received
	})
	t.Run("allowHalfOpen keeps the sending side", func(t *testing.T) {
		result := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const ev = [];
const s = require('tls').connect({host: 'localhost', port: %d, ca: %q, allowHalfOpen: true});
for (const name of ['end', 'finish']) s.on(name, () => ev.push(name));
s.resume();
s.on('end', () => setTimeout(() => s.end('late'), 50));
s.on('error', (e) => ev.push('error:' + e.code));
s.on('close', (hadError) => { ev.push('close:' + hadError); done({ ev }); });`, port, ca.pem))
		if want := []any{"end", "finish", "close:false"}; !reflect.DeepEqual(result["ev"], want) {
			t.Errorf("events = %v", result["ev"])
		}
		if got := string(<-received); got != "late" {
			t.Errorf("server received %q", got)
		}
	})
}

// As in Node, end leaves the socket open until the peer finishes too.
func TestTLSSocket_EndWaitsForPeer(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.issue(t, "localhost")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
		<-release
	}()
	result := runJS(t, &runtime.Config{}, tlsDataScript(ln.Addr().(*net.TCPAddr).Port, ca.pem, "", `
s.end('bye');
setTimeout(() => { ev.push('destroy'); s.destroy(); }, 200);`))
	if want := []any{"secureConnect", "finish", "destroy", "close:false"}; !reflect.DeepEqual(result["ev"], want) {
		t.Errorf("events = %v", result["ev"])
	}
}

// hidingFactory gives out sockets without their CloseWrite method.
type hidingFactory struct {
	runtime.RealSocketFactory
	hideTCP, hideTLS bool
}

func (f *hidingFactory) CreateTCPSocket() runtime.TCPSocket {
	socket := f.RealSocketFactory.CreateTCPSocket()
	if f.hideTCP {
		return struct{ runtime.TCPSocket }{socket}
	}
	return socket
}

func (f *hidingFactory) CreateTLSSocket(socket runtime.TCPSocket, config *tls.Config, isServer bool) runtime.TCPSocket {
	wrapped := f.RealSocketFactory.CreateTLSSocket(socket, config, isServer)
	if f.hideTLS {
		return struct{ runtime.TCPSocket }{wrapped}
	}
	return wrapped
}

func TestTLSSocket_EndThroughInjectedSockets(t *testing.T) {
	ca := newTestCA(t)
	port, received := newTLSReplyServer(t, ca.issue(t, "api.test", "localhost"), nil)
	target := "127.0.0.1:" + strconv.Itoa(port)
	redirect := func() *recordingFactory { return &recordingFactory{redirect: map[string]string{"localhost": target}} }
	bye := hex.EncodeToString([]byte("bye"))
	clean := []any{"secureConnect", "finish", "end", "close:false"}

	cases := []struct {
		name    string
		factory runtime.SocketFactory
	}{
		{"custom factory", redirect()},
		{"filtered factory", runtime.NewFilteredSocketFactory(redirect(), runtime.NewAllowAllPolicy())},
		{"close_notify alone when TCP cannot half-close", &hidingFactory{hideTCP: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runJS(t, &runtime.Config{SocketFactory: tc.factory}, tlsDataScript(port, ca.pem, "", `s.end('bye');`))
			if !reflect.DeepEqual(result["ev"], clean) {
				t.Errorf("events = %v", result["ev"])
			}
			if result["hex"] != bye {
				t.Errorf("data = %v", result["hex"])
			}
			if got := string(<-received); got != "bye" {
				t.Errorf("server received %q", got)
			}
		})
	}

	// The written data still arrives, but a socket that cannot half-close has to close,
	// so no reply can come back.
	t.Run("falls back to closing when TLS cannot half-close", func(t *testing.T) {
		result := runJS(t, &runtime.Config{SocketFactory: &hidingFactory{hideTLS: true}}, tlsDataScript(port, ca.pem, "", `s.end('bye');`))
		if events := result["ev"].([]any); len(events) == 0 || events[len(events)-1] != "close:false" {
			t.Errorf("events = %v", result["ev"])
		}
		if got := string(<-received); got != "bye" {
			t.Errorf("server received %q", got)
		}
	})
}

func TestTLS_Servers(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM := ca.issuePEM(t, "localhost", "127.0.0.1")
	otherPEM, otherKeyPEM := ca.issuePEM(t, "other.test")
	clientPEM, clientKeyPEM := ca.issueClientPEM(t, "client.test")
	prelude := fmt.Sprintf(`
const tls = require('tls');
const CA = %q, CERT = %q, KEY = %q, OTHER = %q, OTHER_KEY = %q, CLIENT = %q, CLIENT_KEY = %q;
const secure = async (options, handler) => {
  const server = tls.createServer({ cert: CERT, key: KEY, ...options }, handler);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  return server;
};
const clientOf = (server, options) => ({ port: server.address().port, host: 'localhost', ca: CA, ...options });
`, ca.pem, certPEM, keyPEM, otherPEM, otherKeyPEM, clientPEM, clientKeyPEM)

	result := runScript(t, &runtime.Config{}, prelude+`
const out = {};
const s1 = await secure({}, (c) => c.on('data', (d) => c.end(d)));
out.encryptedEcho = await new Promise((resolve) => {
  const c = tls.connect(clientOf(s1), () => c.write(Buffer.from([0, 255, 1])));
  const b = [];
  c.on('data', (d) => b.push(d));
  c.on('close', () => resolve(Buffer.concat(b).toString('hex')));
});
s1.close();

out.isEncrypted = await new Promise(async (resolve) => {
  const s = await secure({}, (c) => c.end('secret'));
  const raw = net.connect(s.address().port, '127.0.0.1', () => raw.write('not tls'));
  raw.on('data', (d) => { raw.destroy(); s.close(); resolve(d.toString('latin1').includes('secret')); });
  raw.on('error', () => { s.close(); resolve(false); });
  raw.on('close', () => { s.close(); resolve(false); });
});

const props = {};
const s2 = await secure({ ALPNProtocols: ['h2', 'x'] }, (c) => {
  props.server = [c.encrypted, c.authorized, c.alpnProtocol, c.servername, c.getProtocol(), typeof c.getCipher().name, c instanceof tls.TLSSocket, c instanceof net.Socket];
  c.end();
});
await new Promise((resolve) => {
  const c = tls.connect(clientOf(s2, { ALPNProtocols: ['x'] }), () => {
    const leaf = c.getPeerCertificate(true);
    props.client = [c.encrypted, c.authorized, c.alpnProtocol, c.servername, c.getProtocol(), c.getCipher().version, leaf.subject.CN, leaf.issuerCertificate.subject.CN, c.isSessionReused(), c.exportKeyingMaterial(8, 'l').length];
  });
  c.resume();
  c.on('close', resolve);
});
s2.close();
out.props = props;

const certs = [];
const s3 = await secure({ requestCert: true, rejectUnauthorized: false, ca: CA }, (c) => {
  certs.push([c.authorized, c.authorizationError, c.getPeerCertificate().subject && c.getPeerCertificate().subject.CN]);
  c.end();
});
for (const extra of [{}, { cert: CLIENT, key: CLIENT_KEY }]) {
  await new Promise((resolve) => { const c = tls.connect(clientOf(s3, extra)); c.resume(); c.on('close', resolve); });
}
s3.close();
out.clientCerts = certs;

out.requiredClientCert = await new Promise(async (resolve) => {
  const ev = [];
  const s = await secure({ requestCert: true, ca: CA }, (c) => { ev.push('secure'); c.end(); });
  s.on('tlsClientError', () => ev.push('tlsClientError'));
  const c = tls.connect(clientOf(s, { maxVersion: 'TLSv1.2' }));
  c.on('error', () => {});
  c.resume();
  c.on('close', () => setTimeout(() => { s.close(); resolve(ev); }, 20));
});

const peerName = (s, options) => new Promise((resolve) => {
  const c = tls.connect(clientOf(s, { host: '127.0.0.1', ...options }), () => {
    const name = c.getPeerCertificate().subject.CN;
    c.resume();
    c.on('close', () => resolve(name));
  });
  c.on('error', (e) => resolve(e.code));
});
const s4 = await secure({}, (c) => c.end());
s4.addContext('other.test', tls.createSecureContext({ cert: OTHER, key: OTHER_KEY }));
out.addContext = await peerName(s4, { servername: 'other.test' });
s4.close();
const seen = [];
const s5 = await secure({ SNICallback: (name, cb) => {
  seen.push(name);
  const context = tls.createSecureContext({ cert: OTHER, key: OTHER_KEY });
  // Answering twice must not block anything.
  cb(null, context);
  cb(null, context);
} }, (c) => c.end());
out.sniCallback = [await peerName(s5, { servername: 'other.test' }), seen];
s5.close();

out.startTLS = await new Promise(async (resolve) => {
  const context = tls.createSecureContext({ cert: CERT, key: KEY });
  const s = await new Promise((r) => {
    const server = net.createServer((plain) => {
      plain.once('data', () => {
        plain.write('go');
        const upgraded = new tls.TLSSocket(plain, { isServer: true, secureContext: context });
        upgraded.on('data', (d) => upgraded.end(String(d).toUpperCase()));
        upgraded.on('error', () => {});
      });
    });
    server.listen(0, '127.0.0.1', () => r(server));
  });
  const plain = net.connect(s.address().port, '127.0.0.1', () => plain.write('starttls'));
  plain.once('data', () => {
    plain.removeAllListeners('data');
    const c = tls.connect({ socket: plain, host: 'localhost', ca: CA }, () => c.write('hello'));
    c.on('data', (d) => { s.close(); resolve(String(d)); });
    c.on('error', (e) => { s.close(); resolve(e.code); });
  });
});

const badCreds = [];
for (const f of [() => tls.createServer({ key: 'x', cert: 'y' }), () => tls.createSecureContext({ key: 'x', cert: 'y' }), () => tls.createServer({ pfx: 'x' })]) {
  try { f(); badCreds.push('ok'); } catch (e) { badCreds.push(typeof e.code); }
}
out.badCreds = badCreds;

out.versionBound = await new Promise(async (resolve) => {
  const s = await secure({ maxVersion: 'TLSv1.2' }, (c) => c.end());
  const c = tls.connect(clientOf(s), () => {
    const v = c.getProtocol();
    c.resume();
    c.on('close', () => { s.close(); resolve(v); });
  });
});
done(out);`)
	wantJSON(t, result, map[string]any{
		"encryptedEcho": "00ff01",
		"isEncrypted":   false,
		"props": map[string]any{
			"server": []any{true, false, "x", false, "TLSv1.3", "string", true, true},
			"client": []any{true, true, "x", false, "TLSv1.3", "TLSv1.3", "localhost", "Orbital Test CA", false, float64(8)},
		},
		"clientCerts": []any{
			[]any{false, "UNABLE_TO_GET_ISSUER_CERT", nil},
			[]any{true, nil, "client.test"},
		},
		"requiredClientCert": []any{"tlsClientError"},
		"addContext":         "other.test",
		"sniCallback":        []any{"other.test", []any{"other.test"}},
		"startTLS":           "HELLO",
		"badCreds":           []any{"string", "string", "string"},
		"versionBound":       "TLSv1.2",
	})
}
