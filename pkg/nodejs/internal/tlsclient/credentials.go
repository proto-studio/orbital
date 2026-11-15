package tlsclient

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"unicode/utf16"
)

// PrivateKey is one entry of Node's key option: PEM text and the passphrase that decrypts
// it, if any.
type PrivateKey struct {
	PEM        string  `json:"pem"`
	Passphrase *string `json:"passphrase"`
}

// PFX is one entry of Node's pfx option: base64 PKCS#12 data and its passphrase.
type PFX struct {
	Buf        string  `json:"buf"`
	Passphrase *string `json:"passphrase"`
}

// The errors Node reports for credentials it cannot load.
var (
	errBadDecrypt       = errors.New("ERR_OSSL_BAD_DECRYPT: error:1C800064:Provider routines::bad decrypt")
	errMACVerify        = errors.New("mac verify failure")
	errPFXTruncated     = errors.New("not enough data")
	errPFXUnsupported   = errors.New("ERR_CRYPTO_UNSUPPORTED_OPERATION: Unsupported PKCS12 PFX data")
	errKeyValues        = "ERR_OSSL_X509_KEY_VALUES_MISMATCH"
	errNoPFXCertificate = errors.New("ERR_CRYPTO_UNSUPPORTED_OPERATION: Unsupported PKCS12 PFX data")
)

// LoadCertificates builds the certificates for Node's cert, key and pfx options. Certificates
// and keys pair up in order. Encrypted keys are decrypted with their passphrase.
func LoadCertificates(certs []string, keys []PrivateKey, pfx []PFX) ([]tls.Certificate, error) {
	var out []tls.Certificate
	for _, p := range pfx {
		data, err := base64.StdEncoding.DecodeString(p.Buf)
		if err != nil {
			return nil, errPFXTruncated
		}
		cert, err := DecodePFX(data, p.Passphrase)
		if err != nil {
			return nil, err
		}
		out = append(out, cert)
	}
	for i := range certs {
		if i >= len(keys) {
			break
		}
		keyPEM, err := DecryptKeyPEM([]byte(keys[i].PEM), keys[i].Passphrase)
		if err != nil {
			return nil, err
		}
		cert, err := tls.X509KeyPair([]byte(certs[i]), keyPEM)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", errKeyValues, err)
		}
		out = append(out, cert)
	}
	return out, nil
}

// DecryptKeyPEM returns key with any encrypted private key in it decrypted, as PEM that
// crypto/tls reads. An unencrypted key is returned unchanged.
func DecryptKeyPEM(key []byte, passphrase *string) ([]byte, error) {
	rest := key
	var out []byte
	changed := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "ENCRYPTED PRIVATE KEY":
			der, err := decryptPKCS8(block.Bytes, passphrase)
			if err != nil {
				return nil, err
			}
			out = append(out, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})...)
			changed = true
		//nolint:staticcheck // Legacy PEM encryption is what OpenSSL's "traditional" keys use.
		case x509.IsEncryptedPEMBlock(block):
			if passphrase == nil {
				return nil, errBadDecrypt
			}
			//nolint:staticcheck
			der, err := x509.DecryptPEMBlock(block, []byte(*passphrase))
			if err != nil {
				return nil, errBadDecrypt
			}
			out = append(out, pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: der})...)
			changed = true
		default:
			out = append(out, pem.EncodeToMemory(block)...)
		}
	}
	if !changed {
		return key, nil
	}
	return out, nil
}

var (
	oidPBES2     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidHMACSHA1  = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACSHA2s = map[string]func() hash.Hash{
		"1.2.840.113549.2.8":  sha256.New224,
		"1.2.840.113549.2.9":  sha256.New,
		"1.2.840.113549.2.10": sha512.New384,
		"1.2.840.113549.2.11": sha512.New,
	}
	oidAES128CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
	oidDES3CBC   = asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 7}
)

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type encryptedPrivateKeyInfo struct {
	Algorithm     algorithmIdentifier
	EncryptedData []byte
}

type pbes2Params struct {
	KeyDerivationFunc algorithmIdentifier
	EncryptionScheme  algorithmIdentifier
}

