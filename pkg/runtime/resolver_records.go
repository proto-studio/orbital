package runtime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// RecordResolver is an optional extension of Resolver for querying DNS records directly,
// as Node's dns.resolve functions do. Without it, only the record types Resolver covers are
// available.
type RecordResolver interface {
	// LookupRecords queries name for rrtype (A, AAAA, ANY, CAA, CNAME, MX, NAPTR, NS, PTR,
	// SOA, SRV or TXT). servers, if not empty, are "host:port" addresses to ask instead of
	// the system's. It returns values in the shape Node reports, with a ttl where Node
	// has one, or a *DNSQueryError.
	LookupRecords(ctx context.Context, name, rrtype string, servers []string) ([]map[string]any, error)
}

// DNSQueryError is a failed DNS query, with the code Node reports for it.
type DNSQueryError struct {
	Code string
}

func (e *DNSQueryError) Error() string { return e.Code + ": DNS query failed" }

var dnsRecordTypes = map[string]dnsmessage.Type{
	"A":     dnsmessage.TypeA,
	"AAAA":  dnsmessage.TypeAAAA,
	"ANY":   dnsmessage.TypeALL,
	"CAA":   dnsmessage.Type(257),
	"CNAME": dnsmessage.TypeCNAME,
	"MX":    dnsmessage.TypeMX,
	"NAPTR": dnsmessage.Type(35),
	"NS":    dnsmessage.TypeNS,
	"PTR":   dnsmessage.TypePTR,
	"SOA":   dnsmessage.TypeSOA,
	"SRV":   dnsmessage.TypeSRV,
	"TXT":   dnsmessage.TypeTXT,
}

// SystemDNSServers returns the "host:port" addresses of the system's DNS servers.
func SystemDNSServers() []string {
	file, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer file.Close()

	var servers []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" && net.ParseIP(fields[1]) != nil {
			servers = append(servers, net.JoinHostPort(fields[1], "53"))
		}
	}
	return servers
}

type realRecords struct{}

// RecordsFor returns how resolver queries records directly, or nil if it does not.
// RealResolver offers this only as itself: a type that embeds it to restrict lookups must
// not inherit a way around those restrictions.
func RecordsFor(resolver Resolver) RecordResolver {
	if _, real := resolver.(*RealResolver); real {
		return realRecords{}
	}
	if records, ok := resolver.(RecordResolver); ok {
		return records
	}
	return nil
}

// LookupRecords queries DNS servers directly.
func (realRecords) LookupRecords(ctx context.Context, name, rrtype string, servers []string) ([]map[string]any, error) {
	qtype, ok := dnsRecordTypes[rrtype]
	if !ok {
		return nil, &DNSQueryError{Code: "ENOTIMP"}
	}
	if len(servers) == 0 {
		servers = SystemDNSServers()
	}
	if len(servers) == 0 {
		return nil, &DNSQueryError{Code: "ECONNREFUSED"}
	}

	fqdn := name
	if !strings.HasSuffix(fqdn, ".") {
		fqdn += "."
	}
	qname, err := dnsmessage.NewName(fqdn)
	if err != nil {
		return nil, &DNSQueryError{Code: "EBADNAME"}
	}

	var lastErr error
	for _, server := range servers {
		message, err := exchangeDNS(ctx, server, qname, qtype)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		return decodeDNSResponse(message, qtype, rrtype)
	}
	return nil, lastErr
}

// dnsMessageID is random, because it and the source port are all that stop an off-path
// sender forging a reply.
func dnsMessageID() (uint16, error) {
	var id [2]byte
	if _, err := rand.Read(id[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(id[:]), nil
}

func exchangeDNS(ctx context.Context, server string, name dnsmessage.Name, qtype dnsmessage.Type) (*dnsmessage.Message, error) {
	id, err := dnsMessageID()
	if err != nil {
		return nil, err
	}
	query := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: qtype, Class: dnsmessage.ClassINET}},
	}
	packed, err := query.Pack()
	if err != nil {
		return nil, &DNSQueryError{Code: "EBADQUERY"}
	}

	message, err := exchangeDNSOver(ctx, "udp", server, packed, id)
	if err == nil && message.Truncated {
		message, err = exchangeDNSOver(ctx, "tcp", server, packed, id)
	}
	if err != nil {
		return nil, err
	}
	// A reply must answer the question that was asked.
	if len(message.Questions) != 1 || message.Questions[0].Type != qtype ||
		!strings.EqualFold(message.Questions[0].Name.String(), name.String()) {
		return nil, &DNSQueryError{Code: "EBADRESP"}
	}
	return message, nil
}

