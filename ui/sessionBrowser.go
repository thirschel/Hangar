package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"claude-squad/session/copilot"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type BrowserAction int

const (
	BrowserActionNone BrowserAction = iota
	BrowserActionClose
	BrowserActionRestart
)

type browserFocus int

const (
	browserFocusSearch browserFocus = iota
	browserFocusPreview
)

type SessionBrowser struct {
	width, height int

	search textinput.Model
	focus  browserFocus

	allSessions []copilot.Session
	filtered    []copilot.Session
	selectedIdx int
	filterFunc  func(sessions []copilot.Session, query string) []copilot.Session

	skipped  int
	indexing bool

	previewCache map[string]string
}

var (
	browserSearchStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})
	browserMutedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
	browserPaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62"))
	browserPreviewTitleStyle = lipgloss.NewStyle().
					Bold(true).
					Foreground(lipgloss.Color("62"))
)

func NewSessionBrowser() *SessionBrowser {
	ti := textinput.New()
	ti.Prompt = "Search: "
	ti.Placeholder = "metadata terms"
	ti.Focus()
	return &SessionBrowser{
		search:       ti,
		previewCache: make(map[string]string),
	}
}

// SetSize sizes the component to the available content area (full-screen minus menu/err box).
func (b *SessionBrowser) SetSize(width, height int) {
	b.width = width
	b.height = height
	inputWidth := width - len(b.search.Prompt) - 16
	if inputWidth < 1 {
		inputWidth = 1
	}
	b.search.Width = inputWidth
}

// SetSessions replaces the full session set and recomputes the filtered view using the current query.
func (b *SessionBrowser) SetSessions(sessions []copilot.Session) {
	b.allSessions = append([]copilot.Session(nil), sessions...)
	sortSessionsByUpdatedAt(b.allSessions)
	b.recomputeFiltered()
}

// SetFilterFunc overrides the default in-memory metadata filter when non-nil.
func (b *SessionBrowser) SetFilterFunc(fn func(sessions []copilot.Session, query string) []copilot.Session) {
	b.filterFunc = fn
	b.recomputeFiltered()
}

func (b *SessionBrowser) SetQuery(q string) {
	b.search.SetValue(q)
	b.recomputeFiltered()
}

func (b *SessionBrowser) Query() string {
	return b.search.Value()
}

// SetSkipped records how many sessions discovery skipped due to parse errors, shown
// as a footer count.
func (b *SessionBrowser) SetSkipped(n int) {
	b.skipped = n
}

// SetIndexing toggles the "indexing…" footer hint shown while the content index
// builds in the background (search degrades to metadata-only until it is ready).
func (b *SessionBrowser) SetIndexing(indexing bool) {
	b.indexing = indexing
}

// HandleKeyPress processes one key while the browser is focused.
func (b *SessionBrowser) HandleKeyPress(msg tea.KeyMsg) (BrowserAction, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return BrowserActionClose, nil
	case tea.KeyEnter:
		if b.GetSelected() != nil {
			return BrowserActionRestart, nil
		}
		return BrowserActionNone, nil
	case tea.KeyUp:
		b.Up()
		return BrowserActionNone, nil
	case tea.KeyDown:
		b.Down()
		return BrowserActionNone, nil
	case tea.KeyTab:
		b.toggleFocus()
		return BrowserActionNone, nil
	default:
		if msg.String() == "ctrl+k" {
			b.Up()
			return BrowserActionNone, nil
		}
		if msg.String() == "ctrl+j" {
			b.Down()
			return BrowserActionNone, nil
		}
		oldQuery := b.Query()
		var cmd tea.Cmd
		b.search, cmd = b.search.Update(msg)
		if b.Query() != oldQuery {
			b.recomputeFiltered()
		}
		return BrowserActionNone, cmd
	}
}

// GetSelected returns the currently highlighted session, or nil when the filtered list is empty.
func (b *SessionBrowser) GetSelected() *copilot.Session {
	if len(b.filtered) == 0 || b.selectedIdx < 0 || b.selectedIdx >= len(b.filtered) {
		return nil
	}
	return &b.filtered[b.selectedIdx]
}

