// Package network - Socket interfaces for TCP/UDP operations.
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	// ErrConnectionRefused is returned when connection is refused
	ErrConnectionRefused = errors.New("ECONNREFUSED: connection refused")
	// ErrSocketClosed is returned when operating on a closed socket
	ErrSocketClosed = errors.New("ENOTCONN: socket is closed")
	// ErrAddressInUse is returned when address is already in use
	ErrAddressInUse = errors.New("EADDRINUSE: address already in use")
	// ErrNetworkUnreachable is returned in sandbox mode
	ErrNetworkUnreachable = errors.New("ENETUNREACH: network unreachable (sandboxed)")
)

// TCPSocket represents a TCP connection interface.
type TCPSocket interface {
	// Connect establishes a connection to the given address.
	Connect(ctx context.Context, address string, port int) error

	// Write sends data on the socket.
	Write(data []byte) (int, error)

	// Read reads data from the socket.
	Read(buf []byte) (int, error)

	// Close closes the socket.
	Close() error

	// SetTimeout sets read/write timeout.
	SetTimeout(d time.Duration) error

	// SetKeepAlive enables/disables keep-alive.
	SetKeepAlive(enable bool) error

	// SetNoDelay enables/disables Nagle's algorithm.
	SetNoDelay(enable bool) error

	// LocalAddr returns the local address.
	LocalAddr() net.Addr

	// RemoteAddr returns the remote address.
	RemoteAddr() net.Addr

	// Ref marks the socket as referenced (keeps event loop alive).
	Ref()

	// Unref marks the socket as unreferenced.
	Unref()
}

// ResolvedTCPSocket is an optional extension of TCPSocket for sockets that make
// access decisions by hostname. Callers that resolve DNS themselves (the http2
// module, through Config.DNSResolver) use it to connect to a pinned IP while
// still reporting the name the guest asked for. A socket without this method
// receives Connect(ctx, ip, port) and so only ever sees the resolved IP.
type ResolvedTCPSocket interface {
	TCPSocket

	// ConnectResolved connects to ip, which the caller resolved from hostname.
	// Implementations must connect to ip and must not resolve hostname again.
	ConnectResolved(ctx context.Context, hostname, ip string, port int) error
}

// HalfClosableTCPSocket is an optional extension of TCPSocket for sockets that can
// finish sending while still receiving, as Node's socket.end needs.
type HalfClosableTCPSocket interface {
	TCPSocket

	// CloseWrite signals the end of the outgoing data. Read keeps working until the
	// peer finishes too. A wrapper whose underlying socket cannot do this returns
	// errors.ErrUnsupported.
	CloseWrite() error
}

// CloseWrite half-closes socket, or returns errors.ErrUnsupported if it cannot.
func CloseWrite(socket TCPSocket) error {
	if halfClosable, ok := socket.(HalfClosableTCPSocket); ok {
		return halfClosable.CloseWrite()
	}
	return errors.ErrUnsupported
}

// NewSocketConn adapts a connected TCPSocket to a net.Conn so protocol stacks
// such as crypto/tls can run on top of an injected SocketFactory.
//
// Deadlines reach the socket if it implements DeadlineSetter and are otherwise no-ops,
// in which case callers interrupt blocked I/O by closing the connection.
func NewSocketConn(socket TCPSocket) net.Conn {
	return &socketConn{socket: socket}
}

type socketConn struct {
	socket    TCPSocket
	closeOnce sync.Once
	closeErr  error
}

func (c *socketConn) Read(b []byte) (int, error)  { return c.socket.Read(b) }
func (c *socketConn) Write(b []byte) (int, error) { return c.socket.Write(b) }
func (c *socketConn) LocalAddr() net.Addr         { return socketAddr(c.socket.LocalAddr()) }
func (c *socketConn) RemoteAddr() net.Addr        { return socketAddr(c.socket.RemoteAddr()) }

func (c *socketConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.socket.Close() })
	return c.closeErr
}

func (c *socketConn) SetDeadline(t time.Time) error {
	if d, ok := c.socket.(DeadlineSetter); ok {
		return d.SetDeadline(t)
	}
	return nil
}

