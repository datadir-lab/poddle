package poddled

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// podmanStats is the slice of the podman engine the production stats source uses.
type podmanStats interface {
	AutoscaleStats() ([]sandbox.MemStat, error)
}

// productionStats returns the StatsSource the daemon runs with. When
// PODDLE_AUTOSCALE_STATS names a file, it reads synthetic pod stats from that
// JSON — the seam that lets an e2e drive memory pressure where `podman stats`
// has no cgroup. Otherwise it reads live stats from podman.
func productionStats(eng podmanStats) StatsSource {
	if path := os.Getenv("PODDLE_AUTOSCALE_STATS"); path != "" {
		return func() ([]PodStat, error) { return readStatsFile(path) }
	}
	return func() ([]PodStat, error) {
		ms, err := eng.AutoscaleStats()
		if err != nil {
			return nil, err
		}
		out := make([]PodStat, len(ms))
		for i, m := range ms {
			out[i] = PodStat{Name: m.Name, Mode: m.Mode, Size: m.Size, MemPercent: m.MemPercent}
		}
		return out, nil
	}
}

// readStatsFile loads synthetic PodStats from a JSON-array file. A missing file
// means no pods (not an error), so an e2e can create it partway through a run.
func readStatsFile(path string) ([]PodStat, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stats []PodStat
	if err := json.Unmarshal(b, &stats); err != nil {
		return nil, fmt.Errorf("autoscale stats file %s: %w", path, err)
	}
	return stats, nil
}

// selfMover grows a pod by running `poddle move <pod> --size <size> --detach`
// with this same binary (the daemon IS the poddle CLI). Headless pods
// auto-resume; --detach keeps the daemon from blocking on attach.
func selfMover() Mover {
	self, _ := os.Executable()
	return func(pod, size string) error {
		if self == "" {
			return fmt.Errorf("cannot locate the poddle binary to move %q", pod)
		}
		out, err := exec.Command(self, "move", pod, "--size", size, "--detach").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}
}

// autoscaleInterval reads PODDLE_AUTOSCALE_INTERVAL (a Go duration like "1s"),
// defaulting to 15s. The seam lets an e2e tick the loop fast.
func autoscaleInterval() time.Duration {
	if v := os.Getenv("PODDLE_AUTOSCALE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Second
}
