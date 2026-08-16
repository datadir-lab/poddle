package ls

import (
	"bytes"
	"strings"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

type fakeLister struct {
	engine.Engine
	list []sandbox.Sandbox
	err  error
}

func (f fakeLister) List() ([]sandbox.Sandbox, error) { return f.list, f.err }

func TestLs_RendersTable(t *testing.T) {
	p := fakeLister{list: []sandbox.Sandbox{
		{ID: "abc123", Name: "app", Template: "python", Size: "strong", State: "running", Repo: "r"},
	}}
	c := NewCmd(&app.App{Engine: p})
	var out bytes.Buffer
	c.SetOut(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	for _, w := range []string{"ID", "NAME", "app", "python", "running"} {
		if !strings.Contains(s, w) {
			t.Errorf("output missing %q in:\n%s", w, s)
		}
	}
}

func TestLs_Empty(t *testing.T) {
	c := NewCmd(&app.App{Engine: fakeLister{}})
	var out bytes.Buffer
	c.SetOut(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "ID") {
		t.Errorf("header missing when empty:\n%s", out.String())
	}
}
