package up

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/prompt"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// testHarnesses is a registry with a claude-code stand-in for up tests.
func testHarnesses() harness.Registry {
	return harness.Registry{
		"claude-code": &harness.FakeHarness{
			HarnessName: "claude-code",
			Vendors:     []string{"anthropic"},
			Provs:       []string{"install-claude-code"},
		},
	}
}

type fakeCreator struct {
	engine.Engine
	spec      sandbox.Spec
	attached  string
	createErr error
	attachErr error
	log       *[]string // optional lifecycle recorder (nil = off)
}

func (f *fakeCreator) Create(s sandbox.Spec) (string, error) {
	if f.log != nil {
		*f.log = append(*f.log, "create")
	}
	f.spec = s
	return "cid123", f.createErr
}

func (f *fakeCreator) Attach(id string) error {
	if f.log != nil {
		*f.log = append(*f.log, "attach")
	}
	f.attached = id
	return f.attachErr
}

// spyBroker satisfies up's broker seam and records the lifecycle call order.
type spyBroker struct {
	log *[]string
}

func (s *spyBroker) Serve(addr string) (string, error) {
	*s.log = append(*s.log, "serve")
	return "127.0.0.1:12345", nil
}
func (s *spyBroker) Addr() string { return "127.0.0.1:12345" }
func (s *spyBroker) Store(broker.Credential) (string, error) {
	*s.log = append(*s.log, "store")
	return "cid", nil
}
func (s *spyBroker) IssueHandle(credID, scope string, ttl time.Duration) (broker.Handle, error) {
	*s.log = append(*s.log, "issue")
	return broker.Handle{Value: "poddle_spy"}, nil
}
func (s *spyBroker) Revoke(v string)            { *s.log = append(*s.log, "revoke:"+v) }
func (s *spyBroker) Stop(context.Context) error { *s.log = append(*s.log, "stop"); return nil }

func TestUp_CreatesAndAttaches(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, broker.NewBroker())
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"mybox", "--size", "strong"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.Name != "mybox" {
		t.Errorf("name = %q", f.spec.Name)
	}
	if f.spec.Size != "strong" || f.spec.CPUs != 8 || f.spec.Memory != "16g" {
		t.Errorf("size resolution = %+v", f.spec)
	}
	if f.attached != "cid123" {
		t.Errorf("expected attach to cid123, got %q", f.attached)
	}
	if !strings.Contains(out.String(), "cid123") {
		t.Errorf("id not printed: %q", out.String())
	}
}

func TestUp_DetachSkipsAttach(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, broker.NewBroker())
	c.SetArgs([]string{"--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.attached != "" {
		t.Errorf("attach should be skipped, got %q", f.attached)
	}
}

func TestUp_UnknownHarness_Errors(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--harness", "bogus", "--detach"})

	if err := c.Execute(); err == nil {
		t.Error("expected an error for an unknown harness")
	}
	if f.spec.Name != "" {
		t.Error("pod should not be created when the harness is invalid")
	}
}

func TestUp_WithIdentity_Secretless(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	fake := &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{
			Mode: broker.ModeSubscription, Vendor: "anthropic",
			Secret: "SUPERSECRET", BaseURL: "https://api.anthropic.com",
		},
	}
	reg := idn.Registry{"anthropic": fake}

	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--identity", "work"}) // fake Attach returns instantly; no --detach

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// A handle reaches the pod...
	if h := f.spec.Env["HANDLE"]; !strings.HasPrefix(h, "poddle_") {
		t.Errorf("expected a handle in pod env, got %q", h)
	}
	// ...and the real secret does NOT.
	for k, v := range f.spec.Env {
		if strings.Contains(v, "SUPERSECRET") {
			t.Fatalf("real secret leaked into pod env: %s=%s", k, v)
		}
	}
	// Harness provisions are folded into the spec.
	if len(f.spec.Setup) == 0 {
		t.Error("expected harness provisions in spec.Setup")
	}
}

func TestUp_NoIdentity_SelectExisting(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	f := &fakeCreator{}
	// options: ["work (anthropic)", "+ Add new", "None"] → pick index 0 (existing).
	pr := &prompt.FakePrompter{Selects: []int{0}}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, broker.NewBroker())
	c.SetArgs([]string{"mybox"}) // no --identity, no --detach

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h := f.spec.Env["HANDLE"]; !strings.HasPrefix(h, "poddle_") {
		t.Errorf("selected identity should be wired in; env = %v", f.spec.Env)
	}
}

