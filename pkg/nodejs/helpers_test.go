package nodejs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
)

// runJS runs source through the real runtime, including its event loop, and returns
// the value the script passed to done(). Fixtures inject everything via cfg.
func runJS(t *testing.T, cfg *runtime.Config, source string) map[string]any {
	t.Helper()
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	cfg.EnableConsole = true
	cfg.EnableTimers = true
	inst, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Runtime.Dispose()
	return runIn(t, inst, source)
}

func runIn(t *testing.T, inst *Instance, source string) map[string]any {
	t.Helper()
	prelude := `globalThis.done = (v) => { globalThis.__result = JSON.stringify(v); };
globalThis.errInfo = (e) => e ? { code: e.code || null, message: e.message } : null;
`
	if _, err := inst.Runtime.Run(prelude+source, "test.js"); err != nil {
		t.Fatalf("script failed: %v", err)
	}
	return resultOf(t, inst)
}

func resultOf(t *testing.T, inst *Instance) map[string]any {
	t.Helper()
	val, err := inst.Runtime.GetGlobal("__result")
	if err != nil {
		t.Fatal(err)
	}
	if val == nil || val.IsUndefined() {
		t.Fatalf("script never called done() (killed: %v, reason: %q)", inst.Runtime.IsKilled(), inst.Runtime.KillReason())
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(val.String()), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// testCA issues certificates for TLS fixtures.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
	pem  string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Orbital Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{
		cert: cert,
		key:  key,
		pool: pool,
		pem:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}

func (ca *testCA) issue(t *testing.T, names ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, name := range names {
		if ip := net.ParseIP(name); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, name)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// recordingFactory is an injected SocketFactory. Every connection it opens is logged,
// and addresses in redirect reach a local fixture, so tests can use non-local
// destination addresses without leaving the machine.
type recordingFactory struct {
	runtime.RealSocketFactory
	mu       sync.Mutex
	dials    []string
	redirect map[string]string
}

func (f *recordingFactory) CreateTCPSocket() runtime.TCPSocket {
	return &recordingSocket{RealTCPSocket: &runtime.RealTCPSocket{}, factory: f}
}

func (f *recordingFactory) dialed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dials...)
}

type recordingSocket struct {
	*runtime.RealTCPSocket
	factory *recordingFactory
}

func (s *recordingSocket) Connect(ctx context.Context, address string, port int) error {
	target := net.JoinHostPort(address, itoa(port))
	s.factory.mu.Lock()
	s.factory.dials = append(s.factory.dials, target)
	redirect, ok := s.factory.redirect[address]
	s.factory.mu.Unlock()
	if ok {
		host, portStr, err := net.SplitHostPort(redirect)
		if err != nil {
			return err
		}
		return s.RealTCPSocket.Connect(ctx, host, atoi(portStr))
	}
	return s.RealTCPSocket.Connect(ctx, address, port)
}

// recordingResolver is an injected DNS resolver with a fixed table.
type recordingResolver struct {
	runtime.NoOpResolver
	mu      sync.Mutex
	hosts   map[string][]string
	lookups []string
}

func (r *recordingResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lookups = append(r.lookups, host)
	addrs, ok := r.hosts[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, net.ParseIP(addr))
	}
	return out, nil
}

// newEchoListener starts a TCP server that echoes, and returns its address.
func newEchoListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
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
	return ln.Addr().String()
}

// issueClientPEM issues a client certificate and returns it and its key as PEM.
func (ca *testCA) issueClientPEM(t *testing.T, name string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return encodePEM(t, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
}

// issuePEM issues a certificate and returns it and its key as PEM.
func (ca *testCA) issuePEM(t *testing.T, names ...string) (string, string) {
	t.Helper()
	return encodePEM(t, ca.issue(t, names...))
}

// selfSignedPEM returns the CA's own self-signed certificate and key as PEM.
func (ca *testCA) selfSignedPEM(t *testing.T) (string, string) {
	t.Helper()
	return encodePEM(t, tls.Certificate{Certificate: [][]byte{ca.cert.Raw}, PrivateKey: ca.key})
}

func encodePEM(t *testing.T, cert tls.Certificate) (string, string) {
	t.Helper()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	return string(certPEM), string(keyPEM)
}
