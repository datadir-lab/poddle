package policy

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// FuzzDecide fuzzes the request-matching hot path with attacker-influenced
// patterns and requests. It must never panic, and a deny must always carry a
// reason (the Decide contract).
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

// FuzzPolicyUnmarshal feeds arbitrary TOML to the policy decoder, then exercises
// Decide on whatever parsed. Loading an untrusted policy file must not panic,
// and a successfully parsed policy must be usable.
func FuzzPolicyUnmarshal(f *testing.F) {
	f.Add("allow_upstreams = ['.example.com']\negress = 'block'\n")
	f.Add("")
	f.Add("methods = { \"api\" = ['GET','POST'] }")
	f.Add("deny_upstreams = ['a', 'b', 'c']")
	f.Add("not valid = toml")
	f.Fuzz(func(t *testing.T, src string) {
		var p Policy
		if err := toml.Unmarshal([]byte(src), &p); err != nil {
			return
		}
		_, _ = p.Decide("api.example.com", "GET") // parsed policy must be usable
	})
}
