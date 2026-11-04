// Package tlsclient holds the certificate verification shared by node:tls and
// node:http2, so both apply Node's options and report Node's error codes.
package tlsclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ErrNotVerified means a socket connected without running the Config passed to it, so
// nothing checked the peer's certificate.
var ErrNotVerified = errors.New("ERR_TLS_CERT_NOT_VERIFIED: the socket factory did not apply the TLS configuration, " +
	"so the server's certificate was not verified")

// Options are Node's client TLS options, as passed from JavaScript.
type Options struct {
	RejectUnauthorized bool `json:"rejectUnauthorized"`
	// CA replaces the default roots unless it is nil.
	CA         []string `json:"ca"`
	Cert       string   `json:"cert"`
	Key        string   `json:"key"`
	Servername string   `json:"servername"`
	// ScriptChecksIdentity leaves the hostname check to a JavaScript
	// checkServerIdentity callback. The chain is still verified here.
	ScriptChecksIdentity bool `json:"scriptChecksIdentity"`
}

// Verifier checks the peer of one connection during its handshake and records the result.
type Verifier struct {
	// Config is the configuration to hand to the handshake.
	Config *tls.Config

	reject               bool
	scriptChecksIdentity bool

	mu      sync.Mutex
	ran     bool
	authErr error
	peer    []*x509.Certificate
}

// New builds the verifier for a connection to host. A non-nil base is the embedder's
// configuration and is authoritative: opts cannot change it. Otherwise opts apply as
// they do in Node.
func New(base *tls.Config, host string, opts Options) (*Verifier, error) {
	v := &Verifier{reject: true}
	name := host

	var cfg *tls.Config
	if base != nil {
		cfg = base.Clone()
		v.reject = !base.InsecureSkipVerify
		if cfg.ServerName != "" {
			name = cfg.ServerName
		}
	} else {
		cfg = &tls.Config{}
		v.reject = opts.RejectUnauthorized
		v.scriptChecksIdentity = opts.ScriptChecksIdentity
		if opts.Servername != "" {
			name = opts.Servername
		}
		if opts.CA != nil {
			// As in Node, text that holds no certificate adds nothing, so it is the
			// verification that fails. A non-nil pool never falls back to system roots.
			pool := x509.NewCertPool()
			for _, pem := range opts.CA {
				pool.AppendCertsFromPEM([]byte(pem))
			}
			cfg.RootCAs = pool
		}
		// As in Node, a certificate without its key, or the reverse, is not an error.
		// The connection just presents no client certificate.
		if opts.Cert != "" && opts.Key != "" {
			cert, err := tls.X509KeyPair([]byte(opts.Cert), []byte(opts.Key))
			if err != nil {
				return nil, fmt.Errorf("ERR_OSSL_X509_KEY_VALUES_MISMATCH: %w", err)
			}
			cfg.Certificates = []tls.Certificate{cert}
		}
	}
	cfg.ServerName = name

	// crypto/tls verifies the hostname before the chain and stops the handshake on any
	// failure. Node reports the chain first and can continue with the failure recorded,
	// so verification runs below instead. It covers every handshake, resumed ones included.
	cfg.InsecureSkipVerify = true
	next := cfg.VerifyConnection
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		err := v.verify(cfg, cs)
		v.mu.Lock()
		v.ran = true
		v.authErr = err
		v.peer = cs.PeerCertificates
		v.mu.Unlock()
		if err != nil && v.reject {
			return err
		}
		if next != nil {
			return next(cs)
		}
		return nil
	}
	v.Config = cfg
	return v, nil
}

func (v *Verifier) verify(cfg *tls.Config, cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server provided no certificate")
	}
	leaf := cs.PeerCertificates[0]
	opts := x509.VerifyOptions{Roots: cfg.RootCAs, Intermediates: x509.NewCertPool()}
	if cfg.Time != nil {
		opts.CurrentTime = cfg.Time()
	}
	for _, cert := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	_, err := leaf.Verify(opts)
	if err == nil && !v.scriptChecksIdentity {
		err = leaf.VerifyHostname(cfg.ServerName)
	}
	if err != nil {
		return &tls.CertificateVerificationError{UnverifiedCertificates: cs.PeerCertificates, Err: err}
	}
	return nil
}

