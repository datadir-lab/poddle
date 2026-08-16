package up

import (
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

func TestResize_BySize(t *testing.T) {
	f := &fakeCreator{}
	c := NewResizeCmd(&app.App{Engine: f})
	c.SetArgs([]string{"box", "strong"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.resized) != 1 || f.resized[0] != "box:8:16g" {
		t.Errorf("resized = %v, want [box:8:16g]", f.resized)
	}
}

func TestResize_Flags(t *testing.T) {
	f := &fakeCreator{}
	c := NewResizeCmd(&app.App{Engine: f})
	c.SetArgs([]string{"box", "--cpus", "4", "--memory", "8g"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(f.resized) != 1 || f.resized[0] != "box:4:8g" {
		t.Errorf("resized = %v, want [box:4:8g]", f.resized)
	}
}

func TestResize_NeedsSpec(t *testing.T) {
	f := &fakeCreator{}
	c := NewResizeCmd(&app.App{Engine: f})
	c.SetArgs([]string{"box"}) // no size, no flags
	if err := c.Execute(); err == nil {
		t.Error("expected an error with no size or --cpus/--memory")
	}
}
