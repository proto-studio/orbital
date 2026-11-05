// Package tls implements the Node.js tls module. A TLS socket is a net socket whose
// transport has been upgraded in place, so it shares all of node:net's behavior and the
// runtime's injected network controls.
package tls

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"proto.zip/studio/orbital/pkg/nodejs/internal/tlsclient"
	netmodule "proto.zip/studio/orbital/pkg/nodejs/net"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed tls.js
var tlsJS string

// TLS provides TLS/SSL functionality.
type TLS struct {
	rt  *runtime.Runtime
	net *netmodule.Net

	mu          sync.Mutex
	sessions    map[int64]*session
	contexts    map[int64]*serverContext
	nextContext int64
}

// session is the TLS state of one upgraded socket.
type session struct {
	conn   *tls.Conn
	peer   func() []*x509.Certificate
	config *tls.Config
}

// New creates a new TLS module on top of the given net module.
func New(netModule *netmodule.Net) *TLS {
	return &TLS{
		net:      netModule,
		sessions: make(map[int64]*session),
		contexts: make(map[int64]*serverContext),
	}
}

// Name returns the module name.
func (t *TLS) Name() string {
	return "tls"
}

// Register sets up the tls module.
func (t *TLS) Register(rt *runtime.Runtime) error {
	t.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	funcs := map[string]v8.FunctionCallback{
		"validateClient":       t.validateClientFunc,
		"upgradeClient":        t.upgradeClientFunc,
		"createServerContext":  t.createServerContextFunc,
		"upgradeServer":        t.upgradeServerFunc,
		"getPeerCertificate":   t.getPeerCertificateFunc,
		"getCertificate":       t.getCertificateFunc,
		"getCipher":            t.getCipherFunc,
		"getProtocol":          t.getProtocolFunc,
		"isSessionReused":      t.isSessionReusedFunc,
		"exportKeyingMaterial": t.exportKeyingMaterialFunc,
	}

	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := internal.Set(name, val); err != nil {
			return err
		}
	}

	if err := rt.SetGlobal("__tls_internal", internal); err != nil {
		return err
	}

	// Must come after net module
	_, err = rt.RunScript(tlsJS, "tls.js")
	return err
}

func (t *TLS) deliver(fn func(ctx *v8.Context)) {
	t.rt.EventLoop().EnqueueMicrotask(func() {
		ctx := t.rt.Context()
		fn(ctx)
		ctx.PerformMicrotaskCheckpoint()
	})
}

func jsString(ctx *v8.Context, s string) *v8.Value {
	v, err := ctx.NewString(s)
	if err != nil {
		return ctx.Null()
	}
	return v
}

func jsJSON(ctx *v8.Context, v any) *v8.Value {
	encoded, err := json.Marshal(v)
	if err != nil {
		return ctx.Null()
	}
	return jsString(ctx, string(encoded))
}

// handshakeResult is what the JavaScript handshake callback receives.
type handshakeResult struct {
	Message              string `json:"message,omitempty"`
	Code                 string `json:"code,omitempty"`
	Authorized           bool   `json:"authorized"`
	AuthorizationError   string `json:"authorizationError,omitempty"`
	ScriptChecksIdentity bool   `json:"scriptChecksIdentity"`
	ALPN                 string `json:"alpn,omitempty"`
	Servername           string `json:"servername,omitempty"`
}

// clientOptions are tls.connect's options.
type clientOptions struct {
	tlsclient.Options
	ALPNProtocols []string `json:"ALPNProtocols"`
	MinVersion    string   `json:"minVersion"`
	MaxVersion    string   `json:"maxVersion"`
}

func (o *clientOptions) newVerifier(base *tls.Config, host string) (*tlsclient.Verifier, error) {
	o.Options.SNIOnlyFromServername = true
	verifier, err := tlsclient.New(base, host, o.Options)
	if err != nil {
		return nil, err
	}
	if len(o.ALPNProtocols) > 0 {
		verifier.Config.NextProtos = o.ALPNProtocols
	}
	if base == nil {
		if err := applyVersions(verifier.Config, o.MinVersion, o.MaxVersion); err != nil {
			return nil, err
		}
	}
	return verifier, nil
}

