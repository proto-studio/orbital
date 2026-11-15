package tlsclient

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// IdentityError is the failure of Node's tls.checkServerIdentity: the certificate does not
// name the host.
type IdentityError struct {
	Host   string
	Reason string
}

func (e *IdentityError) Error() string {
	return "Hostname/IP does not match certificate's altnames: " + e.Reason
}

// altName is one subject alternative name, in certificate order.
type altName struct {
	kind  int // the GeneralName tag
	value []byte
}

const (
	sanEmail = 1
	sanDNS   = 2
	sanURI   = 6
	sanIP    = 7
)

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
var oidCommonName = asn1.ObjectIdentifier{2, 5, 4, 3}

// parsedAltNames lists the names crypto/x509 read, grouped by kind.
func parsedAltNames(cert *x509.Certificate) []altName {
	var names []altName
	for _, name := range cert.DNSNames {
		names = append(names, altName{kind: sanDNS, value: []byte(name)})
	}
	for _, ip := range cert.IPAddresses {
		if v4 := ip.To4(); v4 != nil && len(ip) == 4 {
			ip = v4
		}
		names = append(names, altName{kind: sanIP, value: ip})
	}
	for _, uri := range cert.URIs {
		names = append(names, altName{kind: sanURI, value: []byte(uri.String())})
	}
	for _, email := range cert.EmailAddresses {
		names = append(names, altName{kind: sanEmail, value: []byte(email)})
	}
	return names
}

// subjectAltNames returns the names of the kinds Node prints, in the order the certificate
// lists them. It reports false if the extension can't be read.
func subjectAltNames(cert *x509.Certificate) ([]altName, bool) {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		var raw []asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &raw); err != nil {
			return nil, false
		}
		var names []altName
		for _, value := range raw {
			if value.Class != asn1.ClassContextSpecific || value.IsCompound {
				continue
			}
			switch value.Tag {
			case sanEmail, sanDNS, sanURI, sanIP:
				names = append(names, altName{kind: value.Tag, value: value.Bytes})
			}
		}
		return names, true
	}
	return nil, true
}

// FormatAltNames returns a certificate's subjectaltname as Node prints it. A name with a
// character that would make the list ambiguous is written as a JSON string after its prefix.
func FormatAltNames(cert *x509.Certificate) string {
	names, ok := subjectAltNames(cert)
	if !ok {
		names = parsedAltNames(cert)
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		switch name.kind {
		case sanDNS:
			parts = append(parts, printAltName(name.value, "DNS"))
		case sanURI:
			parts = append(parts, printAltName(name.value, "URI"))
		case sanEmail:
			parts = append(parts, printAltName(name.value, "email"))
		case sanIP:
			parts = append(parts, "IP Address:"+printIP(name.value))
		}
	}
	return strings.Join(parts, ", ")
}

func isSafeAltName(name []byte) bool {
	for _, c := range name {
		switch {
		case c == '"', c == '\\', c == ',', c == '\'':
			return false
		case c < ' ' || c > '~':
			return false
		}
	}
	return true
}

func printAltName(name []byte, prefix string) string {
	if isSafeAltName(name) {
		return prefix + ":" + string(name)
	}
	var b strings.Builder
	b.WriteString(prefix + `:"`)
	for _, c := range name {
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '"':
			b.WriteString(`\"`)
		case c >= ' ' && c != ',' && c <= '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\u%04x`, c)
		}
	}
	b.WriteString(`"`)
	return b.String()
}

// printIP writes an address as OpenSSL does: IPv6 as eight uppercase groups.
func printIP(ip []byte) string {
	switch len(ip) {
	case 4:
		return fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], ip[3])
	case 16:
		groups := make([]string, 8)
		for i := range groups {
			groups[i] = fmt.Sprintf("%X", int(ip[2*i])<<8|int(ip[2*i+1]))
		}
		return strings.Join(groups, ":")
	}
	return "<invalid>"
}

// commonNames returns the subject's Common Name values.
func commonNames(cert *x509.Certificate) []string {
	var names []string
	for _, attr := range cert.Subject.Names {
		if attr.Type.Equal(oidCommonName) {
			if s, ok := attr.Value.(string); ok {
				names = append(names, s)
			}
		}
	}
	return names
}

// CheckServerIdentity applies Node's tls.checkServerIdentity rules to cert for hostname,
// and returns an *IdentityError if they do not accept it.
func CheckServerIdentity(hostname string, cert *x509.Certificate) error {
	// The names come from crypto/x509's parse, which the chain was verified with.
	dnsNames := cert.DNSNames
	var ips []netip.Addr
	for _, ip := range cert.IPAddresses {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			ips = append(ips, addr)
		}
	}

	hostname = strings.TrimSuffix(hostname, ".")
	cns := commonNames(cert)

	var reason string
	if addr, ok := parseHostIP(hostname); ok {
		for _, ip := range ips {
			if ip == addr {
				return nil
			}
		}
		listed := make([]string, len(ips))
		for i, ip := range ips {
			listed[i] = ip.String()
		}
		reason = "IP: " + hostname + " is not in the cert's list: " + strings.Join(listed, ", ")
	} else if len(dnsNames) > 0 || len(cns) > 1 || (len(cns) == 1 && cns[0] != "") {
		hostParts := splitHost(hostname)
		if len(dnsNames) > 0 {
			for _, name := range dnsNames {
				if matchHost(hostParts, name) {
					return nil
				}
			}
			reason = "Host: " + hostname + ". is not in the cert's altnames: " + FormatAltNames(cert)
		} else {
			// The Common Name is used only when there are no DNS names.
			for _, cn := range cns {
				if matchHost(hostParts, cn) {
					return nil
				}
			}
			reason = "Host: " + hostname + ". is not cert's CN: " + strings.Join(cns, ",")
		}
	} else {
		reason = "Cert does not contain a DNS name"
	}
	return &IdentityError{Host: hostname, Reason: reason}
}

