package http2

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/nodejs/internal/tlsclient"
	"proto.zip/studio/orbital/pkg/runtime"
	"syscall"
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
// it is the boundary net.connect already goes through. With real sockets, the client
// restricts nothing when it is the real one or the default left by a nil HTTPClient.
func (h *HTTP2) checkTransport() error {
	if _, real := h.rt.SocketFactory().(*runtime.RealSocketFactory); !real {
		return nil
	}
	if h.rt.HTTPOverSockets() {
		return nil
	}
	return errNoHTTP2Transport
}

// connectSocket connects through the runtime's injected resolver and socket factory.
// The connection counts against Config.MaxOpenSockets until it closes.
func (h *HTTP2) connectSocket(ctx context.Context, opts dialOptions) (net.Conn, error) {
	release, err := h.rt.OpenSocket()
	if err != nil {
		return nil, err
	}
	result, err := netdial.Connect(ctx, h.rt, opts.host, opts.port, netdial.Options{})
	if err != nil {
		release()
		return nil, err
	}
	return &slotConn{Conn: runtime.NewSocketConn(result.Socket), release: release}, nil
}

// slotConn gives back its connection's place under Config.MaxOpenSockets when it closes.
type slotConn struct {
	net.Conn
	release func()
}

func (c *slotConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
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
	if errors.Is(err, syscall.EMFILE) {
		// No socket left to open (Config.MaxOpenSockets): Node's error for a connection
		// it couldn't open. http2.js adds the errno, syscall, address and port.
		return fmt.Sprintf("connect EMFILE %s:%d - Local (undefined:undefined)", opts.host, opts.port), "EMFILE"
	}
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
