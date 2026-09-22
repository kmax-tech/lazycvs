package tui

import (
	"sort"

	"github.com/charmbracelet/bubbles/key"
)

// The keymap registry: one table describing what every user-facing key
// means, and the only source the keybar and the help modal render from.
//
// Before this table, bindings (keys.go), keybar (app_layout.go), help
// modal (dialog.go) and the user guide each carried their own copy of
// the truth. They drifted: issue 021 found keys advertised but dead,
// issue 024 found the same fact stated five times with one copy wrong.
//
// The rule the table encodes — and that every future key decision is
// measured against:
//
//  1. Scope comes from the selection, not from the key. With files
//     marked, an action applies to the marked set; otherwise to the
//     cursor row.
//  2. Shift is the same action, sharper (u update → U force update),
//     never "the bulk variant of".
//  3. External tools are their own group, not a Shift accident.

// keyContext is the snapshot the keybar and help render against — a
// value, never the App itself. Predicates stay pure and testable
// without a TUI, and a new piece of app state has to be added here
// deliberately instead of leaking in through a pointer.
type keyContext struct {
	tab    Tab
	focus  Panel
	status string // status of the cursor row: "", "M", "C", "?", "A", "R"
	marked int    // size of the marked set

	// History-only state.
	histMode      HistoryMode
	compare       bool // an anchor is set, or the working copy is pinned in
	pairMergeable bool // the compared pair is two real revisions

	// stagedInput is true while the commit message field has focus. It
	// suppresses the action row entirely: the keys belong to the text
	// input, not to the file list.
	stagedInput bool
}

// keyContext is the single adapter from App to context. Everything
// downstream works on the value.
func (m App) keyContext() keyContext {
	ctx := keyContext{
		tab:    m.activeTab,
		focus:  m.focus,
		marked: len(m.filelist.marked),
	}
	if path := m.selectedFilePath(); path != "" {
		ctx.status = m.statusMap[path]
	}
	if m.activeTab == TabStaged {
		ctx.stagedInput = m.staged.mode == StagedCommit && m.staged.input.Focused()
	}
	if m.activeTab == TabHistory {
		ctx.histMode = m.history.mode
		ctx.compare = m.history.HasCompare() || m.history.vsWorking
		from, to := m.comparePair()
		ctx.pairMergeable = from != "" && to != "" &&
			from != workingRev && to != workingRev
	}
	return ctx
}

// keyGroup is the semantic family of an action — what kind of thing it
// does. It drives grouping in the help modal and is what a later
// leader-key layer would branch on.
type keyGroup int

const (
	groupNav keyGroup = iota
	groupAct
	groupExternal
	groupView
	groupCVS
	groupConsole
)

// keyScope says what an action operates on. Scope is a property of the
// action, not of the key's case — see rule 1 above.
type keyScope int

const (
	scopeGlobal    keyScope = iota // the app or the current view
	scopeCursor                    // the row under the cursor
	scopeSelection                 // marked set, falling back to the cursor row
	scopeDirectory                 // the directory under the cursor, subtree included
)

// keyEntry describes one user-facing key. A binding may appear more
// than once when a tab genuinely gives it a different meaning or rank
// (space marks in Files and unstages in Staged); the collision test
// only forbids two entries that are live in the *same* context.
type keyEntry struct {
	binding key.Binding
	group   keyGroup
	scope   keyScope

	// tabs limits the entry to those tabs; empty means every tab.
	tabs []Tab
	// applies narrows further, on context the tab alone doesn't say
	// (cursor status, History mode). nil means "whenever the tab matches".
	applies func(keyContext) bool

	// Keybar placement. bar == 0 keeps the entry out of the keybar (help
	// only); otherwise it is the rank within its segment. Ranks are
	// spaced so an entry can be slotted in without renumbering.
	bar        int
	barSegment string // segment id: act, view, cvs, hint, anchor, scroll
	barKey     string // display override; default is binding.Help().Key
	barDesc    string // display override; default is binding.Help().Desc

	// Help placement. An empty section keeps the entry out of the help
	// modal's key rows.
	section  string
	helpKey  string // display override; default is binding.Help().Key
	helpDesc string
}

