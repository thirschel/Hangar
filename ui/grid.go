package ui

import (
	"fmt"
	"strconv"
	"strings"

	"hangar/session"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// preferredTileWidth is the target outer width (in cells) of a single tile when
// the column count is in Auto mode. The Auto column count is derived by dividing
// the available width by this value.
const preferredTileWidth = 40

// topBarHeight is the number of lines reserved at the top of the grid for the
// status/hint bar. The tile area occupies the remaining height.
const topBarHeight = 1

// minTileWidth and minTileHeight are the smallest outer tile dimensions that can
// still fit a border plus a header line. Below these the grid renders only the
// top bar.
const (
	minTileWidth  = 3
	minTileHeight = 3
)

var (
	// gridBorderColor is the border color of an unfocused tile.
	gridBorderColor = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#555555"}
	// gridFocusColor highlights the border/header of the focused tile.
	gridFocusColor = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	// gridLiveColor marks the focused tile while it is receiving raw keystrokes
	// (passthrough/LIVE).
	gridLiveColor = lipgloss.AdaptiveColor{Light: "#D70000", Dark: "#FF5F5F"}

	gridTopBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#7A7474", Dark: "#9C9494"})
	gridHeaderStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#DDDDDD"})
	gridFocusHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(gridFocusColor)
	gridLiveHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(gridLiveColor)
)

// GridView renders several agents at once as bordered tiles in a grid. It is a
// pure presentation component: it owns layout, focus, mouse hit-testing and
// per-tile text supplied by the app layer, and never touches a live PTY.
type GridView struct {
	width  int
	height int

	instances []*session.Instance

	// columns is the raw "agents per row" setting. 0 means Auto (derive from
	// width); it is never negative.
	columns int

	// focusIndex is the index of the highlighted tile, always kept in [0, n-1]
	// when there is at least one instance.
	focusIndex int

	// passthrough is true while the focused tile is actively receiving raw
	// keystrokes.
	passthrough bool

	// tileContent holds the captured screen text for each tile, keyed by the
	// instance Title.
	tileContent map[string]string
}

// NewGridView returns an empty GridView in Auto column mode.
func NewGridView() *GridView {
	return &GridView{
		tileContent: make(map[string]string),
	}
}

// SetSize sets the overall drawable area (top bar plus tiles). Negative values
// are clamped to zero.
func (g *GridView) SetSize(width, height int) {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	g.width = width
	g.height = height
}

// SetInstances replaces the agents to display, in order, and clamps focus back
// to a valid index.
func (g *GridView) SetInstances(instances []*session.Instance) {
	cp := make([]*session.Instance, len(instances))
	copy(cp, instances)
	g.instances = cp
	g.clampFocus()
}

// Instances returns the agents currently displayed, in order.
func (g *GridView) Instances() []*session.Instance {
	return g.instances
}

// SetColumns sets the raw "agents per row" setting. 0 means Auto; negative
// values clamp to 0.
func (g *GridView) SetColumns(cols int) {
	if cols < 0 {
		cols = 0
	}
	g.columns = cols
}

// Columns returns the raw column setting (0 == Auto).
func (g *GridView) Columns() int {
	return g.columns
}

// EffectiveColumns returns the column count actually used right now. It is >= 1
// when there are instances and 0 when there are none.
func (g *GridView) EffectiveColumns() int {
	n := len(g.instances)
	if n == 0 {
		return 0
	}
	cols := g.columns
	if cols <= 0 {
		// Auto: floor(width / preferredTileWidth), then clamp to [1, n].
		cols = g.width / preferredTileWidth
	}
	return max(1, min(cols, n))
}

// CycleColumns advances the setting 0(Auto) -> 1 -> 2 -> ... -> n -> 0, where n
// is the number of instances.
func (g *GridView) CycleColumns() {
	n := len(g.instances)
	next := g.columns + 1
	if next > n {
		next = 0
	}
	g.columns = next
}

// FocusIndex returns the index of the focused tile.
func (g *GridView) FocusIndex() int {
	return g.focusIndex
}

// SetFocus sets the focused tile, clamping to [0, n-1].
func (g *GridView) SetFocus(idx int) {
	n := len(g.instances)
	if n == 0 {
		g.focusIndex = 0
		return
	}
	g.focusIndex = max(0, min(idx, n-1))
}

// FocusedInstance returns the focused agent, or nil when the grid is empty.
func (g *GridView) FocusedInstance() *session.Instance {
	if len(g.instances) == 0 {
		return nil
	}
	return g.instances[g.focusIndex]
}

// FocusNext moves focus to the next tile linearly, wrapping across the whole set.
func (g *GridView) FocusNext() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	g.focusIndex = (g.focusIndex + 1) % n
}