func (c *socketConn) SetReadDeadline(t time.Time) error {
	if d, ok := c.socket.(DeadlineSetter); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}

func (c *socketConn) SetWriteDeadline(t time.Time) error {
	if d, ok := c.socket.(DeadlineSetter); ok {
		return d.SetWriteDeadline(t)
	}
	return nil
}

// socketAddr substitutes a placeholder for sockets that report no address, since
// net.Conn callers do not expect a nil Addr.
func socketAddr(addr net.Addr) net.Addr {
	if addr != nil {
		return addr
	}
	return &net.TCPAddr{}
}

// TCPServer represents a TCP server interface.
type TCPServer interface {
	// Listen starts listening on the given address and port.
	Listen(ctx context.Context, address string, port int) error

	// Accept accepts a new connection.
	Accept(ctx context.Context) (TCPSocket, error)

	// Close stops the server.
	Close() error

	// Addr returns the server's address.
	Addr() net.Addr

	// Ref marks the server as referenced.
	Ref()

	// Unref marks the server as unreferenced.
	Unref()
}

// UDPSocket represents a UDP socket interface.
type UDPSocket interface {
	// Bind binds the socket to an address and port.
	Bind(ctx context.Context, address string, port int) error

	// Send sends data to the given address.
	Send(data []byte, address string, port int) (int, error)

	// Receive receives data from the socket.
	Receive(buf []byte) (int, string, int, error)

	// Close closes the socket.
	Close() error

	// SetBroadcast enables/disables broadcast.
	SetBroadcast(enable bool) error

	// SetMulticastTTL sets multicast TTL.
	SetMulticastTTL(ttl int) error

	// AddMembership joins a multicast group.
	AddMembership(multicastAddr string, interfaceAddr string) error

	// DropMembership leaves a multicast group.
	DropMembership(multicastAddr string, interfaceAddr string) error

	// LocalAddr returns the local address.
	LocalAddr() net.Addr

	// Ref marks the socket as referenced.
	Ref()

	// Unref marks the socket as unreferenced.
	Unref()
}

// SocketFactory creates socket instances.
type SocketFactory interface {
	// CreateTCPSocket creates a new TCP socket.
	CreateTCPSocket() TCPSocket

	// CreateTCPServer creates a new TCP server.
	CreateTCPServer() TCPServer

	// CreateUDPSocket creates a new UDP socket.
	CreateUDPSocket(socketType string) UDPSocket

	// CreateTLSSocket wraps a TCP socket with TLS. The Node modules do not call this: they
	// run TLS themselves on a socket from CreateTCPSocket, so certificate verification
	// does not depend on the factory.
	CreateTLSSocket(socket TCPSocket, config *tls.Config, isServer bool) TCPSocket
}

// RealSocketFactory creates real network sockets.
type RealSocketFactory struct{}

// NewRealSocketFactory creates a factory for real sockets.
func NewRealSocketFactory() *RealSocketFactory {
	return &RealSocketFactory{}
}

// CreateTCPSocket creates a new real TCP socket.
func (f *RealSocketFactory) CreateTCPSocket() TCPSocket {
	return &RealTCPSocket{}
}

// CreateTCPServer creates a new real TCP server.
func (f *RealSocketFactory) CreateTCPServer() TCPServer {
	return &RealTCPServer{}
}

// CreateUDPSocket creates a new real UDP socket with the specified type (udp, udp4, or udp6).
func (f *RealSocketFactory) CreateUDPSocket(socketType string) UDPSocket {
	return &RealUDPSocket{socketType: socketType}
}

// CreateTLSSocket wraps an existing TCP socket with TLS encryption.
func (f *RealSocketFactory) CreateTLSSocket(socket TCPSocket, config *tls.Config, isServer bool) TCPSocket {
	return &RealTLSSocket{
		underlying: socket,
		config:     config,
		isServer:   isServer,
	}
}

// RealTCPSocket implements TCPSocket using real network.
type RealTCPSocket struct {
	conn      net.Conn
	mu        sync.Mutex
	timeout   time.Duration
	ref       bool
	network   string // "" means TCP
	localAddr net.Addr
}

