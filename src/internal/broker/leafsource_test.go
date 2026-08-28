package broker

import (
	"net"
	"testing"
)

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
