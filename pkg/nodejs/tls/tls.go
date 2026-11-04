// Package tls implements the Node.js tls module for TLS/SSL networking.
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
	"strings"
	"sync"
	"sync/atomic"

	"proto.zip/studio/orbital/pkg/nodejs/internal/tlsclient"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed tls.js
var tlsJS string

// TLS provides TLS/SSL functionality.
type TLS struct {
	rt       *runtime.Runtime
	sockets  map[int64]*connection
	socketID int64
	mu       sync.Mutex
}

// connection is one tls.connect socket. verifier is nil for a server-side socket.
type connection struct {
	socket   runtime.TCPSocket
	verifier *tlsclient.Verifier
	host     string
	ctx      context.Context
	cancel   context.CancelFunc
}

// connectResult is what the JavaScript connect callback receives.
type connectResult struct {
	Message              string `json:"message,omitempty"`
	Code                 string `json:"code,omitempty"`
	Authorized           bool   `json:"authorized"`
	AuthorizationError   string `json:"authorizationError,omitempty"`
	ScriptChecksIdentity bool   `json:"scriptChecksIdentity"`
}

// New creates a new TLS module.
func New() *TLS {
	return &TLS{
		sockets: make(map[int64]*connection),
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

	// Create internal object with native functions
	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// Register functions
	funcs := map[string]v8.FunctionCallback{
		"createTLSSocket":    t.createTLSSocketFunc,
		"connect":            t.connectFunc,
		"write":              t.writeFunc,
		"read":               t.readFunc,
		"shutdown":           t.shutdownFunc,
		"close":              t.closeFunc,
		"getPeerCertificate": t.getPeerCertificateFunc,
		"getCipher":          t.getCipherFunc,
		"getProtocol":        t.getProtocolFunc,
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
	if _, err := rt.RunScript(tlsJS, "tls.js"); err != nil {
		return err
	}

	return nil
}

func (t *TLS) createTLSSocketFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 3 {
		return ctx.Throw("tls: invalid connect options")
	}

	isServer := args[0].Boolean()
	host := args[1].String()

	var opts tlsclient.Options
	if err := json.Unmarshal([]byte(args[2].String()), &opts); err != nil {
		return ctx.Throw("tls: invalid connect options")
	}

	config := &tls.Config{}
	var verifier *tlsclient.Verifier
	if !isServer {
		var err error
		if verifier, err = tlsclient.New(t.rt.ClientTLSConfig(), host, opts); err != nil {
			// Returned rather than thrown so JavaScript can attach the code.
			msg, code := tlsclient.Describe(err, host, 0)
			encoded, _ := json.Marshal(connectResult{Message: strings.TrimPrefix(msg, code+": "), Code: code})
			return t.jsonString(encoded)
		}
		config = verifier.Config
	}

	// Create underlying TCP socket first
	tcpSocket := t.rt.SocketFactory().CreateTCPSocket()

	// Wrap with TLS
	tlsSocket := t.rt.SocketFactory().CreateTLSSocket(tcpSocket, config, isServer)

	conn := &connection{socket: tlsSocket, verifier: verifier, host: host}
	conn.ctx, conn.cancel = context.WithCancel(t.rt.ExecutionContext())

	t.mu.Lock()
	id := atomic.AddInt64(&t.socketID, 1)
	t.sockets[id] = conn
	t.mu.Unlock()

	return ctx.NewNumber(float64(id))
}

func (t *TLS) connectFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	port := int(args[1].Integer())
	callback := args[2]

	t.mu.Lock()
	conn, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.deliverConnect(callback, connectResult{Message: "Socket not found"})
		return nil
	}

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		err := conn.socket.Connect(conn.ctx, conn.host, port)
		if err == nil && conn.verifier != nil {
			err = conn.verifier.Check()
		}
		result := resultOf(conn, port, err)

		t.rt.EventLoop().EnqueueMicrotask(func() {
			t.deliverConnect(callback, result)
		})
	}()

	return nil
}

func resultOf(conn *connection, port int, err error) connectResult {
	var result connectResult
	if err != nil {
		result.Message, result.Code = tlsclient.Describe(err, conn.host, port)
	}
	if conn.verifier == nil {
		return result
	}
	result.Authorized = conn.verifier.Authorized()
	result.ScriptChecksIdentity = conn.verifier.ScriptChecksIdentity()
	if authErr := conn.verifier.AuthorizationError(); authErr != nil {
		_, result.AuthorizationError = tlsclient.Describe(authErr, conn.host, port)
	}
	return result
}

func (t *TLS) deliverConnect(callback *v8.Value, result connectResult) {
	encoded, err := json.Marshal(result)
	if err != nil {
		encoded = []byte(`{"message":"tls: internal error"}`)
	}
	t.callCallback(callback, "", t.jsonString(encoded))
}