// Connect establishes a TCP connection to the specified address and port.
func (s *RealTCPSocket) Connect(ctx context.Context, address string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dialer := net.Dialer{Timeout: 30 * time.Second}
	var (
		conn net.Conn
		err  error
	)
	if s.network == "unix" {
		conn, err = dialer.DialContext(ctx, "unix", address)
	} else {
		if s.localAddr != nil {
			dialer.LocalAddr = s.localAddr
		}
		conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
	}
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}

// Write sends data on the TCP connection.
func (s *RealTCPSocket) Write(data []byte) (int, error) {
	s.mu.Lock()
	conn := s.conn
	timeout := s.timeout
	s.mu.Unlock()

	if conn == nil {
		return 0, ErrSocketClosed
	}
	if timeout > 0 {
		conn.SetWriteDeadline(time.Now().Add(timeout))
	}
	// The lock is not held across the write: a peer that stops reading must not
	// prevent Close from interrupting it.
	return conn.Write(data)
}

// Read receives data from the TCP connection into the buffer.
func (s *RealTCPSocket) Read(buf []byte) (int, error) {
	s.mu.Lock()
	conn := s.conn
	timeout := s.timeout
	s.mu.Unlock()

	if conn == nil {
		return 0, ErrSocketClosed
	}
	if timeout > 0 {
		conn.SetReadDeadline(time.Now().Add(timeout))
	}
	return conn.Read(buf)
}

// Close closes the TCP connection.
func (s *RealTCPSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// CloseWrite shuts down the sending side of the TCP connection.
func (s *RealTCPSocket) CloseWrite() error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return ErrSocketClosed
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return errors.ErrUnsupported
}

// SetTimeout sets the read/write timeout for the connection.
func (s *RealTCPSocket) SetTimeout(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timeout = d
	return nil
}

// SetKeepAlive enables or disables TCP keep-alive.
func (s *RealTCPSocket) SetKeepAlive(enable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return ErrSocketClosed
	}
	if tcpConn, ok := s.conn.(*net.TCPConn); ok {
		return tcpConn.SetKeepAlive(enable)
	}
	return nil
}

// SetNoDelay enables or disables Nagle's algorithm.
func (s *RealTCPSocket) SetNoDelay(enable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return ErrSocketClosed
	}
	if tcpConn, ok := s.conn.(*net.TCPConn); ok {
		return tcpConn.SetNoDelay(enable)
	}
	return nil
}

// LocalAddr returns the local network address.
func (s *RealTCPSocket) LocalAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (s *RealTCPSocket) RemoteAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.RemoteAddr()
}

// Ref marks the socket as referenced for the event loop.
func (s *RealTCPSocket) Ref() { s.ref = true }

// Unref marks the socket as unreferenced for the event loop.
func (s *RealTCPSocket) Unref() { s.ref = false }

// RealTCPServer implements TCPServer using real network.
type RealTCPServer struct {
	listener net.Listener
	mu       sync.Mutex
	ref      bool
	network  string // "" means TCP
}

// Listen starts the server listening on the specified address and port.
func (s *RealTCPServer) Listen(ctx context.Context, address string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		listener net.Listener
		err      error
	)
	if s.network == "unix" {
		listener, err = net.Listen("unix", address)
	} else {
		// "tcp" would make 0.0.0.0 a dual-stack wildcard, which also takes IPv6 traffic and
		// misses a conflict with another listener. "::" stays dual-stack, as in Node.
		network := "tcp"
		if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
			network = "tcp4"
		}
		listener, err = net.Listen(network, net.JoinHostPort(address, strconv.Itoa(port)))
	}
	if err != nil {
		return err
	}
	s.listener = listener
	return nil
}

// Accept waits for and accepts a new TCP connection.
func (s *RealTCPServer) Accept(ctx context.Context) (TCPSocket, error) {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()

	if listener == nil {
		return nil, ErrSocketClosed
	}

	conn, err := listener.Accept()
	if err != nil {
		return nil, err
	}

	return &RealTCPSocket{conn: conn}, nil
}

// Close stops the server and closes the listener.
func (s *RealTCPServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.listener = nil
	return err
}

