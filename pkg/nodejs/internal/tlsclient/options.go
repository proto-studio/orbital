package tlsclient

import (
	"crypto/tls"
	"errors"
	"strings"
)

// suiteInfo is a TLS 1.2 suite crypto/tls implements, with the properties OpenSSL's cipher
// strings select on.
type suiteInfo struct {
	name string
	id   uint16
	kx   string // key exchange: ECDHE or RSA
	auth string // RSA or ECDSA
	enc  string // AES128GCM, AES256GCM, CHACHA20, AES128 or AES256
	mac  string // AEAD, SHA1, SHA256 or SHA384
}

// openSSLCiphers lists, under OpenSSL's names, the TLS 1.2 suites crypto/tls implements that
// OpenSSL 3 offers at its default security level. 3DES is left out, as it is there.
var openSSLCiphers = []suiteInfo{
	{"ECDHE-ECDSA-AES256-GCM-SHA384", tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, "ECDHE", "ECDSA", "AES256GCM", "AEAD"},
	{"ECDHE-RSA-AES256-GCM-SHA384", tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, "ECDHE", "RSA", "AES256GCM", "AEAD"},
	{"ECDHE-ECDSA-CHACHA20-POLY1305", tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, "ECDHE", "ECDSA", "CHACHA20", "AEAD"},
	{"ECDHE-RSA-CHACHA20-POLY1305", tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256, "ECDHE", "RSA", "CHACHA20", "AEAD"},
	{"ECDHE-ECDSA-AES128-GCM-SHA256", tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, "ECDHE", "ECDSA", "AES128GCM", "AEAD"},
	{"ECDHE-RSA-AES128-GCM-SHA256", tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, "ECDHE", "RSA", "AES128GCM", "AEAD"},
	{"ECDHE-ECDSA-AES128-SHA256", tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256, "ECDHE", "ECDSA", "AES128", "SHA256"},
	{"ECDHE-RSA-AES128-SHA256", tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256, "ECDHE", "RSA", "AES128", "SHA256"},
	{"ECDHE-ECDSA-AES256-SHA", tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, "ECDHE", "ECDSA", "AES256", "SHA1"},
	{"ECDHE-RSA-AES256-SHA", tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA, "ECDHE", "RSA", "AES256", "SHA1"},
	{"ECDHE-ECDSA-AES128-SHA", tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, "ECDHE", "ECDSA", "AES128", "SHA1"},
	{"ECDHE-RSA-AES128-SHA", tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA, "ECDHE", "RSA", "AES128", "SHA1"},
	{"AES256-GCM-SHA384", tls.TLS_RSA_WITH_AES_256_GCM_SHA384, "RSA", "RSA", "AES256GCM", "AEAD"},
	{"AES128-GCM-SHA256", tls.TLS_RSA_WITH_AES_128_GCM_SHA256, "RSA", "RSA", "AES128GCM", "AEAD"},
	{"AES128-SHA256", tls.TLS_RSA_WITH_AES_128_CBC_SHA256, "RSA", "RSA", "AES128", "SHA256"},
	{"AES256-SHA", tls.TLS_RSA_WITH_AES_256_CBC_SHA, "RSA", "RSA", "AES256", "SHA1"},
	{"AES128-SHA", tls.TLS_RSA_WITH_AES_128_CBC_SHA, "RSA", "RSA", "AES128", "SHA1"},
}

// cipherGroup returns the suites an OpenSSL cipher-string word selects. A word OpenSSL knows
// that names only suites crypto/tls lacks selects none; a word it doesn't know at all
// reports false.
func cipherGroup(word string) ([]suiteInfo, bool) {
	match := func(keep func(c suiteInfo) bool) []suiteInfo {
		var out []suiteInfo
		for _, c := range openSSLCiphers {
			if keep(c) {
				out = append(out, c)
			}
		}
		return out
	}
	aes := func(c suiteInfo) bool { return strings.HasPrefix(c.enc, "AES") }
	switch word {
	case "ALL", "DEFAULT", "HIGH":
		return match(func(suiteInfo) bool { return true }), true
	case "COMPLEMENTOFDEFAULT", "COMPLEMENTOFALL", "MEDIUM", "LOW", "EXPORT", "NULL", "eNULL", "aNULL", "3DES", "DES",
		"RC4", "MD5", "IDEA", "SEED", "CAMELLIA", "CAMELLIA128", "CAMELLIA256", "ARIA", "ARIA128",
		"ARIA256", "ARIAGCM", "PSK", "kPSK", "aPSK", "SRP", "kSRP", "aSRP", "DSS", "aDSS", "DH",
		"kDH", "kDHE", "kEDH", "EDH", "DHE", "ADH", "AECDH", "aGOST", "kGOST", "GOST", "CCM",
		"CCM8", "AESCCM", "AESCCM8":
		return nil, true
	case "ECDHE", "EECDH", "kECDHE", "kEECDH", "ECDH":
		return match(func(c suiteInfo) bool { return c.kx == "ECDHE" }), true
	case "kRSA", "RSA": // RSA is an alias of kRSA
		return match(func(c suiteInfo) bool { return c.kx == "RSA" }), true
	case "aRSA":
		return match(func(c suiteInfo) bool { return c.auth == "RSA" }), true
	case "aECDSA", "ECDSA":
		return match(func(c suiteInfo) bool { return c.auth == "ECDSA" }), true
	case "AES":
		return match(aes), true
	case "AES128":
		return match(func(c suiteInfo) bool { return strings.HasPrefix(c.enc, "AES128") }), true
	case "AES256":
		return match(func(c suiteInfo) bool { return strings.HasPrefix(c.enc, "AES256") }), true
	case "AESGCM":
		return match(func(c suiteInfo) bool { return strings.HasSuffix(c.enc, "GCM") }), true
	case "CHACHA20":
		return match(func(c suiteInfo) bool { return c.enc == "CHACHA20" }), true
	case "AEAD":
		return match(func(c suiteInfo) bool { return c.mac == "AEAD" }), true
	// The protocol groups hold the suites defined for that version, which for crypto/tls's
	// suites are the CBC ones with a SHA-1 MAC.
	case "SHA1", "SHA", "TLSv1", "TLSv1.0", "SSLv3":
		return match(func(c suiteInfo) bool { return c.mac == "SHA1" }), true
	// The digest groups go by the record MAC, so AEAD suites are in none of them, and
	// crypto/tls has no suite with a SHA-384 MAC.
	case "SHA256":
		return match(func(c suiteInfo) bool { return c.mac == "SHA256" }), true
	case "SHA384":
		return nil, true
	case "TLSv1.2":
		return match(func(c suiteInfo) bool { return c.mac != "SHA1" }), true
	}
	for _, c := range openSSLCiphers {
		if c.name == word {
			return []suiteInfo{c}, true
		}
	}
	return nil, false
}

