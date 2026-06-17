package ui

import (
	"claude-squad/session"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/require"
)

func newTestList(titles ...string) *List {
	s := spinner.New()
	l := NewList(&s, false)
	for _, t := range titles {
		inst, _ := session.NewInstance(session.InstanceOptions{
			Title:   t,
			Path:    ".",
			Program: "echo",
		})
		l.AddInstance(inst)
	}
	return l
}

func titlesOf(insts []*session.Instance) []string {
	out := make([]string, len(insts))
	for i, inst := range insts {
		out[i] = inst.Title
	}
	return out
}

func TestMoveSelectedUp(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"
	b := l.GetSelectedInstance()
	require.Equal(t, "b", b.Title)

	moved := l.MoveSelectedUp()
	require.True(t, moved)
	require.Equal(t, []string{"b", "a", "c"}, titlesOf(l.items))
	// Selection follows the instance by identity, not by index.
	require.Same(t, b, l.GetSelectedInstance())
}

func TestMoveSelectedUp_AtTop(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(0)

	require.False(t, l.MoveSelectedUp())
	require.Equal(t, []string{"a", "b", "c"}, titlesOf(l.items))
}

func TestMoveSelectedDown(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"
	b := l.GetSelectedInstance()

	moved := l.MoveSelectedDown()
	require.True(t, moved)
	require.Equal(t, []string{"a", "c", "b"}, titlesOf(l.items))
	require.Same(t, b, l.GetSelectedInstance())
}

func TestMoveSelectedDown_AtBottom(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(2)

	require.False(t, l.MoveSelectedDown())
	require.Equal(t, []string{"a", "b", "c"}, titlesOf(l.items))
}

func TestMoveWithSingleItem(t *testing.T) {
	l := newTestList("only")
	l.SetSelectedInstance(0)

	require.False(t, l.MoveSelectedUp())
	require.False(t, l.MoveSelectedDown())
}

func TestUpDown_WrapAmongVisibleInstances(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(0)

	l.Down()
	require.Equal(t, "b", l.GetSelectedInstance().Title)
	l.Down()
	require.Equal(t, "c", l.GetSelectedInstance().Title)
	l.Down() // wraps
	require.Equal(t, "a", l.GetSelectedInstance().Title)
	l.Up() // wraps back
	require.Equal(t, "c", l.GetSelectedInstance().Title)
}

func TestUpDown_SkipsHeaders(t *testing.T) {
	l := newTestList("a", "b")
	a, b := l.items[0], l.items[1]
	// Inject a synthetic sectioned layout (as Group-by-repo / Pinned-pending
	// would produce) and verify navigation only lands on instance rows.
	l.rows = []displayRow{
		{kind: rowHeader, header: "Section 1"},
		{kind: rowInstance, instance: a, number: 1},
		{kind: rowHeader, header: "Section 2"},
		{kind: rowInstance, instance: b, number: 2},
	}
	l.selected = a

	l.Down()
	require.Same(t, b, l.GetSelectedInstance())
	l.Down() // wraps, skipping both headers
	require.Same(t, a, l.GetSelectedInstance())
}

func TestGetSelectedInstance_HiddenByFilterReturnsNil(t *testing.T) {
	l := newTestList("alpha", "beta")
	l.SetSelectedInstance(0) // select "alpha"
	require.Equal(t, "alpha", l.GetSelectedInstance().Title)

	// Filter excludes the selected instance: the desired selection is retained
	// internally but GetSelectedInstance() reports nil so the preview empties.
	l.SetFilter("beta")
	require.Nil(t, l.GetSelectedInstance())
	require.Equal(t, 1, l.VisibleCount())

	// Clearing the filter restores the original visible selection.
	l.SetFilter("")
	require.Equal(t, "alpha", l.GetSelectedInstance().Title)
}

func TestRemoveInstance_ClampsSelectionToNearestVisible(t *testing.T) {
	l := newTestList("a", "b", "c")
	b := l.items[1]
	l.SelectInstance(b)

	l.RemoveInstance(b)
	require.Equal(t, []string{"a", "c"}, titlesOf(l.items))
	// Removed the selected (index 1): clamp forward to the instance now at index 1.
	require.Equal(t, "c", l.GetSelectedInstance().Title)
}

func TestRemoveInstance_NonSelectedKeepsSelection(t *testing.T) {
	l := newTestList("a", "b", "c")
	c := l.items[2]
	l.SelectInstance(c)

	l.RemoveInstance(l.items[0]) // remove "a"
	require.Equal(t, []string{"b", "c"}, titlesOf(l.items))
	require.Same(t, c, l.GetSelectedInstance())
}

