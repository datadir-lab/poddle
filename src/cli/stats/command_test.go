package stats

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

type fakeStats struct {
	engine.Engine
	stats []sandbox.Stat
	err   error
}

func (f *fakeStats) Stats() ([]sandbox.Stat, error) { return f.stats, f.err }

func TestStats_RendersTable(t *testing.T) {
	f := &fakeStats{stats: []sandbox.Stat{
		{Name: "box", CPU: "12.5%", Mem: "512MB / 4GB", MemPerc: "12.5%"},
	}}
	c := NewCmd(&app.App{Engine: f})
	var out bytes.Buffer
	c.SetOut(&out)
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, w := range []string{"NAME", "CPU", "MEM%", "box", "12.5%", "512MB / 4GB"} {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q:\n%s", w, got)
		}
	}
}
