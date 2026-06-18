package ui

import (
	"hangar/session"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkInstance(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{
		Title:   title,
		Path:    ".",
		Program: "echo",
	})
	require.NoError(t, err)
	return inst
}

func instanceTitles(rows []displayRow) []string {
	var out []string
	for _, r := range rows {
		if r.kind == rowInstance {
			out = append(out, r.instance.Title)
		}
	}
	return out
}

func TestBuildView_ManualEqualsInput(t *testing.T) {
	items := []*session.Instance{
		mkInstance(t, "a"),
		mkInstance(t, "b"),
		mkInstance(t, "c"),
	}
	rows := buildView(items, ModeManual, "", StatusAll)
	require.Equal(t, []string{"a", "b", "c"}, instanceTitles(rows))

	// Numbering is continuous and 1-based over visible instances.
	require.Equal(t, 1, rows[0].number)
	require.Equal(t, 2, rows[1].number)
	require.Equal(t, 3, rows[2].number)
}

func TestBuildView_FilterByTitle(t *testing.T) {
	items := []*session.Instance{
		mkInstance(t, "frontend"),
		mkInstance(t, "backend"),
		mkInstance(t, "frontend-tests"),
	}
	rows := buildView(items, ModeManual, "front", StatusAll)
	require.Equal(t, []string{"frontend", "frontend-tests"}, instanceTitles(rows))
	// Numbering remains continuous over the filtered set.
	require.Equal(t, 1, rows[0].number)
	require.Equal(t, 2, rows[1].number)
}

func TestBuildView_FilterCaseInsensitive(t *testing.T) {
	items := []*session.Instance{mkInstance(t, "MyService")}
	require.Len(t, buildView(items, ModeManual, "myservice", StatusAll), 1)
	require.Len(t, buildView(items, ModeManual, "SERVICE", StatusAll), 1)
	require.Len(t, buildView(items, ModeManual, "nomatch", StatusAll), 0)
}

func mkStatusInstance(t *testing.T, title string, status session.Status, waiting bool) *session.Instance {
	t.Helper()
	if waiting {
		inst := newPausedTestInstance(t, title, t.TempDir())
		inst.SetStatus(session.Running)
		inst.RefreshWaitingForUser(false, true)
		return inst
	}
	inst := mkInstance(t, title)
	inst.SetStatus(status)
	return inst
}

func TestStatusFilter_Cycling(t *testing.T) {
	require.Equal(t, StatusWaiting, StatusAll.Next())
	require.Equal(t, StatusBusy, StatusWaiting.Next())
	require.Equal(t, StatusIdle, StatusBusy.Next())
	require.Equal(t, StatusPaused, StatusIdle.Next())
	require.Equal(t, StatusAll, StatusPaused.Next())
}

func TestInstanceStatusBucket(t *testing.T) {
	require.Equal(t, StatusWaiting, instanceStatusBucket(mkStatusInstance(t, "waiting", session.Running, true)))
	require.Equal(t, StatusBusy, instanceStatusBucket(mkStatusInstance(t, "busy", session.Running, false)))
	require.Equal(t, StatusBusy, instanceStatusBucket(mkStatusInstance(t, "loading", session.Loading, false)))
	require.Equal(t, StatusIdle, instanceStatusBucket(mkStatusInstance(t, "idle", session.Ready, false)))
	require.Equal(t, StatusPaused, instanceStatusBucket(mkStatusInstance(t, "paused", session.Paused, false)))
}

func TestCountByStatus(t *testing.T) {
	items := []*session.Instance{
		mkStatusInstance(t, "waiting", session.Running, true),
		mkStatusInstance(t, "busy", session.Running, false),
		mkStatusInstance(t, "loading", session.Loading, false),
		mkStatusInstance(t, "idle", session.Ready, false),
		mkStatusInstance(t, "paused", session.Paused, false),
	}
	require.Equal(t, StatusCounts{Waiting: 1, Busy: 2, Idle: 1, Paused: 1, Total: 5}, CountByStatus(items))
}

func TestBuildView_StatusFilters(t *testing.T) {
	items := []*session.Instance{
		mkStatusInstance(t, "waiting", session.Running, true),
		mkStatusInstance(t, "busy", session.Running, false),
		mkStatusInstance(t, "idle", session.Ready, false),
		mkStatusInstance(t, "paused", session.Paused, false),
	}
	tests := []struct {
		filter StatusFilter
		want   []string
	}{
		{StatusAll, []string{"waiting", "busy", "idle", "paused"}},
		{StatusWaiting, []string{"waiting"}},
		{StatusBusy, []string{"busy"}},
		{StatusIdle, []string{"idle"}},
		{StatusPaused, []string{"paused"}},
	}
	for _, tc := range tests {
		t.Run(tc.filter.String(), func(t *testing.T) {
			rows := buildView(items, ModeManual, "", tc.filter)
			require.Equal(t, tc.want, instanceTitles(rows))
		})
	}
}