func tlsVersion(name string) (uint16, error) {
	switch name {
	case "TLSv1":
		return tls.VersionTLS10, nil
	case "TLSv1.1":
		return tls.VersionTLS11, nil
	case "TLSv1.2":
		return tls.VersionTLS12, nil
	case "TLSv1.3":
		return tls.VersionTLS13, nil
	}
	return 0, fmt.Errorf("ERR_TLS_INVALID_PROTOCOL_VERSION: %s is not a valid TLS protocol version", name)
}

func applyVersions(cfg *tls.Config, minVersion, maxVersion string) error {
	if minVersion != "" {
		v, err := tlsVersion(minVersion)
		if err != nil {
			return err
		}
		cfg.MinVersion = v
	}
	if maxVersion != "" {
		v, err := tlsVersion(maxVersion)
		if err != nil {
			return err
		}
		cfg.MaxVersion = v
	}
	return nil
}

func describeForJS(err error) handshakeResult {
	msg, code := tlsclient.Describe(err, "", 0)
	if code != "" {
		msg = strings.TrimPrefix(msg, code+": ")
	}
	return handshakeResult{Message: msg, Code: code}
}

// validateClient(optionsJSON) checks options before anything connects, because bad
// credentials throw from tls.connect in Node. It returns null or a JSON error.
func (t *TLS) validateClientFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Null()
	}
	var opts clientOptions
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		return ctx.Throw("tls: invalid connect options")
	}
	if _, err := opts.newVerifier(t.rt.ClientTLSConfig(), "localhost"); err != nil {
		return jsJSON(ctx, describeForJS(err))
	}
	return ctx.Null()
}

// upgradeClient(netId, host, optionsJSON, callback) starts a client handshake on a
// connected socket. callback receives a JSON handshakeResult.
func (t *TLS) upgradeClientFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("tls: invalid arguments")
	}
	id := int64(args[0].Integer())
	host := args[1].String()
	callback := args[3]

	var opts clientOptions
	if err := json.Unmarshal([]byte(args[2].String()), &opts); err != nil {
		return ctx.Throw("tls: invalid connect options")
	}
	verifier, err := opts.newVerifier(t.rt.ClientTLSConfig(), host)
	if err != nil {
		t.deliver(func(ctx *v8.Context) { t.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, describeForJS(err))) })
		return nil
	}

	finish := func(conn *tls.Conn, err error) handshakeResult {
		var result handshakeResult
		if err != nil {
			result = describeForJS(err)
		}
		result.Authorized = verifier.Authorized()
		result.ScriptChecksIdentity = verifier.ScriptChecksIdentity()
		if authErr := verifier.AuthorizationError(); authErr != nil {
			_, result.AuthorizationError = tlsclient.Describe(authErr, host, 0)
		}
		if err == nil {
			result.ALPN = conn.ConnectionState().NegotiatedProtocol
		}
		return result
	}
	t.startHandshake(id, callback, verifier.Config, false, verifier.PeerCertificates, finish)
	return nil
}

// startHandshake upgrades the socket in place and reports the outcome to callback.
func (t *TLS) startHandshake(id int64, callback *v8.Value, cfg *tls.Config, isServer bool,
	peer func() []*x509.Certificate, finish func(*tls.Conn, error) handshakeResult) {
	fail := func(err error) {
		t.deliver(func(ctx *v8.Context) { t.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, describeForJS(err))) })
	}

	conn, ok := t.net.Conn(id)
	if !ok {
		fail(runtime.ErrSocketClosed)
		return
	}
	raw := conn.Socket()
	transport, err := conn.BeginUpgrade()
	if err != nil {
		fail(err)
		return
	}

	var tlsConn *tls.Conn
	if isServer {
		tlsConn = tls.Server(transport, cfg)
	} else {
		tlsConn = tls.Client(transport, cfg)
	}

	t.mu.Lock()
	t.sessions[id] = &session{conn: tlsConn, peer: peer, config: cfg}
	t.mu.Unlock()
	context.AfterFunc(conn.Context(), func() {
		t.mu.Lock()
		delete(t.sessions, id)
		t.mu.Unlock()
	})

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		err := tlsConn.HandshakeContext(conn.Context())
		if err == nil {
			conn.SetSocket(&socket{Conn: tlsConn, raw: raw})
		}
		result := finish(tlsConn, err)
		t.deliver(func(ctx *v8.Context) { t.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, result)) })
	}()
}