func (e keyEntry) inTab(tab Tab) bool {
	if len(e.tabs) == 0 {
		return true
	}
	for _, t := range e.tabs {
		if t == tab {
			return true
		}
	}
	return false
}

func (e keyEntry) live(ctx keyContext) bool {
	if !e.inTab(ctx.tab) {
		return false
	}
	return e.applies == nil || e.applies(ctx)
}

func (e keyEntry) barText() (string, string) {
	k, d := e.binding.Help().Key, e.binding.Help().Desc
	if e.barKey != "" {
		k = e.barKey
	}
	if e.barDesc != "" {
		d = e.barDesc
	}
	return k, d
}

// --- predicates --------------------------------------------------------
//
// Named rather than inlined so the registry reads as a statement about
// the UI ("only on a conflict") and so the test suite can exercise them
// one by one.

func onConflict(ctx keyContext) bool { return ctx.status == "C" }

func historyPlain(ctx keyContext) bool {
	return !ctx.compare && ctx.histMode != HistoryDiff
}

// historyShowingDiff is true whenever the right pane holds a diff —
// either an explicit diff view or a picked comparison pair. That is
// when the side-by-side toggle has something to toggle.
func historyShowingDiff(ctx keyContext) bool {
	return ctx.compare || ctx.histMode == HistoryDiff
}

// outsideCompare gates the actions that address a single file rather
// than a pair (editing the working copy, restoring one revision).
func outsideCompare(ctx keyContext) bool { return !ctx.compare }

func historyCompare(ctx keyContext) bool { return ctx.compare }

func historyComparePair(ctx keyContext) bool {
	return ctx.compare && ctx.pairMergeable
}

func inConsole(ctx keyContext) bool { return ctx.focus == PanelConsole }

func outsideConsole(ctx keyContext) bool { return ctx.focus != PanelConsole }

// --- the registry ------------------------------------------------------

// Keys handled inside a single widget's own handler rather than through
// the keyMap table, but user-facing all the same — documented here so
// the help modal has one source, not two.
var (
	commitEditorKey = key.NewBinding(key.WithKeys("ctrl+e"),
		key.WithHelp("C-e", "commit message in $EDITOR"))
	consoleResizeKey = key.NewBinding(key.WithKeys("+", "-"),
		key.WithHelp("+ / -", "grow / shrink console"))
)

var fileTabs = []Tab{TabTree, TabFavorites}