func TestBuildView_StatusFilterComposesWithModeAndTextFilter(t *testing.T) {
	items := []*session.Instance{
		mkStatusInstance(t, "frontend-waiting", session.Running, true),
		mkStatusInstance(t, "backend-waiting", session.Running, true),
		mkStatusInstance(t, "frontend-busy", session.Running, false),
	}

	rows := buildView(items, ModePinnedPending, "front", StatusWaiting)
	require.Equal(t, []string{pendingHeader, workspacesHeader}, headersOf(rows))
	require.Equal(t, []string{"frontend-waiting"}, instanceTitles(rows))
}

func TestMatchesFilter_TitleAndPath(t *testing.T) {
	inst := mkInstance(t, "alpha")
	require.True(t, matchesFilter(inst, ""))        // empty matches all
	require.True(t, matchesFilter(inst, "  "))      // whitespace-only matches all
	require.True(t, matchesFilter(inst, "alph"))    // title substring
	require.False(t, matchesFilter(inst, "zzz"))    // no match
	require.True(t, matchesFilter(inst, inst.Path)) // path match
}

func TestSidebarMode_CyclingAndValidation(t *testing.T) {
	require.Equal(t, ModeGroupByRepo, ModeManual.Next())
	require.Equal(t, ModeRecentActivity, ModeGroupByRepo.Next())
	require.Equal(t, ModePinnedPending, ModeRecentActivity.Next())
	require.Equal(t, ModeManual, ModePinnedPending.Next()) // wraps

	require.Equal(t, ModePinnedPending, ModeManual.Prev()) // wraps backward
	require.Equal(t, ModeManual, ModeGroupByRepo.Prev())

	require.Equal(t, ModeManual, ValidSidebarMode(-1))
	require.Equal(t, ModeManual, ValidSidebarMode(999))
	require.Equal(t, ModeRecentActivity, ValidSidebarMode(int(ModeRecentActivity)))
}

// mkItem builds a synthetic sidebarItem with controlled repo/activity/pending
// data so the mode builders can be tested without real git worktrees.
func mkItem(t *testing.T, title, repoKey, repoLabel string, activity time.Time, pending bool) sidebarItem {
	return sidebarItem{
		instance:     mkInstance(t, title),
		repoKey:      repoKey,
		repoLabel:    repoLabel,
		activityTime: activity,
		pending:      pending,
	}
}

func headersOf(rows []displayRow) []string {
	var out []string
	for _, r := range rows {
		if r.kind == rowHeader {
			out = append(out, r.header)
		}
	}
	return out
}

func TestBuildGroupByRepo_GroupsHeadersAndNumbering(t *testing.T) {
	now := time.Now()
	items := []sidebarItem{
		mkItem(t, "a1", "/work/alpha", "alpha", now, false),
		mkItem(t, "b1", "/work/beta", "beta", now, false),
		mkItem(t, "a2", "/work/alpha", "alpha", now, false),
	}
	rows := buildGroupByRepo(items)

	require.Equal(t, []string{"alpha", "beta"}, headersOf(rows)) // alphabetical
	require.Equal(t, rowHeader, rows[0].kind)
	require.Equal(t, "a1", rows[1].instance.Title)
	require.Equal(t, "a2", rows[2].instance.Title)
	require.Equal(t, rowHeader, rows[3].kind)
	require.Equal(t, "b1", rows[4].instance.Title)
	// Numbering is continuous across instances, ignoring headers.
	require.Equal(t, 1, rows[1].number)
	require.Equal(t, 2, rows[2].number)
	require.Equal(t, 3, rows[4].number)
}

func TestBuildGroupByRepo_SingleRepoStillHasHeader(t *testing.T) {
	rows := buildGroupByRepo([]sidebarItem{
		mkItem(t, "x", "/r/only", "only", time.Now(), false),
	})
	require.Equal(t, []string{"only"}, headersOf(rows))
	require.Len(t, rows, 2)
}

