package ui

import (
	"hangar/session"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnimator_FirstRetargetDoesNotPulse(t *testing.T) {
	a := newAnimator()
	active := a.retarget([]*session.Instance{mkInstance(t, "a"), mkInstance(t, "b")})
	require.False(t, active)
	require.False(t, a.active())
}

func TestAnimator_PulsesMovedRows(t *testing.T) {
	a := newAnimator()
	x, y := mkInstance(t, "x"), mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y}) // baseline
	active := a.retarget([]*session.Instance{y, x})
	require.True(t, active)
	require.Greater(t, a.pulseLevel(x), 0.0)
	require.Greater(t, a.pulseLevel(y), 0.0)
}

func TestAnimator_NewInstancePulsesButStationaryDoesNot(t *testing.T) {
	a := newAnimator()
	x := mkInstance(t, "x")
	a.retarget([]*session.Instance{x})
	y := mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y}) // x stays slot 0; y is new
	require.Equal(t, 0.0, a.pulseLevel(x))
	require.Greater(t, a.pulseLevel(y), 0.0)
}

func TestAnimator_StepSettles(t *testing.T) {
	a := newAnimator()
	x, y := mkInstance(t, "x"), mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y})
	a.retarget([]*session.Instance{y, x})
	require.True(t, a.active())

	for i := 0; i < pulseFrames; i++ {
		a.Step()
	}
	require.False(t, a.active())
	require.Equal(t, 0.0, a.pulseLevel(x))
}

func TestAnimator_PulseLevelFades(t *testing.T) {
	a := newAnimator()
	x, y := mkInstance(t, "x"), mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y})
	a.retarget([]*session.Instance{y, x})
	l0 := a.pulseLevel(x)
	a.Step()
	require.Less(t, a.pulseLevel(x), l0)
}

func TestAnimator_DropsInvisibleInstances(t *testing.T) {
	a := newAnimator()
	x, y := mkInstance(t, "x"), mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y})
	a.retarget([]*session.Instance{y, x}) // both pulse
	// x disappears (e.g. filtered out); its pulse is dropped without panic.
	a.retarget([]*session.Instance{y})
	require.Equal(t, 0.0, a.pulseLevel(x))
}

func TestAnimator_MidAnimationRetargetSettlesToFinal(t *testing.T) {
	a := newAnimator()
	x, y, z := mkInstance(t, "x"), mkInstance(t, "y"), mkInstance(t, "z")
	a.retarget([]*session.Instance{x, y, z})
	a.retarget([]*session.Instance{y, x, z}) // first change
	a.Step()
	// A second change arrives mid-animation: retarget (no second loop needed).
	a.retarget([]*session.Instance{z, y, x})
	require.True(t, a.active())
	// Stepping enough frames always settles to a quiescent state.
	for i := 0; i < pulseFrames+2; i++ {
		a.Step()
	}
	require.False(t, a.active())
}

func TestAnimator_Reset(t *testing.T) {
	a := newAnimator()
	x, y := mkInstance(t, "x"), mkInstance(t, "y")
	a.retarget([]*session.Instance{x, y})
	a.retarget([]*session.Instance{y, x})
	require.True(t, a.active())

	a.reset([]*session.Instance{y, x})
	require.False(t, a.active())
	// From the reset baseline, the same order does not pulse but a move does.
	require.False(t, a.retarget([]*session.Instance{y, x}))
	require.True(t, a.retarget([]*session.Instance{x, y}))
}
