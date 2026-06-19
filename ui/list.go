package ui

import (
	"errors"
	"fmt"
	"hangar/log"
	"hangar/session"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const readyIcon = "● "
const pausedIcon = "⏸ "

var readyStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var addedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var removedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#de613e"))

var pausedStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

var titleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var listDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

var statusCountsStyle = lipgloss.NewStyle().
	Padding(0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

var selectedTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var selectedDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var mainTitle = lipgloss.NewStyle().
	Background(lipgloss.Color("62")).
	Foreground(lipgloss.Color("230"))

var autoYesStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.Color("#1a1a1a"))

// sidebarHeaderStyle renders non-selectable section headers (Group-by-repo and
// Pinned-pending modes).
var sidebarHeaderStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#9D7CD8"})

// emptySearchStyle renders the "no matches" hint when a filter excludes everything.
var emptySearchStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Italic(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

// searchInputStyle renders the live search input bar at the top of the sidebar.
var searchInputStyle = lipgloss.NewStyle().
	Padding(0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

type List struct {
	// items is the canonical, user-controlled order. It is what GetInstances()
	// returns and what is persisted; display order is derived from it.
	items []*session.Instance
	// selected is the identity-based desired selection. It may temporarily point
	// at an instance hidden by the active filter, in which case
	// GetSelectedInstance() returns nil while the desired selection is retained.
	selected *session.Instance
	// mode is the active view mode.
	mode SidebarMode
	// filter is the active search query (empty = no filter).
	filter string
	// statusFilter is the active status filter (StatusAll = no filter).
	statusFilter StatusFilter
	// searching is true while the search input bar is open (even with an empty
	// query), so the sidebar renders the input line.
	searching bool
	// rows is the cached view-model, recomputed whenever items/mode/filter change.
	rows []displayRow
	// animator decorates rows that recently moved (pulse/crossfade). Purely cosmetic.
	animator *animator
	// motionConfig is the base motion toggle (config flag + reduced-motion). The
	// full animationsEnabled predicate also accounts for terminal size and count.
	motionConfig bool

	height, width int
	renderer      *InstanceRenderer
	autoyes       bool

	// repos maps repo path to instance count, used only for the legacy
	// "show repo only when multiple repos" display rule outside Group-by-repo mode.
	// Keyed by path (not name) so distinct repos sharing a basename are counted
	// separately.
	repos map[string]int
}

// motionVisibleThreshold is the maximum number of visible workspaces for which
// animations run; above it, updates are instant (reduced-motion).
const motionVisibleThreshold = 20

func NewList(spinner *spinner.Model, autoYes bool) *List {
	return &List{
		items:        []*session.Instance{},
		renderer:     &InstanceRenderer{spinner: spinner},
		repos:        make(map[string]int),
		autoyes:      autoYes,
		animator:     newAnimator(),
		motionConfig: true,
	}
}

// recompute rebuilds the cached view-model from the canonical items, the active
// mode, and the filters, then updates the animator. Call after any change to
// items/mode/filter/statusFilter/selection.
func (l *List) recompute() {
	l.rows = buildView(l.items, l.mode, l.filter, l.statusFilter)
	if l.animator == nil {
		return
	}
	vis := l.visibleInstances()
	if l.animationsEnabled() {
		l.animator.retarget(vis)
	} else {
		l.animator.reset(vis)
	}
}

// animationsEnabled is the reduced-motion predicate: motion runs only when the
// config allows it, the terminal isn't too small, and there aren't too many
// visible workspaces. When false, layout changes are instant.
func (l *List) animationsEnabled() bool {
	return l.motionConfig && !l.terminalTooSmall() && l.VisibleCount() <= motionVisibleThreshold
}

func (l *List) terminalTooSmall() bool {
	return l.width < 20 || l.height < 6
}

// SetMotionConfig sets the base motion toggle (config flag and/or reduced-motion).
func (l *List) SetMotionConfig(enabled bool) { l.motionConfig = enabled }

// IsAnimating reports whether a row pulse is currently in progress.
func (l *List) IsAnimating() bool {
	return l.animator != nil && l.animator.active()
}

// StepAnimation advances the row animation by one frame, returning true if any
// pulse remains active.
func (l *List) StepAnimation() bool {
	if l.animator == nil {
		return false
	}
	return l.animator.Step()
}

// Refresh recomputes the cached view (and animator) without changing the
// selection. Call after instance state changes (status/activity/pending) that can
// affect ordering in Recent-activity / Pinned-pending modes.
func (l *List) Refresh() { l.recompute() }

// ResetMotion records the current layout without pulsing and clears any active
// pulses. Used after the initial load so startup doesn't flash.
func (l *List) ResetMotion() {
	if l.animator != nil {
		l.animator.reset(l.visibleInstances())
	}
}

// Mode returns the active view mode.
func (l *List) Mode() SidebarMode { return l.mode }

// SetMode sets the active view mode and recomputes the view.
func (l *List) SetMode(mode SidebarMode) {
	l.mode = mode
	l.recompute()
}

// Filter returns the active search query.
func (l *List) Filter() string { return l.filter }

// SetFilter sets the active search query and recomputes the view.
func (l *List) SetFilter(filter string) {
	l.filter = filter
	l.recompute()
}

// StatusFilter returns the active status filter.
func (l *List) StatusFilter() StatusFilter { return l.statusFilter }

// SetStatusFilter sets the active status filter and recomputes the view.
func (l *List) SetStatusFilter(filter StatusFilter) {
	idx := indexOfInstance(l.items, l.selected)
	l.statusFilter = filter
	l.recompute()
	if l.selected != nil && !l.isVisible(l.selected) {
		l.selected = l.nearestVisible(idx)
	}
}

// StatusCounts returns counts over the full canonical item set.
func (l *List) StatusCounts() StatusCounts { return CountByStatus(l.items) }

// Searching reports whether the search input bar is currently open.
func (l *List) Searching() bool { return l.searching }

// SetSearching opens or closes the search input bar.
func (l *List) SetSearching(searching bool) { l.searching = searching }

// VisibleCount returns the number of instance rows currently visible.
func (l *List) VisibleCount() int { return len(l.visibleInstances()) }

// HasVisible reports whether any instance row is currently visible.
func (l *List) HasVisible() bool { return l.VisibleCount() > 0 }

// visibleInstances returns the instances rendered as instance rows, in display order.
func (l *List) visibleInstances() []*session.Instance {
	out := make([]*session.Instance, 0, len(l.rows))
	for _, r := range l.rows {
		if r.kind == rowInstance {
			out = append(out, r.instance)
		}
	}
	return out
}

// isVisible reports whether target is currently rendered as an instance row.
func (l *List) isVisible(target *session.Instance) bool {
	return indexOfInstance(l.visibleInstances(), target) >= 0
}

func indexOfInstance(list []*session.Instance, target *session.Instance) int {
	if target == nil {
		return -1
	}
	for i, inst := range list {
		if inst == target {
			return i
		}
	}
	return -1
}

// nearestVisible returns the visible instance whose canonical position is closest
// to canonicalIdx (searching forward first, then backward). Used to clamp the
// selection after a removal. Returns nil only when nothing is visible.
func (l *List) nearestVisible(canonicalIdx int) *session.Instance {
	vis := l.visibleInstances()
	if len(vis) == 0 {
		return nil
	}
	// Clamp into range: after removing the last canonical item, canonicalIdx can
	// equal len(l.items), which would index out of range in the backward branch.
	if canonicalIdx >= len(l.items) {
		canonicalIdx = len(l.items) - 1
	}
	if canonicalIdx < 0 {
		canonicalIdx = 0
	}
	visSet := make(map[*session.Instance]bool, len(vis))
	for _, inst := range vis {
		visSet[inst] = true
	}
	for offset := 0; offset < len(l.items); offset++ {
		if canonicalIdx+offset < len(l.items) && visSet[l.items[canonicalIdx+offset]] {
			return l.items[canonicalIdx+offset]
		}
		if canonicalIdx-offset >= 0 && visSet[l.items[canonicalIdx-offset]] {
			return l.items[canonicalIdx-offset]
		}
	}
	return vis[0]
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
	l.renderer.setWidth(width)
}

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	for i, item := range l.items {
		if !item.Started() || item.Paused() {
			continue
		}

		if innerErr := item.SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}

func (l *List) NumInstances() int {
	return len(l.items)
}

// InstanceRenderer handles rendering of session.Instance objects
type InstanceRenderer struct {
	spinner *spinner.Model
	width   int
}

func (r *InstanceRenderer) setWidth(width int) {
	r.width = AdjustPreviewWidth(width)
}

// ɹ and ɻ are other options.
const branchIcon = "Ꮧ"

// pulseBackground returns the highlight background for a pulsing row, fading as
// the pulse level decreases.
func pulseBackground(level float64) lipgloss.Color {
	switch {
	case level > 0.8:
		return lipgloss.Color("#3a4a7a")
	case level > 0.6:
		return lipgloss.Color("#33406a")
	case level > 0.4:
		return lipgloss.Color("#2c365a")
	case level > 0.2:
		return lipgloss.Color("#262e4a")
	default:
		return lipgloss.Color("#20263a")
	}
}

func (r *InstanceRenderer) Render(i *session.Instance, idx int, selected bool, hasMultipleRepos bool, pulse float64) string {
	prefix := fmt.Sprintf(" %d. ", idx)
	if idx >= 10 {
		prefix = prefix[:len(prefix)-1]
	}
	titleS := selectedTitleStyle
	descS := selectedDescStyle
	if !selected {
		titleS = titleStyle
		descS = listDescStyle
	}
	// A pulse briefly highlights the title of a row that just moved, fading out
	// over several frames. Purely cosmetic.
	if pulse > 0 {
		titleS = titleS.Background(pulseBackground(pulse)).Foreground(lipgloss.Color("#ffffff"))
	}

	// add spinner next to title if it's running
	var join string
	switch i.Status {
	case session.Running, session.Loading:
		join = fmt.Sprintf("%s ", r.spinner.View())
	case session.Ready:
		join = readyStyle.Render(readyIcon)
	case session.Paused:
		join = pausedStyle.Render(pausedIcon)
	default:
	}

	// Muted relative time since the agent last produced output (e.g. "5m"),
	// rendered as a small trailing suffix on the title line just before the status
	// icon. humanizeSince returns "" for a zero activity time, in which case the
	// suffix is omitted. It is also dropped when it would starve the title of width
	// — the title is primary. activityWidth includes a leading space separator.
	activityText := humanizeSince(i.EffectiveActivityTime(), time.Now())
	activityWidth := 0
	if activityText != "" {
		w := runewidth.StringWidth(activityText) + 1
		if r.width-3-runewidth.StringWidth(prefix)-1-w >= 4 {
			activityWidth = w
		} else {
			activityText = ""
		}
	}

	// Cut the title if it's too long
	titleText := i.Title
	widthAvail := r.width - 3 - runewidth.StringWidth(prefix) - 1 - activityWidth
	if widthAvail > 0 && runewidth.StringWidth(titleText) > widthAvail {
		titleText = runewidth.Truncate(titleText, widthAvail-3, "...")
	}

	// Inherit the title line's background (selected highlight / pulse) so the muted
	// suffix blends in, mirroring how the diff stats inherit descS's background.
	activitySuffix := ""
	if activityText != "" {
		activitySuffix = browserMutedStyle.Background(titleS.GetBackground()).Render(" " + activityText)
	}

	title := titleS.Render(lipgloss.JoinHorizontal(
		lipgloss.Left,
		lipgloss.Place(r.width-3-activityWidth, 1, lipgloss.Left, lipgloss.Center, fmt.Sprintf("%s %s", prefix, titleText)),
		activitySuffix,
		" ",
		join,
	))

	stat := i.GetDiffStats()

	var diff string
	var addedDiff, removedDiff string
	if stat == nil || stat.Error != nil || stat.IsEmpty() {
		// Don't show diff stats if there's an error or if they don't exist
		addedDiff = ""
		removedDiff = ""
		diff = ""
	} else {
		addedDiff = fmt.Sprintf("+%d", stat.Added)
		removedDiff = fmt.Sprintf("-%d ", stat.Removed)
		diff = lipgloss.JoinHorizontal(
			lipgloss.Center,
			addedLinesStyle.Background(descS.GetBackground()).Render(addedDiff),
			lipgloss.Style{}.Background(descS.GetBackground()).Foreground(descS.GetForeground()).Render(","),
			removedLinesStyle.Background(descS.GetBackground()).Render(removedDiff),
		)
	}

	remainingWidth := r.width
	remainingWidth -= runewidth.StringWidth(prefix)
	remainingWidth -= runewidth.StringWidth(branchIcon)
	remainingWidth -= 2 // for the literal " " and "-" in the branchLine format string

	diffWidth := runewidth.StringWidth(addedDiff) + runewidth.StringWidth(removedDiff)
	if diffWidth > 0 {
		diffWidth += 1
	}

	// Use fixed width for diff stats to avoid layout issues
	remainingWidth -= diffWidth

	branch := i.Branch
	if i.Started() && hasMultipleRepos {
		repoName, err := i.RepoName()
		if err != nil {
			log.ErrorLog.Printf("could not get repo name in instance renderer: %v", err)
		} else {
			branch += fmt.Sprintf(" (%s)", repoName)
		}
	}
	// Don't show branch if there's no space for it. Or show ellipsis if it's too long.
	branchWidth := runewidth.StringWidth(branch)
	if remainingWidth < 0 {
		branch = ""
	} else if remainingWidth < branchWidth {
		if remainingWidth < 3 {
			branch = ""
		} else {
			// We know the remainingWidth is at least 4 and branch is longer than that, so this is safe.
			branch = runewidth.Truncate(branch, remainingWidth-3, "...")
		}
	}
	remainingWidth -= runewidth.StringWidth(branch)

	// Add spaces to fill the remaining width.
	spaces := ""
	if remainingWidth > 0 {
		spaces = strings.Repeat(" ", remainingWidth)
	}

	branchLine := fmt.Sprintf("%s %s-%s%s%s", strings.Repeat(" ", len(prefix)), branchIcon, branch, spaces, diff)

	// join title and subtitle
	text := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		descS.Render(branchLine),
	)

	return text
}

func (l *List) String() string {
	titleText := fmt.Sprintf(" Instances · %s ", l.mode)
	if l.statusFilter != StatusAll {
		titleText = fmt.Sprintf(" Instances · %s · %s ", l.mode, l.statusFilter)
	}
	const autoYesText = " auto-yes "

	// Write the title.
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("\n")

	// Write title line
	// add padding of 2 because the border on list items adds some extra characters
	titleWidth := AdjustPreviewWidth(l.width) + 2
	if !l.autoyes {
		b.WriteString(lipgloss.Place(
			titleWidth, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText)))
	} else {
		title := lipgloss.Place(
			titleWidth/2, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText))
		autoYes := lipgloss.Place(
			titleWidth-(titleWidth/2), 1, lipgloss.Right, lipgloss.Bottom, autoYesStyle.Render(autoYesText))
		b.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top, title, autoYes))
	}

	b.WriteString("\n")
	b.WriteString("\n")

	if countsText := formatStatusCounts(l.StatusCounts()); countsText != "" {
		b.WriteString(statusCountsStyle.Render(countsText))
		b.WriteString("\n\n")
	}

	// Search input bar (rendered while searching, even with an empty query).
	if l.searching {
		query := l.filter
		bar := fmt.Sprintf("Search: %s▏", query)
		if query != "" {
			bar += fmt.Sprintf("  (%d)", l.VisibleCount())
		}
		b.WriteString(searchInputStyle.Render(bar))
		b.WriteString("\n\n")
	}

	// Render the view-model rows: non-selectable headers interleaved with
	// workspace rows. Numbering is continuous across visible instances.
	hasMultipleRepos := len(l.repos) > 1
	if len(l.rows) == 0 && len(l.items) > 0 && (l.filter != "" || l.statusFilter != StatusAll) {
		msg := fmt.Sprintf("no matches for %q", l.filter)
		if l.filter == "" {
			msg = fmt.Sprintf("no %s sessions", l.statusFilter)
		}
		b.WriteString(emptySearchStyle.Render(msg))
	}
	for i, row := range l.rows {
		switch row.kind {
		case rowHeader:
			b.WriteString(sidebarHeaderStyle.Render(row.header))
		case rowInstance:
			selected := row.instance == l.selected
			pulse := 0.0
			if l.animator != nil {
				pulse = l.animator.pulseLevel(row.instance)
			}
			b.WriteString(l.renderer.Render(row.instance, row.number, selected, hasMultipleRepos, pulse))
		}
		if i != len(l.rows)-1 {
			b.WriteString("\n\n")
		}
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, b.String())
}