func (t *TLS) jsonString(encoded []byte) *v8.Value {
	val, _ := t.rt.Context().NewString(string(encoded))
	return val
}

// writeFunc is write(id, base64Data, callback). Bytes cross the boundary as base64
// because a JavaScript string cannot carry them unchanged.
func (t *TLS) writeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[2]
	data, err := base64.StdEncoding.DecodeString(args[1].String())
	if err != nil {
		t.callCallback(callback, "tls: invalid write data", nil)
		return nil
	}

	t.mu.Lock()
	conn, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "Socket not found", nil)
		return nil
	}
	socket := conn.socket

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		_, err := socket.Write(data)

		t.rt.EventLoop().EnqueueMicrotask(func() {
			if err != nil {
				t.callCallback(callback, err.Error(), nil)
			} else {
				t.callCallback(callback, "", nil)
			}
		})
	}()

	return nil
}

// readFunc is read(id, callback). The callback receives base64 data, or null at the
// end of the stream.
func (t *TLS) readFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	id := int64(args[0].Integer())
	callback := args[1]

	t.mu.Lock()
	conn, ok := t.sockets[id]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "Socket not found", nil)
		return nil
	}
	socket := conn.socket

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		buf := make([]byte, 65536)
		n, _ := socket.Read(buf)

		// crypto/tls can return data and EOF together when close_notify follows the last
		// record. The data comes first; the next read reports the end again.
		var encoded string
		if n > 0 {
			encoded = base64.StdEncoding.EncodeToString(buf[:n])
		}

		t.rt.EventLoop().EnqueueMicrotask(func() {
			ctx := t.rt.Context()
			if n == 0 {
				t.callCallback(callback, "", ctx.Null())
				return
			}
			dataStr, _ := ctx.NewString(encoded)
			t.callCallback(callback, "", dataStr)
		})
	}()

	return nil
}

// shutdownFunc is shutdown(id, callback). It finishes the sending side and leaves the
// receiving side open. A socket from a factory that cannot half-close is closed
// outright, which still delivers what was written but ends the read side too. The
// peer being gone already is not an error, as in Node.
func (t *TLS) shutdownFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 2 {
		return nil
	}

	callback := args[1]

	t.mu.Lock()
	conn, ok := t.sockets[int64(args[0].Integer())]
	t.mu.Unlock()

	if !ok {
		t.callCallback(callback, "", nil)
		return nil
	}
	socket := conn.socket

	t.rt.EventLoop().AddPendingWork()
	go func() {
		defer t.rt.EventLoop().DonePendingWork()

		if errors.Is(runtime.CloseWrite(socket), errors.ErrUnsupported) {
			socket.Close()
		}

		t.rt.EventLoop().EnqueueMicrotask(func() {
			t.callCallback(callback, "", nil)
		})
	}()

	return nil
}

func (t *TLS) closeFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	id := int64(args[0].Integer())

	t.mu.Lock()
	conn, ok := t.sockets[id]
	if ok {
		delete(t.sockets, id)
	}
	t.mu.Unlock()

	if ok {
		conn.cancel()
		conn.socket.Close()
	}

	return nil
}

// getPeerCertificateFunc returns the peer's certificate as JSON, or undefined if the
// handshake has not received one.
func (t *TLS) getPeerCertificateFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Undefined()
	}

	t.mu.Lock()
	conn, ok := t.sockets[int64(args[0].Integer())]
	t.mu.Unlock()
	if !ok || conn.verifier == nil {
		return ctx.Undefined()
	}
	peer := conn.verifier.PeerCertificates()
	if len(peer) == 0 {
		return ctx.Undefined()
	}
	encoded, err := json.Marshal(describeCertificate(peer[0]))
	if err != nil {
		return ctx.Undefined()
	}
	return t.jsonString(encoded)
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

func (t *TLS) getCipherFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	obj, _ := ctx.NewObject()
	nameStr, _ := ctx.NewString("TLS_AES_256_GCM_SHA384")
	versionStr, _ := ctx.NewString("TLSv1.3")
	obj.Set("name", nameStr)
	obj.Set("standardName", nameStr)
	obj.Set("version", versionStr)
	return obj
}

func (t *TLS) getProtocolFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	val, _ := ctx.NewString("TLSv1.3")
	return val
}

func (t *TLS) callCallback(callback *v8.Value, errMsg string, result *v8.Value) {
	if callback == nil || !callback.IsFunction() {
		return
	}

	ctx := t.rt.Context()
	var errVal *v8.Value
	if errMsg != "" {
		errVal, _ = ctx.NewString(errMsg)
	} else {
		errVal = ctx.Null()
	}

	if result == nil {
		result = ctx.Undefined()
	}

	callback.Call(ctx.Undefined(), errVal, result)
}
