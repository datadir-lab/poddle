package broker

import "testing"

func TestRewriteLoopbackHost(t *testing.T) {
	const lb = "host.containers.internal"
	cases := []struct {
		name     string
		hostport string
		loopback string
		want     string
	}{
		// Loopback forms → rewritten, port preserved.
		{"ipv4 with port", "127.0.0.1:5432", lb, "host.containers.internal:5432"},
		{"ipv4 bare", "127.0.0.1", lb, "host.containers.internal"},
		{"localhost with port", "localhost:6379", lb, "host.containers.internal:6379"},
		{"localhost bare", "localhost", lb, "host.containers.internal"},
		{"127.0.0.0/8 alias", "127.0.0.2:5432", lb, "host.containers.internal:5432"},
		{"ipv6 loopback bracketed", "[::1]:5432", lb, "host.containers.internal:5432"},
		{"ipv6 loopback bare", "::1", lb, "host.containers.internal"},

		// Non-loopback → unchanged.
		{"public host", "api.anthropic.com:443", lb, "api.anthropic.com:443"},
		{"public host bare", "example.com", lb, "example.com"},
		{"lan ip", "192.168.1.10:5432", lb, "192.168.1.10:5432"},
		{"unspecified addr left alone", "0.0.0.0:8080", lb, "0.0.0.0:8080"},
		{"empty", "", lb, ""},

		// Disabled (bare-host broker / tests): never rewrite, even loopback.
		{"disabled keeps loopback", "127.0.0.1:5432", "", "127.0.0.1:5432"},
		{"disabled keeps localhost", "localhost:6379", "", "localhost:6379"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RewriteLoopbackHost(c.hostport, c.loopback); got != c.want {
				t.Errorf("RewriteLoopbackHost(%q, %q) = %q, want %q", c.hostport, c.loopback, got, c.want)
			}
		})
	}
}
