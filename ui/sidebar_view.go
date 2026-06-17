package ui

import (
	"hangar/session"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SidebarMode selects how the workspace sidebar orders and groups instances.
// Modes are mutually exclusive and cycled with a single key.
type SidebarMode int

const (
	// ModeManual is the canonical user-controlled order (reorderable with K/J).
	ModeManual SidebarMode = iota
	// ModeGroupByRepo clusters instances under a per-repository header.
	ModeGroupByRepo
	// ModeRecentActivity sorts by most recent observed activity, newest first.
	ModeRecentActivity
	// ModePinnedPending lifts waiting-for-user instances into a pinned section.
	ModePinnedPending
)

// sidebarModeCount is the number of modes; used for cycling.
const sidebarModeCount = int(ModePinnedPending) + 1

const (
	noRepoLabel      = "(no repo)"
	pendingHeader    = "Pending"
	workspacesHeader = "Workspaces"
)

// String returns the short label used in the sidebar title (e.g. "recent").
func (m SidebarMode) String() string {
	switch m {
	case ModeManual:
		return "manual"
	case ModeGroupByRepo:
		return "by repo"
	case ModeRecentActivity:
		return "recent"
	case ModePinnedPending:
		return "pending"
	default:
		return "manual"
	}
}

// Next returns the next mode in the forward cycle.
func (m SidebarMode) Next() SidebarMode {
	return SidebarMode((int(m) + 1) % sidebarModeCount)
}

// Prev returns the previous mode in the cycle.
func (m SidebarMode) Prev() SidebarMode {
	return SidebarMode((int(m) + sidebarModeCount - 1) % sidebarModeCount)
}

// ValidSidebarMode validates a persisted mode value, falling back to ModeManual
// for unknown values (back-compat with older / corrupt state).
func ValidSidebarMode(v int) SidebarMode {
	if v < 0 || v >= sidebarModeCount {
		return ModeManual
	}
	return SidebarMode(v)
}

// rowKind distinguishes section headers from workspace rows in the view-model.
type rowKind int

const (
	rowHeader rowKind = iota
	rowInstance
)

// displayRow is one rendered line of the sidebar: either a (non-selectable)
// section header or a workspace instance row. number is 1-based and continuous
// across visible instances, ignoring headers.
type displayRow struct {
	kind     rowKind
	header   string
	instance *session.Instance
	number   int
}

// sidebarItem is the per-instance data the view-model builders operate on. It is
// extracted once from each session.Instance (see extractItem) so the builders
// remain pure functions over plain data — no I/O, no instance internals — and are
// trivially unit-testable.
type sidebarItem struct {
	instance     *session.Instance
	repoKey      string // repo root path; "" when unknown (unstarted / no worktree)
	repoLabel    string // repo display name; "" when unknown
	activityTime time.Time
	pending      bool
}

// extractItem derives the view-model data for a single instance.
func extractItem(inst *session.Instance) sidebarItem {
	item := sidebarItem{
		instance:     inst,
		activityTime: inst.EffectiveActivityTime(),
		pending:      inst.IsWaitingForUser(),
	}
	if path, err := inst.RepoPath(); err == nil && path != "" {
		item.repoKey = path
		item.repoLabel = filepath.Base(path)
	}
	return item
}

// buildView is the pure, deterministic sidebar view-model builder. It filters the
// canonical items, extracts plain view data, and dispatches to a mode builder.
// It has no timers and no I/O, so it is trivially unit-testable.
func buildView(items []*session.Instance, mode SidebarMode, filter string) []displayRow {
	its := make([]sidebarItem, 0, len(items))
	for _, inst := range items {
		if !matchesFilter(inst, filter) {
			continue
		}
		its = append(its, extractItem(inst))
	}

	switch mode {
	case ModeGroupByRepo:
		return buildGroupByRepo(its)
	case ModeRecentActivity:
		return buildRecentActivity(its)
	case ModePinnedPending:
		return buildPinnedPending(its)
	default:
		return buildManual(its)
	}
}

// buildManual renders instances in canonical order with continuous numbering.
func buildManual(items []sidebarItem) []displayRow {
	rows := make([]displayRow, 0, len(items))
	for i, it := range items {
		rows = append(rows, displayRow{kind: rowInstance, instance: it.instance, number: i + 1})
	}
	return rows
}

// buildRecentActivity orders instances by most recent observed activity, newest
// first. Ties are broken by CreatedAt (desc) then Title (asc), which is fully
// deterministic and never changes across ticks — so instances updated in the same
// metadata batch (equal activity timestamps) keep a stable relative order and
// concurrently streaming agents do not swap slots every tick (anti-thrash, R2).
func buildRecentActivity(items []sidebarItem) []displayRow {
	sorted := make([]sidebarItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if !a.activityTime.Equal(b.activityTime) {
			return a.activityTime.After(b.activityTime)
		}
		if !a.instance.CreatedAt.Equal(b.instance.CreatedAt) {
			return a.instance.CreatedAt.After(b.instance.CreatedAt)
		}
		return a.instance.Title < b.instance.Title
	})

	rows := make([]displayRow, 0, len(sorted))
	for i, it := range sorted {
		rows = append(rows, displayRow{kind: rowInstance, instance: it.instance, number: i + 1})
	}
	return rows
}

