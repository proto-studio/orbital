package http2

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/nodejs/internal/tlsclient"
	"proto.zip/studio/orbital/pkg/runtime"
)

// errNoHTTP2Transport is returned rather than falling back to unrestricted sockets.
var errNoHTTP2Transport = errors.New("ERR_HTTP2_TRANSPORT_UNAVAILABLE: the configured HTTPClient cannot carry HTTP/2 " +
	"and Config.SocketFactory does not restrict connections; set Config.SocketFactory " +
	"(for example runtime.NewFilteredSocketFactory) to define how HTTP/2 connects")

var errNotHTTP2 = errors.New("ERR_HTTP2_ERROR: Protocol error")

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

// connectSocket connects through the runtime's injected resolver and socket factory.
func (h *HTTP2) connectSocket(ctx context.Context, opts dialOptions) (net.Conn, error) {
	result, err := netdial.Connect(ctx, h.rt, opts.host, opts.port, netdial.Options{})
	if err != nil {
		return nil, err
	}
	return runtime.NewSocketConn(result.Socket), nil
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
