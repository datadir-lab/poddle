package tlsca

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoad_GeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	a, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Persisted: both files exist and the key is private.
	if _, err := os.Stat(filepath.Join(dir, certFile)); err != nil {
		t.Fatalf("CA cert not written: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatalf("CA key not written: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("CA key perm = %v, want 0600", info.Mode().Perm())
	}
	if !a.Cert().IsCA {
		t.Error("issued cert should be a CA")
	}

	// A second Load reuses the persisted CA (same certificate), not a new one.
	b, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Cert().Equal(b.Cert()) {
		t.Error("Load should reuse the persisted CA, not regenerate it")
	}
}

func TestLeafFor_ChainsToCAAndCaches(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lc, err := a.LeafFor("api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	// The leaf verifies against the CA and carries the right name.
	pool := x509.NewCertPool()
	pool.AddCert(a.Cert())
	if _, err := lc.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "api.example.com"}); err != nil {
		t.Errorf("leaf should chain to the CA and match the host: %v", err)
	}
	// Cached: a second call returns the same certificate.
	lc2, _ := a.LeafFor("api.example.com")
	if lc != lc2 {
		t.Error("LeafFor should cache per host")
	}
	// An IP host gets an IPAddresses SAN (not a DNS name).
	ipLeaf, err := a.LeafFor("169.254.169.254")
	if err != nil {
		t.Fatal(err)
	}
	if len(ipLeaf.Leaf.IPAddresses) != 1 {
		t.Errorf("an IP host should get an IP SAN; got %+v", ipLeaf.Leaf.IPAddresses)
	}
}

// TestInterceptionHandshake is the end-to-end proof: a client that trusts the
// poddle CA (the trust injected into an intercepted pod) completes a TLS
// handshake against a server presenting a leaf minted on the fly for the SNI —
// exactly what the forward-proxy interceptor does.
func TestInterceptionHandshake(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tc := tls.Server(conn, &tls.Config{
			GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return a.LeafFor(chi.ServerName)
			},
		})
		_ = tc.Handshake()
		_ = tc.Close()
	}()

	pool := x509.NewCertPool()
	pool.AddCert(a.Cert())
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "read.example.com"})
	if err != nil {
		t.Fatalf("a CA-trusting client must accept the minted leaf: %v", err)
	}
	defer conn.Close()
	if cn := conn.ConnectionState().PeerCertificates[0].Subject.CommonName; cn != "read.example.com" {
		t.Errorf("presented leaf CN = %q, want read.example.com", cn)
	}
}
