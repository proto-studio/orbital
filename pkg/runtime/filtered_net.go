// Package network - Filtered socket factory with policy enforcement.
package runtime

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"time"
)

// FilteredSocketFactory wraps a SocketFactory and applies network policy.
type FilteredSocketFactory struct {
	underlying SocketFactory
	policy     NetworkPolicy
}

// NewFilteredSocketFactory creates a socket factory that enforces network policy.
func NewFilteredSocketFactory(underlying SocketFactory, policy NetworkPolicy) *FilteredSocketFactory {
	if underlying == nil {
		underlying = NewRealSocketFactory()
	}
	if policy == nil {
		policy = NewAllowAllPolicy()
	}
	return &FilteredSocketFactory{
		underlying: underlying,
		policy:     policy,
	}
}

// CreateTCPSocket creates a TCP socket with policy enforcement.
func (f *FilteredSocketFactory) CreateTCPSocket() TCPSocket {
	return &FilteredTCPSocket{
		underlying: f.underlying.CreateTCPSocket(),
		policy:     f.policy,
	}
}

// CreateTCPServer creates a TCP server with policy enforcement.
func (f *FilteredSocketFactory) CreateTCPServer() TCPServer {
	return &FilteredTCPServer{
		underlying: f.underlying.CreateTCPServer(),
		policy:     f.policy,
	}
}

// CreateUDPSocket creates a UDP socket with policy enforcement.
func (f *FilteredSocketFactory) CreateUDPSocket(socketType string) UDPSocket {
	return &FilteredUDPSocket{
		underlying: f.underlying.CreateUDPSocket(socketType),
		policy:     f.policy,
	}
}

// CreateTLSSocket creates a TLS socket with policy enforcement.
func (f *FilteredSocketFactory) CreateTLSSocket(socket TCPSocket, config *tls.Config, isServer bool) TCPSocket {
	// If the socket is already filtered, extract the underlying
	if filtered, ok := socket.(*FilteredTCPSocket); ok {
		return &FilteredTCPSocket{
			underlying: f.underlying.CreateTLSSocket(filtered.underlying, config, isServer),
			policy:     f.policy,
		}
	}
	return &FilteredTCPSocket{
		underlying: f.underlying.CreateTLSSocket(socket, config, isServer),
		policy:     f.policy,
	}
}

// Policy returns the current network policy.
func (f *FilteredSocketFactory) Policy() NetworkPolicy {
	return f.policy
}

// FilteredTCPSocket wraps a TCPSocket with policy enforcement.
type FilteredTCPSocket struct {
	underlying TCPSocket
	policy     NetworkPolicy
	connected  bool
}

// Connect checks the policy before connecting.
func (s *FilteredTCPSocket) Connect(ctx context.Context, address string, port int) error {
	if err := s.policy.CheckConnection(ProtocolTCP, DirectionOutbound, address, port); err != nil {
		return err
	}
	if err := s.underlying.Connect(ctx, address, port); err != nil {
		return err
	}
	s.connected = true
	return nil
}

// Write delegates to the underlying socket.
func (s *FilteredTCPSocket) Write(data []byte) (int, error) {
	return s.underlying.Write(data)
}

// Read delegates to the underlying socket.
func (s *FilteredTCPSocket) Read(buf []byte) (int, error) {
	return s.underlying.Read(buf)
}

// Close delegates to the underlying socket.
func (s *FilteredTCPSocket) Close() error {
	return s.underlying.Close()
}

// SetTimeout delegates to the underlying socket.
func (s *FilteredTCPSocket) SetTimeout(d time.Duration) error {
	return s.underlying.SetTimeout(d)
}

// SetKeepAlive delegates to the underlying socket.
func (s *FilteredTCPSocket) SetKeepAlive(enable bool) error {
	return s.underlying.SetKeepAlive(enable)
}

// SetNoDelay delegates to the underlying socket.
func (s *FilteredTCPSocket) SetNoDelay(enable bool) error {
	return s.underlying.SetNoDelay(enable)
}

// LocalAddr delegates to the underlying socket.
func (s *FilteredTCPSocket) LocalAddr() net.Addr {
	return s.underlying.LocalAddr()
}

// RemoteAddr delegates to the underlying socket.
func (s *FilteredTCPSocket) RemoteAddr() net.Addr {
	return s.underlying.RemoteAddr()
}

// Ref delegates to the underlying socket.
func (s *FilteredTCPSocket) Ref() {
	s.underlying.Ref()
}

// Unref delegates to the underlying socket.
func (s *FilteredTCPSocket) Unref() {
	s.underlying.Unref()
}

// FilteredTCPServer wraps a TCPServer with policy enforcement.
type FilteredTCPServer struct {
	underlying TCPServer
	policy     NetworkPolicy
	listenPort int
}

// Listen checks the policy before listening.
func (s *FilteredTCPServer) Listen(ctx context.Context, address string, port int) error {
	if err := s.policy.CheckConnection(ProtocolTCP, DirectionInbound, address, port); err != nil {
		return err
	}
	if err := s.underlying.Listen(ctx, address, port); err != nil {
		return err
	}
	s.listenPort = port
	return nil
}

// Accept delegates to the underlying server, wrapping the returned socket.
func (s *FilteredTCPServer) Accept(ctx context.Context) (TCPSocket, error) {
	socket, err := s.underlying.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &FilteredTCPSocket{
		underlying: socket,
		policy:     s.policy,
		connected:  true,
	}, nil
}

// Close delegates to the underlying server.
func (s *FilteredTCPServer) Close() error {
	return s.underlying.Close()
}

