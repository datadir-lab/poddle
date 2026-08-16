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

func TestDown_RemovesByArg(t *testing.T) {
	f := &fakeRemover{}
	c := NewCmd(&app.App{Engine: f})
	c.SetArgs([]string{"mybox"})

	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.removed != "mybox" {
		t.Errorf("removed = %q, want mybox", f.removed)
	}
}