func (b *SessionBrowser) String() string {
	width := b.width
	height := b.height
	if width <= 0 || height <= 0 {
		return b.renderContent(maxInt(width, 1), maxInt(height, 1))
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, b.renderContent(width, height))
}

func (b *SessionBrowser) renderContent(width, height int) string {
	if len(b.allSessions) == 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, "No Copilot sessions found")
	}
	if len(b.filtered) == 0 {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, fmt.Sprintf("No matches for %q", b.Query()))
	}

	search := b.renderSearch(width)
	bodyHeight := height - lipgloss.Height(search) - 1
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	if width < 80 {
		listHeight := bodyHeight / 2
		if listHeight < 1 {
			listHeight = 1
		}
		previewHeight := bodyHeight - listHeight
		if previewHeight < 1 {
			previewHeight = 1
		}
		body := lipgloss.JoinVertical(
			lipgloss.Left,
			b.renderList(width, listHeight),
			b.renderPreview(width, previewHeight),
		)
		return lipgloss.JoinVertical(lipgloss.Left, search, body)
	}

	listWidth := width / 2
	list := b.renderList(listWidth, bodyHeight)
	preview := b.renderPreview(width-listWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, preview)
	return lipgloss.JoinVertical(lipgloss.Left, search, body)
}

func (b *SessionBrowser) renderSearch(width int) string {
	b.SetSize(b.width, b.height)
	status := fmt.Sprintf("%d/%d shown", len(b.filtered), len(b.allSessions))
	if b.indexing {
		status += " · indexing…"
	}
	if b.skipped > 0 {
		status += fmt.Sprintf(" · %d skipped", b.skipped)
	}
	count := browserMutedStyle.Render(status)
	bar := lipgloss.JoinHorizontal(lipgloss.Center, b.search.View(), "  ", count)
	return browserSearchStyle.Width(maxInt(width, 1)).Render(bar)
}

func (b *SessionBrowser) renderList(width, height int) string {
	innerWidth := maxInt(width-browserPaneStyle.GetHorizontalFrameSize(), 1)
	innerHeight := maxInt(height-browserPaneStyle.GetVerticalFrameSize(), 1)
	// Scroll the viewport so the selected row stays visible past the fold.
	start := 0
	if b.selectedIdx >= innerHeight {
		start = b.selectedIdx - innerHeight + 1
	}
	rows := make([]string, 0, innerHeight)
	for i := start; i < len(b.filtered); i++ {
		rows = append(rows, b.renderRow(b.filtered[i], i == b.selectedIdx, innerWidth))
		if len(rows) >= innerHeight {
			break
		}
	}
	return browserPaneStyle.Width(maxInt(width, 1)).Height(maxInt(height, 1)).Render(strings.Join(rows, "\n"))
}

func (b *SessionBrowser) renderRow(s copilot.Session, selected bool, width int) string {
	style := listDescStyle
	if selected {
		style = selectedDescStyle
	}
	glyph := " "
	if s.InUse {
		glyph = readyStyle.Render("●")
	} else if !s.HasEvents {
		glyph = browserMutedStyle.Render("○")
	}
	name := SafeDisplay(s.DisplayName())
	repo := SafeDisplay(s.Repository)
	branch := SafeDisplay(s.Branch)
	meta := strings.Trim(strings.Join(nonEmpty(repo, branch), " · "), " ")
	updated := relativeTime(s.UpdatedAt)
	reserved := runewidth.StringWidth(glyph) + runewidth.StringWidth(meta) + runewidth.StringWidth(updated) + 6
	nameWidth := width - reserved
	if nameWidth < 4 {
		nameWidth = 4
	}
	name = runewidth.Truncate(name, nameWidth, "...")
	line := fmt.Sprintf("%s %s", glyph, name)
	if meta != "" {
		line += "  " + browserMutedStyle.Render(meta)
	}
	line += "  " + browserMutedStyle.Render(updated)
	return style.Width(maxInt(width, 1)).Render(line)
}