// Check returns ErrNotVerified if the connection must be refused because the socket
// connected without running Config.
func (v *Verifier) Check() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.ran && v.reject {
		return ErrNotVerified
	}
	return nil
}

// Authorized reports whether the peer's certificate was verified successfully.
func (v *Verifier) Authorized() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ran && v.authErr == nil
}

// AuthorizationError returns the failure recorded for the peer's certificate, if any.
func (v *Verifier) AuthorizationError() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.authErr
}

// ScriptChecksIdentity reports whether JavaScript must run the hostname check. It is
// false once the chain has failed, because Node does not call the callback then.
func (v *Verifier) ScriptChecksIdentity() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.scriptChecksIdentity && v.authErr == nil
}

// PeerCertificates returns the certificates the peer presented.
func (v *Verifier) PeerCertificates() []*x509.Certificate {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.peer
}

var codePrefix = regexp.MustCompile(`^([A-Z][A-Z0-9_]+): (.*)$`)

// Describe converts a connection failure to the message and code Node reports. An error
// with a "CODE: text" message, such as a sandbox denial, keeps its text and reports CODE.
func Describe(err error, host string, port int) (string, string) {
	var (
		dnsErr  *net.DNSError
		hostErr x509.HostnameError
		certErr x509.CertificateInvalidError
		verify  *tls.CertificateVerificationError
		errno   syscall.Errno
	)
	switch {
	case errors.As(err, &hostErr):
		return altnameMessage(hostErr), "ERR_TLS_CERT_ALTNAME_INVALID"
	case errors.As(err, &certErr):
		if certErr.Reason == x509.Expired {
			return "certificate has expired", "CERT_HAS_EXPIRED"
		}
		return certErr.Error(), "ERR_TLS_CERT_INVALID"
	case errors.As(err, &verify):
		// Any other verification failure is an untrusted chain. The concrete error
		// differs between Go's verifier and the platform verifiers.
		if n := len(verify.UnverifiedCertificates); n > 0 {
			last := verify.UnverifiedCertificates[n-1]
			if bytes.Equal(last.RawIssuer, last.RawSubject) {
				if n == 1 {
					return "self-signed certificate", "DEPTH_ZERO_SELF_SIGNED_CERT"
				}
				return "self-signed certificate in certificate chain", "SELF_SIGNED_CERT_IN_CHAIN"
			}
		}
		return "unable to verify the first certificate", "UNABLE_TO_VERIFY_LEAF_SIGNATURE"
	case errors.As(err, &dnsErr):
		return "getaddrinfo ENOTFOUND " + host, "ENOTFOUND"
	case errors.As(err, &errno):
		code := errnoName(errno)
		return "connect " + code + " " + net.JoinHostPort(host, strconv.Itoa(port)), code
	case errors.Is(err, context.Canceled):
		return "The operation was aborted", "ABORT_ERR"
	case errors.Is(err, context.DeadlineExceeded):
		return "The operation timed out", "ETIMEDOUT"
	}
	if m := codePrefix.FindStringSubmatch(err.Error()); m != nil {
		return err.Error(), m[1]
	}
	return err.Error(), ""
}

// altnameMessage matches the text of Node's checkServerIdentity.
func altnameMessage(err x509.HostnameError) string {
	names := make([]string, 0, len(err.Certificate.DNSNames)+len(err.Certificate.IPAddresses))
	for _, name := range err.Certificate.DNSNames {
		names = append(names, "DNS:"+name)
	}
	for _, ip := range err.Certificate.IPAddresses {
		names = append(names, "IP Address:"+ip.String())
	}
	altnames := strings.Join(names, ", ")
	if net.ParseIP(err.Host) != nil {
		return "Hostname/IP does not match certificate's altnames: IP: " + err.Host + " is not in the cert's list: " + altnames
	}
	return "Hostname/IP does not match certificate's altnames: Host: " + err.Host + ". is not in the cert's altnames: " + altnames
}

func errnoName(errno syscall.Errno) string {
	switch errno {
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ENETUNREACH:
		return "ENETUNREACH"
	case syscall.EADDRNOTAVAIL:
		return "EADDRNOTAVAIL"
	case syscall.EPIPE:
		return "EPIPE"
	}
	return errno.Error()
}
