package crypto

import (
	"bytes"
	gocrypto "crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	_ "embed"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strings"
	"time"

	"proto.zip/studio/orbital/pkg/v8"
)

// X509Certificate's native half: parsing, and the fields and checks Node
// prints the way OpenSSL does. x509.js builds the class over these.

//go:embed x509.js
var x509JS string

// --- parsing ----------------------------------------------------------------

type x509ParseError struct{ code, message string }

func (e *x509ParseError) Error() string { return e.message }

var (
	errNoStartLine = &x509ParseError{"ERR_OSSL_PEM_NO_START_LINE", "error:0480006C:PEM routines::no start line"}
	errWrongTag    = &x509ParseError{"ERR_OSSL_ASN1_WRONG_TAG", "error:068000A8:asn1 encoding routines::wrong tag"}
)

// parseCertificateInput reads the first PEM certificate in data or, failing
// that, data as DER, as Node does.
func parseCertificateInput(data []byte) (*x509.Certificate, error) {
	rest := data
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = r
		switch block.Type {
		case "CERTIFICATE", "X509 CERTIFICATE", "TRUSTED CERTIFICATE":
			der := block.Bytes
			// A trusted certificate is the certificate followed by OpenSSL's
			// trust settings; only the first SEQUENCE is the certificate.
			if block.Type == "TRUSTED CERTIFICATE" {
				var first asn1.RawValue
				if _, err := asn1.Unmarshal(der, &first); err == nil {
					der = first.FullBytes
				}
			}
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, errWrongTag
			}
			return cert, nil
		}
	}
	if cert, err := x509.ParseCertificate(data); err == nil {
		return cert, nil
	}
	return nil, errNoStartLine
}

// --- names ------------------------------------------------------------------

var x509ShortNames = map[string]string{
	"2.5.4.3": "CN", "2.5.4.4": "SN", "2.5.4.5": "serialNumber", "2.5.4.6": "C", "2.5.4.7": "L",
	"2.5.4.8": "ST", "2.5.4.9": "street", "2.5.4.10": "O", "2.5.4.11": "OU", "2.5.4.12": "title",
	"2.5.4.13": "description", "2.5.4.15": "businessCategory", "2.5.4.16": "postalAddress",
	"2.5.4.17": "postalCode", "2.5.4.18": "postOfficeBox", "2.5.4.20": "telephoneNumber",
	"2.5.4.41": "name", "2.5.4.42": "GN", "2.5.4.43": "initials", "2.5.4.44": "generationQualifier",
	"2.5.4.45": "x500UniqueIdentifier", "2.5.4.46": "dnQualifier", "2.5.4.54": "dmdName",
	"2.5.4.65": "pseudonym", "2.5.4.72": "role", "2.5.4.97": "organizationIdentifier",
	"1.2.840.113549.1.9.1": "emailAddress", "1.2.840.113549.1.9.2": "unstructuredName",
	"1.2.840.113549.1.9.8":       "unstructuredAddress",
	"0.9.2342.19200300.100.1.25": "DC", "0.9.2342.19200300.100.1.1": "UID",
	"1.3.6.1.4.1.311.60.2.1.1": "jurisdictionL", "1.3.6.1.4.1.311.60.2.1.2": "jurisdictionST",
	"1.3.6.1.4.1.311.60.2.1.3": "jurisdictionC",
}

func attrName(oid asn1.ObjectIdentifier) string {
	if n, ok := x509ShortNames[oid.String()]; ok {
		return n
	}
	return oid.String()
}

// nameAttr is one attribute of a distinguished name, with its value as UTF-8.
type nameAttr struct {
	Key   string `json:"k"`
	Value string `json:"v"`
	rdn   int
	// opaque marks a value that isn't a string type, shown as "#" and
	// its DER in hex; names compare such values as they are.
	opaque bool
}

