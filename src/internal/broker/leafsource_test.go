package broker

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// TestCustodyLeafSource_CompletesRealHandshake drives an actual TLS handshake using
// a leaf minted via the keeper and reassembled by custodyLeafSource — proving the
// reassembled PKCS#8 key can PROVE POSSESSION (complete a handshake), not merely
// chain-verify. A client trusting the CA accepts the keeper-signed leaf.
func TestCustodyLeafSource_CompletesRealHandshake(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsca.Load(dir) // the client's trust root
	if err != nil {
		t.Fatalf("Load CA: %v", err)
	}
	k := newLocalKeeper(NewHandles(NewVault()))
	if err := k.EnsureCA(dir); err != nil { // same dir -> the SAME CA signs leaves
		t.Fatalf("EnsureCA: %v", err)
	}
	src := newCustodyLeafSource(k)

	const host = "handshake.example"
	serverTLS := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return src.LeafFor(host)
	}}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert())
	clientTLS := &tls.Config{RootCAs: roots, ServerName: host}

	cliConn, srvConn := net.Pipe()
	srv := tls.Server(srvConn, serverTLS)
	cli := tls.Client(cliConn, clientTLS)
	errc := make(chan error, 2)
	go func() { errc <- srv.Handshake() }()
	go func() { errc <- cli.Handshake() }()
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil {
			t.Fatalf("TLS handshake with keeper-minted leaf failed: %v", err)
		}
	}
	if got := cli.ConnectionState().PeerCertificates[0].Subject.CommonName; got != host {
		t.Errorf("client saw leaf CN %q, want %q", got, host)
	}
	_ = cli.Close()
	_ = srv.Close()
}

func TestKeeper_EnsureCAAndSignLeaf(t *testing.T) {
	k := newLocalKeeper(NewHandles(NewVault()))
	// SignLeaf before EnsureCA is an error (no CA loaded), not a panic.
	if _, _, err := k.SignLeaf("example.com"); err == nil {
		t.Error("SignLeaf before EnsureCA should error")
	}
	if err := k.EnsureCA(t.TempDir()); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	certDER, keyDER, err := k.SignLeaf("example.com")
	if err != nil {
		t.Fatalf("SignLeaf: %v", err)
	}
	if len(certDER) == 0 || len(keyDER) == 0 {
		t.Fatal("SignLeaf returned empty cert/key")
	}
}

func TestKeeperRPC_SignLeafAcrossWire(t *testing.T) {
	dir := t.TempDir()
	k := newLocalKeeper(NewHandles(NewVault()))
	cliConn, srvConn := net.Pipe()
	go func() { _ = serveKeeper(srvConn, k) }()
	c := newSocketKeeperClient(cliConn)
	t.Cleanup(func() { _ = c.Close(); _ = srvConn.Close() })

	// EnsureCA and SignLeaf both cross the wire; the CA key stays keeper-side.
	if err := c.EnsureCA(dir); err != nil {
		t.Fatalf("EnsureCA over RPC: %v", err)
	}
	certDER, keyDER, err := c.SignLeaf("host.example")
	if err != nil {
		t.Fatalf("SignLeaf over RPC: %v", err)
	}
	if len(certDER) == 0 || len(keyDER) == 0 {
		t.Fatal("SignLeaf over RPC returned empty cert/key")
	}

	// The keeper-backed LeafSource reassembles a usable leaf and caches it, so a
	// repeat handshake to the same host returns the identical *tls.Certificate
	// without re-RPCing.
	src := newCustodyLeafSource(c)
	lc, err := src.LeafFor("host.example")
	if err != nil {
		t.Fatalf("LeafFor: %v", err)
	}
	if lc.Leaf == nil || lc.PrivateKey == nil {
		t.Fatal("reassembled leaf is incomplete")
	}
	if lc.Leaf.Subject.CommonName != "host.example" {
		t.Errorf("leaf CN = %q, want host.example", lc.Leaf.Subject.CommonName)
	}
	lc2, err := src.LeafFor("host.example")
	if err != nil {
		t.Fatalf("LeafFor (cached): %v", err)
	}
	if lc != lc2 {
		t.Error("custodyLeafSource should return the cached *tls.Certificate on a repeat host")
	}
}

func TestBroker_LeafSource_SignsViaKeeper(t *testing.T) {
	// The facade's EnsureCA + LeafSource wire the interception path through custody.
	b := NewBroker()
	if err := b.EnsureCA(t.TempDir()); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	lc, err := b.LeafSource().LeafFor("api.example")
	if err != nil {
		t.Fatalf("LeafSource.LeafFor: %v", err)
	}
	if lc.Leaf == nil || lc.Leaf.Subject.CommonName != "api.example" {
		t.Errorf("unexpected leaf: %+v", lc.Leaf)
	}
}