// socket presents an established TLS connection as a socket.
type socket struct {
	*tls.Conn
	raw runtime.TCPSocket
}

// Connect is never called: the connection already exists.
func (s *socket) Connect(context.Context, string, int) error { return nil }

// SetTimeout delegates to the underlying socket.
func (s *socket) SetTimeout(d time.Duration) error { return s.raw.SetTimeout(d) }

// SetKeepAlive delegates to the underlying socket.
func (s *socket) SetKeepAlive(enable bool) error { return s.raw.SetKeepAlive(enable) }

// SetNoDelay delegates to the underlying socket.
func (s *socket) SetNoDelay(enable bool) error { return s.raw.SetNoDelay(enable) }

// LocalAddr delegates to the underlying socket.
func (s *socket) LocalAddr() net.Addr { return s.raw.LocalAddr() }

// RemoteAddr delegates to the underlying socket.
func (s *socket) RemoteAddr() net.Addr { return s.raw.RemoteAddr() }

// Ref delegates to the underlying socket.
func (s *socket) Ref() { s.raw.Ref() }

// Unref delegates to the underlying socket.
func (s *socket) Unref() { s.raw.Unref() }

// CloseWrite sends close_notify and then, where the underlying socket supports it, a TCP
// FIN. The close_notify alone tells a TLS peer the data is complete, so an underlying
// socket that cannot half-close is not an error.
func (s *socket) CloseWrite() error {
	if err := s.Conn.CloseWrite(); err != nil {
		return err
	}
	if err := runtime.CloseWrite(s.raw); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	return nil
}

// SetLinger delegates to the underlying socket if it supports linger.
func (s *socket) SetLinger(seconds int) error {
	if l, ok := s.raw.(runtime.LingerSetter); ok {
		return l.SetLinger(seconds)
	}
	return errors.ErrUnsupported
}

// serverContext is a parsed tls.createSecureContext or tls.createServer configuration.
type serverContext struct {
	certificates []tls.Certificate
	clientCAs    *x509.CertPool
	minVersion   uint16
	maxVersion   uint16
}

type contextOptions struct {
	Cert       []string `json:"cert"`
	Key        []string `json:"key"`
	CA         []string `json:"ca"`
	MinVersion string   `json:"minVersion"`
	MaxVersion string   `json:"maxVersion"`
}

// createServerContext(optionsJSON) parses credentials and returns an id, or a JSON error.
func (t *TLS) createServerContextFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("tls: invalid context options")
	}
	var opts contextOptions
	if err := json.Unmarshal([]byte(args[0].String()), &opts); err != nil {
		return ctx.Throw("tls: invalid context options")
	}

	context := &serverContext{}
	if len(opts.Cert) != len(opts.Key) && len(opts.Key) != 0 && len(opts.Cert) != 0 {
		return jsJSON(ctx, handshakeResult{Message: "certificates and keys do not pair up", Code: "ERR_OSSL_X509_KEY_VALUES_MISMATCH"})
	}
	for i := range opts.Cert {
		if i >= len(opts.Key) {
			break
		}
		cert, err := tls.X509KeyPair([]byte(opts.Cert[i]), []byte(opts.Key[i]))
		if err != nil {
			return jsJSON(ctx, handshakeResult{Message: err.Error(), Code: "ERR_OSSL_X509_KEY_VALUES_MISMATCH"})
		}
		context.certificates = append(context.certificates, cert)
	}
	if opts.CA != nil {
		context.clientCAs = x509.NewCertPool()
		for _, pem := range opts.CA {
			context.clientCAs.AppendCertsFromPEM([]byte(pem))
		}
	}
	probe := &tls.Config{}
	if err := applyVersions(probe, opts.MinVersion, opts.MaxVersion); err != nil {
		return jsJSON(ctx, describeForJS(err))
	}
	context.minVersion = probe.MinVersion
	context.maxVersion = probe.MaxVersion

	t.mu.Lock()
	t.nextContext++
	id := t.nextContext
	t.contexts[id] = context
	t.mu.Unlock()
	return ctx.NewNumber(float64(id))
}

