package up

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/config"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/harness"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/policy"
	"github.com/datadir-lab/poddle/src/internal/prompt"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
	"github.com/datadir-lab/poddle/src/internal/tlsca"
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

// The brokerNet seam: the real engine is *podman.Provider (which has these), so
// the fake engine must satisfy it too, resolving a fixed broker peer IP.
func (f *fakeCreator) EnsurePodLockNetwork(pod string) (string, error) {
	return "poddle-lock-" + pod, nil
}
func (f *fakeCreator) ConnectBrokerToPod(_, _ string) error      { return nil }
func (f *fakeCreator) BrokerIPOnPod(_, _ string) (string, error) { return "10.89.9.9", nil }

// stubNet is a no-op brokerNet for buildSpec unit tests, resolving a fixed
// broker peer IP on the pod's lock network.
type stubNet struct{}

func (stubNet) EnsurePodLockNetwork(pod string) (string, error) { return "poddle-lock-" + pod, nil }
func (stubNet) ConnectBrokerToPod(_, _ string) error            { return nil }
func (stubNet) BrokerIPOnPod(_, _ string) (string, error)       { return "10.89.9.9", nil }

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
func (s *spyBroker) SetPolicy(pod string, p *policy.Policy) error {
	*s.log = append(*s.log, "policy:"+p.Name)
	return nil
}
func (s *spyBroker) Egress(pod string) (string, string, error) {
	*s.log = append(*s.log, "egress")
	return "poddle_egr_spy", "127.0.0.1:9", nil
}

// captureBroker records the policy bound via SetPolicy so tests can assert the
// derived default-deny allow-list.
type captureBroker struct {
	stubBroker
	policy *policy.Policy
}

func (c *captureBroker) SetPolicy(_ string, p *policy.Policy) error {
	c.policy = p
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
func (stubBroker) Audit(audit.Event) error                { return nil }
func (stubBroker) SetPolicy(string, *policy.Policy) error { return nil }
func (stubBroker) Egress(string) (string, string, error) {
	return "poddle_egr_stub", "127.0.0.1:9", nil
}

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
	// Stub the host-autoscaler spawn so the test never launches a real detached
	// process, and assert `up --autoscale` ensures it is running.
	var gotSocket string
	orig := ensureHostAutoscaler
	ensureHostAutoscaler = func(socket string) { gotSocket = socket }
	defer func() { ensureHostAutoscaler = orig }()

	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses()}, stubBroker{})
	c.SetArgs([]string{"ibox", "--autoscale", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !f.spec.Autoscale {
		t.Error("up --autoscale should set spec.Autoscale (interactive pods get warn-only)")
	}
	if gotSocket == "" {
		t.Error("up --autoscale should ensure the host autoscaler is running")
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
	// The connector's git rewrite must be the FIRST setup step (before the clone),
	// pinned to the broker's lock-net peer IP (10.89.9.9 from the fake engine).
	wantGit := `git config --global url."http://poddle_spy:x@10.89.9.9:12345/".insteadOf "http://192.168.1.166:3000/"`
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
	want := []string{"ensure", "gateway", "issue", "policy:poddle-default", "egress", "create", "audit:pod.up", "exec"}
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
	want := []string{"ensure", "gateway", "issue", "policy:poddle-default", "egress", "create", "audit:pod.up"}
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
	want := []string{"ensure", "gateway", "issue", "policy:poddle-default", "egress", "create", "audit:pod.up", "attach"}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("lifecycle = %v, want %v", log, want)
	}
}

func TestUp_Identity_PodUsesBrokerPeerIP(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}

	var log []string
	f := &fakeCreator{log: &log} // BrokerIPOnPod() → 10.89.9.9
	spy := &spyBroker{log: &log} // Gateway() → 127.0.0.1:12345
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: testHarnesses()}, spy)
	c.SetArgs([]string{"mybox", "--identity", "work"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The pod must reach the broker via its IP on the pod's internal lock net —
	// its sole route out — not loopback or the host alias.
	want := "http://10.89.9.9:12345"
	if got := f.spec.Env["BROKER_ADDR"]; got != want {
		t.Errorf("pod broker URL = %q, want %q", got, want)
	}
}

func TestUp_Policy_BindsWithoutConnectors(t *testing.T) {
	store := policy.NewFileStore(t.TempDir())
	if err := store.Put(&policy.Policy{Name: "lockdown", AllowUpstreams: []string{"api.anthropic.com"}, Egress: "block"}); err != nil {
		t.Fatal(err)
	}
	var log []string
	f := &fakeCreator{}
	spy := &spyBroker{log: &log}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Policies: store}, spy)
	c.SetArgs([]string{"box", "--policy", "lockdown", "--detach"}) // no identity, no connectors

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The policy label must bind even with no brokered credential, so the
	// dashboard's pod view shows the pod is governed.
	if f.spec.PolicyName != "lockdown" {
		t.Errorf("spec.PolicyName = %q, want lockdown (policy must bind without connectors)", f.spec.PolicyName)
	}
	// Enforcement must be applied: SetPolicy + forced egress, so the pod's
	// arbitrary outbound traffic is governed (that traffic exists regardless of
	// whether the pod has a connector).
	joined := strings.Join(log, ",")
	if !strings.Contains(joined, "policy:lockdown") {
		t.Errorf("SetPolicy(lockdown) should be called; broker log = %v", log)
	}
	if !strings.Contains(joined, "egress") {
		t.Errorf("forced egress should be wired; broker log = %v", log)
	}
	// The pod's arbitrary egress is routed through the broker's forward proxy.
	if f.spec.Env["HTTP_PROXY"] == "" {
		t.Errorf("HTTP_PROXY should point the pod at the broker's forward proxy; env = %v", f.spec.Env)
	}
}