func exchangeDNSOver(ctx context.Context, network, server string, packed []byte, id uint16) (*dnsmessage.Message, error) {
	const attemptTimeout = 5 * time.Second
	const attempts = 2

	var lastErr error = &DNSQueryError{Code: "ETIMEOUT"}
	for attempt := 0; attempt < attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		message, err := exchangeDNSOnce(attemptCtx, network, server, packed, id)
		cancel()
		if err == nil {
			return message, nil
		}
		lastErr = err
		var opErr *net.OpError
		if errors.As(err, &opErr) && !opErr.Timeout() {
			return nil, &DNSQueryError{Code: "ECONNREFUSED"}
		}
		if ctx.Err() != nil {
			return nil, &DNSQueryError{Code: "ECANCELLED"}
		}
	}
	var netErr net.Error
	if errors.As(lastErr, &netErr) && netErr.Timeout() {
		return nil, &DNSQueryError{Code: "ETIMEOUT"}
	}
	return nil, lastErr
}

func exchangeDNSOnce(ctx context.Context, network, server string, packed []byte, id uint16) (*dnsmessage.Message, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	var reply []byte
	if network == "tcp" {
		framed := binary.BigEndian.AppendUint16(nil, uint16(len(packed)))
		if _, err := conn.Write(append(framed, packed...)); err != nil {
			return nil, err
		}
		reader := bufio.NewReader(conn)
		var size uint16
		if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
			return nil, err
		}
		reply = make([]byte, size)
		if _, err := readFull(reader, reply); err != nil {
			return nil, err
		}
	} else {
		if _, err := conn.Write(packed); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		// A connected UDP socket only hears from the server. A reply with the wrong id
		// is still ignored, so an off-path sender cannot answer for it by guessing a port.
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return nil, err
			}
			if n >= 2 && binary.BigEndian.Uint16(buf) == id {
				reply = buf[:n]
				break
			}
		}
	}

	var message dnsmessage.Message
	if err := message.Unpack(reply); err != nil {
		return nil, &DNSQueryError{Code: "EBADRESP"}
	}
	if message.ID != id {
		return nil, &DNSQueryError{Code: "EBADRESP"}
	}
	return &message, nil
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func decodeDNSResponse(message *dnsmessage.Message, qtype dnsmessage.Type, rrtype string) ([]map[string]any, error) {
	switch message.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return nil, &DNSQueryError{Code: "ENOTFOUND"}
	case dnsmessage.RCodeServerFailure:
		return nil, &DNSQueryError{Code: "ESERVFAIL"}
	case dnsmessage.RCodeRefused:
		return nil, &DNSQueryError{Code: "EREFUSED"}
	case dnsmessage.RCodeNotImplemented:
		return nil, &DNSQueryError{Code: "ENOTIMP"}
	case dnsmessage.RCodeFormatError:
		return nil, &DNSQueryError{Code: "EFORMERR"}
	default:
		return nil, &DNSQueryError{Code: "ESERVFAIL"}
	}

	var out []map[string]any
	for _, answer := range message.Answers {
		if qtype != dnsmessage.TypeALL && answer.Header.Type != qtype {
			continue
		}
		record := decodeDNSRecord(answer)
		if record != nil {
			out = append(out, record)
		}
	}
	if len(out) == 0 {
		return nil, &DNSQueryError{Code: "ENODATA"}
	}
	return out, nil
}

func dnsName(name dnsmessage.Name) string {
	return strings.TrimSuffix(name.String(), ".")
}

