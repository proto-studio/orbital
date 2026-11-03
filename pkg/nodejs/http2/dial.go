package http2

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/runtime"
)

// errNoHTTP2Transport is returned rather than falling back to unrestricted sockets.
var errNoHTTP2Transport = errors.New("ERR_HTTP2_TRANSPORT_UNAVAILABLE: the configured HTTPClient cannot carry HTTP/2 " +
	"and Config.SocketFactory does not restrict connections; set Config.SocketFactory " +
	"(for example runtime.NewFilteredSocketFactory) to define how HTTP/2 connects")

var errNotHTTP2 = errors.New("ERR_HTTP2_ERROR: Protocol error")

var errInvalidHost = errors.New("ERR_INVALID_URL: Invalid URL")

// dialJSON holds the options http2.connect passes from JavaScript.
type dialJSON struct {
	Scheme             string   `json:"scheme"`
	RejectUnauthorized bool     `json:"rejectUnauthorized"`
	CA                 []string `json:"ca"`
	Cert               string   `json:"cert"`
	Key                string   `json:"key"`
	Servername         string   `json:"servername"`
}

type dialOptions struct {
	dialJSON
	host string
	port int
}

// dial connects to the session's destination through the runtime's injected
// controls. It returns the connection to speak HTTP/2 on and the socket beneath it.
func (h *HTTP2) dial(ctx context.Context, opts dialOptions) (net.Conn, net.Conn, error) {
	if err := h.checkTransport(); err != nil {
		return nil, nil, err
	}

	var (
		tlsConfig *tls.Config
		err       error
	)
	if opts.Scheme == "https" {
		// Fail on bad TLS options before touching the network.
		if tlsConfig, err = h.clientTLSConfig(opts); err != nil {
			return nil, nil, err
		}
	}

	raw, err := h.connectSocket(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	if tlsConfig == nil {
		return raw, raw, nil
	}

	conn := tls.Client(raw, tlsConfig)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, nil, err
	}
	if conn.ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		raw.Close()
		return nil, nil, errNotHTTP2
	}
	return conn, raw, nil
}

// checkTransport refuses to open real, unfiltered sockets for an embedder that
// restricted networking only through HTTPClient. HTTPClient exposes whole requests,
// not connections, so it cannot carry HTTP/2. Any injected SocketFactory is honored:
// it is the boundary net.connect already goes through.
func (h *HTTP2) checkTransport() error {
	if _, real := h.rt.SocketFactory().(*runtime.RealSocketFactory); !real {
		return nil
	}
	if _, real := h.rt.HTTPClient().(*runtime.RealHTTPClient); real {
		return nil
	}
	return errNoHTTP2Transport
}

// connectSocket resolves the destination with the injected resolver and connects
// with a socket from the injected factory. The IP handed to the socket is the one
// resolved here, so the factory's decision applies to the actual destination and a
// DNS change cannot move the connection afterwards.
func (h *HTTP2) connectSocket(ctx context.Context, opts dialOptions) (net.Conn, error) {
	ips, isName, err := h.resolve(ctx, opts.host)
	if err != nil {
		return nil, err
	}

	var firstErr error
	for _, ip := range ips {
		socket := h.rt.SocketFactory().CreateTCPSocket()
		var err error
		// Only a name that went through DNS is reported as a hostname. Anything else
		// the guest wrote must not reach a policy as a name it could match on.
		if resolved, ok := socket.(runtime.ResolvedTCPSocket); ok && isName {
			err = resolved.ConnectResolved(ctx, opts.host, ip, opts.port)
		} else {
			err = socket.Connect(ctx, ip, opts.port)
		}
		if err == nil {
			return runtime.NewSocketConn(socket), nil
		}
		socket.Close()
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, firstErr
}

// resolve returns canonical IPs for host, and whether host is a name resolved through
// DNS rather than an address literal. Literals are canonicalized, so an IPv4-mapped
// IPv6 literal is checked as the IPv4 address it reaches. Zones are rejected as in
// Node's URL parser: the zone is free-form text that does not choose the destination.
func (h *HTTP2) resolve(ctx context.Context, host string) ([]string, bool, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, false, errInvalidHost
		}
		return []string{addr.Unmap().String()}, false, nil
	}
	if strings.ContainsAny(host, ":%") {
		return nil, false, errInvalidHost
	}
	ips, err := h.rt.DNSResolver().LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, false, err
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, addr.Unmap().String())
		}
	}
	if len(out) == 0 {
		return nil, false, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return out, true, nil
}