func (t *TLS) context(id int64) *serverContext {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.contexts[id]
}

type serverOptions struct {
	Context            int64    `json:"context"`
	ALPNProtocols      []string `json:"ALPNProtocols"`
	RequestCert        bool     `json:"requestCert"`
	RejectUnauthorized bool     `json:"rejectUnauthorized"`
	HasSNI             bool     `json:"hasSNI"`
}

// upgradeServer(netId, optionsJSON, chooseContext, callback) starts a server handshake.
// chooseContext(servername, reply) picks a secure context for a ClientHello, and is only
// called when the options ask for it. callback receives a JSON handshakeResult.
func (t *TLS) upgradeServerFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("tls: invalid arguments")
	}
	id := int64(args[0].Integer())
	var opts serverOptions
	if err := json.Unmarshal([]byte(args[1].String()), &opts); err != nil {
		return ctx.Throw("tls: invalid server options")
	}
	chooseContext := args[2]
	callback := args[3]

	conn, ok := t.net.Conn(id)
	if !ok {
		t.deliver(func(ctx *v8.Context) {
			t.rt.MakeCallback(callback, ctx.Undefined(), jsJSON(ctx, describeForJS(runtime.ErrSocketClosed)))
		})
		return nil
	}

	var (
		mu       sync.Mutex
		peer     []*x509.Certificate
		authErr  error
		sawCerts bool
	)
	active := t.context(opts.Context)

	buildConfig := func(sc *serverContext) *tls.Config {
		cfg := &tls.Config{
			NextProtos: opts.ALPNProtocols,
		}
		if sc != nil {
			cfg.Certificates = sc.certificates
			cfg.MinVersion = sc.minVersion
			cfg.MaxVersion = sc.maxVersion
		}
		if opts.RequestCert {
			// The client certificate is checked below, so the outcome can be recorded
			// when the server does not reject.
			cfg.ClientAuth = tls.RequestClientCert
			var roots *x509.CertPool
			if sc != nil {
				roots = sc.clientCAs
			}
			cfg.VerifyConnection = func(cs tls.ConnectionState) error {
				err := verifyClient(cs, roots)
				mu.Lock()
				peer = cs.PeerCertificates
				sawCerts = true
				authErr = err
				mu.Unlock()
				if err != nil && opts.RejectUnauthorized {
					return err
				}
				return nil
			}
		}
		return cfg
	}

	cfg := buildConfig(active)
	if opts.HasSNI {
		cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			chosen := make(chan int64, 1)
			t.deliver(func(ctx *v8.Context) {
				tmpl, err := ctx.Isolate().NewFunctionTemplate(func(replyInfo *v8.FunctionCallbackInfo) *v8.Value {
					var id int64
					if args := replyInfo.Args(); len(args) > 0 {
						id = int64(args[0].Integer())
					}
					// A script may call back more than once. Only the first answer counts.
					select {
					case chosen <- id:
					default:
					}
					return nil
				})
				if err != nil {
					chosen <- 0
					return
				}
				reply, err := tmpl.GetFunction(ctx)
				if err != nil {
					chosen <- 0
					return
				}
				t.rt.MakeCallback(chooseContext, ctx.Undefined(), jsString(ctx, hello.ServerName), reply)
			})
			select {
			case id := <-chosen:
				if sc := t.context(id); sc != nil {
					return buildConfig(sc), nil
				}
				return nil, nil
			case <-conn.Context().Done():
				return nil, conn.Context().Err()
			}
		}
	}

	finish := func(tlsConn *tls.Conn, err error) handshakeResult {
		var result handshakeResult
		if err != nil {
			result = describeForJS(err)
		}
		mu.Lock()
		defer mu.Unlock()
		result.Authorized = opts.RequestCert && sawCerts && authErr == nil
		if authErr != nil {
			_, result.AuthorizationError = tlsclient.Describe(authErr, "", 0)
		}
		if err == nil {
			state := tlsConn.ConnectionState()
			result.ALPN = state.NegotiatedProtocol
			result.Servername = state.ServerName
		}
		return result
	}
	peers := func() []*x509.Certificate {
		mu.Lock()
		defer mu.Unlock()
		return peer
	}
	t.startHandshake(id, callback, cfg, true, peers, finish)
	return nil
}