// Addr returns the server's network address.
func (s *RealTCPServer) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Ref marks the server as referenced for the event loop.
func (s *RealTCPServer) Ref() { s.ref = true }

// Unref marks the server as unreferenced for the event loop.
func (s *RealTCPServer) Unref() { s.ref = false }

// RealTLSSocket wraps a TCP socket with TLS.
type RealTLSSocket struct {
	underlying TCPSocket
	tlsConn    *tls.Conn
	config     *tls.Config
	isServer   bool
	closed     bool
	mu         sync.Mutex
}

// Connect establishes a TLS connection by first connecting the underlying socket then
// performing the TLS handshake. The lock is not held across the handshake, so a peer
// that never answers cannot prevent Close from interrupting it.
func (s *RealTLSSocket) Connect(ctx context.Context, address string, port int) error {
	if err := s.underlying.Connect(ctx, address, port); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.underlying.Close()
		return ErrSocketClosed
	}

	// A real socket's connection keeps the deadlines tls.Conn sets when closing.
	conn := NewSocketConn(s.underlying)
	if realSocket, ok := s.underlying.(*RealTCPSocket); ok {
		realSocket.mu.Lock()
		conn = realSocket.conn
		realSocket.mu.Unlock()
	}

	if s.isServer {
		s.tlsConn = tls.Server(conn, s.config)
	} else {
		s.tlsConn = tls.Client(conn, s.config)
	}
	tlsConn := s.tlsConn
	s.mu.Unlock()

	return tlsConn.HandshakeContext(ctx)
}

// Write sends encrypted data over the TLS connection.
func (s *RealTLSSocket) Write(data []byte) (int, error) {
	s.mu.Lock()
	tlsConn := s.tlsConn
	s.mu.Unlock()

	if tlsConn == nil {
		return 0, ErrSocketClosed
	}
	return tlsConn.Write(data)
}

// Read receives and decrypts data from the TLS connection.
func (s *RealTLSSocket) Read(buf []byte) (int, error) {
	s.mu.Lock()
	tlsConn := s.tlsConn
	s.mu.Unlock()

	if tlsConn == nil {
		return 0, ErrSocketClosed
	}
	return tlsConn.Read(buf)
}

// Close closes the TLS connection and the underlying TCP socket.
func (s *RealTLSSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	if s.tlsConn != nil {
		s.tlsConn.Close()
		s.tlsConn = nil
	}
	return s.underlying.Close()
}

// CloseWrite sends close_notify and then, where the underlying socket supports it, a
// TCP FIN. The close_notify alone tells a TLS peer the data is complete, so an
// underlying socket that cannot half-close is not an error.
func (s *RealTLSSocket) CloseWrite() error {
	s.mu.Lock()
	tlsConn := s.tlsConn
	s.mu.Unlock()

	if tlsConn == nil {
		return ErrSocketClosed
	}
	if err := tlsConn.CloseWrite(); err != nil {
		return err
	}
	if err := CloseWrite(s.underlying); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return err
	}
	return nil
}

// SetTimeout delegates to the underlying socket.
func (s *RealTLSSocket) SetTimeout(d time.Duration) error { return s.underlying.SetTimeout(d) }

// SetKeepAlive delegates to the underlying socket.
func (s *RealTLSSocket) SetKeepAlive(enable bool) error { return s.underlying.SetKeepAlive(enable) }

// SetNoDelay delegates to the underlying socket.
func (s *RealTLSSocket) SetNoDelay(enable bool) error { return s.underlying.SetNoDelay(enable) }

// LocalAddr delegates to the underlying socket.
func (s *RealTLSSocket) LocalAddr() net.Addr { return s.underlying.LocalAddr() }

// RemoteAddr delegates to the underlying socket.
func (s *RealTLSSocket) RemoteAddr() net.Addr { return s.underlying.RemoteAddr() }

// Ref delegates to the underlying socket.
func (s *RealTLSSocket) Ref() { s.underlying.Ref() }

// Unref delegates to the underlying socket.
func (s *RealTLSSocket) Unref() { s.underlying.Unref() }