// repoBucket collects the instances belonging to one repository.
type repoBucket struct {
	key          string
	label        string
	displayLabel string
	items        []sidebarItem
}

// buildGroupByRepo clusters instances under per-repository headers. Grouping keys
// on the repo root path (not name), so distinct repos that share a basename are
// kept separate (R7). Groups are sorted alphabetically by label; within a group
// canonical order is preserved. Colliding basenames are disambiguated with a
// trailing path hint. Headers always render, even for a single repo.
func buildGroupByRepo(items []sidebarItem) []displayRow {
	buckets := make(map[string]*repoBucket)
	var order []*repoBucket
	for _, it := range items {
		key := it.repoKey
		label := it.repoLabel
		if key == "" {
			label = noRepoLabel
		}
		b, ok := buckets[key]
		if !ok {
			b = &repoBucket{key: key, label: label}
			buckets[key] = b
			order = append(order, b)
		}
		b.items = append(b.items, it)
	}

	sort.SliceStable(order, func(i, j int) bool {
		if order[i].label != order[j].label {
			return order[i].label < order[j].label
		}
		return order[i].key < order[j].key
	})
	disambiguateLabels(order)

	rows := make([]displayRow, 0, len(items)+len(order))
	n := 0
	for _, b := range order {
		rows = append(rows, displayRow{kind: rowHeader, header: b.displayLabel})
		for _, it := range b.items {
			n++
			rows = append(rows, displayRow{kind: rowInstance, instance: it.instance, number: n})
		}
	}
	return rows
}

// disambiguateLabels sets each bucket's displayLabel, appending a parent-directory
// hint when two repos share a basename so headers stay distinct.
func disambiguateLabels(buckets []*repoBucket) {
	counts := make(map[string]int)
	for _, b := range buckets {
		counts[b.label]++
	}
	for _, b := range buckets {
		if counts[b.label] > 1 && b.key != "" {
			hint := filepath.Base(filepath.Dir(b.key))
			b.displayLabel = b.label + " (" + hint + ")"
		} else {
			b.displayLabel = b.label
		}
	}
}

// buildPinnedPending lifts waiting-for-user instances into a top "Pending"
// section (canonical order), with the remaining instances below under a
// "Workspaces" header (canonical order). When nothing is pending it renders as a
// plain list with no headers.
func buildPinnedPending(items []sidebarItem) []displayRow {
	var pending, rest []sidebarItem
	for _, it := range items {
		if it.pending {
			pending = append(pending, it)
		} else {
			rest = append(rest, it)
		}
	}

	rows := make([]displayRow, 0, len(items)+2)
	n := 0
	if len(pending) > 0 {
		rows = append(rows, displayRow{kind: rowHeader, header: pendingHeader})
		for _, it := range pending {
			n++
			rows = append(rows, displayRow{kind: rowInstance, instance: it.instance, number: n})
		}
		rows = append(rows, displayRow{kind: rowHeader, header: workspacesHeader})
	}
	for _, it := range rest {
		n++
		rows = append(rows, displayRow{kind: rowInstance, instance: it.instance, number: n})
	}
	return rows
}

// matchesFilter reports whether the instance matches the case-insensitive
// substring query against its Title or repo path (Path and, when available,
// RepoName). An empty filter matches everything.
func matchesFilter(inst *session.Instance, filter string) bool {
	if filter == "" {
		return true
	}
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(inst.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(inst.Path), q) {
		return true
	}
	if repo, err := inst.RepoName(); err == nil && repo != "" {
		if strings.Contains(strings.ToLower(repo), q) {
			return true
		}
	}
	return false
}