// FocusPrev moves focus to the previous tile linearly, wrapping across the whole set.
func (g *GridView) FocusPrev() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	g.focusIndex = (g.focusIndex - 1 + n) % n
}

// FocusLeft moves focus one column left within the current row (clamped at the
// row edge; no wrap).
func (g *GridView) FocusLeft() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	cols := g.EffectiveColumns()
	row, col := g.focusIndex/cols, g.focusIndex%cols
	if col > 0 {
		g.focusIndex = row*cols + (col - 1)
	}
}

// FocusRight moves focus one column right within the current row (clamped at the
// row edge and at the last populated tile; no wrap).
func (g *GridView) FocusRight() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	cols := g.EffectiveColumns()
	row, col := g.focusIndex/cols, g.focusIndex%cols
	if col < cols-1 {
		if idx := row*cols + (col + 1); idx < n {
			g.focusIndex = idx
		}
	}
}

// FocusUp moves focus one row up in the same column (clamped at the top row).
func (g *GridView) FocusUp() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	cols := g.EffectiveColumns()
	row, col := g.focusIndex/cols, g.focusIndex%cols
	if row > 0 {
		g.focusIndex = (row-1)*cols + col
	}
}

// FocusDown moves focus one row down in the same column (clamped at the last row
// and at the last populated tile).
func (g *GridView) FocusDown() {
	n := len(g.instances)
	if n == 0 {
		return
	}
	cols := g.EffectiveColumns()
	row, col := g.focusIndex/cols, g.focusIndex%cols
	if idx := (row+1)*cols + col; idx < n {
		g.focusIndex = idx
	}
}

// FocusAt returns the tile index under the point (x, y), where the coordinates
// are relative to the grid's own top-left origin. It returns (-1, false) when
// the point is over the top bar or outside the populated tile grid.
func (g *GridView) FocusAt(x, y int) (int, bool) {
	n := len(g.instances)
	if n == 0 || g.width <= 0 || g.height <= 0 {
		return -1, false
	}
	if x < 0 || x >= g.width || y < topBarHeight || y >= g.height {
		return -1, false
	}
	cols := g.EffectiveColumns()
	rows := g.rowCount()
	if cols <= 0 || rows <= 0 {
		return -1, false
	}
	tileW := g.width / cols
	tileH := (g.height - topBarHeight) / rows
	if tileW <= 0 || tileH <= 0 {
		return -1, false
	}
	col := x / tileW
	row := (y - topBarHeight) / tileH
	if col >= cols || row >= rows {
		return -1, false
	}
	idx := row*cols + col
	if idx >= n {
		return -1, false
	}
	return idx, true
}

// SetPassthrough sets whether the focused tile is actively receiving raw keystrokes.
func (g *GridView) SetPassthrough(on bool) {
	g.passthrough = on
}

// Passthrough reports whether the focused tile is actively receiving raw keystrokes.
func (g *GridView) Passthrough() bool {
	return g.passthrough
}

// SetTileContent stores the captured screen text for the tile whose instance has
// the given Title. The app calls this each tick.
func (g *GridView) SetTileContent(title, content string) {
	if g.tileContent == nil {
		g.tileContent = make(map[string]string)
	}
	g.tileContent[title] = content
}

// ClearTileContent drops all captured per-tile text.
func (g *GridView) ClearTileContent() {
	g.tileContent = make(map[string]string)
}

// TileContentSize returns the inner content size (width, height in cells) of a
// single tile: the tile box minus its border (2) and header (1). All tiles share
// one size. It returns (0, 0) when there is no layout yet.
func (g *GridView) TileContentSize() (int, int) {
	n := len(g.instances)
	if n == 0 || g.width <= 0 || g.height <= 0 {
		return 0, 0
	}
	cols := g.EffectiveColumns()
	rows := g.rowCount()
	if cols <= 0 || rows <= 0 {
		return 0, 0
	}
	tileW := g.width / cols
	tileH := (g.height - topBarHeight) / rows
	return max(1, tileW-2), max(1, tileH-3)
}

