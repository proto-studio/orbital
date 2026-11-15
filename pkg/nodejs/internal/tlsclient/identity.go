package tlsclient

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net/netip"
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

// subjectAltNames returns the names of the kinds Node prints, in the order the certificate
// lists them.
func subjectAltNames(cert *x509.Certificate) []altName {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		var raw []asn1.RawValue
		if rest, err := asn1.Unmarshal(ext.Value, &raw); err != nil || len(rest) > 0 {
			return nil
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
		return names
	}
	return nil
}

// FormatAltNames returns a certificate's subjectaltname as Node prints it. A name with a
// character that would make the list ambiguous is written as a JSON string after its prefix.
func FormatAltNames(cert *x509.Certificate) string {
	names := subjectAltNames(cert)
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
	var dnsNames []string
	var ips []netip.Addr
	for _, name := range subjectAltNames(cert) {
		switch name.kind {
		case sanDNS:
			dnsNames = append(dnsNames, string(name.value))
		case sanIP:
			if addr, ok := netip.AddrFromSlice(name.value); ok {
				ips = append(ips, addr)
			}
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

func splitHost(host string) []string {
	return strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
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