func parseName(raw []byte) ([]nameAttr, error) {
	var seq []asn1.RawValue
	if _, err := asn1.Unmarshal(raw, &seq); err != nil {
		return nil, err
	}
	var out []nameAttr
	for i, set := range seq {
		var atvs []struct {
			Type  asn1.ObjectIdentifier
			Value asn1.RawValue
		}
		if _, err := asn1.UnmarshalWithParams(set.FullBytes, &atvs, "set"); err != nil {
			return nil, err
		}
		for _, atv := range atvs {
			v, opaque := asn1StringValue(atv.Value)
			out = append(out, nameAttr{Key: attrName(atv.Type), Value: v, rdn: i, opaque: opaque})
		}
	}
	return out, nil
}

// asn1StringValue converts an ASN.1 string to UTF-8 as OpenSSL does. Other
// types come back as "#" and their DER in hex, with opaque set.
func asn1StringValue(v asn1.RawValue) (s string, opaque bool) {
	switch v.Tag {
	case 30: // BMPString: UTF-16BE
		var sb strings.Builder
		for i := 0; i+1 < len(v.Bytes); i += 2 {
			sb.WriteRune(rune(v.Bytes[i])<<8 | rune(v.Bytes[i+1]))
		}
		return sb.String(), false
	case 28: // UniversalString: UTF-32BE
		var sb strings.Builder
		for i := 0; i+3 < len(v.Bytes); i += 4 {
			sb.WriteRune(rune(v.Bytes[i])<<24 | rune(v.Bytes[i+1])<<16 | rune(v.Bytes[i+2])<<8 | rune(v.Bytes[i+3]))
		}
		return sb.String(), false
	case 12: // UTF8String
		return string(v.Bytes), false
	case 19, 22, 26: // Printable, IA5, Visible: ASCII
		return string(v.Bytes), false
	case 20: // T61String, read as Latin-1
		var sb strings.Builder
		for _, b := range v.Bytes {
			sb.WriteRune(rune(b))
		}
		return sb.String(), false
	}
	return "#" + strings.ToUpper(hex.EncodeToString(v.FullBytes)), true
}

// escape2253 escapes a value as OpenSSL's ASN1_STRFLGS_ESC_2253 and
// ESC_CTRL do; other non-ASCII characters stay as UTF-8.
func escape2253(s string, escCtrl bool) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ',' || c == '+' || c == '"' || c == '\\' || c == '<' || c == '>' || c == ';':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case (c == '#' || c == ' ') && i == 0:
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c == ' ' && i == len(s)-1:
			sb.WriteString("\\ ")
		case escCtrl && (c < 0x20 || c == 0x7f):
			fmt.Fprintf(&sb, "\\%02X", c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// formatNameMultiline prints a name one attribute per line, as Node's
// subject and issuer do.
func formatNameMultiline(attrs []nameAttr) string {
	var sb strings.Builder
	for i, a := range attrs {
		if i > 0 {
			if a.rdn == attrs[i-1].rdn {
				sb.WriteString(" + ")
			} else {
				sb.WriteString("\n")
			}
		}
		sb.WriteString(a.Key + "=" + escape2253(a.Value, true))
	}
	return sb.String()
}

// formatNameRFC2253 prints a name in RFC 2253 order (last RDN first), for
// DirName entries.
func formatNameRFC2253(attrs []nameAttr) string {
	var parts []string
	for i := len(attrs) - 1; i >= 0; {
		j := i
		for j > 0 && attrs[j-1].rdn == attrs[i].rdn {
			j--
		}
		var vals []string
		for k := j; k <= i; k++ {
			vals = append(vals, attrs[k].Key+"="+escape2253(attrs[k].Value, false))
		}
		parts = append(parts, strings.Join(vals, "+"))
		i = j - 1
	}
	return strings.Join(parts, ",")
}

// --- alt names --------------------------------------------------------------

// altNameSafe and printAltName follow Node's IsSafeAltName and PrintAltName:
// names that could be misread are JSON-quoted.
func altNameSafe(s string, utf8Mode bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\', ',', '\'':
			return false
		}
		if utf8Mode {
			if c < ' ' || c == 0x7f {
				return false
			}
		} else if c < ' ' || c > '~' {
			return false
		}
	}
	return true
}