func TestBuildGroupByRepo_DuplicateBasenameDisambiguated(t *testing.T) {
	keyA := filepath.Join("parentA", "repo")
	keyB := filepath.Join("parentB", "repo")
	items := []sidebarItem{
		mkItem(t, "p", keyA, "repo", time.Now(), false),
		mkItem(t, "q", keyB, "repo", time.Now(), false),
	}
	rows := buildGroupByRepo(items)
	wantA := "repo (" + filepath.Base(filepath.Dir(keyA)) + ")"
	wantB := "repo (" + filepath.Base(filepath.Dir(keyB)) + ")"
	require.Equal(t, []string{wantA, wantB}, headersOf(rows))
}

func TestBuildGroupByRepo_NoRepoBucket(t *testing.T) {
	items := []sidebarItem{
		mkItem(t, "started", "/r/realrepo", "realrepo", time.Now(), false),
		mkItem(t, "unstarted", "", "", time.Now(), false),
	}
	rows := buildGroupByRepo(items)
	// "(no repo)" sorts before "realrepo".
	require.Equal(t, []string{noRepoLabel, "realrepo"}, headersOf(rows))
}

func TestBuildRecentActivity_OrdersByActivityDesc(t *testing.T) {
	now := time.Now()
	items := []sidebarItem{
		mkItem(t, "old", "", "", now.Add(-2*time.Hour), false),
		mkItem(t, "new", "", "", now, false),
		mkItem(t, "mid", "", "", now.Add(-1*time.Hour), false),
	}
	require.Equal(t, []string{"new", "mid", "old"}, instanceTitles(buildRecentActivity(items)))
}

func TestBuildRecentActivity_EqualActivityIsStableAndDeterministic(t *testing.T) {
	batch := time.Now()
	// All three updated in the same metadata batch -> equal activity timestamps.
	a := mkItem(t, "a", "", "", batch, false)
	a.instance.CreatedAt = time.Unix(100, 0)
	b := mkItem(t, "b", "", "", batch, false)
	b.instance.CreatedAt = time.Unix(300, 0)
	c := mkItem(t, "c", "", "", batch, false)
	c.instance.CreatedAt = time.Unix(200, 0)
	items := []sidebarItem{a, b, c}

	rows1 := instanceTitles(buildRecentActivity(items))
	rows2 := instanceTitles(buildRecentActivity(items))
	// Tiebreak by CreatedAt desc -> b, c, a. No thrash across repeated builds.
	require.Equal(t, []string{"b", "c", "a"}, rows1)
	require.Equal(t, rows1, rows2)
}

func TestBuildRecentActivity_UpdatedInstanceMovesToTop(t *testing.T) {
	now := time.Now()
	items := []sidebarItem{
		mkItem(t, "a", "", "", now.Add(-time.Hour), false),
		mkItem(t, "b", "", "", now.Add(-2*time.Hour), false),
	}
	require.Equal(t, []string{"a", "b"}, instanceTitles(buildRecentActivity(items)))

	items[1].activityTime = now // b just became active
	require.Equal(t, []string{"b", "a"}, instanceTitles(buildRecentActivity(items)))
}

func TestBuildPinnedPending_PartitionsHeadersAndNumbering(t *testing.T) {
	now := time.Now()
	items := []sidebarItem{
		mkItem(t, "w1", "", "", now, false),
		mkItem(t, "p1", "", "", now, true),
		mkItem(t, "w2", "", "", now, false),
		mkItem(t, "p2", "", "", now, true),
	}
	rows := buildPinnedPending(items)

	require.Equal(t, []string{pendingHeader, workspacesHeader}, headersOf(rows))
	require.Equal(t, "p1", rows[1].instance.Title) // pending, canonical order
	require.Equal(t, "p2", rows[2].instance.Title)
	require.Equal(t, "w1", rows[4].instance.Title) // rest, canonical order
	require.Equal(t, "w2", rows[5].instance.Title)
	// Continuous numbering across both sections.
	require.Equal(t, 1, rows[1].number)
	require.Equal(t, 2, rows[2].number)
	require.Equal(t, 3, rows[4].number)
	require.Equal(t, 4, rows[5].number)
}

func TestBuildPinnedPending_NoPendingRendersPlainList(t *testing.T) {
	now := time.Now()
	items := []sidebarItem{
		mkItem(t, "w1", "", "", now, false),
		mkItem(t, "w2", "", "", now, false),
	}
	rows := buildPinnedPending(items)
	require.Empty(t, headersOf(rows))
	require.Equal(t, []string{"w1", "w2"}, instanceTitles(rows))
}