func formatStatusCounts(counts StatusCounts) string {
	if counts.Total == 0 {
		return ""
	}
	parts := make([]string, 0, 4)
	if counts.Waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", counts.Waiting))
	}
	if counts.Busy > 0 {
		parts = append(parts, fmt.Sprintf("%d busy", counts.Busy))
	}
	if counts.Idle > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", counts.Idle))
	}
	if counts.Paused > 0 {
		parts = append(parts, fmt.Sprintf("%d paused", counts.Paused))
	}
	return strings.Join(parts, " · ")
}

// Down selects the next visible instance row, skipping headers and wrapping.
func (l *List) Down() {
	vis := l.visibleInstances()
	if len(vis) == 0 {
		return
	}
	idx := indexOfInstance(vis, l.selected)
	if idx < 0 {
		l.selected = vis[0]
		return
	}
	l.selected = vis[(idx+1)%len(vis)]
}

// KillSelected kills the currently selected (visible) instance's session and
// removes it from the list, clamping the selection to the nearest visible
// instance. Replaces the old index-based Kill().
func (l *List) KillSelected() {
	target := l.selected
	if target == nil {
		return
	}
	// Kill the terminal session / worktree.
	if err := target.Kill(); err != nil {
		log.ErrorLog.Printf("could not kill instance: %v", err)
	}
	l.RemoveInstance(target)
}