func (b *SessionBrowser) renderPreview(width, height int) string {
	innerWidth := maxInt(width-browserPaneStyle.GetHorizontalFrameSize(), 1)
	sel := b.GetSelected()
	if sel == nil {
		return browserPaneStyle.Width(maxInt(width, 1)).Height(maxInt(height, 1)).Render("")
	}
	b.loadSelectedPreview()
	lines := []string{
		browserPreviewTitleStyle.Render(SafeDisplay(sel.DisplayName())),
		fmt.Sprintf("Name: %s", valueOr(SafeDisplay(sel.Name), "-")),
		fmt.Sprintf("Repository: %s", valueOr(SafeDisplay(sel.Repository), "-")),
		fmt.Sprintf("Branch: %s", valueOr(SafeDisplay(sel.Branch), "-")),
		fmt.Sprintf("Origin root: %s", valueOr(SafeDisplay(sel.OriginRoot), "-")),
		fmt.Sprintf("Updated: %s", relativeTime(sel.UpdatedAt)),
		"",
		browserMutedStyle.Render("Transcript snippet"),
	}
	if cached := b.previewCache[sel.ID]; strings.TrimSpace(cached) != "" {
		lines = append(lines, wrapLine(cached, innerWidth)...)
	} else {
		lines = append(lines, browserMutedStyle.Render("No cached snippet loaded"))
	}
	return browserPaneStyle.Width(maxInt(width, 1)).Height(maxInt(height, 1)).Render(strings.Join(lines, "\n"))
}

func (b *SessionBrowser) Down() {
	if len(b.filtered) == 0 {
		return
	}
	if b.selectedIdx < len(b.filtered)-1 {
		b.selectedIdx++
	} else {
		b.selectedIdx = 0
	}
}

func (b *SessionBrowser) Up() {
	if len(b.filtered) == 0 {
		return
	}
	if b.selectedIdx > 0 {
		b.selectedIdx--
	} else {
		b.selectedIdx = len(b.filtered) - 1
	}
}

func (b *SessionBrowser) toggleFocus() {
	if b.focus == browserFocusSearch {
		b.focus = browserFocusPreview
		b.search.Blur()
		return
	}
	b.focus = browserFocusSearch
	b.search.Focus()
}

func (b *SessionBrowser) recomputeFiltered() {
	selectedID := ""
	if selected := b.GetSelected(); selected != nil {
		selectedID = selected.ID
	}
	if b.filterFunc != nil {
		b.filtered = append([]copilot.Session(nil), b.filterFunc(append([]copilot.Session(nil), b.allSessions...), b.Query())...)
	} else {
		b.filtered = defaultSessionFilter(b.allSessions, b.Query())
	}
	b.preserveOrClampSelection(selectedID)
}

func (b *SessionBrowser) preserveOrClampSelection(selectedID string) {
	if len(b.filtered) == 0 {
		b.selectedIdx = 0
		return
	}
	if selectedID != "" {
		for i := range b.filtered {
			if b.filtered[i].ID == selectedID {
				b.selectedIdx = i
				return
			}
		}
	}
	if b.selectedIdx < 0 {
		b.selectedIdx = 0
	}
	if b.selectedIdx >= len(b.filtered) {
		b.selectedIdx = len(b.filtered) - 1
	}
}

func (b *SessionBrowser) loadSelectedPreview() {
	sel := b.GetSelected()
	if sel == nil || sel.ID == "" {
		return
	}
	if _, ok := b.previewCache[sel.ID]; ok {
		return
	}
	msg, err := copilot.FirstUserMessage(*sel)
	if err != nil {
		msg = ""
	}
	b.previewCache[sel.ID] = SafeDisplay(msg)
}

func defaultSessionFilter(sessions []copilot.Session, query string) []copilot.Session {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return append([]copilot.Session(nil), sessions...)
	}
	filtered := make([]copilot.Session, 0, len(sessions))
	for _, s := range sessions {
		haystack := strings.ToLower(strings.Join([]string{s.DisplayName(), s.Repository, s.Branch, s.OriginRef}, " "))
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func sortSessionsByUpdatedAt(sessions []copilot.Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func wrapLine(s string, width int) []string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || width <= 0 {
		return []string{s}
	}
	var lines []string
	for runewidth.StringWidth(s) > width {
		lines = append(lines, runewidth.Truncate(s, width, ""))
		runes := []rune(s)
		cut := minInt(len(runes), width)
		s = strings.TrimSpace(string(runes[cut:]))
	}
	lines = append(lines, s)
	return lines
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