// verifyClient checks a client's certificate against the context's CAs, or the system's
// when it has none.
func verifyClient(cs tls.ConnectionState, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return errNoClientCertificate
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: x509.NewCertPool(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	for _, cert := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
		return &tls.CertificateVerificationError{UnverifiedCertificates: cs.PeerCertificates, Err: err}
	}
	return nil
}

var errNoClientCertificate = errors.New("UNABLE_TO_GET_ISSUER_CERT: unable to get issuer certificate")

func (t *TLS) session(info *v8.FunctionCallbackInfo) *session {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[int64(args[0].Integer())]
}

// getPeerCertificate(netId) returns the peer's certificate chain as JSON, leaf first, or
// undefined if the handshake has not received one.
func (t *TLS) getPeerCertificateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	s := t.session(info)
	if s == nil {
		return ctx.Undefined()
	}
	return encodeChain(ctx, s.peer())
}

// getCertificate(netId) returns this side's certificate as JSON, or undefined.
func (t *TLS) getCertificateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	s := t.session(info)
	if s == nil || len(s.config.Certificates) == 0 || len(s.config.Certificates[0].Certificate) == 0 {
		return ctx.Undefined()
	}
	cert, err := x509.ParseCertificate(s.config.Certificates[0].Certificate[0])
	if err != nil {
		return ctx.Undefined()
	}
	return encodeChain(ctx, []*x509.Certificate{cert})
}

func encodeChain(ctx *v8.Context, chain []*x509.Certificate) *v8.Value {
	if len(chain) == 0 {
		return ctx.Undefined()
	}
	out := make([]map[string]any, len(chain))
	for i, cert := range chain {
		out[i] = describeCertificate(cert)
	}
	return jsJSON(ctx, out)
}

// describeCertificate builds the object Node's getPeerCertificate returns. raw is
// base64 here and becomes a Buffer in JavaScript.
func describeCertificate(cert *x509.Certificate) map[string]any {
	sum1 := sha1.Sum(cert.Raw)
	sum256 := sha256.Sum256(cert.Raw)
	sum512 := sha512.Sum512(cert.Raw)
	const validity = "Jan _2 15:04:05 2006 GMT"
	out := map[string]any{
		"subject":        describeName(cert.Subject),
		"issuer":         describeName(cert.Issuer),
		"ca":             cert.IsCA,
		"valid_from":     cert.NotBefore.UTC().Format(validity),
		"valid_to":       cert.NotAfter.UTC().Format(validity),
		"fingerprint":    fingerprint(sum1[:]),
		"fingerprint256": fingerprint(sum256[:]),
		"fingerprint512": fingerprint(sum512[:]),
		"serialNumber":   strings.ToUpper(hex.EncodeToString(cert.SerialNumber.Bytes())),
		"raw":            base64.StdEncoding.EncodeToString(cert.Raw),
	}
	if names := altNames(cert); len(names) > 0 {
		out["subjectaltname"] = strings.Join(names, ", ")
	}
	return out
}

func altNames(cert *x509.Certificate) []string {
	var names []string
	for _, name := range cert.DNSNames {
		names = append(names, "DNS:"+name)
	}
	for _, ip := range cert.IPAddresses {
		names = append(names, "IP Address:"+ip.String())
	}
	for _, uri := range cert.URIs {
		names = append(names, "URI:"+uri.String())
	}
	for _, email := range cert.EmailAddresses {
		names = append(names, "email:"+email)
	}
	return names
}

