package attach

import (
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/engine"
)

type fakeAttacher struct {
	engine.Engine
	attached string
}

func (f *fakeAttacher) Attach(id string) error { f.attached = id; return nil }

func TestAttach_ByArg(t *testing.T) {
	f := &fakeAttacher{}
	c := NewCmd(&app.App{Engine: f})
	c.SetArgs([]string{"mybox"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.attached != "mybox" {
		t.Errorf("attached = %q, want mybox", f.attached)
	}
}