// RealUDPSocket implements UDPSocket using real network.
type RealUDPSocket struct {
	conn       *net.UDPConn
	socketType string
	mu         sync.Mutex
	ref        bool
}

// Bind binds the UDP socket to the specified address and port.
func (s *RealUDPSocket) Bind(ctx context.Context, address string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindLocked(address, port)
}

func (s *RealUDPSocket) bindLocked(address string, port int) error {
	network := "udp"
	if s.socketType == "udp4" {
		network = "udp4"
	} else if s.socketType == "udp6" {
		network = "udp6"
	}

	addr := net.JoinHostPort(address, strconv.Itoa(port))
	udpAddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP(network, udpAddr)
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}

// Send sends a UDP datagram to the specified address and port.
func (s *RealUDPSocket) Send(data []byte, address string, port int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// An unbound socket binds an ephemeral source port on first send, as Node does.
	if s.conn == nil {
		if err := s.bindLocked(s.wildcardAddress(), 0); err != nil {
			return 0, err
		}
	}

	addr := net.JoinHostPort(address, strconv.Itoa(port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, err
	}

	return s.conn.WriteToUDP(data, udpAddr)
}

func (s *RealUDPSocket) wildcardAddress() string {
	if s.socketType == "udp6" {
		return "::"
	}
	return "0.0.0.0"
}

// Receive receives a UDP datagram, returning bytes read, sender address, port, and error.
func (s *RealUDPSocket) Receive(buf []byte) (int, string, int, error) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return 0, "", 0, ErrSocketClosed
	}

	n, addr, err := conn.ReadFromUDP(buf)
	if err != nil {
		return 0, "", 0, err
	}

	return n, addr.IP.String(), addr.Port, nil
}

// Close closes the UDP socket.
func (s *RealUDPSocket) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// SetBroadcast enables or disables broadcast mode.
func (s *RealUDPSocket) SetBroadcast(enable bool) error {
	// Go's UDP doesn't have a direct SetBroadcast, but broadcast works by default
	return nil
}

// SetMulticastTTL sets the TTL for multicast packets (not fully implemented).
func (s *RealUDPSocket) SetMulticastTTL(ttl int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return ErrSocketClosed
	}
	// Would need syscall for this
	return nil
}

// AddMembership joins a multicast group (simplified - requires platform syscalls for full support).
func (s *RealUDPSocket) AddMembership(multicastAddr string, interfaceAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return ErrSocketClosed
	}

	// Multicast group membership requires platform-specific syscalls
	// This is a simplified implementation - full support would need
	// golang.org/x/net/ipv4 or ipv6 packages
	return nil
}

// DropMembership leaves a multicast group (simplified - requires platform syscalls for full support).
func (s *RealUDPSocket) DropMembership(multicastAddr string, interfaceAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return ErrSocketClosed
	}

	// Multicast group membership requires platform-specific syscalls
	return nil
}

// LocalAddr returns the local network address.
func (s *RealUDPSocket) LocalAddr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

// Ref marks the socket as referenced for the event loop.
func (s *RealUDPSocket) Ref() { s.ref = true }

// Unref marks the socket as unreferenced for the event loop.
func (s *RealUDPSocket) Unref() { s.ref = false }

// NoOpSocketFactory creates sandboxed sockets that block all operations.
type NoOpSocketFactory struct{}

// NewNoOpSocketFactory creates a factory that blocks all socket operations.
func NewNoOpSocketFactory() *NoOpSocketFactory {
	return &NoOpSocketFactory{}
}

// CreateTCPSocket creates a no-op TCP socket that blocks all operations.
func (f *NoOpSocketFactory) CreateTCPSocket() TCPSocket {
	return &NoOpTCPSocket{}
}

// CreateTCPServer creates a no-op TCP server that blocks all operations.
func (f *NoOpSocketFactory) CreateTCPServer() TCPServer {
	return &NoOpTCPServer{}
}

// CreateUDPSocket creates a no-op UDP socket that blocks all operations.
func (f *NoOpSocketFactory) CreateUDPSocket(socketType string) UDPSocket {
	return &NoOpUDPSocket{}
}

