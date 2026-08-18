package up

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/audit"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/config"
	"git.dev.datadir.co/datadir/poddle/src/internal/connector"
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
	spec        sandbox.Spec
	attached    string
	execed      string
	detached    string
	removed     string
	volsRemoved string
	podInfo     sandbox.PodInfo
	ttyExeced   string
	resized     []string
	createErr   error
	attachErr   error
	log         *[]string // optional lifecycle recorder (nil = off)
}

func (f *fakeCreator) Exec(id, command string) error {
	if f.log != nil {
		*f.log = append(*f.log, "exec")
	}
	f.execed = command
	return nil
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

func (f *fakeCreator) Remove(id string) error {
	f.removed = id
	return nil
}

func (f *fakeCreator) ExecDetached(id, command string) error {
	f.detached = command
	return nil
}

func (f *fakeCreator) Resize(id string, cpus float64, memory string) error {
	f.resized = append(f.resized, fmt.Sprintf("%s:%g:%s", id, cpus, memory))
	return nil
}

func (f *fakeCreator) RemoveVolumesForPod(pod string) error {
	f.volsRemoved = pod
	return nil
}

func (f *fakeCreator) PodInfo(id string) (sandbox.PodInfo, error) { return f.podInfo, nil }
func (f *fakeCreator) ExecTTY(id, command string) error           { f.ttyExeced = command; return nil }

// spyBroker satisfies up's podBroker seam and records the call order.
type spyBroker struct {
	log *[]string
}

func (s *spyBroker) EnsureRunning() error { *s.log = append(*s.log, "ensure"); return nil }
func (s *spyBroker) Gateway() (string, error) {
	*s.log = append(*s.log, "gateway")
	return "127.0.0.1:12345", nil
}
func (s *spyBroker) RedisAddr() (string, error)    { return "127.0.0.1:16379", nil }
func (s *spyBroker) PostgresAddr() (string, error) { return "127.0.0.1:15432", nil }
func (s *spyBroker) RevokePod(pod string) error {
	*s.log = append(*s.log, "revoke:"+pod)
	return nil
}
func (s *spyBroker) IssueHandle(pod, scope string, _ broker.Credential) (string, error) {
	*s.log = append(*s.log, "issue")
	return "poddle_spy", nil
}
func (s *spyBroker) Audit(e audit.Event) error {
	*s.log = append(*s.log, "audit:"+string(e.Kind))
	return nil
}

// stubBroker is a no-op podBroker for tests that don't exercise brokered creds.
type stubBroker struct{}

func (stubBroker) EnsureRunning() error          { return nil }
func (stubBroker) Gateway() (string, error)      { return "127.0.0.1:0", nil }
func (stubBroker) RedisAddr() (string, error)    { return "127.0.0.1:0", nil }
func (stubBroker) PostgresAddr() (string, error) { return "127.0.0.1:0", nil }
func (stubBroker) RevokePod(string) error        { return nil }
func (stubBroker) IssueHandle(pod, scope string, _ broker.Credential) (string, error) {
	return "poddle_stub", nil
}
func (stubBroker) Audit(audit.Event) error { return nil }

func TestUp_CreatesAndAttaches(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
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

func TestUp_AutoscaleFlag(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
	c.SetArgs([]string{"ibox", "--autoscale", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !f.spec.Autoscale {
		t.Error("up --autoscale should set spec.Autoscale (interactive pods get warn-only)")
	}
}

func TestUp_DetachSkipsAttach(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, stubBroker{})
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses(), Prompter: pr}, stubBroker{})
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("explicit --identity should not prompt: %v", err)
	}
	if h := f.spec.Env["HANDLE"]; !strings.HasPrefix(h, "poddle_") {
		t.Errorf("explicit identity should be wired in; env = %v", f.spec.Env)
	}
}

type fakeTemplates struct {
	tpl config.Template
	err error
}

func (f fakeTemplates) Resolve(name string) (config.Template, error) { return f.tpl, f.err }

func TestUp_Template_Applied(t *testing.T) {
	f := &fakeCreator{}
	tpl := config.Template{
		Image: "docker.io/library/node:22", Size: "strong",
		Env: map[string]string{"FOO": "bar"}, Setup: []string{"echo hi"},
	}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	c.SetArgs([]string{"box"}) // no flags → template values apply

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.Image != "docker.io/library/node:22" {
		t.Errorf("image = %q, want the template image", f.spec.Image)
	}
	if f.spec.Size != "strong" || f.spec.CPUs != 8 {
		t.Errorf("size not from template: %+v", f.spec)
	}
	if f.spec.Env["FOO"] != "bar" {
		t.Errorf("template env not applied: %v", f.spec.Env)
	}
	if len(f.spec.Setup) == 0 || f.spec.Setup[0] != "echo hi" {
		t.Errorf("template setup not applied: %v", f.spec.Setup)
	}
}