func TestUp_NoIdentity_None(t *testing.T) {
	store := idn.NewStore(t.TempDir()) // empty
	reg := idn.Registry{"anthropic": &idn.FakeProvider{ProviderName: "anthropic"}}
	f := &fakeCreator{}
	// empty store → options ["+ Add new", "None"] → pick index 1 (None).
	pr := &prompt.FakePrompter{Selects: []int{1}}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, broker.NewBroker())
	c.SetArgs([]string{"mybox"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := f.spec.Env["HANDLE"]; ok {
		t.Errorf("None should leave a plain pod; env = %v", f.spec.Env)
	}
	if f.spec.Name != "mybox" {
		t.Error("pod should still be created for a plain sandbox")
	}
}

func TestUp_NoIdentity_AddNew(t *testing.T) {
	store := idn.NewStore(t.TempDir()) // empty
	prov := &idn.FakeProvider{
		ProviderName: "anthropic", Authed: false,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}
	reg := idn.Registry{"anthropic": prov}
	f := &fakeCreator{}
	// identity select ["+ Add new"(0), "None"(1)] → 0; provider select ["anthropic"(0)] → 0; name input → "work".
	pr := &prompt.FakePrompter{Selects: []int{0, 0}, Inputs: []string{"work"}}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, broker.NewBroker())
	c.SetArgs([]string{"mybox"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !prov.AuthCalled {
		t.Error("add-new should authenticate the new identity")
	}
	if _, err := store.Get("work"); err != nil {
		t.Errorf("new identity should be created: %v", err)
	}
	if h := f.spec.Env["HANDLE"]; !strings.HasPrefix(h, "poddle_") {
		t.Errorf("new identity should be wired in; env = %v", f.spec.Env)
	}
}

func TestUp_Detach_NoPrompt(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	reg := idn.Registry{"anthropic": &idn.FakeProvider{ProviderName: "anthropic"}}
	f := &fakeCreator{}
	pr := &prompt.FakePrompter{} // errors if prompted
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("--detach should not prompt: %v", err)
	}
	if _, ok := f.spec.Env["HANDLE"]; ok {
		t.Error("--detach should leave a plain pod")
	}
}

func TestUp_ExplicitIdentity_NoPrompt(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	f := &fakeCreator{}
	pr := &prompt.FakePrompter{} // errors if prompted
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("explicit --identity should not prompt: %v", err)
	}
	if h := f.spec.Env["HANDLE"]; !strings.HasPrefix(h, "poddle_") {
		t.Errorf("explicit identity should be wired in; env = %v", f.spec.Env)
	}
}

func TestUp_DetachWithIdentity_Errors(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}

	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--identity", "work", "--detach"})

	if err := c.Execute(); err == nil {
		t.Error("expected an error for --detach with --identity")
	}
	if f.spec.Name != "" {
		t.Error("pod should not be created when --detach + --identity is rejected")
	}
}

func TestUp_Identity_ServesAndTearsDown(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}

	var log []string
	f := &fakeCreator{log: &log}
	spy := &spyBroker{log: &log}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, spy)
	c.SetArgs([]string{"mybox", "--identity", "work"}) // fake Attach returns instantly

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Broker is served and wired before create/attach, then revoked + stopped
	// once the (instant, faked) attached session ends.
	want := []string{"serve", "store", "issue", "create", "attach", "revoke:poddle_spy", "stop"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("lifecycle = %v, want %v", log, want)
	}
}

func TestUp_Identity_PodUsesHostAlias(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}

	var log []string
	f := &fakeCreator{log: &log}
	spy := &spyBroker{log: &log} // Addr() → 127.0.0.1:12345
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, spy)
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The pod must reach the broker via the container host alias, not loopback.
	want := "http://host.containers.internal:12345"
	if got := f.spec.Env["BROKER_ADDR"]; got != want {
		t.Errorf("pod broker URL = %q, want %q", got, want)
	}
}

func TestUp_WithIdentity_ReauthsWhenStale(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	fake := &idn.FakeProvider{
		ProviderName: "anthropic", Authed: false, // not authed yet
		Cred: broker.Credential{Mode: broker.ModeSubscription, Vendor: "anthropic", Secret: "x"},
	}
	reg := idn.Registry{"anthropic": fake}

	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, broker.NewBroker())
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.AuthCalled {
		t.Error("expected re-auth on the client when the identity was not authenticated")
	}
}
