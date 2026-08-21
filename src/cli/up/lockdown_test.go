package up

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

func TestBuildSpec_LocksEgressWhenBrokered(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	// A policy forces the pod's arbitrary egress through the broker's forward
	// proxy — so this pod exercises both a brokered credential (identity) and
	// the forward channel, covering every pod-facing address.
	pols := policy.NewFileStore(t.TempDir())
	if err := pols.Put(&policy.Policy{Name: "lockdown", AllowUpstreams: []string{"api.anthropic.com"}, Egress: "block"}); err != nil {
		t.Fatal(err)
	}
	a := &app.App{Harnesses: testHarnesses(), Identities: store, Providers: reg, Policies: pols}
	o := buildOpts{name: "box", harnessName: "claude-code", identityName: "work", policyName: "lockdown"}

	spec, _, _, err := buildSpec(&cobra.Command{}, a, stubBroker{}, stubNet{}, o)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Network == nil || len(spec.Network.AllowList) == 0 {
		t.Fatal("a brokered pod must have spec.Network set with a broker allow-list")
	}
	// Every allow-list entry must pin to the broker's lock-net peer IP — the
	// pod's sole route out.
	for _, hp := range spec.Network.AllowList {
		if hp.Host != "10.89.9.9" {
			t.Errorf("allow host = %q, want the broker peer IP 10.89.9.9", hp.Host)
		}
	}
	// The pod's addresses point at the peer IP, never the old host alias.
	if got := spec.Env["BROKER_ADDR"]; !strings.Contains(got, "10.89.9.9") {
		t.Errorf("pod broker addr = %q, want it pinned to 10.89.9.9", got)
	}
	if got := spec.Env["HTTP_PROXY"]; !strings.Contains(got, "10.89.9.9") {
		t.Errorf("pod forward proxy = %q, want it pinned to 10.89.9.9", got)
	}
	if spec.Env["NO_PROXY"] != "10.89.9.9" {
		t.Errorf("NO_PROXY = %q, want 10.89.9.9", spec.Env["NO_PROXY"])
	}
	for k, v := range spec.Env {
		if strings.Contains(v, "host.containers.internal") {
			t.Fatalf("pod env still points at host.containers.internal: %s=%s", k, v)
		}
	}
}

func TestBuildSpec_NoLockWhenNotBrokered(t *testing.T) {
	a := &app.App{Harnesses: testHarnesses()}
	o := buildOpts{name: "box", harnessName: "claude-code"} // no identity/connectors/policy

	spec, _, _, err := buildSpec(&cobra.Command{}, a, stubBroker{}, stubNet{}, o)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Network != nil {
		t.Error("a non-brokered pod must not lock egress (Network stays nil)")
	}
}
