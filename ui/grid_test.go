package ui

import (
	"fmt"
	"testing"

	"hangar/session"

	"github.com/stretchr/testify/require"
)

// gridInstances builds instances directly (no live PTY) for grid tests.
func gridInstances(titles ...string) []*session.Instance {
	out := make([]*session.Instance, len(titles))
	for i, t := range titles {
		out[i] = &session.Instance{Title: t, Status: session.Ready}
	}
	return out
}

// gridInstancesN builds n instances with deterministic titles.
func gridInstancesN(n int) []*session.Instance {
	titles := make([]string, n)
	for i := range titles {
		titles[i] = fmt.Sprintf("agent-%d", i)
	}
	return gridInstances(titles...)
}

func TestGridView_AutoColumns(t *testing.T) {
	cases := []struct {
		name  string
		width int
		n     int
		want  int
	}{
		{"very-narrow-clamps-to-one", 10, 5, 1},
		{"sub-tile-width-clamps-to-one", 39, 3, 1},
		{"exactly-one-tile", 40, 5, 1},
		{"two-cols", 80, 5, 2},
		{"three-cols", 120, 5, 3},
		{"capped-at-n", 400, 5, 5},
		{"single-instance-capped", 80, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGridView()
			g.SetSize(tc.width, 40)
			g.SetInstances(gridInstancesN(tc.n))

			got := g.EffectiveColumns()
			require.Equal(t, tc.want, got)
			require.GreaterOrEqual(t, got, 1, "never 0 when n>0")
			require.LessOrEqual(t, got, tc.n, "never more than n")
			require.Equal(t, 0, g.Columns(), "raw setting stays Auto")
		})
	}
}

func TestGridView_ManualColumnsClamped(t *testing.T) {
	g := NewGridView()
	g.SetInstances(gridInstancesN(3))

	g.SetColumns(2)
	require.Equal(t, 2, g.Columns())
	require.Equal(t, 2, g.EffectiveColumns())

	g.SetColumns(5) // greater than n
	require.Equal(t, 5, g.Columns(), "raw setting preserved")
	require.Equal(t, 3, g.EffectiveColumns(), "clamped to n")

	g.SetColumns(1)
	require.Equal(t, 1, g.EffectiveColumns())

	g.SetColumns(-4) // negative clamps to Auto
	require.Equal(t, 0, g.Columns())
}

func TestGridView_CycleColumns(t *testing.T) {
	g := NewGridView()
	g.SetInstances(gridInstancesN(3))
	require.Equal(t, 0, g.Columns(), "starts in Auto")

	// 0(Auto) -> 1 -> 2 -> 3 -> 0 -> 1 ...
	for _, want := range []int{1, 2, 3, 0, 1} {
		g.CycleColumns()
		require.Equal(t, want, g.Columns())
	}
}

func TestGridView_RowsAndTileContentSize(t *testing.T) {
	g := NewGridView()
	g.SetSize(120, 40)
	g.SetInstances(gridInstancesN(6))

	var widths []int
	for _, cols := range []int{1, 2, 3} {
		g.SetColumns(cols)

		wantRows := (6 + cols - 1) / cols // ceil(n/cols)
		require.Equal(t, wantRows, g.rowCount())

		cw, ch := g.TileContentSize()
		require.Greater(t, cw, 0)
		require.Greater(t, ch, 0)
		widths = append(widths, cw)
	}
	for i := 1; i < len(widths); i++ {
		require.Less(t, widths[i], widths[i-1], "content width shrinks as columns grow")
	}
}

func TestGridView_FocusNextPrevWrap(t *testing.T) {
	g := NewGridView()
	g.SetInstances(gridInstancesN(5))
	g.SetColumns(2)
	require.Equal(t, 0, g.FocusIndex())

	for _, want := range []int{1, 2, 3, 4, 0} {
		g.FocusNext()
		require.Equal(t, want, g.FocusIndex())
	}
	for _, want := range []int{4, 3, 2, 1, 0} {
		g.FocusPrev()
		require.Equal(t, want, g.FocusIndex())
	}
}

// TestGridView_FocusDirectionsClamp uses 5 instances at 2 columns => 3 rows:
//
//	0 1
//	2 3
//	4
func TestGridView_FocusDirectionsClamp(t *testing.T) {
	newGrid := func() *GridView {
		g := NewGridView()
		g.SetInstances(gridInstancesN(5))
		g.SetColumns(2)
		return g
	}

	cases := []struct {
		name  string
		start int
		move  func(*GridView)
		want  int
	}{
		{"right-from-0", 0, (*GridView).FocusRight, 1},
		{"left-from-0-clamps", 0, (*GridView).FocusLeft, 0},
		{"down-from-0", 0, (*GridView).FocusDown, 2},
		{"up-from-0-clamps", 0, (*GridView).FocusUp, 0},
		{"left-from-1", 1, (*GridView).FocusLeft, 0},
		{"right-from-1-clamps-row-edge", 1, (*GridView).FocusRight, 1},
		{"up-from-3", 3, (*GridView).FocusUp, 1},
		{"left-from-3", 3, (*GridView).FocusLeft, 2},
		{"down-from-3-clamps-empty-slot", 3, (*GridView).FocusDown, 3},
		{"up-from-4", 4, (*GridView).FocusUp, 2},
		{"right-from-4-clamps-empty-slot", 4, (*GridView).FocusRight, 4},
		{"down-from-4-clamps", 4, (*GridView).FocusDown, 4},
		{"left-from-4-clamps", 4, (*GridView).FocusLeft, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid()
			g.SetFocus(tc.start)
			require.Equal(t, tc.start, g.FocusIndex())
			tc.move(g)
			require.Equal(t, tc.want, g.FocusIndex())
		})
	}
}

