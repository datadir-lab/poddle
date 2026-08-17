package poddled

import (
	"context"
	"time"
)

// PodStat is one opted-in pod's live memory pressure, as the autoscaler sees it.
type PodStat struct {
	Name       string
	Mode       string  // interactive | headless | exec
	Size       string  // weak | strong
	MemPercent float64 // memory used as a percent of the pod's cap
}

// StatsSource returns the current autoscale-opted-in pods and their pressure.
type StatsSource func() ([]PodStat, error)

// Mover grows a pod onto the next size tier (production: `poddle move`).
type Mover func(pod, size string) error

// WarnFunc surfaces that an interactive pod is near its memory limit. Such pods
// are never auto-moved (a human is attached and the daemon can't reattach a
// TTY), so we notify instead.
type WarnFunc func(pod string, memPercent float64)

// sizeLadder is the grow-only size progression. A pod grows to the next entry;
// one already at the last entry can't grow further.
var sizeLadder = []string{"weak", "strong"}

// nextTier returns the tier a pod of the given size grows into, and whether a
// larger tier exists.
func nextTier(size string) (string, bool) {
	for i, s := range sizeLadder {
		if s == size {
			if i+1 < len(sizeLadder) {
				return sizeLadder[i+1], true
			}
			return "", false // already at the top
		}
	}
	return "", false // unknown size — don't guess
}

// Autoscaler is poddled's reactive memory-grow control loop. Memory is
// incompressible: you can't shrink a pod below its live usage or grow it in
// place, so the answer to memory pressure is to move the session onto a bigger
// shell. The loop watches opted-in pods and, when a headless pod stays over
// HighWater for Sustain consecutive ticks, grows it one tier (which resumes the
// agent). Interactive pods are warned, never moved. Grow-only.
type Autoscaler struct {
	Interval  time.Duration // poll cadence
	Cooldown  time.Duration // per-pod quiet period after acting (avoid thrashing)
	HighWater float64       // memory percent that counts as pressure
	Sustain   int           // consecutive over-HighWater ticks before acting (hysteresis)

	Stats StatsSource
	Move  Mover
	Warn  WarnFunc
	Log   func(format string, args ...any) // optional; nil = discard
	Now   func() time.Time                 // injectable clock; nil = time.Now

	state map[string]*podState
}

type podState struct {
	streak       int
	cooldownTill time.Time
}

// Run ticks the loop until ctx is cancelled.
func (a *Autoscaler) Run(ctx context.Context) {
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.tick()
		}
	}
}

// tick evaluates every opted-in pod once — the unit under test.
func (a *Autoscaler) tick() {
	if a.state == nil {
		a.state = map[string]*podState{}
	}
	stats, err := a.Stats()
	if err != nil {
		a.logf("autoscale: stats: %v", err)
		return
	}
	now := a.now()
	seen := make(map[string]bool, len(stats))
	for _, s := range stats {
		seen[s.Name] = true
		st := a.state[s.Name]
		if st == nil {
			st = &podState{}
			a.state[s.Name] = st
		}
		if s.MemPercent < a.HighWater {
			st.streak = 0
			continue
		}
		st.streak++
		if st.streak < a.Sustain || now.Before(st.cooldownTill) {
			continue
		}
		a.act(s)
		st.streak = 0
		st.cooldownTill = now.Add(a.Cooldown)
	}
	for name := range a.state { // forget pods that went away (down/removed)
		if !seen[name] {
			delete(a.state, name)
		}
	}
}

// act responds to a pod that has been under sustained pressure.
func (a *Autoscaler) act(s PodStat) {
	switch s.Mode {
	case "headless":
		next, ok := nextTier(s.Size)
		if !ok {
			a.logf("autoscale: %q at top tier %q, still %.0f%% mem", s.Name, s.Size, s.MemPercent)
			return
		}
		if err := a.Move(s.Name, next); err != nil {
			a.logf("autoscale: grow %q %s->%s failed: %v", s.Name, s.Size, next, err)
			return
		}
		a.logf("autoscale: grew %q %s->%s at %.0f%% mem", s.Name, s.Size, next, s.MemPercent)
	case "interactive":
		if a.Warn != nil {
			a.Warn(s.Name, s.MemPercent)
		}
		a.logf("autoscale: %q at %.0f%% mem — interactive, run `poddle move %s --size strong`", s.Name, s.MemPercent, s.Name)
	}
	// exec / unknown: nothing to scale.
}

func (a *Autoscaler) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Autoscaler) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log(format, args...)
	}
}
