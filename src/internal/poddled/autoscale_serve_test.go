package poddled

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionStats_FileSeam(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path,
		[]byte(`[{"name":"job","mode":"headless","size":"weak","memPercent":95}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODDLE_AUTOSCALE_STATS", path)

	got, err := productionStats(nil)() // podman engine unused when the seam is set
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "job" || got[0].Mode != "headless" || got[0].MemPercent != 95 {
		t.Fatalf("file-seam stats = %+v", got)
	}
}

func TestProductionStats_MissingFileIsEmpty(t *testing.T) {
	t.Setenv("PODDLE_AUTOSCALE_STATS", filepath.Join(t.TempDir(), "absent.json"))
	got, err := productionStats(nil)()
	if err != nil {
		t.Fatalf("a missing stats file must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a missing stats file should yield no pods, got %+v", got)
	}
}

func TestDaemon_EventRingBounded(t *testing.T) {
	d := New(nil, nil) // broker + audit unused by recordEvent's ring
	for i := 0; i < maxEvents+10; i++ {
		d.recordEvent(fmt.Sprintf("e%d", i))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.events) != maxEvents {
		t.Errorf("ring should cap at %d, got %d", maxEvents, len(d.events))
	}
	if d.events[0] != "e10" { // the 10 oldest dropped, newest kept
		t.Errorf("ring should keep the newest events; first = %q", d.events[0])
	}
}
