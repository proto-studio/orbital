// Package network - Filtered socket factory with policy enforcement.
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
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

// ConnectResolved checks the requested name and the resolved IP against the
// policy, then connects to that same IP so DNS cannot change the destination
// between the check and the connection.
func (s *FilteredTCPSocket) ConnectResolved(ctx context.Context, hostname, ip string, port int) error {
	if err := checkResolvedConnection(s.policy, ProtocolTCP, DirectionOutbound, hostname, ip, port); err != nil {
		return err
	}
	var err error
	if resolved, ok := s.underlying.(ResolvedTCPSocket); ok {
		err = resolved.ConnectResolved(ctx, hostname, ip, port)
	} else {
		err = s.underlying.Connect(ctx, ip, port)
	}
	if err != nil {
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

// CloseWrite delegates to the underlying socket.
func (s *FilteredTCPSocket) CloseWrite() error {
	return CloseWrite(s.underlying)
}

// SetDeadline delegates to the underlying socket if it supports deadlines.
func (s *FilteredTCPSocket) SetDeadline(t time.Time) error {
	if d, ok := s.underlying.(DeadlineSetter); ok {
		return d.SetDeadline(t)
	}
	return nil
}

// SetReadDeadline delegates to the underlying socket if it supports deadlines.
func (s *FilteredTCPSocket) SetReadDeadline(t time.Time) error {
	if d, ok := s.underlying.(DeadlineSetter); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}

// SetWriteDeadline delegates to the underlying socket if it supports deadlines.
func (s *FilteredTCPSocket) SetWriteDeadline(t time.Time) error {
	if d, ok := s.underlying.(DeadlineSetter); ok {
		return d.SetWriteDeadline(t)
	}
	return nil
}

// SetLocalAddr delegates to the underlying socket if it can choose its local address.
// The policy applies to the destination, which this does not change.
func (s *FilteredTCPSocket) SetLocalAddr(ip string, port int) {
	if b, ok := s.underlying.(LocalBindableTCPSocket); ok {
		b.SetLocalAddr(ip, port)
	}
}

// SetLinger delegates to the underlying socket if it supports linger.
func (s *FilteredTCPSocket) SetLinger(seconds int) error {
	if l, ok := s.underlying.(LingerSetter); ok {
		return l.SetLinger(seconds)
	}
	return errors.ErrUnsupported
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
	if err := s.checkImplicitBind(); err != nil {
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

// SendResolved checks the requested name and the resolved IP against the policy, then
// sends to that same IP.
func (s *FilteredUDPSocket) SendResolved(data []byte, hostname, ip string, port int) (int, error) {
	if err := checkResolvedConnection(s.policy, ProtocolUDP, DirectionOutbound, hostname, ip, port); err != nil {
		return 0, err
	}
	if err := s.checkImplicitBind(); err != nil {
		return 0, err
	}
	if resolved, ok := s.underlying.(ResolvedUDPSocket); ok {
		return resolved.SendResolved(data, hostname, ip, port)
	}
	return s.underlying.Send(data, ip, port)
}

// checkImplicitBind applies the policy of an explicit Bind to the bind a real socket makes
// on its first send.
func (s *FilteredUDPSocket) checkImplicitBind() error {
	if real, ok := s.underlying.(*RealUDPSocket); ok && !s.bound {
		if err := s.policy.CheckConnection(ProtocolUDP, DirectionInbound, real.wildcardAddress(), 0); err != nil {
			return err
		}
		s.bound = true
	}
	return nil
}

func (s *FilteredUDPSocket) options() (UDPSocketOptions, error) {
	if o, ok := s.underlying.(UDPSocketOptions); ok {
		return o, nil
	}
	return nil, errors.ErrUnsupported
}

// SetTTL delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) SetTTL(ttl int) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.SetTTL(ttl)
}

// SetMulticastLoopback delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) SetMulticastLoopback(enable bool) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.SetMulticastLoopback(enable)
}

// SetMulticastInterface delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) SetMulticastInterface(address string) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.SetMulticastInterface(address)
}

// JoinSourceGroup delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) JoinSourceGroup(source, group, iface string) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.JoinSourceGroup(source, group, iface)
}

// LeaveSourceGroup delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) LeaveSourceGroup(source, group, iface string) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.LeaveSourceGroup(source, group, iface)
}

// SetReadBuffer delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) SetReadBuffer(size int) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.SetReadBuffer(size)
}

// SetWriteBuffer delegates to the underlying socket if it supports the option.
func (s *FilteredUDPSocket) SetWriteBuffer(size int) error {
	o, err := s.options()
	if err != nil {
		return err
	}
	return o.SetWriteBuffer(size)
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

// HTTPOverSockets reports whether node:http may open its own connections rather than send
// whole requests through HTTPClient. It may when the client is the real one, because the
// socket factory is then the boundary, or when the client only applies a built-in policy
// that the socket factory applies too. A built-in policy decides an HTTP request as it
// does the connection carrying it, so the sockets lose nothing. Any other client is the
// embedder's own and must see every request.
func (rt *Runtime) HTTPOverSockets() bool {
	switch client := rt.httpClient.(type) {
	case *RealHTTPClient:
		return true
	case *FilteredHTTPClient:
		if _, real := client.underlying.(*RealHTTPClient); !real {
			return false
		}
		factory, ok := rt.socketFactory.(*FilteredSocketFactory)
		return ok && samePolicy(client.policy, factory.policy)
	}
	return false
}

func samePolicy(a, b NetworkPolicy) bool {
	switch a.(type) {
	case *AllowAllPolicy, *DenyAllPolicy, *HostListPolicy, *RuleBasedPolicy:
		return a == b
	}
	return false
}
