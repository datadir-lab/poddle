package tlsca

import (
	"crypto/x509"
	"testing"
)

// TestSignLeafDER_RoundTrip: a leaf minted as serializable DER + PKCS#8 key (the
// form that crosses the Tier-2 keeper/front boundary) reassembles into a valid
// tls.Certificate that still chains to the CA and carries the host — proving the
// CA key needn't cross to produce a usable leaf front-side.
func TestSignLeafDER_RoundTrip(t *testing.T) {
	a, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load CA: %v", err)
	}
	certDER, keyDER, err := a.SignLeafDER("example.com")
	if err != nil {
		t.Fatalf("SignLeafDER: %v", err)
	}
	lc, err := LeafFromDER(certDER, keyDER)
	if err != nil {
		t.Fatalf("LeafFromDER: %v", err)
	}
	if lc.PrivateKey == nil || lc.Leaf == nil || len(lc.Certificate) != 1 {
		t.Fatalf("reassembled tls.Certificate is incomplete: %+v", lc)
	}
	if lc.Leaf.Subject.CommonName != "example.com" {
		t.Errorf("leaf CN = %q, want example.com", lc.Leaf.Subject.CommonName)
	}
	// The reassembled leaf still verifies against the CA (so the front can present
	// it and a pod trusting the CA accepts it).
	roots := x509.NewCertPool()
	roots.AddCert(a.Cert())
	if _, err := lc.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "example.com"}); err != nil {
		t.Errorf("reassembled leaf does not verify against the CA: %v", err)
	}
}

// TestLeafFromDER_RejectsGarbage: a corrupt DER/key (a hostile keeper response)
// errors cleanly rather than panicking.
func TestLeafFromDER_RejectsGarbage(t *testing.T) {
	if _, err := LeafFromDER([]byte("not-a-cert"), []byte("not-a-key")); err == nil {
		t.Error("LeafFromDER should reject garbage input")
	}
}