func printAltName(s string, utf8Mode bool, prefix string) string {
	if altNameSafe(s, utf8Mode) {
		if prefix != "" {
			return prefix + ":" + s
		}
		return s
	}
	var sb strings.Builder
	sb.WriteByte('"')
	if prefix != "" {
		sb.WriteString(prefix + ":")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			sb.WriteString("\\\\")
		case c == '"':
			sb.WriteString("\\\"")
		case (c >= ' ' && c != ',' && c <= '~') || (utf8Mode && c&0x80 != 0):
			sb.WriteByte(c)
		default:
			// Node prints a signed char here, so bytes from 0x80 come out
			// as ﾀ and up.
			v := uint16(int8(c))
			fmt.Fprintf(&sb, "\\u%04x", v)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

var (
	oidUPN      = "1.3.6.1.4.1.311.20.2.3"
	oidXmpp     = "1.3.6.1.5.5.7.8.5"
	oidSRVName  = "1.3.6.1.5.5.7.8.7"
	oidNAIRealm = "1.3.6.1.5.5.7.8.8"
)

func formatIP(b []byte) string {
	switch len(b) {
	case 4:
		return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
	case 16:
		parts := make([]string, 8)
		for i := 0; i < 8; i++ {
			parts[i] = fmt.Sprintf("%X", uint16(b[2*i])<<8|uint16(b[2*i+1]))
		}
		return strings.Join(parts, ":")
	}
	return "<invalid>"
}

// formatGeneralName prints one GeneralName as Node's PrintGeneralName does.
func formatGeneralName(gn asn1.RawValue) string {
	if gn.Class != asn1.ClassContextSpecific {
		return "<unsupported>"
	}
	switch gn.Tag {
	case 0:
		var on struct {
			Type  asn1.ObjectIdentifier
			Value asn1.RawValue `asn1:"explicit,tag:0"`
		}
		if _, err := asn1.UnmarshalWithParams(gn.FullBytes, &on, "tag:0"); err != nil {
			return "othername:<unsupported>"
		}
		// The value may still be in its [0] EXPLICIT wrapper.
		if on.Value.Class == asn1.ClassContextSpecific && on.Value.Tag == 0 && on.Value.IsCompound {
			var inner asn1.RawValue
			if _, err := asn1.Unmarshal(on.Value.Bytes, &inner); err == nil {
				on.Value = inner
			}
		}
		prefix, unicode := "", true
		switch on.Type.String() {
		case oidUPN:
			prefix = "UPN"
		case oidXmpp:
			prefix = "XmppAddr"
		case oidSRVName:
			prefix, unicode = "SRVName", false
		case oidNAIRealm:
			prefix = "NAIRealm"
		}
		if prefix == "" || (unicode && on.Value.Tag != 12) || (!unicode && on.Value.Tag != 22) {
			return "othername:<unsupported>"
		}
		return "othername:" + printAltName(string(on.Value.Bytes), unicode, prefix)
	case 1:
		return "email:" + printAltName(string(gn.Bytes), false, "")
	case 2:
		return "DNS:" + printAltName(string(gn.Bytes), false, "")
	case 3:
		return "X400Name:<unsupported>"
	case 4:
		attrs, err := parseName(gn.Bytes)
		if err != nil {
			return "DirName:<unsupported>"
		}
		return "DirName:" + printAltName(formatNameRFC2253(attrs), true, "")
	case 5:
		return "EdiPartyName:<unsupported>"
	case 6:
		return "URI:" + printAltName(string(gn.Bytes), false, "")
	case 7:
		return "IP Address:" + formatIP(gn.Bytes)
	case 8:
		oid, err := parseRawOID(gn.Bytes)
		if err != nil {
			return "Registered ID:<unsupported>"
		}
		return "Registered ID:" + oid.String()
	}
	return "<unsupported>"
}

func parseRawOID(content []byte) (asn1.ObjectIdentifier, error) {
	full := append([]byte{0x06, byte(len(content))}, content...)
	var oid asn1.ObjectIdentifier
	_, err := asn1.Unmarshal(full, &oid)
	return oid, err
}

func findExtension(cert *x509.Certificate, oid string) []byte {
	for _, e := range cert.Extensions {
		if e.Id.String() == oid {
			return e.Value
		}
	}
	return nil
}

func generalNames(der []byte) ([]asn1.RawValue, error) {
	var names []asn1.RawValue
	_, err := asn1.Unmarshal(der, &names)
	return names, err
}

func subjectAltName(cert *x509.Certificate) (string, bool) {
	ext := findExtension(cert, "2.5.29.17")
	if ext == nil {
		return "", false
	}
	names, err := generalNames(ext)
	if err != nil {
		return "", false
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = formatGeneralName(n)
	}
	return strings.Join(parts, ", "), true
}

var accessMethodNames = map[string]string{
	"1.3.6.1.5.5.7.48.1": "OCSP",
	"1.3.6.1.5.5.7.48.2": "CA Issuers",
	"1.3.6.1.5.5.7.48.3": "AD Time Stamping",
	"1.3.6.1.5.5.7.48.5": "CA Repository",
}

func infoAccess(cert *x509.Certificate) (string, bool) {
	ext := findExtension(cert, "1.3.6.1.5.5.7.1.1")
	if ext == nil {
		return "", false
	}
	var descs []struct {
		Method   asn1.ObjectIdentifier
		Location asn1.RawValue
	}
	if _, err := asn1.Unmarshal(ext, &descs); err != nil {
		return "", false
	}
	lines := make([]string, len(descs))
	for i, d := range descs {
		name, ok := accessMethodNames[d.Method.String()]
		if !ok {
			name = d.Method.String()
		}
		lines[i] = name + " - " + formatGeneralName(d.Location)
	}
	return strings.Join(lines, "\n"), true
}

// --- the certificate as JSON ------------------------------------------------

func colonHex(b []byte) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	parts := make([]string, 0, len(b))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// asn1TimeString prints a time as OpenSSL's ASN1_TIME_print does.
func asn1TimeString(t time.Time) string {
	t = t.UTC()
	return fmt.Sprintf("%s %2d %02d:%02d:%02d %d GMT", t.Format("Jan"), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Year())
}

func serialHex(n *big.Int) string {
	if n.Sign() == 0 {
		return "0"
	}
	h := strings.ToUpper(hex.EncodeToString(new(big.Int).Abs(n).Bytes()))
	if n.Sign() < 0 {
		return "-" + h
	}
	return h
}

// isCA is OpenSSL's X509_check_ca(cert) == 1: a basicConstraints CA whose
// key usage, if any, allows signing certificates.
func isCA(cert *x509.Certificate) bool {
	if findExtension(cert, "2.5.29.15") != nil && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return false
	}
	return cert.BasicConstraintsValid && cert.IsCA
}

var curveNames = map[string][2]string{"P-256": {"prime256v1", "P-256"}, "P-384": {"secp384r1", "P-384"}, "P-521": {"secp521r1", "P-521"}}

type x509Info struct {
	Subject        string     `json:"subject"`
	Issuer         string     `json:"issuer"`
	SubjectAttrs   []nameAttr `json:"subjectAttrs"`
	IssuerAttrs    []nameAttr `json:"issuerAttrs"`
	SubjectAltName *string    `json:"subjectAltName"`
	InfoAccess     *string    `json:"infoAccess"`
	KeyUsage       []string   `json:"keyUsage"`
	CA             bool       `json:"ca"`
	SerialNumber   string     `json:"serialNumber"`
	ValidFrom      string     `json:"validFrom"`
	ValidTo        string     `json:"validTo"`
	Fingerprint    string     `json:"fingerprint"`
	Fingerprint256 string     `json:"fingerprint256"`
	Fingerprint512 string     `json:"fingerprint512"`
	Raw            string     `json:"raw"`
	SPKI           string     `json:"spki"`
	PEM            string     `json:"pem"`
	// toLegacyObject's key details.
	Modulus   string `json:"modulus,omitempty"`
	Exponent  string `json:"exponent,omitempty"`
	Bits      int    `json:"bits,omitempty"`
	PubKey    string `json:"pubkey,omitempty"`
	ASN1Curve string `json:"asn1Curve,omitempty"`
	NISTCurve string `json:"nistCurve,omitempty"`
}

func describeCertificate(cert *x509.Certificate) (*x509Info, error) {
	subj, err := parseName(cert.RawSubject)
	if err != nil {
		return nil, err
	}
	iss, err := parseName(cert.RawIssuer)
	if err != nil {
		return nil, err
	}
	s1 := sha1.Sum(cert.Raw)
	s256 := sha256.Sum256(cert.Raw)
	s512 := sha512.Sum512(cert.Raw)
	if subj == nil {
		subj = []nameAttr{}
	}
	if iss == nil {
		iss = []nameAttr{}
	}
	info := &x509Info{
		Subject: formatNameMultiline(subj), Issuer: formatNameMultiline(iss),
		SubjectAttrs: subj, IssuerAttrs: iss,
		CA: isCA(cert), SerialNumber: serialHex(cert.SerialNumber),
		ValidFrom: asn1TimeString(cert.NotBefore), ValidTo: asn1TimeString(cert.NotAfter),
		Fingerprint: colonHex(s1[:]), Fingerprint256: colonHex(s256[:]), Fingerprint512: colonHex(s512[:]),
		Raw:  base64.StdEncoding.EncodeToString(cert.Raw),
		SPKI: base64.StdEncoding.EncodeToString(cert.RawSubjectPublicKeyInfo),
		PEM:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})),
	}
	if san, ok := subjectAltName(cert); ok {
		info.SubjectAltName = &san
	}
	if ia, ok := infoAccess(cert); ok {
		info.InfoAccess = &ia
	}
	if ext := findExtension(cert, "2.5.29.37"); ext != nil {
		var oids []asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(ext, &oids); err == nil {
			info.KeyUsage = make([]string, len(oids))
			for i, o := range oids {
				info.KeyUsage[i] = o.String()
			}
		}
	}
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		info.Modulus = strings.ToUpper(hex.EncodeToString(pub.N.Bytes()))
		info.Exponent = fmt.Sprintf("0x%x", pub.E)
		info.Bits = pub.N.BitLen()
		info.PubKey = info.SPKI
	case *ecdsa.PublicKey:
		names, ok := curveNames[pub.Curve.Params().Name]
		if ok {
			info.Bits = pub.Curve.Params().N.BitLen()
			if b, err := pub.ECDH(); err == nil {
				info.PubKey = base64.StdEncoding.EncodeToString(b.Bytes())
			}
			info.ASN1Curve, info.NISTCurve = names[0], names[1]
		}
	}
	return info, nil
}

