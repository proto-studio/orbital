package tlsclient

import (
	"crypto/tls"
	"errors"
	"strings"
)

// openSSLCiphers maps OpenSSL's TLS 1.2 cipher names to the suites crypto/tls implements.
var openSSLCiphers = map[string]uint16{
	"ECDHE-ECDSA-AES128-GCM-SHA256": tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-RSA-AES128-GCM-SHA256":   tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-ECDSA-AES256-GCM-SHA384": tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-RSA-AES256-GCM-SHA384":   tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-ECDSA-CHACHA20-POLY1305": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-RSA-CHACHA20-POLY1305":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-ECDSA-AES128-SHA256":     tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-RSA-AES128-SHA256":       tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-ECDSA-AES128-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	"ECDHE-RSA-AES128-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"ECDHE-ECDSA-AES256-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	"ECDHE-RSA-AES256-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"ECDHE-RSA-DES-CBC3-SHA":        tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
	"AES128-GCM-SHA256":             tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	"AES256-GCM-SHA384":             tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	"AES128-SHA256":                 tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
	"AES128-SHA":                    tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	"AES256-SHA":                    tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	"DES-CBC3-SHA":                  tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

// openSSLKeywords are cipher-string words that stand for a group of ciphers. A list that
// uses one keeps crypto/tls's default TLS 1.2 suites, less any named exclusions.
var openSSLKeywords = map[string]bool{
	"DEFAULT": true, "ALL": true, "HIGH": true, "MEDIUM": true, "SECURE128": true, "SECURE192": true,
	"ECDHE": true, "EECDH": true, "ECDH": true, "kECDHE": true, "AESGCM": true, "AES": true,
	"AES128": true, "AES256": true, "CHACHA20": true, "aRSA": true, "aECDSA": true, "SHA256": true,
	"SHA384": true, "TLSv1.2": true, "kRSA": true, "RSA": true, "ECDSA": true, "COMPLEMENTOFDEFAULT": true,
}

var errNoCipherMatch = errors.New("ERR_SSL_NO_CIPHER_MATCH: error:0A0000B9:SSL routines::no cipher match")

// ApplyCiphers applies Node's ciphers option. The OpenSSL names choose the TLS 1.2 ciphers;
// when there are none, only TLS 1.3 is allowed. Names starting with TLS_ are TLS 1.3 suites,
// which crypto/tls does not let a configuration choose, so they are accepted and have no
// effect.
func ApplyCiphers(cfg *tls.Config, ciphers string) error {
	if ciphers == "" {
		return nil
	}
	var tls13, tls12 []string
	for _, name := range strings.Split(ciphers, ":") {
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "TLS_") {
			tls13 = append(tls13, name)
		} else {
			tls12 = append(tls12, name)
		}
	}

	var suites []uint16
	if len(tls12) > 0 {
		keyword := false
		var named, excluded []uint16
		for _, token := range tls12 {
			exclude := false
			switch token[0] {
			case '!', '-':
				exclude = true
				token = token[1:]
			case '+', '@':
				continue
			}
			// OpenSSL joins conditions with '+', as in ECDHE+AESGCM.
			if !exclude && strings.Contains(token, "+") {
				keyword = true
				continue
			}
			if id, ok := openSSLCiphers[token]; ok {
				if exclude {
					excluded = append(excluded, id)
				} else {
					named = append(named, id)
				}
				continue
			}
			if !exclude && openSSLKeywords[token] {
				keyword = true
			}
		}
		switch {
		case len(named) > 0:
			suites = without(named, excluded)
		case keyword:
			for _, suite := range tls.CipherSuites() {
				if !strings.Contains(suite.Name, "_RSA_WITH_") || strings.Contains(suite.Name, "ECDHE") {
					suites = append(suites, suite.ID)
				}
			}
			suites = without(suites, excluded)
		}
		if len(suites) == 0 {
			if len(tls13) == 0 {
				return errNoCipherMatch
			}
		}
	}

	cfg.CipherSuites = suites
	// A list with no TLS 1.2 ciphers leaves only TLS 1.3, which always has its own.
	if len(suites) == 0 && cfg.MinVersion < tls.VersionTLS13 &&
		(cfg.MaxVersion == 0 || cfg.MaxVersion > tls.VersionTLS12) {
		cfg.MinVersion = tls.VersionTLS13
	}
	return nil
}

func without(ids, excluded []uint16) []uint16 {
	var out []uint16
	for _, id := range ids {
		keep := true
		for _, x := range excluded {
			if id == x {
				keep = false
			}
		}
		if keep {
			out = append(out, id)
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