var keyRegistry = []keyEntry{
	// ── Navigation (keybar row 1 is rendered from group nav) ─────────
	{binding: keys.Down, group: groupNav, scope: scopeGlobal,
		bar: 10, barSegment: "nav", barKey: "j/k", barDesc: "nav",
		section: "Navigation", helpKey: "j / k", helpDesc: "cursor down / up"},
	{binding: keys.Right, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "l / →", helpDesc: "expand dir; nothing to expand → right pane"},
	{binding: keys.Left, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "h / ←", helpDesc: "collapse / up (tree); right pane → back left"},
	{binding: keys.CollapseAll, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpDesc: "collapse all, jump to root (tree)"},
	{binding: keys.Top, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "g / G", helpDesc: "top / bottom"},
	{binding: keys.PageUp, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "PgUp/PgDn", helpDesc: "scroll page (also C-u / C-d)"},
	{binding: keys.Enter, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpDesc: "select + jump to right pane (yazi-style)"},
	{binding: keys.FocusL, group: groupNav, scope: scopeGlobal,
		bar: 20, barSegment: "nav", barKey: "C-h/C-l", barDesc: "pane",
		section: "Navigation", helpKey: "C-h / C-←", helpDesc: "focus left pane"},
	{binding: keys.FocusR, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "C-l / C-→", helpDesc: "focus right pane"},
	{binding: keys.FocusC, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "C-j / C-↓", helpDesc: "focus console"},
	{binding: keys.FocusUp, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpKey: "C-k / C-↑", helpDesc: "back to panes from console"},
	{binding: keys.Tab, group: groupNav, scope: scopeGlobal,
		section: "Navigation", helpDesc: "cycle focused panel"},
	{binding: keys.BackTab, group: groupNav, scope: scopeGlobal},
	// Siblings described on their partner's help row (j/k, g/G,
	// PgUp/PgDn): registered so the completeness test stays strict,
	// silent so the help doesn't say the same thing twice.
	{binding: keys.Up, group: groupNav, scope: scopeGlobal},
	{binding: keys.Bottom, group: groupNav, scope: scopeGlobal},
	{binding: keys.PageDown, group: groupNav, scope: scopeGlobal},
	{binding: keys.Search, group: groupNav, scope: scopeGlobal,
		bar: 70, barSegment: "nav", barDesc: "search",
		section: "Navigation", helpDesc: "fuzzy search files"},
	{binding: keys.Help, group: groupNav, scope: scopeGlobal,
		bar: 80, barSegment: "nav"},
	{binding: keys.Escape, group: groupNav, scope: scopeGlobal,
		bar: 90, barSegment: "nav"},
	{binding: keys.Quit, group: groupNav, scope: scopeGlobal,
		bar: 100, barSegment: "nav",
		section: "Navigation", helpDesc: "quit lazycvs (inside a dialog: close it)"},

	// ── Tabs ──────────────────────────────────────────────────────────
	{binding: keys.Tab1, group: groupNav, scope: scopeGlobal,
		bar: 30, barSegment: "nav", barDesc: "files",
		section: "Tabs", helpDesc: "Files — tree+filelist OR tree+diff preview"},
	{binding: keys.Tab2, group: groupNav, scope: scopeGlobal,
		bar: 40, barSegment: "nav", barDesc: "fav",
		section: "Tabs", helpDesc: "Favorites — pinned directories"},
	{binding: keys.Tab3, group: groupNav, scope: scopeGlobal,
		bar: 50, barSegment: "nav", barDesc: "staged",
		section: "Tabs", helpDesc: "Staged — files marked for commit"},
	{binding: keys.Tab4, group: groupNav, scope: scopeGlobal,
		bar: 60, barSegment: "nav", barDesc: "history",
		section: "Tabs", helpDesc: "History — revisions of the selected file"},

	// ── File actions, Files/Favorites tabs ────────────────────────────
	// Table order is the help modal's reading order; the bar ranks below
	// are what order the keybar, independently.
	{binding: keys.Diff, group: groupAct, scope: scopeCursor, tabs: fileTabs,
		bar: 10, barSegment: "act",
		section: "File actions", helpDesc: "diff (working vs base revision)"},
	{binding: keys.Commit, group: groupAct, scope: scopeSelection, tabs: fileTabs,
		bar: 20, barSegment: "act",
		section: "File actions", helpDesc: "commit cursor file (or marked files)"},
	{binding: commitEditorKey, group: groupExternal, scope: scopeGlobal, tabs: []Tab{TabStaged},
		section: "File actions", helpDesc: "commit message in $EDITOR (while input focused)"},
	{binding: keys.Add, group: groupAct, scope: scopeCursor, tabs: []Tab{TabTree},
		bar: 30, barSegment: "act",
		section: "File actions", helpDesc: "add cursor file (?-status) to CVS"},
	{binding: keys.Revert, group: groupAct, scope: scopeSelection, tabs: fileTabs,
		bar: 50, barSegment: "act",
		section: "File actions", helpDesc: "revert cursor file (bulk in Staged tab)"},
	{binding: keys.RestoreBackup, group: groupAct, scope: scopeSelection,
		section: "File actions", helpDesc: "restore .lazycvs-backup (marked / dir subtree / cursor)"},
	{binding: keys.Remove, group: groupAct, scope: scopeSelection, tabs: []Tab{TabTree},
		bar: 40, barSegment: "act",
		section: "File actions", helpDesc: "remove (single file or marked set)"},
	{binding: keys.Ignore, group: groupAct, scope: scopeSelection, tabs: fileTabs,
		bar: 60, barSegment: "act",
		section: "File actions", helpDesc: "ignore — add pattern to .cvsignore"},
	{binding: keys.Edit, group: groupExternal, scope: scopeCursor, tabs: fileTabs,
		bar: 70, barSegment: "act",
		section: "File actions", helpDesc: "edit in $EDITOR"},

	// External tools are their own group — rule 3: E and M are not the
	// "sharper" variants of e, they are a different kind of action.
	{binding: keys.EditDiff, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpDesc: "external diff tool (config.diff_tool)"},
	{binding: keys.Merge, group: groupExternal, scope: scopeCursor,
		tabs: []Tab{TabTree, TabFavorites, TabStaged}, applies: onConflict,
		bar: 10, barSegment: "hint",
		section: "File actions", helpDesc: "external merge tool (conflict files)"},
	{binding: keys.Open, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpDesc: "open in OS default app"},
	// The C-e alias of e. A second row for a binding already listed is
	// how an alias gets documented without inventing a second entry in
	// the keybar — it carries no bar rank.
	{binding: keys.Edit, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpKey: "C-e", helpDesc: "edit in $EDITOR (picker-style alias of e)"},
	{binding: keys.AltOpen, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpDesc: "alternate opener (config [editor] alt_open)"},
	{binding: keys.Reveal, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpDesc: "reveal in Finder / file manager"},
	{binding: keys.CopyPath, group: groupExternal, scope: scopeCursor,
		section: "File actions", helpDesc: "copy absolute path to clipboard"},
	{binding: keys.Space, group: groupAct, scope: scopeCursor, tabs: fileTabs,
		section: "File actions", helpDesc: "mark/unmark"},
	{binding: keys.MarkAll, group: groupAct, scope: scopeDirectory, tabs: fileTabs,
		bar: 80, barSegment: "act",
		section: "File actions", helpDesc: "mark/unmark every changed file in dir"},
	{binding: keys.ClearMarks, group: groupAct, scope: scopeSelection, tabs: fileTabs,
		bar: 90, barSegment: "act", applies: outsideConsole,
		section: "File actions", helpDesc: "clear ALL marks (in console: clear log)"},
	{binding: keys.FavAdd, group: groupAct, scope: scopeDirectory, tabs: []Tab{TabFavorites},
		bar: 100, barSegment: "act",
		section: "File actions", helpKey: "+ / -", helpDesc: "add/remove from favorites"},
	{binding: keys.FavDel, group: groupAct, scope: scopeDirectory, tabs: []Tab{TabFavorites},
		bar: 110, barSegment: "act"},

	// ── View & filters ────────────────────────────────────────────────
	{binding: keys.ViewMode, group: groupView, scope: scopeGlobal, tabs: []Tab{TabTree},
		bar: 10, barSegment: "view",
		section: "View & filters", helpDesc: "layout: Files (tree+list) ↔ Detail (tree+preview)"},
	{binding: keys.ListMode, group: groupView, scope: scopeGlobal, tabs: fileTabs,
		bar: 20, barSegment: "view",
		section: "View & filters", helpDesc: "list mode: flat → sub → tree"},
	{binding: keys.Filter, group: groupView, scope: scopeGlobal, tabs: fileTabs,
		bar: 30, barSegment: "view", applies: outsideConsole,
		section: "View & filters", helpDesc: "status filter: all → * → M → C → ? → all"},
	{binding: keys.HideIgnored, group: groupView, scope: scopeGlobal, tabs: []Tab{TabTree},
		bar: 40, barSegment: "view",
		section: "View & filters", helpDesc: "toggle hide-ignored on both panes"},
	{binding: keys.TreeList, group: groupView, scope: scopeGlobal, tabs: fileTabs,
		section: "View & filters", helpDesc: "jump list to tree mode"},

	// ── CVS ───────────────────────────────────────────────────────────
	{binding: keys.Status, group: groupCVS, scope: scopeGlobal, tabs: fileTabs,
		bar: 10, barSegment: "cvs",
		section: "CVS", helpDesc: "status refresh (dry-run + scan, parallel)"},
	{binding: keys.Update, group: groupCVS, scope: scopeSelection, tabs: fileTabs,
		bar: 20, barSegment: "cvs",
		section: "CVS", helpDesc: "update (cvs update -d -P)"},
	{binding: keys.ForceUpdate, group: groupCVS, scope: scopeSelection, tabs: fileTabs,
		section: "CVS", helpDesc: "force update (cvs update -C — overwrite!)"},
	{binding: keys.Recent, group: groupCVS, scope: scopeDirectory, tabs: fileTabs,
		bar: 30, barSegment: "cvs",
		section: "CVS", helpDesc: "recent repo changes for dir + subdirs (all users)"},

	// ── Staged tab ────────────────────────────────────────────────────
	{binding: keys.Commit, group: groupAct, scope: scopeSelection, tabs: []Tab{TabStaged},
		bar: 10, barSegment: "act"},
	{binding: keys.Revert, group: groupAct, scope: scopeSelection, tabs: []Tab{TabStaged},
		bar: 20, barSegment: "act"},
	{binding: keys.Ignore, group: groupAct, scope: scopeSelection, tabs: []Tab{TabStaged},
		bar: 30, barSegment: "act"},
	{binding: keys.Diff, group: groupAct, scope: scopeCursor, tabs: []Tab{TabStaged},
		bar: 40, barSegment: "act"},
	{binding: keys.Space, group: groupAct, scope: scopeCursor, tabs: []Tab{TabStaged},
		bar: 50, barSegment: "act", barDesc: "unstage"},
	{binding: keys.ClearMarks, group: groupAct, scope: scopeSelection, tabs: []Tab{TabStaged},
		bar: 60, barSegment: "act", applies: outsideConsole},
	{binding: keys.Update, group: groupCVS, scope: scopeSelection, tabs: []Tab{TabStaged},
		bar: 10, barSegment: "cvs"},

	// ── History tab ───────────────────────────────────────────────────
	// The action segment has three shapes: plain revision browsing, an
	// open diff, and a picked comparison pair. Table order below follows
	// the help modal; bar ranks order the keybar.
	{binding: keys.Enter, group: groupNav, scope: scopeCursor, tabs: []Tab{TabHistory},
		section: "History tab", helpDesc: "show revision content"},
	{binding: keys.Diff, group: groupAct, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 10, barSegment: "act", applies: historyPlain,
		section: "History tab", helpDesc: "show diff vs previous revision"},
	{binding: keys.SideBySide, group: groupView, scope: scopeGlobal, tabs: []Tab{TabHistory},
		bar: 10, barSegment: "act", applies: historyShowingDiff,
		section: "History tab", helpDesc: "toggle side-by-side diff"},
	{binding: keys.Blame, group: groupAct, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 20, barSegment: "act",
		section: "History tab", helpDesc: "blame view"},
	{binding: keys.CompareWorking, group: groupView, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 60, barSegment: "act",
		section: "History tab", helpDesc: "compare vs working copy"},
	{binding: keys.Restore, group: groupAct, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 70, barSegment: "act", applies: historyPlain,
		section: "History tab", helpDesc: "restore selected revision into working copy"},
	{binding: keys.Space, group: groupAct, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 10, barSegment: "anchor", barDesc: "anchor",
		section: "History tab", helpDesc: "anchor revision for two-rev comparison"},
	{binding: keys.EditDiff, group: groupExternal, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 40, barSegment: "act",
		section: "History tab", helpDesc: "open the selected pair in the external diff tool"},
	{binding: keys.Merge, group: groupExternal, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 50, barSegment: "act", applies: historyComparePair,
		section: "History tab", helpDesc: "merge the selected pair into the working copy"},
	{binding: keys.RestoreBackup, group: groupAct, scope: scopeCursor, tabs: []Tab{TabHistory},
		section: "History tab", helpDesc: "restore the file's .lazycvs-backup (e.g. undo a pair merge)"},
	{binding: keys.ScrollLeft, group: groupNav, scope: scopeGlobal, tabs: []Tab{TabHistory},
		bar: 10, barSegment: "scroll", barKey: "</>", barDesc: "scroll",
		section: "History tab", helpKey: "< / >", helpDesc: "horizontal scroll in diff content"},
	{binding: keys.ScrollRight, group: groupNav, scope: scopeGlobal, tabs: []Tab{TabHistory}},
	{binding: keys.Escape, group: groupNav, scope: scopeGlobal, tabs: []Tab{TabHistory},
		bar: 80, barSegment: "act", applies: historyCompare,
		section: "History tab", helpDesc: "cancel comparison mode"},
	{binding: keys.Edit, group: groupExternal, scope: scopeCursor, tabs: []Tab{TabHistory},
		bar: 30, barSegment: "act", applies: outsideCompare},

	// ── Console ───────────────────────────────────────────────────────
	{binding: keys.Filter, group: groupConsole, scope: scopeGlobal, applies: inConsole,
		section: "Console", helpDesc: "cycle filter: compact → verbose → errors → slow"},
	{binding: consoleResizeKey, group: groupConsole, scope: scopeGlobal, applies: inConsole,
		section: "Console", helpDesc: "grow / shrink the console panel (when focused)"},
	{binding: keys.Clear, group: groupConsole, scope: scopeGlobal, applies: inConsole,
		section: "Console", helpDesc: "clear panel (session log file keeps everything)"},
}