// RemoveInstance removes an exact instance from the canonical list (without
// killing its session — used for cancelled/failed unstarted instances), updating
// the repo bookkeeping and clamping the selection if the removed instance was
// selected.
func (l *List) RemoveInstance(target *session.Instance) {
	idx := indexOfInstance(l.items, target)
	if idx < 0 {
		return
	}

	// Unregister the repo path. Unstarted instances have no repo yet, so skip them
	// silently rather than logging an expected error.
	if target.Started() {
		if repoPath, err := target.RepoPath(); err != nil {
			log.ErrorLog.Printf("could not get repo path: %v", err)
		} else {
			l.rmRepo(repoPath)
		}
	}

	wasSelected := l.selected == target
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	l.recompute()
	if wasSelected {
		l.selected = l.nearestVisible(idx)
		l.recompute()
	}
}

func (l *List) Attach() (chan struct{}, error) {
	if l.selected == nil {
		return nil, fmt.Errorf("no instance selected")
	}
	return l.selected.Attach()
}

// Up selects the previous visible instance row, skipping headers and wrapping.
func (l *List) Up() {
	vis := l.visibleInstances()
	if len(vis) == 0 {
		return
	}
	idx := indexOfInstance(vis, l.selected)
	if idx < 0 {
		l.selected = vis[len(vis)-1]
		return
	}
	l.selected = vis[(idx-1+len(vis))%len(vis)]
}

