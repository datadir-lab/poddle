package up

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/config"
	"github.com/datadir-lab/poddle/src/internal/harness"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
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

func TestTask_DetachRunsBackgroundAndKeepsPod(t *testing.T) {
	f := &fakeCreator{}
	c := NewTaskCmd(taskApp(t, f, "AGENT: %s"), stubBroker{})
	c.SetArgs([]string{"big job", "--identity", "work", "--name", "dpod", "--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.execed != "" {
		t.Errorf("detached task should not run synchronously, execed = %q", f.execed)
	}
	if !strings.Contains(f.detached, "AGENT: big job") || !strings.Contains(f.detached, TaskLogPath) {
		t.Errorf("detached command should run the agent to the log, got %q", f.detached)
	}
	if f.removed != "" {
		t.Errorf("detached task should leave the pod up, removed = %q", f.removed)
	}
}

// A task that outlives its run (--detach/--keep) can be moved or auto-moved, so
// its session must persist on named volumes; a one-shot task stays ephemeral.
func hasWorkspaceVol(f *fakeCreator, pod string) bool {
	for _, v := range f.spec.Volumes {
		if v.Name == "poddle-"+pod+"-workspace" && v.Container == "/workspace" {
			return true
		}
	}
	return false
}

func TestTask_DetachPersistsStateForMove(t *testing.T) {
	f := &fakeCreator{}
	c := NewTaskCmd(taskApp(t, f, "AGENT: %s"), stubBroker{})
	c.SetArgs([]string{"big job", "--identity", "work", "--name", "dpod", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !hasWorkspaceVol(f, "dpod") {
		t.Errorf("a detached task must persist state on named volumes; volumes = %v", f.spec.Volumes)
	}
}

func TestTask_KeepPersistsStateForMove(t *testing.T) {
	f := &fakeCreator{}
	c := NewTaskCmd(taskApp(t, f, "run %s"), stubBroker{})
	c.SetArgs([]string{"x", "--identity", "work", "--name", "kpod", "--keep"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !hasWorkspaceVol(f, "kpod") {
		t.Errorf("a kept task must persist state on named volumes; volumes = %v", f.spec.Volumes)
	}
}

func TestTask_OneShotIsEphemeral(t *testing.T) {
	var log []string
	f := &fakeCreator{log: &log}
	c := NewTaskCmd(taskApp(t, f, "run %s"), &spyBroker{log: &log})
	c.SetArgs([]string{"x", "--identity", "work", "--name", "opod"}) // torn down after
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.spec.Volumes) != 0 {
		t.Errorf("a one-shot task should be ephemeral (no named volumes); volumes = %v", f.spec.Volumes)
	}
}

func TestTask_SizingHooks(t *testing.T) {
	f := &fakeCreator{}
	ap := taskApp(t, f, "run %s")
	ap.Templates = fakeTemplates{tpl: config.Template{
		Identity: "work", Harness: "claude-code",
		BeforeTask: "strong", AfterTask: "weak",
	}}
	c := NewTaskCmd(ap, stubBroker{})
	c.SetArgs([]string{"job", "--name", "hpod", "--keep"}) // --keep so after_task fires

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{"hpod:8:", "hpod:2:"} // burst CPU up (strong) then drop (weak); memory untouched
	if len(f.resized) != 2 || f.resized[0] != want[0] || f.resized[1] != want[1] {
		t.Errorf("sizing hooks = %v, want %v (CPU-only)", f.resized, want)
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