// Addr delegates to the underlying server.
func (s *FilteredTCPServer) Addr() net.Addr {
	return s.underlying.Addr()
}

// Ref delegates to the underlying server.
func (s *FilteredTCPServer) Ref() {
	s.underlying.Ref()
}

// Unref delegates to the underlying server.
func (s *FilteredTCPServer) Unref() {
	s.underlying.Unref()
}

// FilteredUDPSocket wraps a UDPSocket with policy enforcement.
type FilteredUDPSocket struct {
	underlying UDPSocket
	policy     NetworkPolicy
	bound      bool
	boundPort  int
}

// Bind checks the policy before binding.
func (s *FilteredUDPSocket) Bind(ctx context.Context, address string, port int) error {
	if err := s.policy.CheckConnection(ProtocolUDP, DirectionInbound, address, port); err != nil {
		return err
	}
	if err := s.underlying.Bind(ctx, address, port); err != nil {
		return err
	}
	s.bound = true
	s.boundPort = port
	return nil
}

// Send checks the policy before sending.
func (s *FilteredUDPSocket) Send(data []byte, address string, port int) (int, error) {
	if err := s.policy.CheckConnection(ProtocolUDP, DirectionOutbound, address, port); err != nil {
		return 0, err
	}
	return s.underlying.Send(data, address, port)
}

// Receive delegates to the underlying socket.
func (s *FilteredUDPSocket) Receive(buf []byte) (int, string, int, error) {
	return s.underlying.Receive(buf)
}

// Close delegates to the underlying socket.
func (s *FilteredUDPSocket) Close() error {
	return s.underlying.Close()
}

// SetBroadcast delegates to the underlying socket.
func (s *FilteredUDPSocket) SetBroadcast(enable bool) error {
	return s.underlying.SetBroadcast(enable)
}

// SetMulticastTTL delegates to the underlying socket.
func (s *FilteredUDPSocket) SetMulticastTTL(ttl int) error {
	return s.underlying.SetMulticastTTL(ttl)
}

// AddMembership delegates to the underlying socket.
func (s *FilteredUDPSocket) AddMembership(multicastAddr string, interfaceAddr string) error {
	return s.underlying.AddMembership(multicastAddr, interfaceAddr)
}

// DropMembership delegates to the underlying socket.
func (s *FilteredUDPSocket) DropMembership(multicastAddr string, interfaceAddr string) error {
	return s.underlying.DropMembership(multicastAddr, interfaceAddr)
}

// LocalAddr delegates to the underlying socket.
func (s *FilteredUDPSocket) LocalAddr() net.Addr {
	return s.underlying.LocalAddr()
}

// Ref delegates to the underlying socket.
func (s *FilteredUDPSocket) Ref() {
	s.underlying.Ref()
}

// Unref delegates to the underlying socket.
func (s *FilteredUDPSocket) Unref() {
	s.underlying.Unref()
}

// FilteredHTTPClient wraps an HTTPClient with policy enforcement.
type FilteredHTTPClient struct {
	underlying HTTPClient
	policy     NetworkPolicy
}

// NewFilteredHTTPClient creates an HTTP client that enforces network policy.
func NewFilteredHTTPClient(underlying HTTPClient, policy NetworkPolicy) *FilteredHTTPClient {
	if underlying == nil {
		underlying = NewRealHTTPClient()
	}
	if policy == nil {
		policy = NewAllowAllPolicy()
	}
	return &FilteredHTTPClient{
		underlying: underlying,
		policy:     policy,
	}
}

// Do checks the policy before making the request.
func (c *FilteredHTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	host, port, err := parseURLHostPort(req.URL)
	if err != nil {
		return nil, err
	}

	if err := c.policy.CheckHTTP(req.Method, host, port, ""); err != nil {
		return nil, err
	}

	return c.underlying.Do(ctx, req)
}

// Get checks the policy before making the request.
func (c *FilteredHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	host, port, err := parseURLHostPort(url)
	if err != nil {
		return nil, err
	}

	if err := c.policy.CheckHTTP("GET", host, port, ""); err != nil {
		return nil, err
	}

	return c.underlying.Get(ctx, url)
}

// Post checks the policy before making the request.
func (c *FilteredHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	host, port, err := parseURLHostPort(url)
	if err != nil {
		return nil, err
	}

	if err := c.policy.CheckHTTP("POST", host, port, ""); err != nil {
		return nil, err
	}

	return c.underlying.Post(ctx, url, contentType, body)
}

// parseURLHostPort extracts host and port from a URL string.
func parseURLHostPort(urlStr string) (string, int, error) {
	// Simple parsing - extract host and port from URL
	// Format: scheme://host:port/path or scheme://host/path

	// Remove scheme
	if idx := strings.Index(urlStr, "://"); idx >= 0 {
		urlStr = urlStr[idx+3:]
	}

	// Remove path
	if idx := strings.Index(urlStr, "/"); idx >= 0 {
		urlStr = urlStr[:idx]
	}

	// Remove query string
	if idx := strings.Index(urlStr, "?"); idx >= 0 {
		urlStr = urlStr[:idx]
	}

	// Extract host and port
	host := urlStr
	port := 80 // default

	if idx := strings.LastIndex(urlStr, ":"); idx >= 0 {
		// Check if this is an IPv6 address
		if !strings.Contains(urlStr, "]") || strings.LastIndex(urlStr, "]") < idx {
			host = urlStr[:idx]
			if p, err := net.LookupPort("tcp", urlStr[idx+1:]); err == nil {
				port = p
			}
		}
	}

	// Remove brackets from IPv6
	host = strings.Trim(host, "[]")

	return host, port, nil
}
