package http2

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/nodejs/internal/tlsclient"
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
	Scheme string `json:"scheme"`
	tlsclient.Options
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
		verifier *tlsclient.Verifier
		err      error
	)
	if opts.Scheme == "https" {
		// Fail on bad TLS options before touching the network.
		if verifier, err = tlsclient.New(h.rt.ClientTLSConfig(), opts.host, opts.Options); err != nil {
			return nil, nil, err
		}
		verifier.Config.NextProtos = []string{http2.NextProtoTLS}
	}

	raw, err := h.connectSocket(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	if verifier == nil {
		return raw, raw, nil
	}

	conn := tls.Client(raw, verifier.Config)
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

// describeError converts a connection failure to the message and code Node reports.
func describeError(err error, opts dialOptions) (string, string) {
	if strings.Contains(err.Error(), "tls: no application protocol") {
		return err.Error(), "ERR_SSL_TLSV1_ALERT_NO_APPLICATION_PROTOCOL"
	}
	msg, code := tlsclient.Describe(err, opts.host, opts.port)
	if code == "ERR_HTTP2_ERROR" || code == "ERR_HTTP2_TRANSPORT_UNAVAILABLE" {
		msg = strings.TrimPrefix(msg, code+": ")
	}
	return msg, code
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