func TestUp_Connector_WiredIntoPodBeforeClone(t *testing.T) {
	cstore := connector.NewStore(t.TempDir())
	if _, err := cstore.Create("my-forgejo", "forgejo", "http://192.168.1.166:3000", "me", "TOK", ""); err != nil {
		t.Fatal(err)
	}

	var log []string
	f := &fakeCreator{}
	spy := &spyBroker{log: &log} // Serve/Addr → 127.0.0.1:12345, IssueHandle → poddle_spy
	c := NewCmd(&app.App{
		Engine:      f,
		Harnesses:   testHarnesses(),
		Templates:   fakeTemplates{tpl: config.Template{Connectors: []string{"my-forgejo"}, Repo: "http://192.168.1.166:3000/datadir/r.git"}},
		Connections: cstore,
	}, spy)
	c.SetArgs([]string{"box", "--exec", "true"}) // --exec avoids the interactive attach

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The connector's git rewrite must be the FIRST setup step (before the clone).
	wantGit := `git config --global url."http://poddle_spy:x@host.containers.internal:12345/".insteadOf "http://192.168.1.166:3000/"`
	if len(f.spec.Setup) == 0 || f.spec.Setup[0] != wantGit {
		t.Errorf("connector rewrite should be first in setup, got:\n%v", f.spec.Setup)
	}
	// The clone follows it.
	if len(f.spec.Setup) < 2 || !strings.Contains(f.spec.Setup[1], "git clone") {
		t.Errorf("clone should follow the rewrite: %v", f.spec.Setup)
	}
}

func TestUp_Template_RepoClonedFirst(t *testing.T) {
	f := &fakeCreator{}
	tpl := config.Template{Repo: "https://git.example/r.git", Setup: []string{"echo after"}}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	c.SetArgs([]string{"box"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.spec.Setup) < 2 || f.spec.Setup[0] != "git clone https://git.example/r.git /workspace" {
		t.Errorf("repo clone should be the first setup step: %v", f.spec.Setup)
	}
	if f.spec.Setup[1] != "echo after" {
		t.Errorf("template setup should follow the clone: %v", f.spec.Setup)
	}
	if f.spec.Repo != "https://git.example/r.git" {
		t.Errorf("repo label = %q", f.spec.Repo)
	}
}

func TestUp_Template_CLIOverrides(t *testing.T) {
	f := &fakeCreator{}
	tpl := config.Template{Image: "docker.io/library/node:22", Size: "strong"}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	c.SetArgs([]string{"box", "--image", "alpine", "--size", "weak"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.Image != "alpine" {
		t.Errorf("CLI --image should override template, got %q", f.spec.Image)
	}
	if f.spec.Size != "weak" {
		t.Errorf("CLI --size should override template, got %q", f.spec.Size)
	}
}

func TestUp_Exec_RunsCommandNotAttach(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
	c.SetArgs([]string{"box", "--exec", "echo hi"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.execed != "echo hi" {
		t.Errorf("exec = %q, want 'echo hi'", f.execed)
	}
	if f.attached != "" {
		t.Errorf("--exec should not attach, got %q", f.attached)
	}
}

func TestUp_Exec_WithIdentityLifecycle(t *testing.T) {
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
	c.SetArgs([]string{"box", "--identity", "work", "--exec", "claude -p hi"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// exec replaces attach; the handle persists (poddled outlives up — no revoke).
	want := []string{"ensure", "gateway", "issue", "create", "audit:pod.up", "exec"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("lifecycle = %v, want %v", log, want)
	}
	if f.execed != "claude -p hi" {
		t.Errorf("exec = %q", f.execed)
	}
}

func TestUp_DetachWithIdentity_Works(t *testing.T) {
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, &spyBroker{log: &log})
	c.SetArgs([]string{"mybox", "--identity", "work", "--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("--detach with --identity now works (poddled persists the handle): %v", err)
	}
	if f.spec.Name != "mybox" {
		t.Errorf("detached pod should be created, got %q", f.spec.Name)
	}
	// Detached: handle issued + pod created, but NOT attached and NOT revoked.
	want := []string{"ensure", "gateway", "issue", "create", "audit:pod.up"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("lifecycle = %v, want %v", log, want)
	}
}

func TestUp_Identity_IssuesHandleAndAttaches(t *testing.T) {
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
	// Handles are issued before create/attach and persist (poddled outlives up).
	// once the (instant, faked) attached session ends.
	want := []string{"ensure", "gateway", "issue", "create", "audit:pod.up", "attach"}
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
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, stubBroker{})
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.AuthCalled {
		t.Error("expected re-auth on the client when the identity was not authenticated")
	}
}