func TestUp_Policy_DefaultAppliesWhenUnspecified(t *testing.T) {
	store := policy.NewFileStore(t.TempDir())
	if err := store.Put(&policy.Policy{Name: "house", AllowUpstreams: []string{"api.anthropic.com"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault("house"); err != nil {
		t.Fatal(err)
	}
	var log []string
	f := &fakeCreator{}
	spy := &spyBroker{log: &log}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Policies: store}, spy)
	c.SetArgs([]string{"box", "--detach"}) // no --policy -> inherit the default

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.PolicyName != "house" {
		t.Errorf("spec.PolicyName = %q, want house (the default should apply when no --policy)", f.spec.PolicyName)
	}
	if !strings.Contains(strings.Join(log, ","), "policy:house") {
		t.Errorf("SetPolicy(house) should be called for the default; broker log = %v", log)
	}
}

func TestUp_Policy_ExplicitEmptyOptsOutOfDefault(t *testing.T) {
	store := policy.NewFileStore(t.TempDir())
	_ = store.Put(&policy.Policy{Name: "house"})
	_ = store.SetDefault("house")
	var log []string
	f := &fakeCreator{}
	spy := &spyBroker{log: &log}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Policies: store}, spy)
	c.SetArgs([]string{"box", "--policy", "", "--detach"}) // explicit opt-out stays ungoverned

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.PolicyName != "" {
		t.Errorf("spec.PolicyName = %q, want empty (--policy \"\" opts out of the default)", f.spec.PolicyName)
	}
}

func TestInjectEgressCA(t *testing.T) {
	dir := t.TempDir()
	// The broker generates + persists the CA on its state dir; injectEgressCA READS
	// that cert (it no longer generates a competing one). Materialize it the same
	// way the broker does, via tlsca.Load.
	if _, err := tlsca.Load(dir); err != nil {
		t.Fatal(err)
	}
	spec := &sandbox.Spec{}
	if err := injectEgressCA(spec, dir); err != nil {
		t.Fatal(err)
	}
	// The CA is mounted read-only at the expected in-pod path.
	mounted := false
	for _, m := range spec.Mounts {
		if m.Container == egressCAPath && m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("egress CA not mounted read-only; mounts = %+v", spec.Mounts)
	}
	// The common toolchains are pointed at it.
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		if spec.Env[k] != egressCAPath {
			t.Errorf("env %s = %q, want %q", k, spec.Env[k], egressCAPath)
		}
	}
	// A Setup step adds it to the OS trust bundle.
	if len(spec.Setup) == 0 || !strings.Contains(spec.Setup[len(spec.Setup)-1], "update-ca-certificates") {
		t.Errorf("expected an OS-trust Setup step; got %v", spec.Setup)
	}
	// Fail-closed: with no CA cert on disk (broker never generated one), setting up
	// interception must error rather than silently create a mismatched CA.
	if err := injectEgressCA(&sandbox.Spec{}, t.TempDir()); err == nil {
		t.Error("injectEgressCA must fail when the egress CA cert is absent")
	}
}