// TestGridView_FocusAt uses 4 instances at 2 columns and an 80x40 area:
// topBarHeight=1, tileW=40, tileH=(40-1)/2=19.
//
//	tile0 x[0,40) y[1,20)   tile1 x[40,80) y[1,20)
//	tile2 x[0,40) y[20,39)  tile3 x[40,80) y[20,39)
func TestGridView_FocusAt(t *testing.T) {
	g := NewGridView()
	g.SetSize(80, 40)
	g.SetInstances(gridInstancesN(4))
	g.SetColumns(2)

	cases := []struct {
		x, y    int
		wantIdx int
		wantOk  bool
	}{
		{5, 5, 0, true},
		{45, 5, 1, true},
		{5, 25, 2, true},
		{45, 25, 3, true},
		{10, 0, -1, false},  // over the top bar
		{-1, 5, -1, false},  // negative x
		{80, 5, -1, false},  // x == width
		{5, 40, -1, false},  // y == height
		{200, 5, -1, false}, // far right
	}
	for _, tc := range cases {
		idx, ok := g.FocusAt(tc.x, tc.y)
		require.Equal(t, tc.wantOk, ok, "ok for point (%d,%d)", tc.x, tc.y)
		require.Equal(t, tc.wantIdx, idx, "idx for point (%d,%d)", tc.x, tc.y)
	}
}

func TestGridView_FocusAtEmptySlot(t *testing.T) {
	g := NewGridView()
	g.SetSize(80, 40)
	g.SetInstances(gridInstancesN(5))
	g.SetColumns(2)
	// rows=3, tileW=40, tileH=(40-1)/3=13. The last row (y in [27,40)) only has
	// col0 (idx4); col1 would be idx5, which is empty.
	idx, ok := g.FocusAt(50, 30)
	require.False(t, ok)
	require.Equal(t, -1, idx)

	idx, ok = g.FocusAt(10, 30)
	require.True(t, ok)
	require.Equal(t, 4, idx)
}

func TestGridView_StringRendersTitlesAndLive(t *testing.T) {
	titles2 := []string{"alpha", "beta"}
	g := NewGridView()
	g.SetSize(100, 40)
	g.SetInstances(gridInstances(titles2...))

	var out string
	require.NotPanics(t, func() { out = g.String() })
	for _, ti := range titles2 {
		require.Contains(t, out, ti)
	}
	require.NotContains(t, out, "LIVE")

	g.SetPassthrough(true)
	require.True(t, g.Passthrough())
	require.NotPanics(t, func() { out = g.String() })
	require.Contains(t, out, "LIVE")

	titles5 := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	g5 := NewGridView()
	g5.SetSize(160, 48)
	g5.SetInstances(gridInstances(titles5...))
	require.NotPanics(t, func() { out = g5.String() })
	for _, ti := range titles5 {
		require.Contains(t, out, ti)
	}
}

func TestGridView_TileContentRendered(t *testing.T) {
	g := NewGridView()
	g.SetSize(100, 40)
	g.SetInstances(gridInstances("alpha", "beta"))

	g.SetTileContent("alpha", "hello-world")
	require.Contains(t, g.String(), "hello-world")

	g.ClearTileContent()
	require.NotContains(t, g.String(), "hello-world")
}

func TestGridView_EmptyGuards(t *testing.T) {
	g := NewGridView()
	require.Empty(t, g.Instances())
	require.Equal(t, 0, g.EffectiveColumns())
	require.Nil(t, g.FocusedInstance())
	require.Equal(t, "No agents selected.", g.String())

	w, h := g.TileContentSize()
	require.Equal(t, 0, w)
	require.Equal(t, 0, h)

	require.NotPanics(t, func() {
		g.FocusNext()
		g.FocusPrev()
		g.FocusLeft()
		g.FocusRight()
		g.FocusUp()
		g.FocusDown()
		g.SetFocus(3)
	})
	require.Equal(t, 0, g.FocusIndex())

	idx, ok := g.FocusAt(5, 5)
	require.False(t, ok)
	require.Equal(t, -1, idx)
}

func TestGridView_SetInstancesClampsFocus(t *testing.T) {
	g := NewGridView()
	g.SetInstances(gridInstancesN(5))
	g.SetFocus(4)
	require.Equal(t, 4, g.FocusIndex())

	g.SetInstances(gridInstancesN(2))
	require.Equal(t, 1, g.FocusIndex(), "focus clamped into the new range")
	require.Equal(t, "agent-1", g.FocusedInstance().Title)
}

func TestGridView_ZeroSizeStringEmpty(t *testing.T) {
	g := NewGridView()
	g.SetInstances(gridInstancesN(2))
	// No SetSize yet: there are instances but no drawable area.
	require.Equal(t, "", g.String())
}