// CreateTLSSocket creates a no-op TLS socket that blocks all operations.
func (f *NoOpSocketFactory) CreateTLSSocket(socket TCPSocket, config *tls.Config, isServer bool) TCPSocket {
	return &NoOpTCPSocket{}
}

// NoOpTCPSocket blocks all TCP operations.
type NoOpTCPSocket struct{}

// Connect always returns ErrNetworkUnreachable.
func (s *NoOpTCPSocket) Connect(ctx context.Context, address string, port int) error {
	return ErrNetworkUnreachable
}

// Write always returns ErrNetworkUnreachable.
func (s *NoOpTCPSocket) Write(data []byte) (int, error) { return 0, ErrNetworkUnreachable }

// Read always returns ErrNetworkUnreachable.
func (s *NoOpTCPSocket) Read(buf []byte) (int, error) { return 0, ErrNetworkUnreachable }

// Close is a no-op.
func (s *NoOpTCPSocket) Close() error { return nil }

// SetTimeout is a no-op.
func (s *NoOpTCPSocket) SetTimeout(d time.Duration) error { return nil }

// SetKeepAlive is a no-op.
func (s *NoOpTCPSocket) SetKeepAlive(enable bool) error { return nil }

// SetNoDelay is a no-op.
func (s *NoOpTCPSocket) SetNoDelay(enable bool) error { return nil }

// LocalAddr always returns nil.
func (s *NoOpTCPSocket) LocalAddr() net.Addr { return nil }

// RemoteAddr always returns nil.
func (s *NoOpTCPSocket) RemoteAddr() net.Addr { return nil }

// Ref is a no-op.
func (s *NoOpTCPSocket) Ref() {}

// Unref is a no-op.
func (s *NoOpTCPSocket) Unref() {}

// NoOpTCPServer blocks all TCP server operations.
type NoOpTCPServer struct{}

// Listen always returns ErrNetworkUnreachable.
func (s *NoOpTCPServer) Listen(ctx context.Context, address string, port int) error {
	return ErrNetworkUnreachable
}

// Accept always returns ErrNetworkUnreachable.
func (s *NoOpTCPServer) Accept(ctx context.Context) (TCPSocket, error) {
	return nil, ErrNetworkUnreachable
}

// Close is a no-op.
func (s *NoOpTCPServer) Close() error { return nil }

// Addr always returns nil.
func (s *NoOpTCPServer) Addr() net.Addr { return nil }

// Ref is a no-op.
func (s *NoOpTCPServer) Ref() {}

// Unref is a no-op.
func (s *NoOpTCPServer) Unref() {}

// NoOpUDPSocket blocks all UDP operations.
type NoOpUDPSocket struct{}

// Bind always returns ErrNetworkUnreachable.
func (s *NoOpUDPSocket) Bind(ctx context.Context, address string, port int) error {
	return ErrNetworkUnreachable
}

// Send always returns ErrNetworkUnreachable.
func (s *NoOpUDPSocket) Send(data []byte, address string, port int) (int, error) {
	return 0, ErrNetworkUnreachable
}

// Receive always returns ErrNetworkUnreachable.
func (s *NoOpUDPSocket) Receive(buf []byte) (int, string, int, error) {
	return 0, "", 0, ErrNetworkUnreachable
}

// Close is a no-op.
func (s *NoOpUDPSocket) Close() error { return nil }

// SetBroadcast is a no-op.
func (s *NoOpUDPSocket) SetBroadcast(enable bool) error { return nil }

// SetMulticastTTL is a no-op.
func (s *NoOpUDPSocket) SetMulticastTTL(ttl int) error { return nil }

// AddMembership always returns ErrNetworkUnreachable.
func (s *NoOpUDPSocket) AddMembership(multicastAddr string, interfaceAddr string) error {
	return ErrNetworkUnreachable
}

// DropMembership always returns ErrNetworkUnreachable.
func (s *NoOpUDPSocket) DropMembership(multicastAddr string, interfaceAddr string) error {
	return ErrNetworkUnreachable
}

// LocalAddr always returns nil.
func (s *NoOpUDPSocket) LocalAddr() net.Addr { return nil }

