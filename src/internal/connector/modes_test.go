package connector

import "testing"

// TestCredential_MapsAllModes exercises brokerMode for every inject mode,
// including the api-key, endpoint, and pass-through (default) branches that the
// git/CI connector tests don't reach.
func TestCredential_MapsAllModes(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, err := s.Create("c", "custom", "https://example.test", "user", "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"bearer", "basic", "api-key", "endpoint", "some-custom-mode"} {
		def := Definition{Mode: mode, BaseURL: "https://example.test"}
		cred, err := Credential(conn, def, nil)
		if err != nil {
			t.Errorf("Credential(mode=%q): %v", mode, err)
			continue
		}
		if cred.Vendor != "custom" {
			t.Errorf("mode %q: vendor = %q, want custom", mode, cred.Vendor)
		}
	}
}
