package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newStartedInstance(t *testing.T) *Instance {
	t.Helper()
	inst, err := NewInstance(InstanceOptions{
		Title:   "test",
		Path:    t.TempDir(),
		Program: "bash",
	})
	require.NoError(t, err)
	inst.started = true
	return inst
}

func TestNoteActivity_AdvancesFromZero(t *testing.T) {
	inst := newStartedInstance(t)
	require.True(t, inst.LastActivityAt.IsZero())

	now := time.Now()
	inst.NoteActivity(now)
	require.Equal(t, now, inst.LastActivityAt)
}

func TestNoteActivity_DwellSuppressesRapidBumps(t *testing.T) {
	inst := newStartedInstance(t)
	t0 := time.Now()
	inst.NoteActivity(t0)

	// Within the dwell window the stored timestamp must not advance, so rapid
	// metadata ticks don't cause sidebar reordering (anti-thrash).
	inst.NoteActivity(t0.Add(recentActivityDwell / 2))
	require.Equal(t, t0, inst.LastActivityAt)

	// Once the dwell elapses, it advances.
	t1 := t0.Add(recentActivityDwell)
	inst.NoteActivity(t1)
	require.Equal(t, t1, inst.LastActivityAt)
}

func TestEffectiveActivityTime_Fallbacks(t *testing.T) {
	inst := newStartedInstance(t)
	created := time.Now().Add(-2 * time.Hour)
	updated := time.Now().Add(-1 * time.Hour)
	inst.CreatedAt = created
	inst.UpdatedAt = updated
	inst.LastActivityAt = time.Time{}

	// No LastActivityAt -> UpdatedAt.
	require.Equal(t, updated, inst.EffectiveActivityTime())

	// No LastActivityAt and no UpdatedAt -> CreatedAt.
	inst.UpdatedAt = time.Time{}
	require.Equal(t, created, inst.EffectiveActivityTime())

	// LastActivityAt wins when set.
	last := time.Now()
	inst.LastActivityAt = last
	require.Equal(t, last, inst.EffectiveActivityTime())
}

func TestRefreshWaitingForUser(t *testing.T) {
	tests := []struct {
		name      string
		status    Status
		autoYes   bool
		started   bool
		updated   bool
		hasPrompt bool
		want      bool
	}{
		{name: "prompt waiting, autoyes off", status: Running, hasPrompt: true, started: true, want: true},
		{name: "prompt but autoyes on (auto-resolved)", status: Running, hasPrompt: true, autoYes: true, started: true, want: false},
		{name: "prompt but screen still updating", status: Running, hasPrompt: true, updated: true, started: true, want: false},
		{name: "no prompt", status: Ready, hasPrompt: false, started: true, want: false},
		{name: "paused with prompt not pending", status: Paused, hasPrompt: true, started: true, want: false},
		{name: "loading with prompt not pending", status: Loading, hasPrompt: true, started: true, want: false},
		{name: "not started", status: Ready, hasPrompt: true, started: false, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := newStartedInstance(t)
			inst.started = tc.started
			inst.Status = tc.status
			inst.AutoYes = tc.autoYes

			inst.RefreshWaitingForUser(tc.updated, tc.hasPrompt)
			require.Equal(t, tc.want, inst.IsWaitingForUser())
		})
	}
}

func TestSetStatus_ClearsPendingOnPauseAndLoading(t *testing.T) {
	inst := newStartedInstance(t)
	inst.Status = Running
	inst.RefreshWaitingForUser(false, true)
	require.True(t, inst.IsWaitingForUser())

	inst.SetStatus(Paused)
	require.False(t, inst.IsWaitingForUser())

	// And Loading clears it too.
	inst.Status = Running
	inst.RefreshWaitingForUser(false, true)
	require.True(t, inst.IsWaitingForUser())
	inst.SetStatus(Loading)
	require.False(t, inst.IsWaitingForUser())
}

func TestLastActivityAt_RoundTrips(t *testing.T) {
	inst := newStartedInstance(t)
	last := time.Now().Truncate(time.Second)
	inst.LastActivityAt = last

	data := inst.ToInstanceData()
	require.Equal(t, last, data.LastActivityAt)
}

func TestLastActivityAt_BackCompatZero(t *testing.T) {
	// Older state.json has no last_activity_at field -> zero value -> the
	// effective time falls back to UpdatedAt/CreatedAt rather than the epoch.
	data := InstanceData{
		LastActivityAt: time.Time{},
		UpdatedAt:      time.Now().Add(-time.Hour),
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	require.True(t, data.LastActivityAt.IsZero())

	inst := &Instance{
		LastActivityAt: data.LastActivityAt,
		UpdatedAt:      data.UpdatedAt,
		CreatedAt:      data.CreatedAt,
	}
	require.Equal(t, data.UpdatedAt, inst.EffectiveActivityTime())
}