func TestUp_SeedsAndPersistsHarnessConfig(t *testing.T) {
	// os.UserConfigDir reads %AppData% on Windows and $XDG_CONFIG_HOME on Linux;
	// set both so harnessconfig.Dir resolves under tmp on either platform.
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	hdir := filepath.Join(tmp, "poddle", "harness", "cfgharness")
	if err := os.MkdirAll(hdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hdir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := harness.Registry{"cfgharness": &harness.FakeHarness{
		HarnessName: "cfgharness", ConfigDirs: "/root/.cfg", Provs: []string{"install"},
	}}
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: reg}, stubBroker{})
	c.SetArgs([]string{"box", "--harness", "cfgharness", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	foundMount := false
	for _, m := range f.spec.Mounts {
		if m.Host == hdir && m.ReadOnly {
			foundMount = true
		}
	}
	if !foundMount {
		t.Errorf("expected a read-only mount of %q; mounts = %+v", hdir, f.spec.Mounts)
	}
	foundCopy := false
	for _, s := range f.spec.Setup {
		if strings.Contains(s, "/root/.cfg") {
			foundCopy = true
		}
	}
	if !foundCopy {
		t.Errorf("expected a seed-copy Setup step into ConfigDir; setup = %v", f.spec.Setup)
	}
	foundVol := false
	for _, v := range f.spec.Volumes {
		if v.Container == "/root/.cfg" {
			foundVol = true
		}
	}
	if !foundVol {
		t.Errorf("expected ConfigDir as a persisted volume; volumes = %+v", f.spec.Volumes)
	}
}

func TestUp_NoSeedWhenHostConfigAbsent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// no host dir created for cfgharness -> no seed mount, but persist volume still applies.
	reg := harness.Registry{"cfgharness": &harness.FakeHarness{HarnessName: "cfgharness", ConfigDirs: "/root/.cfg"}}
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: reg}, stubBroker{})
	c.SetArgs([]string{"box", "--harness", "cfgharness", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, m := range f.spec.Mounts {
		if m.Container == "/run/poddle/harness-seed" {
			t.Errorf("no seed mount expected when host config dir is absent; mounts = %+v", f.spec.Mounts)
		}
	}
	foundVol := false
	for _, v := range f.spec.Volumes {
		if v.Container == "/root/.cfg" {
			foundVol = true
		}
	}
	if !foundVol {
		t.Errorf("persist volume for ConfigDir should still apply; volumes = %+v", f.spec.Volumes)
	}
}

func TestUp_ConfigDirEqualsStateDir_NoDuplicateVolume(t *testing.T) {
	// A harness whose ConfigDir coincides with a StateDir (e.g. codex: both
	// /root/.codex) must produce exactly ONE named volume for that path — podman
	// rejects a duplicate mount destination.
	reg := harness.Registry{"dupharness": &harness.FakeHarness{
		HarnessName: "dupharness", ConfigDirs: "/root/.dup", States: []string{"/root/.dup"},
	}}
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: reg}, stubBroker{})
	c.SetArgs([]string{"box", "--harness", "dupharness", "--detach"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	n := 0
	for _, v := range f.spec.Volumes {
		if v.Container == "/root/.dup" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected exactly one volume at /root/.dup, got %d; volumes = %+v", n, f.spec.Volumes)
	}
}

func TestUp_MCPConnector_WiresHandleEnvAndSetupAfterProvisions(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	cstore := connector.NewStore(t.TempDir())
	if _, err := cstore.Create("linear", "mcp", "https://mcp.linear.app/mcp", "", "PAT-XYZ", ""); err != nil {
		t.Fatal(err)
	}
	hreg := harness.Registry{"fake": &harness.FakeHarness{
		HarnessName: "fake", Vendors: []string{"anthropic"}, Provs: []string{"install-fake"},
		MCPWire: []string{"MCP-WIRED"},
	}}
	f := &fakeCreator{}
	capB := &captureBroker{}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: hreg,
		Connections: cstore, Templates: fakeTemplates{tpl: config.Template{Connectors: []string{"linear"}}}}, capB)
	c.SetArgs([]string{"box", "--identity", "work", "--harness", "fake", "--exec", "true"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if tok := f.spec.Env["PODDLE_MCP_LINEAR"]; !strings.HasPrefix(tok, "poddle_") {
		t.Errorf("PODDLE_MCP_LINEAR = %q, want a handle", tok)
	}
	for k, v := range f.spec.Env {
		if strings.Contains(v, "PAT-XYZ") {
			t.Fatalf("the MCP PAT leaked into pod env: %s=%s", k, v)
		}
	}
	joined := strings.Join(f.spec.Setup, "\n")
	if !strings.Contains(joined, "MCP-WIRED") {
		t.Errorf("MCPWiring setup missing: %v", f.spec.Setup)
	}
	iInstall, iWire := idxContains(f.spec.Setup, "install-fake"), idxContains(f.spec.Setup, "MCP-WIRED")
	if iInstall < 0 || iWire < 0 || iWire < iInstall {
		t.Errorf("MCP wiring must run AFTER Provisions; setup = %v", f.spec.Setup)
	}
	if capB.policy == nil {
		t.Fatal("a derived default-deny policy should be bound")
	}
	found := false
	for _, up := range capB.policy.AllowUpstreams {
		if up == "mcp.linear.app" {
			found = true
		}
	}
	if !found {
		t.Errorf("MCP host not in the egress allow-list; AllowUpstreams = %v", capB.policy.AllowUpstreams)
	}
}

// idxContains returns the first index of ss whose element contains sub, else -1.
func idxContains(ss []string, sub string) int {
	for i, s := range ss {
		if strings.Contains(s, sub) {
			return i
		}
	}
	return -1
}

func TestMcpEnvVar(t *testing.T) {
	// Non-alphanumeric characters in a connection name become underscores.
	if got := mcpEnvVar("my-mcp.srv 1"); got != "PODDLE_MCP_MY_MCP_SRV_1" {
		t.Errorf("mcpEnvVar = %q, want PODDLE_MCP_MY_MCP_SRV_1", got)
	}
}

func TestUp_MCPConnector_BareHostURLIsSchemeDefended(t *testing.T) {
	// A connection base_url with no scheme must still resolve a host for the egress
	// allow-list (applyMCPConnector prepends https:// before parsing).
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Cred: broker.Credential{Vendor: "anthropic", Secret: "s"},
	}}
	cstore := connector.NewStore(t.TempDir())
	if _, err := cstore.Create("bare", "mcp", "mcp.bare.test/mcp", "", "PAT", ""); err != nil {
		t.Fatal(err)
	}
	hreg := harness.Registry{"fake": &harness.FakeHarness{
		HarnessName: "fake", Vendors: []string{"anthropic"}, Provs: []string{"install-fake"},
		MCPWire: []string{"MCP-WIRED"},
	}}
	f := &fakeCreator{}
	capB := &captureBroker{}
	c := NewCmd(&app.App{Engine: f, Identities: store, Providers: reg, Harnesses: hreg,
		Connections: cstore, Templates: fakeTemplates{tpl: config.Template{Connectors: []string{"bare"}}}}, capB)
	c.SetArgs([]string{"box", "--identity", "work", "--harness", "fake", "--exec", "true"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if capB.policy == nil {
		t.Fatal("a derived policy should be bound")
	}
	found := false
	for _, up := range capB.policy.AllowUpstreams {
		if up == "mcp.bare.test" {
			found = true
		}
	}
	if !found {
		t.Errorf("bare-host MCP url should resolve to mcp.bare.test in egress; got %v", capB.policy.AllowUpstreams)
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
