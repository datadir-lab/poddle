package down

import (
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/engine"
)

type fakeRemover struct {
	engine.Engine
	removed string
	err     error
}

func (f *fakeRemover) Remove(id string) error {
	f.removed = id
	return f.err
}

type spyRevoker struct{ revoked string }

func (s *spyRevoker) RevokePod(pod string) error { s.revoked = pod; return nil }

func TestDown_RevokesThenRemoves(t *testing.T) {
	f := &fakeRemover{}
	r := &spyRevoker{}
	c := NewCmd(&app.App{Engine: f}, r)
	c.SetArgs([]string{"mybox"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.revoked != "mybox" {
		t.Errorf("revoked = %q, want mybox", r.revoked)
	}
	if f.removed != "mybox" {
		t.Errorf("removed = %q, want mybox", f.removed)
	}
}
