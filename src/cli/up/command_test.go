package up

import (
	"bytes"
	"strings"
	"testing"

	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

type fakeCreator struct {
	spec      sandbox.Spec
	attached  string
	createErr error
	attachErr error
}

func (f *fakeCreator) Create(s sandbox.Spec) (string, error) {
	f.spec = s
	return "cid123", f.createErr
}

func (f *fakeCreator) Attach(id string) error {
	f.attached = id
	return f.attachErr
}

func TestUp_CreatesAndAttaches(t *testing.T) {
	f := &fakeCreator{}
	c := NewCmd(f, nil, nil)
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
	c := NewCmd(f, nil, nil)
	c.SetArgs([]string{"--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.attached != "" {
		t.Errorf("attach should be skipped, got %q", f.attached)
	}
}

func TestUp_WithIdentity_MaterializesEnv(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	fake := &idn.FakeProvider{
		ProviderName: "anthropic", Authed: true,
		Mat: idn.Materialization{Env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}},
	}
	reg := idn.Registry{"anthropic": fake}

	f := &fakeCreator{}
	c := NewCmd(f, store, reg)
	c.SetArgs([]string{"mybox", "--identity", "work", "--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.spec.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "tok" {
		t.Errorf("identity env not applied: %v", f.spec.Env)
	}
}

func TestUp_WithIdentity_ReauthsWhenStale(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	fake := &idn.FakeProvider{ProviderName: "anthropic", Authed: false} // not authed yet
	reg := idn.Registry{"anthropic": fake}

	f := &fakeCreator{}
	c := NewCmd(f, store, reg)
	c.SetArgs([]string{"mybox", "--identity", "work", "--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.AuthCalled {
		t.Error("expected re-auth on the client when the identity was not authenticated")
	}
}
