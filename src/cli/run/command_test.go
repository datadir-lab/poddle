package run

import (
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
)

type fakeExec struct {
	engine.Engine
	id, cmd string
}

func (f *fakeExec) Exec(id, command string) error {
	f.id = id
	f.cmd = command
	return nil
}

func TestRun_JoinsCommand(t *testing.T) {
	f := &fakeExec{}
	c := NewCmd(&app.App{Engine: f})
	c.SetArgs([]string{"mybox", "ls", "-la"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.id != "mybox" || f.cmd != "ls -la" {
		t.Errorf("Exec(%q, %q), want (mybox, \"ls -la\")", f.id, f.cmd)
	}
}