// clientTLSConfig builds the handshake configuration. An embedder configuration is
// authoritative; otherwise Node's connect options apply. Certificates are verified
// unless one of those explicitly says not to.
func (h *HTTP2) clientTLSConfig(opts dialOptions) (*tls.Config, error) {
	serverName := opts.host

	var cfg *tls.Config
	if base := h.rt.HTTP2TLSConfig(); base != nil {
		cfg = base.Clone()
		if cfg.ServerName != "" {
			serverName = cfg.ServerName
		}
	} else {
		cfg = &tls.Config{InsecureSkipVerify: !opts.RejectUnauthorized}
		if opts.Servername != "" {
			serverName = opts.Servername
		}
		if len(opts.CA) > 0 {
			// As in Node, ca replaces the default roots rather than adding to them.
			pool := x509.NewCertPool()
			for _, pem := range opts.CA {
				if !pool.AppendCertsFromPEM([]byte(pem)) {
					return nil, errors.New("ERR_OSSL_PEM_NO_START_LINE: the ca option contains no valid certificates")
				}
			}
			cfg.RootCAs = pool
		}
		if opts.Cert != "" || opts.Key != "" {
			cert, err := tls.X509KeyPair([]byte(opts.Cert), []byte(opts.Key))
			if err != nil {
				return nil, fmt.Errorf("ERR_OSSL_X509_KEY_VALUES_MISMATCH: %w", err)
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
	}
	cfg.ServerName = serverName
	cfg.NextProtos = []string{http2.NextProtoTLS}
	return cfg, nil
}

// closeNotifyConn reports the end of the connection. HTTP/2 reads continuously, so
// a read failure is how a closed connection is first observed.
type closeNotifyConn struct {
	net.Conn
	once    sync.Once
	onClose func(error)
}

func (c *closeNotifyConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.once.Do(func() {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			c.onClose(err)
		})
	}
	return n, err
}

var codePrefix = regexp.MustCompile(`^([A-Z][A-Z0-9_]+): (.*)$`)

// describeError converts a connection failure to the message and code Node reports.
func describeError(err error, opts dialOptions) (string, string) {
	var (
		dnsErr  *net.DNSError
		hostErr x509.HostnameError
		certErr x509.CertificateInvalidError
		verify  *tls.CertificateVerificationError
		errno   syscall.Errno
	)
	switch {
	case errors.As(err, &hostErr):
		return altnameMessage(hostErr), "ERR_TLS_CERT_ALTNAME_INVALID"
	case errors.As(err, &certErr):
		if certErr.Reason == x509.Expired {
			return "certificate has expired", "CERT_HAS_EXPIRED"
		}
		return certErr.Error(), "ERR_TLS_CERT_INVALID"
	case errors.As(err, &verify):
		// Any other verification failure is an untrusted chain. The concrete error
		// differs between Go's verifier and the platform verifiers.
		if len(verify.UnverifiedCertificates) > 0 {
			leaf := verify.UnverifiedCertificates[0]
			if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
				return "self-signed certificate", "DEPTH_ZERO_SELF_SIGNED_CERT"
			}
		}
		return "unable to verify the first certificate", "UNABLE_TO_VERIFY_LEAF_SIGNATURE"
	case errors.As(err, &dnsErr):
		return "getaddrinfo ENOTFOUND " + opts.host, "ENOTFOUND"
	case errors.As(err, &errno):
		code := errnoName(errno)
		return "connect " + code + " " + net.JoinHostPort(opts.host, strconv.Itoa(opts.port)), code
	case errors.Is(err, context.Canceled):
		return "The operation was aborted", "ABORT_ERR"
	case errors.Is(err, context.DeadlineExceeded):
		return "The operation timed out", "ETIMEDOUT"
	}
	if strings.Contains(err.Error(), "tls: no application protocol") {
		return err.Error(), "ERR_SSL_TLSV1_ALERT_NO_APPLICATION_PROTOCOL"
	}
	if m := codePrefix.FindStringSubmatch(err.Error()); m != nil {
		if m[1] == "ERR_HTTP2_ERROR" || m[1] == "ERR_HTTP2_TRANSPORT_UNAVAILABLE" {
			return m[2], m[1]
		}
		return err.Error(), m[1]
	}
	return err.Error(), ""
}

// altnameMessage matches the text of Node's checkServerIdentity.
func altnameMessage(err x509.HostnameError) string {
	names := make([]string, 0, len(err.Certificate.DNSNames)+len(err.Certificate.IPAddresses))
	for _, name := range err.Certificate.DNSNames {
		names = append(names, "DNS:"+name)
	}
	for _, ip := range err.Certificate.IPAddresses {
		names = append(names, "IP Address:"+ip.String())
	}
	altnames := strings.Join(names, ", ")
	if net.ParseIP(err.Host) != nil {
		return "Hostname/IP does not match certificate's altnames: IP: " + err.Host + " is not in the cert's list: " + altnames
	}
	return "Hostname/IP does not match certificate's altnames: Host: " + err.Host + ". is not in the cert's altnames: " + altnames
}

func errnoName(errno syscall.Errno) string {
	switch errno {
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ENETUNREACH:
		return "ENETUNREACH"
	case syscall.EADDRNOTAVAIL:
		return "EADDRNOTAVAIL"
	case syscall.EPIPE:
		return "EPIPE"
	}
	return errno.Error()
}

// describeStreamError converts a stream failure to a message, code and RST code.
func describeStreamError(err error) (string, string, int) {
	var streamErr http2.StreamError
	if errors.As(err, &streamErr) {
		return "", "", int(streamErr.Code)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "", "", int(http2.ErrCodeCancel)
	case errors.Is(err, context.DeadlineExceeded):
		return "The operation timed out", "ETIMEDOUT", int(http2.ErrCodeInternal)
	}
	return err.Error(), "ERR_HTTP2_ERROR", int(http2.ErrCodeInternal)
}
