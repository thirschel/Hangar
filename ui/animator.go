package ui

import "claude-squad/session"

// pulseFrames is how many animation frames a row's highlight pulse lasts.
const pulseFrames = 5

// animator is a standalone, timer-free decorator for sidebar rows. It tracks each
// instance's previous visible slot and, when an instance moves (or appears), it
// starts a fading highlight "pulse" so the eye can track what changed. A bulk
// change (mode switch, large re-sort) simply pulses every changed row at once
// (the "crossfade").
//
// The animator holds no wall-clock: frames are advanced explicitly via Step(),
// which makes it fully deterministic and unit-testable. It is purely cosmetic —
// selection, ordering, and persistence are committed by the List immediately and
// are never read from or mutated here.
type animator struct {
	// pulses maps an instance to the number of frames of highlight remaining.
	pulses map[*session.Instance]int
	// prevSlots maps an instance to its previous visible slot index.
	prevSlots map[*session.Instance]int
	// initialized is false until the first retarget, so the initial paint does
	// not pulse every row.
	initialized bool
}

func newAnimator() *animator {
	return &animator{
		pulses:    make(map[*session.Instance]int),
		prevSlots: make(map[*session.Instance]int),
	}
}

// retarget compares the new visible instance order against the previous one and
// starts a pulse for every instance whose slot changed (or that is newly
// visible). Pulses for instances that are no longer visible are dropped. It
// returns true if any pulse is active afterwards. The first call only records
// slots (no pulse), so startup doesn't flash.
func (a *animator) retarget(visible []*session.Instance) bool {
	newSlots := make(map[*session.Instance]int, len(visible))
	for i, inst := range visible {
		newSlots[inst] = i
	}

	if a.initialized {
		for inst, slot := range newSlots {
			prev, existed := a.prevSlots[inst]
			if !existed || prev != slot {
				a.pulses[inst] = pulseFrames
			}
		}
		// Drop pulses for instances that are no longer visible.
		for inst := range a.pulses {
			if _, ok := newSlots[inst]; !ok {
				delete(a.pulses, inst)
			}
		}
	}

	a.prevSlots = newSlots
	a.initialized = true
	return a.active()
}

// reset records the current visible slots without pulsing and clears any active
// pulses. Used when animations are disabled so layout changes are instant.
func (a *animator) reset(visible []*session.Instance) {
	a.prevSlots = make(map[*session.Instance]int, len(visible))
	for i, inst := range visible {
		a.prevSlots[inst] = i
	}
	a.pulses = make(map[*session.Instance]int)
	a.initialized = true
}

// active reports whether any pulse is currently in progress.
func (a *animator) active() bool {
	return len(a.pulses) > 0
}

// Step advances the animation by one frame, decrementing every pulse. It returns
// true if any pulse remains active.
func (a *animator) Step() bool {
	for inst, n := range a.pulses {
		if n <= 1 {
			delete(a.pulses, inst)
		} else {
			a.pulses[inst] = n - 1
		}
	}
	return a.active()
}

// pulseLevel returns the highlight intensity for an instance in [0,1], where 1 is
// the freshest pulse and 0 means no pulse.
func (a *animator) pulseLevel(inst *session.Instance) float64 {
	n, ok := a.pulses[inst]
	if !ok {
		return 0
	}
	return float64(n) / float64(pulseFrames)
}
