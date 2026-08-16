package up

import (
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
)

func taskApp(t *testing.T, f *fakeCreator, task string) *app.App {
	t.Helper()
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	h := harness.Registry{"claude-code": &harness.FakeHarness{
		HarnessName: "claude-code", Vendors: []string{"anthropic"}, Task: task,
	}}
	return &app.App{Engine: f, Identities: store, Providers: reg, Harnesses: h}
}

func TestTask_RunsHeadlessAndTearsDown(t *testing.T) {
	var log []string
	f := &fakeCreator{log: &log}
	spy := &spyBroker{log: &log}
	c := NewTaskCmd(taskApp(t, f, "AGENT: %s"), spy)
	c.SetArgs([]string{"say hi", "--identity", "work", "--name", "tpod"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.execed != "AGENT: say hi" {
		t.Errorf("agent command = %q, want \"AGENT: say hi\"", f.execed)
	}
	if f.removed != "tpod" {
		t.Errorf("pod should be torn down, removed = %q", f.removed)
	}
	revoked := false
	for _, e := range log {
		if e == "revoke:tpod" {
			revoked = true
		}
	}
	if !revoked {
		t.Errorf("pod handles should be revoked; log = %v", log)
	}
}

func TestTask_RequiresIdentity(t *testing.T) {
	f := &fakeCreator{}
	c := NewTaskCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
	c.SetArgs([]string{"do a thing"}) // no --identity, no template identity
	if err := c.Execute(); err == nil {
		t.Error("task without an identity should error")
	}
	if f.spec.Name != "" {
		t.Error("no pod should be created when there's no identity")
	}
}

func TestTask_KeepSkipsTeardown(t *testing.T) {
	f := &fakeCreator{}
	c := NewTaskCmd(taskApp(t, f, "run %s"), stubBroker{})
	c.SetArgs([]string{"x", "--identity", "work", "--name", "kpod", "--keep"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.removed != "" {
		t.Errorf("--keep should leave the pod running, removed = %q", f.removed)
	}
}