func (l *List) addRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		l.repos[repo] = 0
	}
	l.repos[repo]++
}

func (l *List) rmRepo(repo string) {
	if _, ok := l.repos[repo]; !ok {
		log.ErrorLog.Printf("repo %s not found", repo)
		return
	}
	l.repos[repo]--
	if l.repos[repo] == 0 {
		delete(l.repos, repo)
	}
}

// AddInstance adds a new instance to the list. It returns a finalizer function that should be called when the instance
// is started. If the instance was restored from storage or is paused, you can call the finalizer immediately.
// When creating a new one and entering the name, you want to call the finalizer once the name is done.
func (l *List) AddInstance(instance *session.Instance) (finalize func()) {
	l.items = append(l.items, instance)
	if l.selected == nil {
		l.selected = instance
	}
	l.recompute()
	// The finalizer registers the repo path once the instance is started.
	return func() {
		repoPath, err := instance.RepoPath()
		if err != nil {
			log.ErrorLog.Printf("could not get repo path: %v", err)
			return
		}

		l.addRepo(repoPath)
		l.recompute()
	}
}

// GetSelectedInstance returns the currently selected instance if it is visible in
// the active mode/filter, otherwise nil (so the preview shows the empty state
// while the desired selection is hidden).
func (l *List) GetSelectedInstance() *session.Instance {
	if l.selected != nil && l.isVisible(l.selected) {
		return l.selected
	}
	return nil
}

