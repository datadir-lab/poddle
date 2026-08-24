package broker

import (
	"net/url"
	"testing"
)

func TestGateway_DialURL_RewritesLoopbackAtDialOnly(t *testing.T) {
	g := NewGateway(nil)
	up, _ := url.Parse("http://127.0.0.1:5432/db")

	// Disabled by default: the upstream is returned unchanged (same pointer).
	if got := g.dialURL(up); got != up {
		t.Fatalf("loopbackHost unset must leave the upstream unchanged, got %v", got)
	}

	g.SetLoopbackHost("host.containers.internal")
	got := g.dialURL(up)
	if got.Host != "host.containers.internal:5432" {
		t.Errorf("dial host = %q, want host.containers.internal:5432", got.Host)
	}
	if got.Scheme != "http" || got.Path != "/db" {
		t.Errorf("dialURL must preserve scheme/path: scheme=%q path=%q", got.Scheme, got.Path)
	}
	// The original upstream URL must be untouched — it is the source of the Host
	// header the real upstream sees.
	if up.Host != "127.0.0.1:5432" {
		t.Errorf("dialURL mutated the original upstream URL: Host=%q", up.Host)
	}

	// A non-loopback upstream is returned unchanged (same pointer).
	pub, _ := url.Parse("https://api.anthropic.com/v1")
	if got := g.dialURL(pub); got != pub {
		t.Errorf("non-loopback upstream must be returned unchanged")
	}
}

func TestForwardProxy_DialTarget_RewritesLoopback(t *testing.T) {
	f := NewForwardProxy(nil, nil)

	// Disabled by default.
	if got := f.dialTarget("127.0.0.1:8080"); got != "127.0.0.1:8080" {
		t.Fatalf("loopbackHost unset must leave the destination unchanged, got %q", got)
	}

	f.SetLoopbackHost("host.containers.internal")
	if got := f.dialTarget("localhost:8080"); got != "host.containers.internal:8080" {
		t.Errorf("dialTarget(localhost:8080) = %q, want host.containers.internal:8080", got)
	}
	if got := f.dialTarget("example.com:443"); got != "example.com:443" {
		t.Errorf("non-loopback destination must be unchanged, got %q", got)
	}
}
