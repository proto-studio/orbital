// Package network - Socket interfaces for TCP/UDP operations.
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
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

	// CreateTLSSocket wraps a TCP socket with TLS.
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
	conn    net.Conn
	mu      sync.Mutex
	timeout time.Duration
	ref     bool
}

// Connect establishes a TCP connection to the specified address and port.
func (s *RealTCPSocket) Connect(ctx context.Context, address string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	addr := net.JoinHostPort(address, strconv.Itoa(port))

	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	s.conn = conn
	return nil
}

// Write sends data on the TCP connection.
func (s *RealTCPSocket) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		return 0, ErrSocketClosed
	}
	if s.timeout > 0 {
		s.conn.SetWriteDeadline(time.Now().Add(s.timeout))
	}
	return s.conn.Write(data)
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
}

// Listen starts the server listening on the specified address and port.
func (s *RealTCPServer) Listen(ctx context.Context, address string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	addr := net.JoinHostPort(address, strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
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
	mu         sync.Mutex
}

// Connect establishes a TLS connection by first connecting the underlying socket then performing the TLS handshake.
func (s *RealTLSSocket) Connect(ctx context.Context, address string, port int) error {
	// First connect the underlying socket
	if err := s.underlying.Connect(ctx, address, port); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Get the underlying connection
	realSocket, ok := s.underlying.(*RealTCPSocket)
	if !ok {
		return errors.New("TLS requires real TCP socket")
	}

	realSocket.mu.Lock()
	conn := realSocket.conn
	realSocket.mu.Unlock()

	// Wrap with TLS
	if s.isServer {
		s.tlsConn = tls.Server(conn, s.config)
	} else {
		s.tlsConn = tls.Client(conn, s.config)
	}

	return s.tlsConn.Handshake()
}

// Write sends encrypted data over the TLS connection.
func (s *RealTLSSocket) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tlsConn == nil {
		return 0, ErrSocketClosed
	}
	return s.tlsConn.Write(data)
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

	if s.tlsConn != nil {
		s.tlsConn.Close()
		s.tlsConn = nil
	}
	return s.underlying.Close()
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

	if s.conn == nil {
		return 0, ErrSocketClosed
	}

	addr := net.JoinHostPort(address, strconv.Itoa(port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return 0, err
	}

	return s.conn.WriteToUDP(data, udpAddr)
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