// SetSelectedInstance selects the canonical instance at idx. Noop if out of bounds.
// Selection is stored by identity; the index is only a convenience for callers
// (and tests) that already know the canonical position.
func (l *List) SetSelectedInstance(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.selected = l.items[idx]
	l.recompute()
}

// SelectInstance selects the given instance by identity if it is in the list.
func (l *List) SelectInstance(target *session.Instance) {
	if indexOfInstance(l.items, target) < 0 {
		return
	}
	l.selected = target
	l.recompute()
}

// SelectNewInstance selects a just-created (possibly unstarted, possibly
// filtered-out) instance by identity, so it is presented and selected while the
// user names it. The caller is responsible for suspending any active filter.
func (l *List) SelectNewInstance(target *session.Instance) {
	l.selected = target
	l.recompute()
}

// SelectFirstVisible selects the first visible instance row, if any. Used while
// searching when the previously selected instance is filtered out.
func (l *List) SelectFirstVisible() {
	vis := l.visibleInstances()
	if len(vis) > 0 {
		l.selected = vis[0]
	}
}

// MoveSelectedUp swaps the selected instance with the canonical instance above it.
func (l *List) MoveSelectedUp() bool {
	idx := indexOfInstance(l.items, l.selected)
	if idx <= 0 {
		return false
	}
	l.items[idx], l.items[idx-1] = l.items[idx-1], l.items[idx]
	l.recompute()
	return true
}

// MoveSelectedDown swaps the selected instance with the canonical instance below it.
func (l *List) MoveSelectedDown() bool {
	idx := indexOfInstance(l.items, l.selected)
	if idx < 0 || idx >= len(l.items)-1 {
		return false
	}
	l.items[idx], l.items[idx+1] = l.items[idx+1], l.items[idx]
	l.recompute()
	return true
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items
}
