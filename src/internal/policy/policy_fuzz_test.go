package policy

import "testing"

// FuzzDecide fuzzes the request-matching hot path with attacker-influenced
// patterns and requests (a pod picks the host and method it asks for). It must
// never panic, and a deny must always carry a reason (the Decide contract).
func FuzzDecide(f *testing.F) {
	f.Add("api.example.com", "GET", ".example.com", "evil.com")
	f.Add("", "", "", "")
	f.Add("x", "CONNECT", ".", "..")
	f.Add("sub.host", "POST", ".host", ".host")
	f.Fuzz(func(t *testing.T, host, method, allow, deny string) {
		p := &Policy{
			AllowUpstreams: []string{allow},
			DenyUpstreams:  []string{deny},
			Methods:        map[string][]string{allow: {method}, ".": {method}},
		}
		allowed, reason := p.Decide(host, method) // must not panic
		if !allowed && reason == "" {
			t.Fatalf("denied without a reason: host=%q method=%q", host, method)
		}
	})
}