func TestRemoveInstance_LastSelectedWithOthersClampsWithoutPanic(t *testing.T) {
	// Regression: removing the last canonical instance while it is selected (and
	// others remain) must clamp backward, not panic with index-out-of-range.
	l := newTestList("a", "b", "c")
	c := l.items[2]
	l.SelectInstance(c)

	require.NotPanics(t, func() { l.RemoveInstance(c) })
	require.Equal(t, []string{"a", "b"}, titlesOf(l.items))
	require.Equal(t, "b", l.GetSelectedInstance().Title) // clamps to the new last
}

func TestRemoveInstance_LastLeavesNoSelection(t *testing.T) {
	l := newTestList("only")
	l.RemoveInstance(l.items[0])
	require.Equal(t, 0, l.NumInstances())
	require.Nil(t, l.GetSelectedInstance())
	require.False(t, l.HasVisible())
}

func TestAddInstance_SelectsFirst(t *testing.T) {
	s := spinner.New()
	l := NewList(&s, false)
	require.Nil(t, l.GetSelectedInstance())

	inst, _ := session.NewInstance(session.InstanceOptions{Title: "first", Path: ".", Program: "echo"})
	l.AddInstance(inst)
	require.Same(t, inst, l.GetSelectedInstance())
}

func TestFilter_VisibleCountAndCleared(t *testing.T) {
	l := newTestList("frontend", "backend", "frontend-tests")
	require.Equal(t, 3, l.VisibleCount())

	l.SetFilter("front")
	require.Equal(t, 2, l.VisibleCount())
	require.Equal(t, []string{"frontend", "frontend-tests"}, titlesOf(l.visibleInstances()))

	l.SetFilter("")
	require.Equal(t, 3, l.VisibleCount())
}

func TestFilter_ComposesWithEveryMode(t *testing.T) {
	for _, mode := range []SidebarMode{ModeManual, ModeGroupByRepo, ModeRecentActivity, ModePinnedPending} {
		l := newTestList("frontend", "backend", "frontend-tests")
		l.SetMode(mode)
		l.SetFilter("front")
		require.Equal(t, 2, l.VisibleCount(), "mode %v", mode)
	}
}

func TestMotion_PulsesOnReorderWhenEnabled(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSize(80, 40) // big enough that motion isn't auto-disabled
	l.ResetMotion()
	l.SetSelectedInstance(1)
	require.True(t, l.MoveSelectedUp())
	require.True(t, l.IsAnimating())
}

func TestMotion_InstantWhenDisabled(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSize(80, 40)
	l.SetMotionConfig(false) // reduced motion / config off
	l.ResetMotion()
	l.SetSelectedInstance(1)
	require.True(t, l.MoveSelectedUp())
	require.False(t, l.IsAnimating())
}

func TestMotion_InstantWhenTerminalTooSmall(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSize(10, 4) // too small -> instant
	l.ResetMotion()
	l.SetSelectedInstance(1)
	require.True(t, l.MoveSelectedUp())
	require.False(t, l.IsAnimating())
}

func TestString_RendersAllModesSearchAndEmptyWithoutPanic(t *testing.T) {
	l := newTestList("alpha", "beta", "gamma")
	l.SetSize(40, 20)

	for _, mode := range []SidebarMode{ModeManual, ModeGroupByRepo, ModeRecentActivity, ModePinnedPending} {
		l.SetMode(mode)
		require.NotEmpty(t, l.String())
	}

	// Search bar open with a query.
	l.SetMode(ModeManual)
	l.SetSearching(true)
	l.SetFilter("alph")
	out := l.String()
	require.Contains(t, out, "Search:")

	// No-match state.
	l.SetFilter("zzzzz")
	require.Contains(t, l.String(), "no matches")
}

func TestListTracksReposByFullPath(t *testing.T) {
	s := spinner.New()
	l := NewList(&s, false)

	root := t.TempDir()
	instA := newPausedTestInstance(t, "a", filepath.Join(root, "owner-a", "app"))
	instB := newPausedTestInstance(t, "b", filepath.Join(root, "owner-b", "app"))

	l.AddInstance(instA)()
	l.AddInstance(instB)()

	require.Len(t, l.repos, 2)
}

func newPausedTestInstance(t *testing.T, title string, repoPath string) *session.Instance {
	t.Helper()

	inst, err := session.FromInstanceData(session.InstanceData{
		Title:   title,
		Path:    repoPath,
		Branch:  "test-branch",
		Status:  session.Paused,
		Program: "echo",
		Worktree: session.GitWorktreeData{
			RepoPath:     repoPath,
			WorktreePath: filepath.Join(t.TempDir(), title),
			SessionName:  title,
			BranchName:   "test-branch",
		},
	})
	require.NoError(t, err)

	return inst
}