// x509ParseFunc backs _x509Parse(base64Input): the certificate's details as
// JSON, or a thrown {code, message} JSON for input Node can't read.
func (c *Crypto) x509ParseFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 1 {
		return ctx.Throw("x509: input is required")
	}
	data, _ := base64.StdEncoding.DecodeString(args[0].String())
	cert, err := parseCertificateInput(data)
	if err == nil {
		var details *x509Info
		if details, err = describeCertificate(cert); err == nil {
			return jsonValue(ctx, details)
		}
		err = errWrongTag
	}
	var pe *x509ParseError
	if !errors.As(err, &pe) {
		pe = errWrongTag
	}
	return jsonValue(ctx, map[string]string{"errorCode": pe.code, "errorMessage": pe.message})
}

func jsonValue(ctx *v8.Context, v any) *v8.Value {
	b, _ := json.Marshal(v)
	s, _ := ctx.NewString(string(b))
	return s
}

// --- checks -----------------------------------------------------------------

const (
	checkAlwaysSubject = 1 << iota
	checkNeverSubject
	checkNoWildcards
	checkNoPartialWildcards
	checkMultiLabelWildcards
	checkSingleLabelSubdomains
	checkDotSubdomains
)

func lowerASCII(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func skipPrefix(pattern string, subjectLen int, flags int) string {
	if flags&checkDotSubdomains == 0 {
		return pattern
	}
	p := pattern
	for len(p) > subjectLen && p[0] != 0 {
		if flags&checkSingleLabelSubdomains != 0 && p[0] == '.' {
			break
		}
		p = p[1:]
	}
	if len(p) == subjectLen {
		return p
	}
	return pattern
}

func equalNoCase(pattern, subject string, flags int) bool {
	pattern = skipPrefix(pattern, len(subject), flags)
	if len(pattern) != len(subject) {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == 0 {
			return false
		}
		if lowerASCII(pattern[i]) != lowerASCII(subject[i]) {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// validStar is OpenSSL's valid_star: the index of the one wildcard a
// pattern may use, or -1.
func validStar(p string, flags int) int {
	const labelStart, labelIDNA, labelHyphen = 1, 2, 4
	star, state, dots := -1, labelStart, 0
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*':
			atStart := state&labelStart != 0
			atEnd := i == len(p)-1 || p[i+1] == '.'
			if star >= 0 || state&labelIDNA != 0 || dots > 0 {
				return -1
			}
			if flags&checkNoPartialWildcards != 0 && (!atStart || !atEnd) {
				return -1
			}
			if !atStart && !atEnd {
				return -1
			}
			star = i
			state &^= labelStart
		case isAlnum(c):
			if state&labelStart != 0 && len(p)-i >= 4 && strings.EqualFold(p[i:i+4], "xn--") {
				state |= labelIDNA
			}
			state &^= labelHyphen | labelStart
		case c == '.':
			if state&(labelHyphen|labelStart) != 0 {
				return -1
			}
			state = labelStart
			dots++
		case c == '-':
			if state&labelStart != 0 {
				return -1
			}
			state |= labelHyphen
		default:
			return -1
		}
	}
	if state&(labelStart|labelHyphen) != 0 || dots < 2 {
		return -1
	}
	return star
}

func wildcardMatch(prefix, suffix, subject string, flags int) bool {
	if len(subject) < len(prefix)+len(suffix) {
		return false
	}
	if !equalNoCase(prefix, subject[:len(prefix)], flags) {
		return false
	}
	start, end := len(prefix), len(subject)-len(suffix)
	if !equalNoCase(subject[end:], suffix, flags) {
		return false
	}
	allowMulti, allowIDNA := false, false
	if len(prefix) == 0 && len(suffix) > 0 && suffix[0] == '.' {
		if start == end {
			return false
		}
		allowIDNA = true
		if flags&checkMultiLabelWildcards != 0 {
			allowMulti = true
		}
	}
	if !allowIDNA && len(subject) >= 4 && strings.EqualFold(subject[:4], "xn--") {
		return false
	}
	if end == start+1 && subject[start] == '*' {
		return true
	}
	for i := start; i < end; i++ {
		c := subject[i]
		if !(isAlnum(c) || c == '-' || (allowMulti && c == '.')) {
			return false
		}
	}
	return true
}

func equalWildcard(pattern, subject string, flags int) bool {
	star := -1
	if !(len(subject) > 1 && subject[0] == '.') {
		star = validStar(pattern, flags)
	}
	if star < 0 {
		return equalNoCase(pattern, subject, flags)
	}
	return wildcardMatch(pattern[:star], pattern[star+1:], subject, flags)
}

func equalEmail(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	i := len(a)
	for i > 0 {
		i--
		if a[i] == '@' || b[i] == '@' {
			if !equalNoCase(a[i:], b[i:], 0) {
				return false
			}
			break
		}
	}
	if i == 0 {
		i = len(a)
	}
	return a[:i] == b[:i]
}

// checkName is OpenSSL's do_x509_check for hosts (dns) and emails: the
// matching name from the certificate, or "".
func checkName(cert *x509.Certificate, chk string, dns bool, flags int) string {
	if chk == "" {
		return ""
	}
	tag, attr := 1, "emailAddress"
	equal := func(pattern, subject string) bool { return equalEmail(pattern, subject) }
	if dns {
		tag, attr = 2, "CN"
		if len(chk) > 1 && chk[0] == '.' {
			flags |= checkDotSubdomains
		}
		if flags&checkNoWildcards != 0 {
			equal = func(p, s string) bool { return equalNoCase(p, s, flags) }
		} else {
			equal = func(p, s string) bool { return equalWildcard(p, s, flags) }
		}
	}
	sanPresent := false
	if ext := findExtension(cert, "2.5.29.17"); ext != nil {
		if names, err := generalNames(ext); err == nil {
			for _, n := range names {
				if n.Class != asn1.ClassContextSpecific || n.Tag != tag {
					continue
				}
				sanPresent = true
				if equal(string(n.Bytes), chk) {
					if !dns {
						return chk
					}
					return string(n.Bytes)
				}
			}
		}
	}
	if flags&checkNeverSubject != 0 || (sanPresent && flags&checkAlwaysSubject == 0) {
		return ""
	}
	attrs, err := parseName(cert.RawSubject)
	if err != nil {
		return ""
	}
	for _, a := range attrs {
		if a.Key == attr && equal(a.Value, chk) {
			if !dns {
				return chk
			}
			return a.Value
		}
	}
	return ""
}

func checkIP(cert *x509.Certificate, ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", errors.New("Invalid IP")
	}
	want := []byte(parsed)
	if !strings.Contains(ip, ":") {
		want = parsed.To4()
	}
	if ext := findExtension(cert, "2.5.29.17"); ext != nil {
		if names, err := generalNames(ext); err == nil {
			for _, n := range names {
				if n.Class == asn1.ClassContextSpecific && n.Tag == 7 && bytes.Equal(n.Bytes, want) {
					return ip, nil
				}
			}
		}
	}
	return "", nil
}

// checkIssued is OpenSSL's X509_check_issued(issuer, subject) == X509_V_OK.
func checkIssued(issuer, subject *x509.Certificate) bool {
	if !bytes.Equal(issuer.RawSubject, subject.RawIssuer) {
		a, err1 := parseName(issuer.RawSubject)
		b, err2 := parseName(subject.RawIssuer)
		if err1 != nil || err2 != nil || canonicalName(a) != canonicalName(b) {
			return false
		}
	}
	// The issuer's key must be of a type that makes the subject's signature.
	if !keyFitsSignature(issuer.PublicKey, subject.SignatureAlgorithm) {
		return false
	}
	if len(subject.AuthorityKeyId) > 0 && len(issuer.SubjectKeyId) > 0 && !bytes.Equal(subject.AuthorityKeyId, issuer.SubjectKeyId) {
		return false
	}
	if findExtension(issuer, "2.5.29.15") != nil && issuer.KeyUsage&x509.KeyUsageCertSign == 0 {
		return false
	}
	return true
}

// canonicalName is a name as OpenSSL compares names: each value trimmed,
// with runs of whitespace collapsed and ASCII letters lowercased, and the
// values of each RDN in a fixed order.
func canonicalName(attrs []nameAttr) string {
	var rdns [][][2]string
	for i := 0; i < len(attrs); {
		j := i
		var vals [][2]string
		for ; j < len(attrs) && attrs[j].rdn == attrs[i].rdn; j++ {
			vals = append(vals, [2]string{attrs[j].Key, canonicalValue(attrs[j])})
		}
		sort.Slice(vals, func(a, b int) bool {
			if vals[a][0] != vals[b][0] {
				return vals[a][0] < vals[b][0]
			}
			return vals[a][1] < vals[b][1]
		})
		rdns = append(rdns, vals)
		i = j
	}
	// JSON keeps the structure, so no value can pass for a boundary.
	b, _ := json.Marshal(rdns)
	return string(b)
}

func canonicalValue(a nameAttr) string {
	v := a.Value
	if a.opaque {
		return "#" + v
	}
	isSpace := func(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }
	var sb strings.Builder
	space := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if isSpace(c) {
			space = true
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteByte(lowerASCII(c))
	}
	return sb.String()
}

func keyFitsSignature(pub any, alg x509.SignatureAlgorithm) bool {
	switch alg {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.SHA256WithRSA, x509.SHA384WithRSA, x509.SHA512WithRSA,
		x509.SHA256WithRSAPSS, x509.SHA384WithRSAPSS, x509.SHA512WithRSAPSS:
		_, ok := pub.(*rsa.PublicKey)
		return ok
	case x509.ECDSAWithSHA1, x509.ECDSAWithSHA256, x509.ECDSAWithSHA384, x509.ECDSAWithSHA512:
		_, ok := pub.(*ecdsa.PublicKey)
		return ok
	case x509.PureEd25519:
		_, ok := pub.(ed25519.PublicKey)
		return ok
	}
	return true
}

var sigHashes = map[x509.SignatureAlgorithm]gocrypto.Hash{
	x509.MD5WithRSA: gocrypto.MD5, x509.SHA1WithRSA: gocrypto.SHA1, x509.SHA256WithRSA: gocrypto.SHA256,
	x509.SHA384WithRSA: gocrypto.SHA384, x509.SHA512WithRSA: gocrypto.SHA512,
	x509.ECDSAWithSHA1: gocrypto.SHA1, x509.ECDSAWithSHA256: gocrypto.SHA256,
	x509.ECDSAWithSHA384: gocrypto.SHA384, x509.ECDSAWithSHA512: gocrypto.SHA512,
	x509.SHA256WithRSAPSS: gocrypto.SHA256, x509.SHA384WithRSAPSS: gocrypto.SHA384, x509.SHA512WithRSAPSS: gocrypto.SHA512,
}

// verifyCertSignature reports whether pub signed cert. Unlike Go's
// CheckSignature it accepts SHA-1 and MD5 signatures, as OpenSSL does.
func verifyCertSignature(cert *x509.Certificate, pub any) bool {
	if k, ok := pub.(ed25519.PublicKey); ok {
		return cert.SignatureAlgorithm == x509.PureEd25519 && ed25519.Verify(k, cert.RawTBSCertificate, cert.Signature)
	}
	h, ok := sigHashes[cert.SignatureAlgorithm]
	if !ok || !h.Available() {
		return false
	}
	d := h.New()
	d.Write(cert.RawTBSCertificate)
	sum := d.Sum(nil)
	switch k := pub.(type) {
	case *rsa.PublicKey:
		switch cert.SignatureAlgorithm {
		case x509.SHA256WithRSAPSS, x509.SHA384WithRSAPSS, x509.SHA512WithRSAPSS:
			return rsa.VerifyPSS(k, h, sum, cert.Signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		case x509.ECDSAWithSHA1, x509.ECDSAWithSHA256, x509.ECDSAWithSHA384, x509.ECDSAWithSHA512:
			return false
		}
		return rsa.VerifyPKCS1v15(k, h, sum, cert.Signature) == nil
	case *ecdsa.PublicKey:
		switch cert.SignatureAlgorithm {
		case x509.ECDSAWithSHA1, x509.ECDSAWithSHA256, x509.ECDSAWithSHA384, x509.ECDSAWithSHA512:
			return ecdsa.VerifyASN1(k, sum, cert.Signature)
		}
	}
	return false
}

type publicKeyEqual interface{ Equal(gocrypto.PublicKey) bool }

// x509CheckFunc backs _x509Check(op, certB64, arg, flags). op is "host",
// "email", "ip", "issued" (arg: the other certificate's DER as base64),
// "verify" or "privateKey" (arg: an SPKI as base64). It returns the match or
// "" for host, email and ip, and "true" or "false" for the rest; an invalid
// IP throws "Invalid IP".
func (c *Crypto) x509CheckFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	args := info.Args()
	if len(args) < 4 {
		return ctx.Throw("x509: missing arguments")
	}
	op := args[0].String()
	der, _ := base64.StdEncoding.DecodeString(args[1].String())
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return ctx.Throw("x509: " + err.Error())
	}
	arg := args[2].String()
	flags := int(args[3].Integer())
	out := ""
	boolStr := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	switch op {
	case "host":
		out = checkName(cert, arg, true, flags)
	case "email":
		out = checkName(cert, arg, false, flags)
	case "ip":
		if out, err = checkIP(cert, arg); err != nil {
			return ctx.Throw(err.Error())
		}
	case "issued":
		otherDER, _ := base64.StdEncoding.DecodeString(arg)
		other, perr := x509.ParseCertificate(otherDER)
		out = boolStr(perr == nil && checkIssued(other, cert))
	case "verify", "privateKey":
		spki, _ := base64.StdEncoding.DecodeString(arg)
		pub, perr := x509.ParsePKIXPublicKey(spki)
		if perr != nil {
			out = "false"
		} else if op == "verify" {
			out = boolStr(verifyCertSignature(cert, pub))
		} else {
			k, ok := cert.PublicKey.(publicKeyEqual)
			out = boolStr(ok && k.Equal(pub))
		}
	default:
		return ctx.Throw("x509: unknown check " + op)
	}
	v, _ := ctx.NewString(out)
	return v
}