// parseHostIP reads hostname as net.isIP does, and returns the address without its zone.
func parseHostIP(hostname string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(hostname)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone(""), true
}

// splitHost lowercases only ASCII letters, so that no other character can turn into one.
// Labels with anything but printable ASCII never match.
func splitHost(host string) []string {
	return strings.Split(asciiLower(strings.TrimSuffix(host, ".")), ".")
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// matchHost reports whether a hostname, split into labels, matches a certificate name. A
// wildcard covers part or all of the leftmost label only, and needs at least three labels.
func matchHost(hostParts []string, pattern string) bool {
	if pattern == "" {
		return false
	}
	patternParts := splitHost(pattern)
	if len(hostParts) != len(patternParts) {
		return false
	}
	for _, part := range patternParts {
		if part == "" {
			return false
		}
		// Unicode labels, control characters and blanks never match.
		for _, r := range part {
			if r < 0x21 || r > 0x7f {
				return false
			}
		}
	}
	for i := len(hostParts) - 1; i > 0; i-- {
		if hostParts[i] != patternParts[i] {
			return false
		}
	}
	hostSubdomain := hostParts[0]
	patternSubdomain := patternParts[0]
	wildcardParts := strings.Split(patternSubdomain, "*")
	// A label with no wildcard, or an A-label, matches only as written.
	if len(wildcardParts) == 1 || strings.Contains(patternSubdomain, "xn--") {
		return hostSubdomain == patternSubdomain
	}
	if len(wildcardParts) > 2 || len(patternParts) <= 2 {
		return false
	}
	prefix, suffix := wildcardParts[0], wildcardParts[1]
	if len(prefix)+len(suffix) > len(hostSubdomain) {
		return false
	}
	return strings.HasPrefix(hostSubdomain, prefix) && strings.HasSuffix(hostSubdomain, suffix)
}

// ConstraintError is a chain whose name constraints exclude the leaf's Common Name. Node has
// no code of its own for this OpenSSL error and reports UNSPECIFIED.
type ConstraintError struct {
	Code    string
	Message string
}

func (e *ConstraintError) Error() string { return e.Message }

var dnsLikeName = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)+\.?$`)

// checkCommonNameConstraints applies the CAs' DNS name constraints to a leaf's Common Name
// when the leaf has no DNS names, as OpenSSL does; crypto/x509 only applies them to SANs.
// A non-empty host is the name the Common Name will be matched against, and is held to the
// constraints too, so a wildcard Common Name can't reach outside them. It returns the
// chains that satisfy the constraints, which are all of them unless the leaf names its
// host only by Common Name.
func checkCommonNameConstraints(leaf *x509.Certificate, chains [][]*x509.Certificate, host string) ([][]*x509.Certificate, error) {
	if len(leaf.DNSNames) > 0 {
		return chains, nil
	}
	var names []string
	for _, cn := range commonNames(leaf) {
		if dnsLikeName.MatchString(cn) {
			names = append(names, asciiLower(strings.TrimSuffix(cn, ".")))
		}
	}
	if host != "" {
		if _, isIP := parseHostIP(host); !isIP {
			names = append(names, asciiLower(strings.TrimSuffix(host, ".")))
		}
	}
	var ok [][]*x509.Certificate
	var firstErr error
	for _, chain := range chains {
		if err := chainPermits(chain, names); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = append(ok, chain)
	}
	if len(ok) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return ok, nil
}

// chainPermits checks names against the DNS name constraints of a chain's CAs.
func chainPermits(chain []*x509.Certificate, names []string) error {
	for _, name := range names {
		for _, ca := range chain[1:] {
			if len(ca.PermittedDNSDomains) > 0 && !anyDomainMatches(name, ca.PermittedDNSDomains) {
				return &ConstraintError{Code: "UNSPECIFIED", Message: "permitted subtree violation"}
			}
			if anyDomainMatches(name, ca.ExcludedDNSDomains) {
				return &ConstraintError{Code: "UNSPECIFIED", Message: "excluded subtree violation"}
			}
		}
	}
	return nil
}

// anyDomainMatches reports whether name is within one of the dNSName constraints: the
// domain itself or a subdomain, or only subdomains for a constraint with a leading dot.
func anyDomainMatches(name string, constraints []string) bool {
	for _, c := range constraints {
		c = asciiLower(c)
		switch {
		case c == "":
			return true
		case strings.HasPrefix(c, "."):
			if strings.HasSuffix(name, c) {
				return true
			}
		case name == c || strings.HasSuffix(name, "."+c):
			return true
		}
	}
	return false
}