// String renders the whole grid (top bar plus tiles).
func (g *GridView) String() string {
	n := len(g.instances)
	if n == 0 {
		return "No agents selected."
	}
	if g.width <= 0 || g.height <= 0 {
		return ""
	}

	cols := g.EffectiveColumns()
	rows := g.rowCount()
	tileW := g.width / cols
	tileH := (g.height - topBarHeight) / rows

	topBar := g.renderTopBar()
	if tileW < minTileWidth || tileH < minTileHeight {
		// Too small to draw tiles; show just the top bar.
		return topBar
	}

	var renderedRows []string
	for r := 0; r < rows; r++ {
		var rowTiles []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				break
			}
			rowTiles = append(rowTiles, g.renderTile(idx, tileW, tileH))
		}
		if len(rowTiles) > 0 {
			renderedRows = append(renderedRows, lipgloss.JoinHorizontal(lipgloss.Top, rowTiles...))
		}
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, renderedRows...)
	return lipgloss.JoinVertical(lipgloss.Left, topBar, grid)
}

// rowCount returns ceil(n / effectiveCols).
func (g *GridView) rowCount() int {
	n := len(g.instances)
	cols := g.EffectiveColumns()
	if cols <= 0 {
		return 0
	}
	return (n + cols - 1) / cols
}

// clampFocus keeps focusIndex within [0, n-1] (or 0 when empty).
func (g *GridView) clampFocus() {
	n := len(g.instances)
	if n == 0 {
		g.focusIndex = 0
		return
	}
	g.focusIndex = max(0, min(g.focusIndex, n-1))
}

// renderTopBar renders the single-line status/hint bar, truncated to the width.
func (g *GridView) renderTopBar() string {
	perRow := "Auto"
	if g.columns > 0 {
		perRow = strconv.Itoa(g.columns)
	}
	label := fmt.Sprintf("Grid · %d agents   Per row: %s", len(g.instances), perRow)
	hint := "[/] per row · Tab focus · Enter type · Ctrl+Q release · Esc exit"
	line := truncateToWidth(label+"   "+hint, g.width)
	return gridTopBarStyle.Width(g.width).MaxHeight(topBarHeight).Render(line)
}

// renderTile renders a single tile of the given outer dimensions.
func (g *GridView) renderTile(idx, tileW, tileH int) string {
	inst := g.instances[idx]

	innerW := max(1, tileW-2)
	innerH := max(1, tileH-2)
	contentH := innerH - 1

	focused := idx == g.focusIndex
	live := focused && g.passthrough

	// Header: "{idx+1} {title} {statusIcon}" plus a LIVE badge in passthrough.
	header := fmt.Sprintf("%d %s %s", idx+1, inst.Title, statusIcon(inst.Status))
	if live {
		header += " ● LIVE"
	}
	header = truncateToWidth(SafeDisplay(header), innerW)

	headerStyle := gridHeaderStyle
	switch {
	case live:
		headerStyle = gridLiveHeaderStyle
	case focused:
		headerStyle = gridFocusHeaderStyle
	}

	inner := headerStyle.Render(header)
	if contentH > 0 {
		body := buildBodyLines(g.tileContent[inst.Title], innerW, contentH)
		inner = inner + "\n" + strings.Join(body, "\n")
	}

	borderColor := gridBorderColor
	switch {
	case live:
		borderColor = gridLiveColor
	case focused:
		borderColor = gridFocusColor
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(innerW).
		Height(innerH).
		Render(inner)
}

// buildBodyLines splits content into exactly h lines, each truncated to width w
// and padded with blank lines to fill.
func buildBodyLines(content string, w, h int) []string {
	lines := make([]string, 0, h)
	if h <= 0 {
		return lines
	}
	var raw []string
	if content != "" {
		raw = strings.Split(SafeDisplay(content), "\n")
	}
	for i := 0; i < h; i++ {
		if i < len(raw) {
			lines = append(lines, truncateToWidth(raw[i], w))
		} else {
			lines = append(lines, "")
		}
	}
	return lines
}

// statusIcon returns a compact glyph for the given status.
func statusIcon(s session.Status) string {
	switch s {
	case session.Ready:
		return "●"
	case session.Running, session.Loading:
		return "…"
	case session.Paused:
		return "⏸"
	default:
		return "·"
	}
}

// truncateToWidth cuts s to a display width of at most w cells.
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(s, w, "")
}
