// Package tlsca is poddle's egress-interception certificate authority: a
// persistent, self-signed CA that mints short-lived per-host leaf certificates,
// presented to a pod during an intercepted TLS handshake so its client accepts
// the connection. The CA certificate is injected into an intercepted pod's trust
// store (see the up command); the CA private key never leaves the broker.
//
// Interception is how poddle enforces HTTP method rules — and redacts secrets —
// on HTTPS egress: the method and body are invisible inside a plain CONNECT
// tunnel, so the forward proxy terminates TLS with a leaf minted here, inspects
// the request, and re-originates to the real upstream. It is strictly opt-in
// (a policy sets intercept); non-intercepting pods keep an opaque tunnel.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	certFile = "egress-ca.crt"
	keyFile  = "egress-ca.key"
	caTTL    = 10 * 365 * 24 * time.Hour // the CA is long-lived (injected into pods)
	leafTTL  = 7 * 24 * time.Hour        // leaves are short-lived (cached in memory)
)

// Authority is a loaded CA plus an in-memory per-host leaf cache.
type Authority struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	mu      sync.Mutex
	leaves  map[string]*tls.Certificate
}

// Load returns the CA persisted under dir, generating and saving a new one if it
// is absent. The key is written 0600; dir is created 0700 if needed.
func Load(dir string) (*Authority, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	certPEM, cErr := os.ReadFile(certPath)
	keyPEM, kErr := os.ReadFile(keyPath)
	if cErr == nil && kErr == nil {
		return fromPEM(certPEM, keyPEM)
	}
	if !os.IsNotExist(cErr) && cErr != nil {
		return nil, fmt.Errorf("read CA cert: %w", cErr)
	}
	return generate(dir, certPath, keyPath)
}

// fromPEM reconstructs an Authority from its stored PEM material.
func fromPEM(certPEM, keyPEM []byte) (*Authority, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, fmt.Errorf("CA cert: not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("CA key: not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}
	return &Authority{cert: cert, key: key, certPEM: certPEM, leaves: map[string]*tls.Certificate{}}, nil
}

// generate creates a fresh CA and persists it.
func generate(dir, certPath, keyPath string) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "poddle egress interception CA", Organization: []string{"poddle"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caTTL),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true, // signs leaves only, never sub-CAs
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse new CA cert: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create CA dir: %w", err)
	}
	// The CA certificate is public — it is distributed into pod trust stores and
	// must stay world-readable for bind-mounts; only the key (below) is 0600.
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil { //nolint:gosec // public CA cert, intentionally world-readable
		return nil, fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}
	return &Authority{cert: cert, key: key, certPEM: certPEM, leaves: map[string]*tls.Certificate{}}, nil
}

// LeafFor mints (and caches) a leaf certificate for host, signed by the CA — the
// cert the interceptor presents to the pod for that SNI.
func (a *Authority) LeafFor(host string) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if lc, ok := a.leaves[host]; ok {
		return lc, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("create leaf cert: %w", err)
	}
	lc := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: mustParse(der)}
	a.leaves[host] = lc
	return lc, nil
}

// CertPEM is the CA certificate in PEM form, for injecting into a pod's trust
// store (mounted file + NODE_EXTRA_CA_CERTS/SSL_CERT_FILE/... env).
func (a *Authority) CertPEM() []byte { return a.certPEM }

// Cert exposes the CA certificate (e.g. to build a verification pool in tests).
func (a *Authority) Cert() *x509.Certificate { return a.cert }

func randSerial() (*big.Int, error) {
	s, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	return s, nil
}

func mustParse(der []byte) *x509.Certificate {
	c, _ := x509.ParseCertificate(der)
	return c
}
