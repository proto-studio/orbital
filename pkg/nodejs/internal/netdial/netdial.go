// Package netdial connects sockets through a runtime's injected controls, and reports
// failures with Node's error details. node:net, node:tls and node:http2 share it, so all
// of them apply policy to the address actually dialed.
package netdial

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"proto.zip/studio/orbital/pkg/runtime"
)

// ErrInvalidHost reports a host that is neither an address nor a name. A zone in an IPv6
// literal is also rejected: it is free-form text that does not choose the destination, so
// it must not reach a policy as something it could match on.
var ErrInvalidHost = errors.New("ERR_INVALID_URL: Invalid URL")

// Resolve returns canonical IPs for host, and whether host is a name resolved through DNS
// rather than an address literal. Literals are canonicalized, so an IPv4-mapped IPv6
// literal is checked as the IPv4 address it reaches. family is 4, 6, or 0 for both.
func Resolve(ctx context.Context, rt *runtime.Runtime, host string, family int) ([]string, bool, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return nil, false, ErrInvalidHost
		}
		return []string{addr.Unmap().String()}, false, nil
	}
	if strings.ContainsAny(host, ":%") {
		return nil, false, ErrInvalidHost
	}
	network := "ip"
	switch family {
	case 4:
		network = "ip4"
	case 6:
		network = "ip6"
	}
	ips, err := rt.DNSResolver().LookupIP(ctx, network, host)
	if err != nil {
		return nil, false, err
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if (family == 4 && !addr.Is4()) || (family == 6 && !addr.Is6()) {
			continue
		}
		out = append(out, addr.String())
	}
	if len(out) == 0 {
		return nil, false, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return out, true, nil
}

// Options adjust how Connect dials.
type Options struct {
	// Family limits the addresses tried to 4 or 6.
	Family int
	// LocalAddress and LocalPort choose the address to connect from, on sockets that
	// support it.
	LocalAddress string
	LocalPort    int
	// Prepare, if set, is given each socket before it connects, so a caller can cancel a
	// connection by closing the socket it is attempting.
	Prepare func(runtime.TCPSocket)
}

// Result describes a successful connection.
type Result struct {
	Socket runtime.TCPSocket
	// IP is the address connected to.
	IP string
	// IsName reports whether the host was a name resolved through DNS.
	IsName bool
}

// Connect resolves host with the injected resolver and connects with sockets from the
// injected factory, trying each address in turn. The IP handed to the socket is the one
// resolved here, so the factory's decision applies to the actual destination and a DNS
// change cannot move the connection afterwards.
func Connect(ctx context.Context, rt *runtime.Runtime, host string, port int, opts Options) (Result, error) {
	ips, isName, err := Resolve(ctx, rt, host, opts.Family)
	if err != nil {
		return Result{}, err
	}

	var firstErr error
	for _, ip := range ips {
		socket := rt.SocketFactory().CreateTCPSocket()
		if opts.Prepare != nil {
			opts.Prepare(socket)
		}
		if opts.LocalAddress != "" || opts.LocalPort != 0 {
			if bindable, ok := socket.(runtime.LocalBindableTCPSocket); ok {
				bindable.SetLocalAddr(opts.LocalAddress, opts.LocalPort)
			}
		}
		var err error
		// Only a name that went through DNS is reported as a hostname. Anything else
		// the guest wrote must not reach a policy as a name it could match on.
		if resolved, ok := socket.(runtime.ResolvedTCPSocket); ok && isName {
			err = resolved.ConnectResolved(ctx, host, ip, port)
		} else {
			err = socket.Connect(ctx, ip, port)
		}
		if err == nil {
			return Result{Socket: socket, IP: ip, IsName: isName}, nil
		}
		socket.Close()
		err = &AddressError{Err: err, Address: ip, Port: port}
		if firstErr == nil {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return Result{}, firstErr
}

// AddressError records the address a connection attempt was made to.
type AddressError struct {
	Err     error
	Address string
	Port    int
}

func (e *AddressError) Error() string { return e.Err.Error() }
func (e *AddressError) Unwrap() error { return e.Err }

// NodeError holds the properties of a Node system error.
type NodeError struct {
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
	Errno    int    `json:"errno,omitempty"`
	Syscall  string `json:"syscall,omitempty"`
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

var codePrefix = regexp.MustCompile(`^([A-Z][A-Z0-9_]+): (.*)$`)

// Describe converts a socket failure to the error Node reports. An error with a
// "CODE: text" message, such as a sandbox denial, keeps its text and reports CODE.
func Describe(err error, syscallName, host string, port int) NodeError {
	out := NodeError{Syscall: syscallName, Address: host, Port: port}

	var addrErr *AddressError
	if errors.As(err, &addrErr) {
		out.Address = addrErr.Address
		out.Port = addrErr.Port
	}

	var (
		dnsErr *net.DNSError
		errno  syscall.Errno
	)
	switch {
	case errors.As(err, &dnsErr), errors.Is(err, ErrInvalidHost):
		out.Code = "ENOTFOUND"
		if dnsErr != nil && dnsErr.IsTimeout {
			out.Code = "EAI_AGAIN"
		}
		out.Syscall = "getaddrinfo"
		out.Hostname = host
		out.Address = ""
		out.Port = 0
		out.Message = "getaddrinfo " + out.Code + " " + host
		return out
	case errors.As(err, &errno):
		out.Code = ErrnoName(errno)
		out.Errno = -int(errno)
	case errors.Is(err, context.Canceled):
		out.Code = "ABORT_ERR"
		out.Message = "The operation was aborted"
		return out
	case errors.Is(err, context.DeadlineExceeded):
		out.Code = "ETIMEDOUT"
	default:
		if m := codePrefix.FindStringSubmatch(err.Error()); m != nil {
			out.Code = m[1]
			out.Message = err.Error()
			return out
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			out.Code = "ETIMEDOUT"
		} else {
			out.Message = err.Error()
			return out
		}
	}

	out.Message = syscallName + " " + out.Code
	if out.Address != "" {
		if out.Port != 0 {
			out.Message += " " + net.JoinHostPort(out.Address, strconv.Itoa(out.Port))
		} else {
			out.Message += " " + out.Address
		}
	}
	return out
}

// ErrnoName returns the symbolic name Node uses for errno.
func ErrnoName(errno syscall.Errno) string {
	switch errno {
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ECONNABORTED:
		return "ECONNABORTED"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ENETUNREACH:
		return "ENETUNREACH"
	case syscall.EADDRNOTAVAIL:
		return "EADDRNOTAVAIL"
	case syscall.EADDRINUSE:
		return "EADDRINUSE"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.EPIPE:
		return "EPIPE"
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.ENOTSOCK:
		return "ENOTSOCK"
	case syscall.EINVAL:
		return "EINVAL"
	case syscall.EAFNOSUPPORT:
		return "EAFNOSUPPORT"
	}
	return errno.Error()
}