type pbkdf2Params struct {
	Salt           []byte
	IterationCount int
	KeyLength      int                 `asn1:"optional"`
	PRF            algorithmIdentifier `asn1:"optional"`
}

func decryptPKCS8(der []byte, passphrase *string) ([]byte, error) {
	var info encryptedPrivateKeyInfo
	if rest, err := asn1.Unmarshal(der, &info); err != nil || len(rest) > 0 {
		return nil, errBadDecrypt
	}
	if passphrase == nil {
		return nil, errBadDecrypt
	}
	plain, err := decryptPBES2(info.Algorithm, info.EncryptedData, *passphrase)
	if errors.Is(err, errPFXUnsupported) {
		return nil, errBadDecrypt
	}
	return plain, err
}

// decryptPBES2 decrypts data encrypted with PBES2 (PBKDF2 and a CBC cipher).
func decryptPBES2(alg algorithmIdentifier, data []byte, passphrase string) ([]byte, error) {
	if !alg.Algorithm.Equal(oidPBES2) {
		return nil, errPFXUnsupported
	}
	var params pbes2Params
	if _, err := asn1.Unmarshal(alg.Parameters.FullBytes, &params); err != nil {
		return nil, errPFXUnsupported
	}
	if !params.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) {
		return nil, errPFXUnsupported
	}
	var kdf pbkdf2Params
	if _, err := asn1.Unmarshal(params.KeyDerivationFunc.Parameters.FullBytes, &kdf); err != nil {
		return nil, errPFXUnsupported
	}
	prf := sha1.New
	if len(kdf.PRF.Algorithm) > 0 && !kdf.PRF.Algorithm.Equal(oidHMACSHA1) {
		h, ok := oidHMACSHA2s[kdf.PRF.Algorithm.String()]
		if !ok {
			return nil, errPFXUnsupported
		}
		prf = h
	}

	var keyLen int
	var newCipher func([]byte) (cipher.Block, error)
	switch scheme := params.EncryptionScheme.Algorithm; {
	case scheme.Equal(oidAES128CBC):
		keyLen, newCipher = 16, aes.NewCipher
	case scheme.Equal(oidAES192CBC):
		keyLen, newCipher = 24, aes.NewCipher
	case scheme.Equal(oidAES256CBC):
		keyLen, newCipher = 32, aes.NewCipher
	case scheme.Equal(oidDES3CBC):
		keyLen, newCipher = 24, des.NewTripleDESCipher
	default:
		return nil, errPFXUnsupported
	}
	var iv []byte
	if _, err := asn1.Unmarshal(params.EncryptionScheme.Parameters.FullBytes, &iv); err != nil {
		return nil, errPFXUnsupported
	}
	if kdf.IterationCount <= 0 || kdf.IterationCount > 10_000_000 {
		return nil, errPFXUnsupported
	}
	key, err := pbkdf2.Key(prf, passphrase, kdf.Salt, kdf.IterationCount, keyLen)
	if err != nil {
		return nil, errPFXUnsupported
	}
	block, err := newCipher(key)
	if err != nil {
		return nil, errPFXUnsupported
	}
	if len(iv) != block.BlockSize() || len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return nil, errBadDecrypt
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	return unpad(plain, block.BlockSize())
}

func unpad(data []byte, blockSize int) ([]byte, error) {
	n := int(data[len(data)-1])
	if n == 0 || n > blockSize || n > len(data) {
		return nil, errBadDecrypt
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return nil, errBadDecrypt
		}
	}
	return data[:len(data)-n], nil
}

// PKCS#12, the subset OpenSSL 3 writes by default and Node 20 reads: PBES2-encrypted
// contents with an HMAC over them.