// Ref is a no-op.
func (s *NoOpUDPSocket) Ref() {}

// Unref is a no-op.
func (s *NoOpUDPSocket) Unref() {}

// DeadlineSetter is an optional extension of TCPSocket for sockets whose blocked reads
// and writes can be interrupted by a deadline. crypto/tls relies on deadlines to bound
// the close_notify it sends when closing.
type DeadlineSetter interface {
	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// SetDeadline sets the read and write deadlines of the connection.
func (s *RealTCPSocket) SetDeadline(t time.Time) error {
	if conn := s.currentConn(); conn != nil {
		return conn.SetDeadline(t)
	}
	return ErrSocketClosed
}

// SetReadDeadline sets the read deadline of the connection.
func (s *RealTCPSocket) SetReadDeadline(t time.Time) error {
	if conn := s.currentConn(); conn != nil {
		return conn.SetReadDeadline(t)
	}
	return ErrSocketClosed
}

// SetWriteDeadline sets the write deadline of the connection.
func (s *RealTCPSocket) SetWriteDeadline(t time.Time) error {
	if conn := s.currentConn(); conn != nil {
		return conn.SetWriteDeadline(t)
	}
	return ErrSocketClosed
}

func (s *RealTCPSocket) currentConn() net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// LocalBindableTCPSocket is an optional extension of TCPSocket for sockets that can
// choose their local address before connecting, as Node's localAddress and localPort
// options need.
type LocalBindableTCPSocket interface {
	TCPSocket

	// SetLocalAddr sets the address to connect from.
	SetLocalAddr(ip string, port int)
}

// SetLocalAddr sets the address to connect from.
func (s *RealTCPSocket) SetLocalAddr(ip string, port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localAddr = &net.TCPAddr{IP: net.ParseIP(ip), Port: port}
}

// LingerSetter is an optional extension of TCPSocket for sockets that can discard
// unsent data on close, as Node's resetAndDestroy needs.
type LingerSetter interface {
	SetLinger(seconds int) error
}

// SetLinger sets SO_LINGER. Zero makes Close send a reset.
func (s *RealTCPSocket) SetLinger(seconds int) error {
	if tcpConn, ok := s.currentConn().(*net.TCPConn); ok {
		return tcpConn.SetLinger(seconds)
	}
	return errors.ErrUnsupported
}

// UnixSocketFactory is an optional extension of SocketFactory for Unix domain sockets.
// A unix socket's address is a filesystem path and it can reach local services a network
// policy knows nothing about, so a factory must opt in by implementing this itself.
type UnixSocketFactory interface {
	// CreateUnixSocket creates a socket whose Connect takes a path and ignores the port.
	CreateUnixSocket() TCPSocket
	// CreateUnixServer creates a server whose Listen takes a path and ignores the port.
	CreateUnixServer() TCPServer
}

type realUnixSockets struct{}

func (realUnixSockets) CreateUnixSocket() TCPSocket { return &RealTCPSocket{network: "unix"} }
func (realUnixSockets) CreateUnixServer() TCPServer { return &RealTCPServer{network: "unix"} }

// UnixSocketsFor returns how factory opens Unix domain sockets, or nil if it does not.
// RealSocketFactory offers them only as itself: a type that embeds it to restrict TCP must
// not inherit a way around those restrictions.
func UnixSocketsFor(factory SocketFactory) UnixSocketFactory {
	if _, real := factory.(*RealSocketFactory); real {
		return realUnixSockets{}
	}
	if unix, ok := factory.(UnixSocketFactory); ok {
		return unix
	}
	return nil
}

// ResolvedUDPSocket is an optional extension of UDPSocket for sockets whose policy decides
// by the requested name as well as the address the name resolved to.
type ResolvedUDPSocket interface {
	UDPSocket

	// SendResolved sends to ip, which hostname was resolved to.
	SendResolved(data []byte, hostname, ip string, port int) (int, error)
}

// UDPSocketOptions is an optional extension of UDPSocket for the socket options Node's dgram
// exposes.
type UDPSocketOptions interface {
	SetTTL(ttl int) error
	SetMulticastLoopback(enable bool) error
	SetMulticastInterface(address string) error
	JoinSourceGroup(source, group, iface string) error
	LeaveSourceGroup(source, group, iface string) error
	SetReadBuffer(size int) error
	SetWriteBuffer(size int) error
}

func (s *RealUDPSocket) udpConn() (*net.UDPConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, ErrSocketClosed
	}
	return s.conn, nil
}