func decodeDNSRecord(answer dnsmessage.Resource) map[string]any {
	ttl := int(answer.Header.TTL)
	switch body := answer.Body.(type) {
	case *dnsmessage.AResource:
		return map[string]any{"type": "A", "address": net.IP(body.A[:]).String(), "ttl": ttl}
	case *dnsmessage.AAAAResource:
		return map[string]any{"type": "AAAA", "address": net.IP(body.AAAA[:]).String(), "ttl": ttl}
	case *dnsmessage.CNAMEResource:
		return map[string]any{"type": "CNAME", "value": dnsName(body.CNAME)}
	case *dnsmessage.NSResource:
		return map[string]any{"type": "NS", "value": dnsName(body.NS)}
	case *dnsmessage.PTRResource:
		return map[string]any{"type": "PTR", "value": dnsName(body.PTR)}
	case *dnsmessage.MXResource:
		return map[string]any{"type": "MX", "exchange": dnsName(body.MX), "priority": int(body.Pref)}
	case *dnsmessage.TXTResource:
		return map[string]any{"type": "TXT", "entries": body.TXT}
	case *dnsmessage.SRVResource:
		return map[string]any{"type": "SRV", "name": dnsName(body.Target), "port": int(body.Port),
			"priority": int(body.Priority), "weight": int(body.Weight)}
	case *dnsmessage.SOAResource:
		return map[string]any{"type": "SOA", "nsname": dnsName(body.NS), "hostmaster": dnsName(body.MBox),
			"serial": body.Serial, "refresh": body.Refresh, "retry": body.Retry, "expire": body.Expire, "minttl": body.MinTTL}
	case *dnsmessage.UnknownResource:
		return decodeUnknownDNSRecord(answer.Header.Type, body.Data)
	}
	return nil
}

func decodeUnknownDNSRecord(rrtype dnsmessage.Type, data []byte) map[string]any {
	switch rrtype {
	case dnsmessage.Type(257):
		if len(data) < 2 || int(data[1]) > len(data)-2 {
			return nil
		}
		tagEnd := 2 + int(data[1])
		record := map[string]any{"type": "CAA", "critical": int(data[0])}
		record[string(data[2:tagEnd])] = string(data[tagEnd:])
		return record
	case dnsmessage.Type(35):
		return decodeNAPTR(data)
	}
	return nil
}

func decodeNAPTR(data []byte) map[string]any {
	if len(data) < 4 {
		return nil
	}
	order := binary.BigEndian.Uint16(data)
	preference := binary.BigEndian.Uint16(data[2:])
	rest := data[4:]
	texts := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		if len(rest) == 0 || int(rest[0]) >= len(rest) {
			return nil
		}
		texts = append(texts, string(rest[1:1+int(rest[0])]))
		rest = rest[1+int(rest[0]):]
	}
	var labels []string
	for len(rest) > 0 && rest[0] != 0 {
		size := int(rest[0])
		if size >= len(rest) {
			return nil
		}
		labels = append(labels, string(rest[1:1+size]))
		rest = rest[1+size:]
	}
	return map[string]any{"type": "NAPTR", "flags": texts[0], "service": texts[1], "regexp": texts[2],
		"replacement": strings.Join(labels, "."), "order": order, "preference": preference}
}

// LookupServiceName returns the name of a well-known port, or the port number as text.
func LookupServiceName(port int, protocol string) string {
	if name, ok := wellKnownServices[port]; ok {
		return name
	}
	return strconv.Itoa(port)
}

var wellKnownServices = map[int]string{
	7: "echo", 20: "ftp-data", 21: "ftp", 22: "ssh", 23: "telnet", 25: "smtp", 53: "domain",
	67: "bootps", 68: "bootpc", 69: "tftp", 80: "http", 88: "kerberos", 110: "pop3", 119: "nntp",
	123: "ntp", 143: "imap", 161: "snmp", 389: "ldap", 443: "https", 465: "smtps", 514: "syslog",
	587: "submission", 636: "ldaps", 853: "domain-s", 989: "ftps-data", 990: "ftps", 993: "imaps",
	995: "pop3s", 1433: "ms-sql-s", 3306: "mysql", 3389: "ms-wbt-server", 5432: "postgresql",
	6379: "redis", 8080: "http-alt",
}