var errNoCipherMatch = errors.New("ERR_SSL_NO_CIPHER_MATCH: error:0A0000B9:SSL routines::no cipher match")

// ApplyCiphers applies Node's ciphers option. The OpenSSL part chooses the TLS 1.2 suites,
// read as OpenSSL reads it: words are added in order, "-" removes and "!" removes for good,
// and "+" joins words that must all hold. With no TLS 1.2 suites, only TLS 1.3 is allowed.
// Names starting with TLS_ are TLS 1.3 suites, which crypto/tls does not let a configuration
// choose, so they are accepted and have no effect.
func ApplyCiphers(cfg *tls.Config, ciphers string) error {
	if ciphers == "" {
		return nil
	}
	var tls13 []string
	var chosen []suiteInfo
	banned := map[uint16]bool{}
	// OpenSSL also knows suites crypto/tls lacks, such as DHE ones, so a list whose suites
	// were all removed again may still have some there. Only a list that never selected
	// anything is refused.
	selectedAny := false
	for _, token := range strings.FieldsFunc(ciphers, func(r rune) bool { return r == ':' || r == ',' || r == ' ' }) {
		if strings.HasPrefix(token, "TLS_") {
			tls13 = append(tls13, token)
			continue
		}
		op := byte(0)
		switch token[0] {
		case '!', '-', '+':
			op = token[0]
			token = token[1:]
		case '@':
			continue
		}
		var selected []suiteInfo
		known := true
		for i, word := range strings.Split(token, "+") {
			group, ok := cipherGroup(word)
			if !ok {
				known = false
				break
			}
			if i == 0 {
				selected = group
				continue
			}
			var both []suiteInfo
			for _, c := range selected {
				for _, g := range group {
					if c.id == g.id {
						both = append(both, c)
					}
				}
			}
			selected = both
		}
		if !known {
			continue
		}
		switch op {
		case '!':
			for _, c := range selected {
				banned[c.id] = true
			}
			chosen = removeCiphers(chosen, selected)
		case '-':
			chosen = removeCiphers(chosen, selected)
		case '+':
			// Moving suites to the end changes only preference, which crypto/tls decides.
		default:
			if len(selected) > 0 {
				selectedAny = true
			}
			for _, c := range selected {
				if !banned[c.id] && !containsCipher(chosen, c) {
					chosen = append(chosen, c)
				}
			}
		}
	}
	if !selectedAny && len(tls13) == 0 {
		return errNoCipherMatch
	}

	// A non-nil, empty list leaves crypto/tls with no TLS 1.2 suites at all.
	suites := make([]uint16, 0, len(chosen))
	for _, c := range chosen {
		suites = append(suites, c.id)
	}
	cfg.CipherSuites = suites
	if len(suites) == 0 && cfg.MinVersion < tls.VersionTLS13 &&
		(cfg.MaxVersion == 0 || cfg.MaxVersion > tls.VersionTLS12) {
		cfg.MinVersion = tls.VersionTLS13
	}
	return nil
}

func containsCipher(list []suiteInfo, c suiteInfo) bool {
	for _, x := range list {
		if x.id == c.id {
			return true
		}
	}
	return false
}

func removeCiphers(list, remove []suiteInfo) []suiteInfo {
	var out []suiteInfo
	for _, c := range list {
		if !containsCipher(remove, c) {
			out = append(out, c)
		}
	}
	return out
}

var curveNames = map[string]tls.CurveID{
	"P-256": tls.CurveP256, "prime256v1": tls.CurveP256, "secp256r1": tls.CurveP256,
	"P-384": tls.CurveP384, "secp384r1": tls.CurveP384,
	"P-521": tls.CurveP521, "secp521r1": tls.CurveP521,
	"X25519": tls.X25519,
}

var errECDHCurve = errors.New("ERR_CRYPTO_OPERATION_FAILED: Failed to set ECDH curve")

// ApplyECDHCurve applies Node's ecdhCurve option: "auto", or curve names separated by colons.
// X448, which crypto/tls does not implement, is accepted and left out.
func ApplyECDHCurve(cfg *tls.Config, curves string) error {
	if curves == "" || curves == "auto" {
		return nil
	}
	seen := map[tls.CurveID]bool{}
	var out []tls.CurveID
	for _, name := range strings.Split(curves, ":") {
		if name == "X448" {
			continue
		}
		id, ok := curveNames[name]
		if !ok || seen[id] {
			return errECDHCurve
		}
		seen[id] = true
		out = append(out, id)
	}
	cfg.CurvePreferences = out
	return nil
}
