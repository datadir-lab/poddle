package up

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/sandbox"
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
	c := NewCmd(f)
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
	if f.spec.Runtime != "container" {
		t.Errorf("runtime = %q", f.spec.Runtime)
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
	c := NewCmd(f)
	c.SetArgs([]string{"--detach"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.attached != "" {
		t.Errorf("attach should be skipped, got %q", f.attached)
	}
}
