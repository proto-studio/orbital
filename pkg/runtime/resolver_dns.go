// Package dns provides interfaces and implementations for DNS resolution.
package runtime

import (
	"context"
	"net"
	"strings"
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

// NoOpResolver resolves as a host does when the process may not use the network: only
// localhost resolves, as the hosts file says, other names are not found (ENOTFOUND), and
// queries that would ask a DNS server, such as dns.resolve4(), fail with ECONNREFUSED, as
// Node reports when it can't reach one. It is the default when Config.DNSResolver is nil.
type NoOpResolver struct{}

// NewNoOpResolver creates a NoOpResolver.
func NewNoOpResolver() *NoOpResolver {
	return &NoOpResolver{}
}

// noOpLocalhost is what localhost resolves to, in the order the usual hosts file lists it.
var noOpLocalhost = []string{"127.0.0.1", "::1"}

func noOpNotFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func isLocalhost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

// LookupHost resolves localhost and reports every other name as not found.
func (r *NoOpResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if isLocalhost(host) {
		return append([]string(nil), noOpLocalhost...), nil
	}
	return nil, noOpNotFound(host)
}

// LookupIP resolves localhost and reports every other name as not found.
func (r *NoOpResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if !isLocalhost(host) {
		return nil, noOpNotFound(host)
	}
	var ips []net.IP
	for _, addr := range noOpLocalhost {
		ip := net.ParseIP(addr)
		switch {
		case network == "ip4" && ip.To4() == nil, network == "ip6" && ip.To4() != nil:
			continue
		}
		ips = append(ips, ip)
	}
	if len(ips) == 0 {
		return nil, noOpNotFound(host)
	}
	return ips, nil
}

// LookupCNAME fails as a query no DNS server answers.
func (r *NoOpResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	return "", noOpRefused
}

// LookupMX fails as a query no DNS server answers.
func (r *NoOpResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return nil, noOpRefused
}

// LookupNS fails as a query no DNS server answers.
func (r *NoOpResolver) LookupNS(ctx context.Context, domain string) ([]*net.NS, error) {
	return nil, noOpRefused
}

// LookupTXT fails as a query no DNS server answers.
func (r *NoOpResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return nil, noOpRefused
}

// LookupSRV fails as a query no DNS server answers.
func (r *NoOpResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return "", nil, noOpRefused
}

// LookupAddr names the loopback addresses localhost and reports every other address as
// not found.
func (r *NoOpResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	if ip := net.ParseIP(addr); ip != nil && ip.IsLoopback() {
		return []string{"localhost"}, nil
	}
	return nil, noOpNotFound(addr)
}

// noOpRecords answers record queries for NoOpResolver. RecordsFor offers it only for
// NoOpResolver itself, so a type that embeds NoOpResolver keeps its own lookups.
type noOpRecords struct{}

// LookupRecords fails as a query no DNS server answers, with ECONNREFUSED. A reverse
// lookup (PTR for an in-addr.arpa or ip6.arpa name) is answered from the hosts file as
// Node's resolver answers it: a loopback address is localhost, and any other address is
// not found.
func (noOpRecords) LookupRecords(ctx context.Context, name, rrtype string, servers []string) ([]map[string]any, error) {
	if rrtype == "PTR" {
		if ip, ok := reverseAddress(name); ok {
			if ip.IsLoopback() {
				return []map[string]any{{"type": "PTR", "value": "localhost"}}, nil
			}
			return nil, &DNSQueryError{Code: "ENOTFOUND"}
		}
	}
	return nil, noOpRefused
}

// reverseAddress parses the address a reverse lookup name such as 1.0.0.127.in-addr.arpa
// stands for.
func reverseAddress(name string) (net.IP, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if rest, ok := strings.CutSuffix(name, ".in-addr.arpa"); ok {
		parts := strings.Split(rest, ".")
		if len(parts) != 4 {
			return nil, false
		}
		for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
			parts[i], parts[j] = parts[j], parts[i]
		}
		ip := net.ParseIP(strings.Join(parts, ".")).To4()
		return ip, ip != nil
	}
	if rest, ok := strings.CutSuffix(name, ".ip6.arpa"); ok {
		nibbles := strings.Split(rest, ".")
		if len(nibbles) != 32 {
			return nil, false
		}
		var hex strings.Builder
		for i := len(nibbles) - 1; i >= 0; i-- {
			if len(nibbles[i]) != 1 {
				return nil, false
			}
			hex.WriteString(nibbles[i])
			if i%4 == 0 && i != 0 {
				hex.WriteByte(':')
			}
		}
		ip := net.ParseIP(hex.String())
		return ip, ip != nil
	}
	return nil, false
}

var noOpRefused = &DNSQueryError{Code: "ECONNREFUSED"}