// describeName lists a field once as a string, or as an array if it repeats, as Node does.
func describeName(name pkix.Name) map[string]any {
	out := map[string]any{}
	add := func(key string, values []string) {
		switch len(values) {
		case 0:
		case 1:
			out[key] = values[0]
		default:
			out[key] = values
		}
	}
	add("C", name.Country)
	add("ST", name.Province)
	add("L", name.Locality)
	add("O", name.Organization)
	add("OU", name.OrganizationalUnit)
	if name.CommonName != "" {
		out["CN"] = name.CommonName
	}
	return out
}

func fingerprint(sum []byte) string {
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

// opensslCipherNames maps the TLS 1.2 suites Go offers to the names Node reports. TLS 1.3
// suites have the same name in both.
var opensslCipherNames = map[uint16]string{
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:         "ECDHE-RSA-AES128-GCM-SHA256",
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:         "ECDHE-RSA-AES256-GCM-SHA384",
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:       "ECDHE-ECDSA-AES128-GCM-SHA256",
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384:       "ECDHE-ECDSA-AES256-GCM-SHA384",
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256:   "ECDHE-RSA-CHACHA20-POLY1305",
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256: "ECDHE-ECDSA-CHACHA20-POLY1305",
	tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:            "ECDHE-RSA-AES128-SHA",
	tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:            "ECDHE-RSA-AES256-SHA",
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA:          "ECDHE-ECDSA-AES128-SHA",
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA:          "ECDHE-ECDSA-AES256-SHA",
	tls.TLS_RSA_WITH_AES_128_GCM_SHA256:               "AES128-GCM-SHA256",
	tls.TLS_RSA_WITH_AES_256_GCM_SHA384:               "AES256-GCM-SHA384",
}

func protocolName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLSv1"
	case tls.VersionTLS11:
		return "TLSv1.1"
	case tls.VersionTLS12:
		return "TLSv1.2"
	case tls.VersionTLS13:
		return "TLSv1.3"
	}
	return "unknown"
}

func established(s *session) (tls.ConnectionState, bool) {
	if s == nil {
		return tls.ConnectionState{}, false
	}
	state := s.conn.ConnectionState()
	return state, state.HandshakeComplete
}

// getCipher(netId) returns the negotiated cipher as JSON, or undefined.
func (t *TLS) getCipherFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	state, ok := established(t.session(info))
	if !ok {
		return ctx.Undefined()
	}
	standard := tls.CipherSuiteName(state.CipherSuite)
	name := opensslCipherNames[state.CipherSuite]
	if name == "" {
		name = standard
	}
	version := "TLSv1/SSLv3"
	if state.Version == tls.VersionTLS13 {
		version = "TLSv1.3"
	} else if state.Version == tls.VersionTLS12 {
		version = "TLSv1.2"
	}
	return jsJSON(ctx, map[string]string{"name": name, "standardName": standard, "version": version})
}

// getProtocol(netId) returns the negotiated protocol version, or null.
func (t *TLS) getProtocolFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	state, ok := established(t.session(info))
	if !ok {
		return ctx.Null()
	}
	return jsString(ctx, protocolName(state.Version))
}

func (t *TLS) isSessionReusedFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	state, ok := established(t.session(info))
	return ctx.NewBoolean(ok && state.DidResume)
}

// exportKeyingMaterial(netId, length, base64Label, base64Context) returns base64 keying
// material. The label crosses as base64 because a NUL in it is significant.
func (t *TLS) exportKeyingMaterialFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	state, ok := established(t.session(info))
	if !ok || len(args) < 3 {
		return ctx.Throw("TLS socket connection must be securely established")
	}
	var context []byte
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		context, _ = base64.StdEncoding.DecodeString(args[3].String())
	}
	label, err := base64.StdEncoding.DecodeString(args[2].String())
	if err != nil {
		return ctx.Throw(err.Error())
	}
	material, err := state.ExportKeyingMaterial(string(label), context, int(args[1].Integer()))
	if err != nil {
		return ctx.Throw(err.Error())
	}
	return jsString(ctx, base64.StdEncoding.EncodeToString(material))
}