// SetTTL sets the unicast TTL or hop limit.
func (s *RealUDPSocket) SetTTL(ttl int) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	if s.isIPv6Socket(conn) {
		return ipv6.NewPacketConn(conn).SetHopLimit(ttl)
	}
	return ipv4.NewPacketConn(conn).SetTTL(ttl)
}

// SetMulticastLoopback sets whether multicast packets return to the local host.
func (s *RealUDPSocket) SetMulticastLoopback(enable bool) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	if s.isIPv6Socket(conn) {
		return ipv6.NewPacketConn(conn).SetMulticastLoopback(enable)
	}
	return ipv4.NewPacketConn(conn).SetMulticastLoopback(enable)
}

// SetMulticastInterface chooses the interface multicast packets leave from.
func (s *RealUDPSocket) SetMulticastInterface(address string) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	iface, err := interfaceForAddress(address)
	if err != nil {
		return err
	}
	if s.isIPv6Socket(conn) {
		return ipv6.NewPacketConn(conn).SetMulticastInterface(iface)
	}
	return ipv4.NewPacketConn(conn).SetMulticastInterface(iface)
}

// JoinSourceGroup joins a source-specific multicast group.
func (s *RealUDPSocket) JoinSourceGroup(source, group, ifaceAddr string) error {
	return s.sourceGroup(source, group, ifaceAddr, true)
}

// LeaveSourceGroup leaves a source-specific multicast group.
func (s *RealUDPSocket) LeaveSourceGroup(source, group, ifaceAddr string) error {
	return s.sourceGroup(source, group, ifaceAddr, false)
}

func (s *RealUDPSocket) sourceGroup(source, group, ifaceAddr string, join bool) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	iface, err := interfaceForAddress(ifaceAddr)
	if err != nil {
		return err
	}
	groupAddr := &net.UDPAddr{IP: net.ParseIP(group)}
	sourceAddr := &net.UDPAddr{IP: net.ParseIP(source)}
	if groupAddr.IP == nil || sourceAddr.IP == nil {
		return syscall.EINVAL
	}
	if s.isIPv6Socket(conn) {
		pc := ipv6.NewPacketConn(conn)
		if join {
			return pc.JoinSourceSpecificGroup(iface, groupAddr, sourceAddr)
		}
		return pc.LeaveSourceSpecificGroup(iface, groupAddr, sourceAddr)
	}
	pc := ipv4.NewPacketConn(conn)
	if join {
		return pc.JoinSourceSpecificGroup(iface, groupAddr, sourceAddr)
	}
	return pc.LeaveSourceSpecificGroup(iface, groupAddr, sourceAddr)
}

func (s *RealUDPSocket) isIPv6Socket(conn *net.UDPConn) bool {
	if s.socketType == "udp6" {
		return true
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	return ok && addr.IP.To4() == nil && s.socketType != "udp4"
}

func interfaceForAddress(address string) (*net.Interface, error) {
	if address == "" {
		return nil, nil
	}
	ip := net.ParseIP(address)
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for i := range interfaces {
		if interfaces[i].Name == address {
			return &interfaces[i], nil
		}
		addrs, err := interfaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ip != nil && ipNet.IP.Equal(ip) {
				return &interfaces[i], nil
			}
		}
	}
	return nil, syscall.EADDRNOTAVAIL
}

// SetReadBuffer sets the size of the receive buffer.
func (s *RealUDPSocket) SetReadBuffer(size int) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	return conn.SetReadBuffer(size)
}

// SetWriteBuffer sets the size of the send buffer.
func (s *RealUDPSocket) SetWriteBuffer(size int) error {
	conn, err := s.udpConn()
	if err != nil {
		return err
	}
	return conn.SetWriteBuffer(size)
}
