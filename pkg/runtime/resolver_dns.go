// Package dns provides interfaces and implementations for DNS resolution.
package runtime

import (
	"context"
	"net"
)

// Resolver defines the interface for DNS resolution.
// This interface can be implemented to sandbox or customize DNS lookups.
type Resolver interface {
	// LookupHost looks up the host names for the given address.
	LookupHost(ctx context.Context, host string) ([]string, error)

	// LookupIP looks up IP addresses for the given host.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)

	// LookupCNAME looks up the canonical name for the given host.
	LookupCNAME(ctx context.Context, host string) (string, error)

	// LookupMX looks up mail exchange records for the given domain.
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)

	// LookupNS looks up name server records for the given domain.
	LookupNS(ctx context.Context, domain string) ([]*net.NS, error)

	// LookupTXT looks up TXT records for the given domain.
	LookupTXT(ctx context.Context, domain string) ([]string, error)

	// LookupSRV looks up SRV records for the given service, proto, and name.
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)

	// LookupAddr performs a reverse lookup for the given address.
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// RealResolver uses the system's DNS resolver.
type RealResolver struct {
	resolver *net.Resolver
}

// NewRealResolver creates a new RealResolver that uses system DNS.
func NewRealResolver() *RealResolver {
	return &RealResolver{
		resolver: net.DefaultResolver,
	}
}

// LookupHost looks up the host names for the given address.
func (r *RealResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return r.resolver.LookupHost(ctx, host)
}

// LookupIP looks up IP addresses for the given host.
func (r *RealResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return r.resolver.LookupIP(ctx, network, host)
}

// LookupCNAME looks up the canonical name for the given host.
func (r *RealResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return r.resolver.LookupCNAME(ctx, host)
}

// LookupMX looks up mail exchange records for the given domain.
func (r *RealResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return r.resolver.LookupMX(ctx, domain)
}

// LookupNS looks up name server records for the given domain.
func (r *RealResolver) LookupNS(ctx context.Context, domain string) ([]*net.NS, error) {
	return r.resolver.LookupNS(ctx, domain)
}

// LookupTXT looks up TXT records for the given domain.
func (r *RealResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return r.resolver.LookupTXT(ctx, domain)
}

// LookupSRV looks up SRV records for the given service, proto, and name.
func (r *RealResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return r.resolver.LookupSRV(ctx, service, proto, name)
}

// LookupAddr performs a reverse lookup for the given address.
func (r *RealResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return r.resolver.LookupAddr(ctx, addr)
}

// SandboxedResolver returns fake/configured results for DNS queries.
// This is useful for sandboxed environments where real DNS lookups are not allowed.
type SandboxedResolver struct {
	// Hosts maps hostnames to IP addresses
	Hosts map[string][]string
	// DefaultIP is returned for unknown hosts (empty string to return error)
	DefaultIP string
	// AllowList contains hostnames that are allowed to be resolved
	// If nil, no hosts are allowed (returns errors for all lookups)
	AllowList []string
}

// NewSandboxedResolver creates a new SandboxedResolver with default configuration.
func NewSandboxedResolver() *SandboxedResolver {
	return &SandboxedResolver{
		Hosts: map[string][]string{
			"localhost": {"127.0.0.1", "::1"},
		},
		DefaultIP: "",
		AllowList: nil,
	}
}

// NewSandboxedResolverWithHosts creates a new SandboxedResolver with custom hosts.
func NewSandboxedResolverWithHosts(hosts map[string][]string) *SandboxedResolver {
	return &SandboxedResolver{
		Hosts:     hosts,
		DefaultIP: "",
		AllowList: nil,
	}
}

// isAllowed checks if the host is in the allowlist.
func (r *SandboxedResolver) isAllowed(host string) bool {
	if r.AllowList == nil {
		return false
	}
	for _, allowed := range r.AllowList {
		if allowed == host || allowed == "*" {
			return true
		}
	}
	return false
}

// LookupHost looks up the host names for the given address.
func (r *SandboxedResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if addrs, ok := r.Hosts[host]; ok {
		return addrs, nil
	}
	if r.DefaultIP != "" {
		return []string{r.DefaultIP}, nil
	}
	return nil, &net.DNSError{Err: "DNS lookup not allowed in sandbox", Name: host, IsNotFound: true}
}

// LookupIP looks up IP addresses for the given host.
func (r *SandboxedResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip != nil {
			if network == "ip4" && ip.To4() != nil {
				ips = append(ips, ip)
			} else if network == "ip6" && ip.To4() == nil {
				ips = append(ips, ip)
			} else if network == "ip" {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

// LookupCNAME looks up the canonical name for the given host.
func (r *SandboxedResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return "", &net.DNSError{Err: "CNAME lookup not allowed in sandbox", Name: host, IsNotFound: true}
}

// LookupMX looks up mail exchange records for the given domain.
func (r *SandboxedResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "MX lookup not allowed in sandbox", Name: domain, IsNotFound: true}
}

// LookupNS looks up name server records for the given domain.
func (r *SandboxedResolver) LookupNS(ctx context.Context, domain string) ([]*net.NS, error) {
	return nil, &net.DNSError{Err: "NS lookup not allowed in sandbox", Name: domain, IsNotFound: true}
}

// LookupTXT looks up TXT records for the given domain.
func (r *SandboxedResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return nil, &net.DNSError{Err: "TXT lookup not allowed in sandbox", Name: domain, IsNotFound: true}
}

// LookupSRV looks up SRV records for the given service, proto, and name.
func (r *SandboxedResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return "", nil, &net.DNSError{Err: "SRV lookup not allowed in sandbox", Name: name, IsNotFound: true}
}

// LookupAddr performs a reverse lookup for the given address.
func (r *SandboxedResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	// Check if any host maps to this address
	for host, addrs := range r.Hosts {
		for _, a := range addrs {
			if a == addr {
				return []string{host}, nil
			}
		}
	}
	return nil, &net.DNSError{Err: "reverse lookup not allowed in sandbox", Name: addr, IsNotFound: true}
}

// NoOpResolver returns errors for all DNS operations.
type NoOpResolver struct{}

// NewNoOpResolver creates a NoOpResolver that blocks all DNS operations.
func NewNoOpResolver() *NoOpResolver {
	return &NoOpResolver{}
}

// LookupHost always returns a DNS error.
func (r *NoOpResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: host, IsNotFound: true}
}

// LookupIP always returns a DNS error.
func (r *NoOpResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: host, IsNotFound: true}
}

// LookupCNAME always returns a DNS error.
func (r *NoOpResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return "", &net.DNSError{Err: "DNS not available", Name: host, IsNotFound: true}
}

// LookupMX always returns a DNS error.
func (r *NoOpResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: domain, IsNotFound: true}
}

// LookupNS always returns a DNS error.
func (r *NoOpResolver) LookupNS(ctx context.Context, domain string) ([]*net.NS, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: domain, IsNotFound: true}
}

// LookupTXT always returns a DNS error.
func (r *NoOpResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: domain, IsNotFound: true}
}

// LookupSRV always returns a DNS error.
func (r *NoOpResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return "", nil, &net.DNSError{Err: "DNS not available", Name: name, IsNotFound: true}
}

// LookupAddr always returns a DNS error.
func (r *NoOpResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return nil, &net.DNSError{Err: "DNS not available", Name: addr, IsNotFound: true}
}
