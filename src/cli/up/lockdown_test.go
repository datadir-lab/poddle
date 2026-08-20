package up

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
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
	a := &app.App{Harnesses: testHarnesses(), Identities: store, Providers: reg}
	o := buildOpts{name: "box", harnessName: "claude-code", identityName: "work"}

	spec, _, _, err := buildSpec(&cobra.Command{}, a, stubBroker{}, o)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Network == nil || len(spec.Network.AllowList) == 0 {
		t.Fatal("a brokered pod must have spec.Network set with a broker allow-list")
	}
}

func TestBuildSpec_NoLockWhenNotBrokered(t *testing.T) {
	a := &app.App{Harnesses: testHarnesses()}
	o := buildOpts{name: "box", harnessName: "claude-code"} // no identity/connectors/policy

	spec, _, _, err := buildSpec(&cobra.Command{}, a, stubBroker{}, o)
	if err != nil {
		t.Fatalf("buildSpec: %v", err)
	}
	if spec.Network != nil {
		t.Error("a non-brokered pod must not lock egress (Network stays nil)")
	}
}