// localBindings are widget-local: they belong to a dialog or a text
// input, never to the global keymap, and are deliberately absent from
// keybar and help rows. Keeping them in an explicit list (rather than
// forcing them into the registry) is what lets the completeness test be
// strict about everything else.
var localBindings = []key.Binding{
	keys.Yes, // dialog confirmation
	keys.No,  // dialog confirmation
}

// pendingRemoval are bindings that have a handler but no place in the
// UI — orphans the registry refuses to legitimize by advertising them.
// Listed here so the completeness test stays strict while the removal
// is still ahead of us.
var pendingRemoval = []key.Binding{
	keys.Preview, // p: preview modal, advertised nowhere; removed in S3c
}

// --- keybar assembly ---------------------------------------------------

// barSegmentOrder is the left-to-right order of keybar segments per tab.
// A segment renders only if at least one of its entries is live.
func barSegmentOrder(tab Tab) []string {
	switch tab {
	case TabStaged:
		return []string{"act", "cvs", "hint"}
	case TabHistory:
		return []string{"act", "anchor", "scroll"}
	default:
		return []string{"act", "view", "cvs", "hint"}
	}
}

// barSegmentLabel is the group label printed before a segment's keys.
// The History action segment is the one that renames itself: what the
// keys do there depends on whether a diff or a pair is open.
func barSegmentLabel(segment string, ctx keyContext) string {
	if ctx.tab == TabHistory && segment == "act" {
		switch {
		case ctx.compare:
			return "[Compare] "
		case ctx.histMode == HistoryDiff:
			return "[Diff] "
		default:
			return "[Action] "
		}
	}
	switch segment {
	case "act":
		return "[Action] "
	case "view":
		return "[View] "
	case "cvs":
		return "[CVS] "
	default:
		return ""
	}
}

// barEntries returns the live entries of one keybar segment, in rank
// order.
func barEntries(ctx keyContext, segment string) []keyEntry {
	var out []keyEntry
	for _, e := range keyRegistry {
		if e.bar == 0 || e.barSegment != segment || !e.live(ctx) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].bar < out[j].bar })
	return out
}

// --- help assembly -----------------------------------------------------

// helpSectionOrder is the order the help modal prints its key sections
// in. Prose blocks around them stay in dialog.go — the table owns the
// rows, not the explanations between them.
var helpSectionOrder = []string{
	"Navigation", "Tabs", "File actions", "View & filters",
	"CVS", "History tab", "Console",
}

// helpRows returns the key rows of one help section, in registry order.
// An entry without helpDesc contributes nothing: that is how a binding
// can be registered (and pass the completeness test) while staying out
// of the help — an alias like C-l/C-→ is described on its sibling's row.
func helpRows(section string) [][2]string {
	var out [][2]string
	for _, e := range keyRegistry {
		if e.section != section || e.helpDesc == "" {
			continue
		}
		k := e.binding.Help().Key
		if e.helpKey != "" {
			k = e.helpKey
		}
		out = append(out, [2]string{k, e.helpDesc})
	}
	return out
}