var (
	oidDataContent      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedContent = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	oidKeyBag           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 1}
	oidShroudedKeyBag   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidCertBag          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidX509Certificate  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
	macHashes           = map[string]struct {
		h func() hash.Hash
		v int
	}{
		"1.3.14.3.2.26":          {sha1.New, 64},
		"2.16.840.1.101.3.4.2.1": {sha256.New, 64},
		"2.16.840.1.101.3.4.2.2": {sha512.New384, 128},
		"2.16.840.1.101.3.4.2.3": {sha512.New, 128},
	}
)

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"tag:0,explicit,optional"`
}

type pfxPDU struct {
	Version  int
	AuthSafe contentInfo
	MacData  macData `asn1:"optional"`
}

type digestInfo struct {
	Algorithm algorithmIdentifier
	Digest    []byte
}

type macData struct {
	Mac        digestInfo
	MacSalt    []byte
	Iterations int `asn1:"optional,default:1"`
}

type encryptedData struct {
	Version              int
	EncryptedContentInfo encryptedContentInfo
}

type encryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm algorithmIdentifier
	EncryptedContent           []byte `asn1:"tag:0,optional"`
}

type safeBag struct {
	ID         asn1.ObjectIdentifier
	Value      asn1.RawValue `asn1:"tag:0,explicit"`
	Attributes asn1.RawValue `asn1:"set,optional"`
}

type certBag struct {
	ID   asn1.ObjectIdentifier
	Data []byte `asn1:"tag:0,explicit"`
}

// DecodePFX reads a PKCS#12 file into a certificate and its key, with any other
// certificates in it as the rest of the chain.
func DecodePFX(data []byte, passphrase *string) (tls.Certificate, error) {
	var pfx pfxPDU
	rest, err := asn1.Unmarshal(data, &pfx)
	if err != nil || len(rest) > 0 {
		return tls.Certificate{}, errPFXTruncated
	}
	if !pfx.AuthSafe.ContentType.Equal(oidDataContent) {
		return tls.Certificate{}, errPFXUnsupported
	}
	var authSafe []byte
	if _, err := asn1.Unmarshal(pfx.AuthSafe.Content.Bytes, &authSafe); err != nil {
		return tls.Certificate{}, errPFXTruncated
	}

	password := ""
	if passphrase != nil {
		password = *passphrase
	}
	if len(pfx.MacData.Mac.Digest) > 0 {
		if err := verifyPFXMAC(pfx.MacData, authSafe, password, passphrase == nil); err != nil {
			return tls.Certificate{}, err
		}
	}

	var contents []contentInfo
	if _, err := asn1.Unmarshal(authSafe, &contents); err != nil {
		return tls.Certificate{}, errPFXTruncated
	}
	var certs []*x509.Certificate
	var keys []crypto.PrivateKey
	for _, ci := range contents {
		var bagsDER []byte
		switch {
		case ci.ContentType.Equal(oidDataContent):
			if _, err := asn1.Unmarshal(ci.Content.Bytes, &bagsDER); err != nil {
				return tls.Certificate{}, errPFXTruncated
			}
		case ci.ContentType.Equal(oidEncryptedContent):
			var ed encryptedData
			if _, err := asn1.Unmarshal(ci.Content.Bytes, &ed); err != nil {
				return tls.Certificate{}, errPFXTruncated
			}
			bagsDER, err = decryptPBES2(ed.EncryptedContentInfo.ContentEncryptionAlgorithm,
				ed.EncryptedContentInfo.EncryptedContent, password)
			if err != nil {
				return tls.Certificate{}, err
			}
		default:
			return tls.Certificate{}, errPFXUnsupported
		}
		var bags []safeBag
		if _, err := asn1.Unmarshal(bagsDER, &bags); err != nil {
			return tls.Certificate{}, errPFXTruncated
		}
		for _, bag := range bags {
			switch {
			case bag.ID.Equal(oidCertBag):
				var cb certBag
				if _, err := asn1.Unmarshal(bag.Value.Bytes, &cb); err != nil || !cb.ID.Equal(oidX509Certificate) {
					continue
				}
				cert, err := x509.ParseCertificate(cb.Data)
				if err != nil {
					return tls.Certificate{}, errPFXUnsupported
				}
				certs = append(certs, cert)
			case bag.ID.Equal(oidKeyBag):
				key, err := x509.ParsePKCS8PrivateKey(bag.Value.Bytes)
				if err != nil {
					return tls.Certificate{}, errPFXUnsupported
				}
				keys = append(keys, key)
			case bag.ID.Equal(oidShroudedKeyBag):
				var info encryptedPrivateKeyInfo
				if _, err := asn1.Unmarshal(bag.Value.Bytes, &info); err != nil {
					return tls.Certificate{}, errPFXTruncated
				}
				der, err := decryptPBES2(info.Algorithm, info.EncryptedData, password)
				if err != nil {
					return tls.Certificate{}, err
				}
				key, err := x509.ParsePKCS8PrivateKey(der)
				if err != nil {
					return tls.Certificate{}, errBadDecrypt
				}
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 || len(certs) == 0 {
		return tls.Certificate{}, errNoPFXCertificate
	}
	key := keys[0]
	leaf := -1
	for i, cert := range certs {
		if publicKeyMatches(cert, key) {
			leaf = i
			break
		}
	}
	if leaf < 0 {
		return tls.Certificate{}, fmt.Errorf("%s: key values mismatch", errKeyValues)
	}
	out := tls.Certificate{PrivateKey: key, Leaf: certs[leaf]}
	out.Certificate = append(out.Certificate, certs[leaf].Raw)
	for i, cert := range certs {
		if i != leaf {
			out.Certificate = append(out.Certificate, cert.Raw)
		}
	}
	return out, nil
}

func publicKeyMatches(cert *x509.Certificate, key crypto.PrivateKey) bool {
	type equaler interface{ Equal(crypto.PublicKey) bool }
	var pub crypto.PublicKey
	switch k := key.(type) {
	case *rsa.PrivateKey:
		pub = &k.PublicKey
	case *ecdsa.PrivateKey:
		pub = &k.PublicKey
	case ed25519.PrivateKey:
		pub = k.Public()
	default:
		return false
	}
	e, ok := pub.(equaler)
	return ok && e.Equal(cert.PublicKey)
}

func verifyPFXMAC(md macData, content []byte, password string, absent bool) error {
	alg, ok := macHashes[md.Mac.Algorithm.Algorithm.String()]
	if !ok {
		return errPFXUnsupported
	}
	if md.Iterations <= 0 || md.Iterations > 10_000_000 {
		return errPFXUnsupported
	}
	candidates := [][]byte{bmpString(password)}
	// With no passphrase, OpenSSL also tries an absent password.
	if absent {
		candidates = append(candidates, nil)
	}
	for _, pass := range candidates {
		key := pkcs12KDF(alg.h, alg.v, md.MacSalt, pass, md.Iterations, 3, alg.h().Size())
		mac := hmac.New(alg.h, key)
		mac.Write(content)
		if subtle.ConstantTimeCompare(mac.Sum(nil), md.Mac.Digest) == 1 {
			return nil
		}
	}
	return errMACVerify
}

// bmpString encodes a password as PKCS#12 does: UTF-16BE with a terminating zero.
func bmpString(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2*len(units)+2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return append(out, 0, 0)
}

// pkcs12KDF is the key derivation of RFC 7292, appendix B.2.
func pkcs12KDF(h func() hash.Hash, v int, salt, password []byte, iterations int, id byte, size int) []byte {
	fill := func(b []byte) []byte {
		if len(b) == 0 {
			return nil
		}
		n := v * ((len(b) + v - 1) / v)
		out := make([]byte, n)
		for i := range out {
			out[i] = b[i%len(b)]
		}
		return out
	}
	D := bytes.Repeat([]byte{id}, v)
	I := append(fill(salt), fill(password)...)
	var out []byte
	for len(out) < size {
		hh := h()
		hh.Write(D)
		hh.Write(I)
		A := hh.Sum(nil)
		for i := 1; i < iterations; i++ {
			hh = h()
			hh.Write(A)
			A = hh.Sum(nil)
		}
		out = append(out, A...)
		if len(out) >= size {
			break
		}
		B := make([]byte, v)
		for i := range B {
			B[i] = A[i%len(A)]
		}
		for j := 0; j < len(I); j += v {
			carry := 1
			for k := v - 1; k >= 0; k-- {
				sum := int(I[j+k]) + int(B[k]) + carry
				I[j+k] = byte(sum)
				carry = sum >> 8
			}
		}
	}
	return out[:size]
}
