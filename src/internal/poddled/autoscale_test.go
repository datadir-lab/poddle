package poddled

import (
	"testing"
	"time"
)

// scaler wires an Autoscaler to scripted stats + recorders with a fixed clock.
type recorder struct {
	moves []string // "pod->size"
	warns []string // "pod"
}

func newScaler(stats func() []PodStat, clock *time.Time) (*Autoscaler, *recorder) {
	r := &recorder{}
	a := &Autoscaler{
		Interval: time.Second, Cooldown: 60 * time.Second,
		HighWater: 85, Sustain: 3,
		Stats: func() ([]PodStat, error) { return stats(), nil },
		Move:  func(pod, size string) error { r.moves = append(r.moves, pod+"->"+size); return nil },
		Warn:  func(pod string, _ float64) { r.warns = append(r.warns, pod) },
		Now:   func() time.Time { return *clock },
	}
	return a, r
}

func ticks(a *Autoscaler, n int) {
	for i := 0; i < n; i++ {
		a.tick()
	}
}

func TestAutoscaler_GrowsAfterSustainedPressure(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "weak", MemPercent: 92}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 2) // under Sustain (3) — no action yet
	if len(r.moves) != 0 {
		t.Fatalf("should not grow before %d sustained ticks; moves = %v", a.Sustain, r.moves)
	}
	ticks(a, 1) // 3rd consecutive high tick — grow
	if len(r.moves) != 1 || r.moves[0] != "job->strong" {
		t.Fatalf("should grow weak->strong once; moves = %v", r.moves)
	}
}

func TestAutoscaler_IgnoresTransientSpike(t *testing.T) {
	clock := time.Unix(0, 0)
	mem := 92.0
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "weak"}}
	a, r := newScaler(func() []PodStat { pods[0].MemPercent = mem; return pods }, &clock)

	ticks(a, 2) // two high ticks
	mem = 40    // then a dip — resets the streak
	ticks(a, 1)
	mem = 92 // high again, but only 2 in a row after the reset
	ticks(a, 2)
	if len(r.moves) != 0 {
		t.Fatalf("a transient spike must not trigger a grow; moves = %v", r.moves)
	}
}

func TestAutoscaler_CooldownBlocksSecondMove(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "weak", MemPercent: 95}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 3) // grow once
	ticks(a, 5) // still hot, but inside the 60s cooldown
	if len(r.moves) != 1 {
		t.Fatalf("cooldown should block a second move; moves = %v", r.moves)
	}
	clock = clock.Add(61 * time.Second) // cooldown elapsed
	ticks(a, 3)                         // re-accumulate Sustain then act
	if len(r.moves) != 2 {
		t.Fatalf("should move again after cooldown; moves = %v", r.moves)
	}
}

func TestAutoscaler_GrowOnlyAtTopTier(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "strong", MemPercent: 97}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 6)
	if len(r.moves) != 0 {
		t.Fatalf("a top-tier pod cannot grow further; moves = %v", r.moves)
	}
}

func TestAutoscaler_InteractiveWarnsNeverMoves(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "dev", Mode: "interactive", Size: "weak", MemPercent: 95}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 3)
	if len(r.moves) != 0 {
		t.Fatalf("interactive pods are never auto-moved; moves = %v", r.moves)
	}
	if len(r.warns) != 1 || r.warns[0] != "dev" {
		t.Fatalf("interactive pod should be warned once; warns = %v", r.warns)
	}
	ticks(a, 4) // still hot but within cooldown — no repeat warn
	if len(r.warns) != 1 {
		t.Fatalf("should warn once per episode (cooldown); warns = %v", r.warns)
	}
}

func TestAutoscaler_BelowThresholdNoop(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "weak", MemPercent: 50}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 10)
	if len(r.moves) != 0 || len(r.warns) != 0 {
		t.Fatalf("a pod under HighWater must be left alone; moves=%v warns=%v", r.moves, r.warns)
	}
}

func TestAutoscaler_ExecIgnored(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "one", Mode: "exec", Size: "weak", MemPercent: 99}}
	a, r := newScaler(func() []PodStat { return pods }, &clock)

	ticks(a, 5)
	if len(r.moves) != 0 || len(r.warns) != 0 {
		t.Fatalf("exec/one-shot pods have nothing to scale; moves=%v warns=%v", r.moves, r.warns)
	}
}

func TestAutoscaler_ForgetsGonePods(t *testing.T) {
	clock := time.Unix(0, 0)
	pods := []PodStat{{Name: "job", Mode: "headless", Size: "weak", MemPercent: 92}}
	present := true
	a, r := newScaler(func() []PodStat {
		if !present {
			return nil
		}
		return pods
	}, &clock)

	ticks(a, 2) // streak = 2
	present = false
	ticks(a, 1) // pod gone — state forgotten
	present = true
	ticks(a, 2) // streak restarts from 0, so only 2 — no move
	if len(r.moves) != 0 {
		t.Fatalf("a pod that vanished should reset its streak; moves = %v", r.moves)
	}
	if _, ok := a.state["job"]; ok && !present {
		t.Error("gone pods should be dropped from state")
	}
}
