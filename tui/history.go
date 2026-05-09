package tui

import (
	"fmt"
	"lazycvs/cvs"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// hScrollStep is how many visible columns one < / > press scrolls by.
const hScrollStep = 8

type HistoryMode int

const (
	HistoryContent HistoryMode = iota
	HistoryDiff
	HistoryBlame
)

// historyFileUI is the per-file UI state (cursor, mode, scroll, compare
// state). Preserved across file switches so navigating away and back
// keeps the user's place — including any active comparison.
type historyFileUI struct {
	cursor        int
	offset        int
	mode          HistoryMode
	diffSBS       bool
	compareAnchor int
	vsWorking     bool
}

// HistoryModel renders the History tab. It owns only the *current* view
// (which file is open, where the cursor is, what's in the viewport). All
// CVS data — revision lists, diffs, file contents — lives in App-owned
// per-path caches (see App.historyData) and is pushed in via Apply* methods
// from the App handlers. This keeps async results from one file from ever
// poisoning another file's view: the cache is keyed by the path the result
// came from, and the model only reflects data for its current path.
type HistoryModel struct {
	path       string
	revisions  []cvs.Revision
	cursor     int
	offset     int
	mode       HistoryMode
	leftWidth  int
	rightWidth int
	height     int

	// Current right-pane projection. diffData is non-nil in HistoryDiff
	// mode when a diff is loaded; content is non-empty otherwise. Both are
	// derived from App-owned caches.
	diffData *cvs.DiffResult
	content  string
	diffSBS  bool

	// Right-pane labels for the panel border title. Set eagerly when the
	// cursor moves so the title reflects the cursor immediately, not after
	// the async result lands.
	diffFromRev string
	diffToRev   string

	// compareAnchor is the index of a revision the user pinned as the
	// "from" side of a diff via the space key. -1 = no anchor; in that
	// case the parent revision is used as "from". When set, the right
	// pane shows revisions[anchor] ↔ cursor instead of cursor.parent ↔
	// cursor.
	compareAnchor int

	// vsWorking, when true, replaces the "to" side of the diff with the
	// on-disk working copy. Toggled with the w key.
	vsWorking bool

	// Working-copy state vs the revision history. Set by ApplyRevisions
	// from the file's CVS status; mutually exclusive.
	//
	// hasWorkingRow      → file is modified locally (status M/C/A); a
	//                      pseudo "working copy local" row is exposed at
	//                      virtual cursor index 0.
	// workingMatchesHead → file is clean; HEAD revision gets a "(working)"
	//                      annotation in the left pane.
	hasWorkingRow      bool
	workingMatchesHead bool

	viewport viewport.Model
	hOffset  int
	rawView  string

	// Per-file UI memory. Saved on path switch, restored on return.
	uiState map[string]historyFileUI
}

func NewHistoryModel() HistoryModel {
	return HistoryModel{
		compareAnchor: -1,
		uiState:       make(map[string]historyFileUI),
	}
}

// --- async messages ---------------------------------------------------------

// historyLoadedMsg carries the parsed `cvs log` output for a path. The path
// is included so the App handler can store it in the right cache slot even
// if the user has since switched files.
type historyLoadedMsg struct {
	path    string
	history *cvs.FileHistory
}

// historyContentMsg carries the on-disk content of one revision (or a
// blame/working-copy result). path+rev key the App's content cache.
type historyContentMsg struct {
	path    string
	rev     string
	content string
}

// historyDiffMsg carries a parsed unified diff between two revisions.
// path + (fromRev:toRev) key the App's diff cache.
type historyDiffMsg struct {
	path    string
	fromRev string
	toRev   string
	diff    *cvs.DiffResult
}

func loadHistory(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.RunReadOnly("log", path)
		if err != nil && result == nil {
			return historyLoadedMsg{path: path}
		}
		return historyLoadedMsg{path: path, history: cvs.ParseLog(result.Stdout)}
	}
}

func loadRevisionContent(exec *cvs.CVSExecutor, path, rev string) tea.Cmd {
	return func() tea.Msg {
		content, err := catRevisionStdout(exec, path, rev)
		if err != nil {
			return historyContentMsg{path: path, rev: rev, content: "Error loading revision: " + err.Error()}
		}
		return historyContentMsg{path: path, rev: rev, content: cvs.EnsureUTF8(content)}
	}
}

func loadRevisionDiff(exec *cvs.CVSExecutor, path, fromRev, toRev string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", "-r", fromRev, "-r", toRev, path)
		var diff *cvs.DiffResult
		if result != nil {
			diff = cvs.ParseDiff(cvs.EnsureUTF8(result.Stdout))
		}
		return historyDiffMsg{path: path, fromRev: fromRev, toRev: toRev, diff: diff}
	}
}

// loadWorkingDiff runs `cvs diff -u -r <fromRev> <file>` (one -r flag), which
// produces a diff from fromRev to the on-disk working copy. The result is
// stored in the diff cache under the sentinel toRev "@working" so the
// working-copy comparison gets cached just like a rev-to-rev diff.
func loadWorkingDiff(exec *cvs.CVSExecutor, path, fromRev string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", "-r", fromRev, path)
		var diff *cvs.DiffResult
		if result != nil {
			diff = cvs.ParseDiff(cvs.EnsureUTF8(result.Stdout))
		}
		return historyDiffMsg{path: path, fromRev: fromRev, toRev: workingRev, diff: diff}
	}
}

// workingRev is the sentinel revision string used as the "to" side of a
// working-copy diff. It's not a real CVS revision, just a stable cache
// key and label. prettyRev maps it to a human-readable form for titles.
const workingRev = "@working"

// prettyRev converts internal sentinel revision identifiers into the
// labels we want to show in panel titles.
func prettyRev(rev string) string {
	if rev == workingRev {
		return "working"
	}
	return rev
}

func loadBlame(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.RunReadOnly("annotate", path)
		if err != nil && result == nil {
			return historyContentMsg{path: path, rev: blameRev, content: "Error loading blame"}
		}
		return historyContentMsg{path: path, rev: blameRev, content: cvs.EnsureUTF8(result.Stdout)}
	}
}

// --- per-file UI state ------------------------------------------------------

// SwitchTo loads UI state for path, saving the current path's state first.
// Caller is responsible for then pushing the file's revisions/diff/content
// from the App's caches via the Apply* methods.
func (m *HistoryModel) SwitchTo(path string) {
	if m.path != "" {
		m.uiState[m.path] = historyFileUI{
			cursor:        m.cursor,
			offset:        m.offset,
			mode:          m.mode,
			diffSBS:       m.diffSBS,
			compareAnchor: m.compareAnchor,
			vsWorking:     m.vsWorking,
		}
	}
	m.path = path
	m.revisions = nil
	m.diffData = nil
	m.content = ""
	m.diffFromRev = ""
	m.diffToRev = ""
	m.hOffset = 0
	m.rawView = ""
	if s, ok := m.uiState[path]; ok {
		m.cursor = s.cursor
		m.offset = s.offset
		m.mode = s.mode
		m.diffSBS = s.diffSBS
		m.compareAnchor = s.compareAnchor
		m.vsWorking = s.vsWorking
	} else {
		m.cursor, m.offset = 0, 0
		m.mode = HistoryDiff
		m.diffSBS = false
		m.compareAnchor = -1
		m.vsWorking = false
	}
}

// ApplyRevisions pushes a freshly loaded revisions list. Called by the
// App handler when historyLoadedMsg arrives for the current path.
// ApplyRevisions pushes a freshly loaded revisions list and the file's
// working-copy state. dirty == true when the file has uncommitted local
// changes (M/C/A) — in that case a pseudo "working copy local" row is
// exposed at virtual cursor index 0 and the working copy can be picked
// for compare like any other row. dirty == false means the working copy
// matches the head revision; ViewLeft annotates HEAD with "(working)".
func (m *HistoryModel) ApplyRevisions(history *cvs.FileHistory, dirty bool) {
	if history != nil {
		m.revisions = history.Revisions
	} else {
		m.revisions = nil
	}
	hasRevisions := len(m.revisions) > 0
	m.hasWorkingRow = dirty && hasRevisions
	m.workingMatchesHead = !dirty && hasRevisions
	rows := m.numRows()
	if m.cursor >= rows {
		m.cursor = 0
		m.offset = 0
	}
	// A previously-saved compare anchor may be stale if the file's
	// dirty state changed (working pseudo-row appeared or disappeared).
	// Drop it rather than referencing a non-existent row.
	if m.compareAnchor >= rows {
		m.compareAnchor = -1
	}
}

// numRows returns the count of selectable rows in the left pane,
// including the pseudo working-copy row when present.
func (m HistoryModel) numRows() int {
	if m.hasWorkingRow {
		return len(m.revisions) + 1
	}
	return len(m.revisions)
}

// revisionIndex maps a left-pane row index to the m.revisions slice
// index. Returns -1 for the pseudo working-copy row (when present).
func (m HistoryModel) revisionIndex(row int) int {
	if m.hasWorkingRow {
		if row == 0 {
			return -1
		}
		return row - 1
	}
	return row
}

// IsWorkingCopyRow reports whether the cursor is on the pseudo
// working-copy row.
func (m HistoryModel) IsWorkingCopyRow() bool {
	return m.hasWorkingRow && m.cursor == 0
}

// CompareAnchorIsWorking reports whether the user pinned the working
// copy as the comparison "from" via space on the pseudo-row.
func (m HistoryModel) CompareAnchorIsWorking() bool {
	return m.hasWorkingRow && m.compareAnchor == 0
}

// CompareAnchorRev returns the revision pinned as compare anchor, or
// nil if no anchor is set or the anchor is the working pseudo-row.
func (m HistoryModel) CompareAnchorRev() *cvs.Revision {
	if m.compareAnchor < 0 {
		return nil
	}
	idx := m.revisionIndex(m.compareAnchor)
	if idx < 0 || idx >= len(m.revisions) {
		return nil
	}
	return &m.revisions[idx]
}

// HeadRevision returns the most recent real revision (skipping the
// pseudo working-copy row), or nil when the file has no revisions yet.
func (m HistoryModel) HeadRevision() *cvs.Revision {
	if len(m.revisions) == 0 {
		return nil
	}
	return &m.revisions[0]
}

// ApplyDiff pushes a parsed diff into the right pane. The labels are kept
// in sync with diffFromRev/diffToRev so the panel title matches the body.
func (m *HistoryModel) ApplyDiff(fromRev, toRev string, diff *cvs.DiffResult) {
	m.diffFromRev = fromRev
	m.diffToRev = toRev
	m.diffData = diff
	m.content = ""
	m.hOffset = 0
	m.resetViewport()
	m.setView(m.renderDiffView())
}

// ApplyContent pushes file content into the right pane (revision content,
// blame, or working copy).
func (m *HistoryModel) ApplyContent(rev, content string) {
	m.diffFromRev = ""
	m.diffToRev = rev
	m.diffData = nil
	m.content = content
	m.hOffset = 0
	m.resetViewport()
	m.setView(content)
}

// SetPendingLabels updates the right-panel header labels eagerly (before
// the async result lands), so the title always matches the cursor.
func (m *HistoryModel) SetPendingLabels(fromRev, toRev string) {
	m.diffFromRev = fromRev
	m.diffToRev = toRev
}

// --- update / view ---------------------------------------------------------

func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	rows := m.numRows()
	switch {
	case key.Matches(keyMsg, keys.Down):
		if m.cursor < rows-1 {
			m.cursor++
			m.ensureVisible()
		}
	case key.Matches(keyMsg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case key.Matches(keyMsg, keys.Top):
		m.cursor = 0
		m.offset = 0
	case key.Matches(keyMsg, keys.Bottom):
		m.cursor = max(0, rows-1)
		m.ensureVisible()
	case key.Matches(keyMsg, keys.PageDown):
		// Each revision row spans 2 visual lines; a page is height/2 rows.
		page := m.height / 2
		if page < 1 {
			page = 1
		}
		m.cursor = clamp(m.cursor+page, 0, max(0, rows-1))
		m.ensureVisible()
	case key.Matches(keyMsg, keys.PageUp):
		page := m.height / 2
		if page < 1 {
			page = 1
		}
		m.cursor = clamp(m.cursor-page, 0, max(0, rows-1))
		m.ensureVisible()
	case key.Matches(keyMsg, keys.Diff):
		m.mode = HistoryDiff
	case key.Matches(keyMsg, keys.Blame):
		m.mode = HistoryBlame
	case key.Matches(keyMsg, keys.Enter):
		m.mode = HistoryContent
	case key.Matches(keyMsg, keys.SideBySide):
		if m.mode == HistoryDiff && m.diffData != nil {
			m.diffSBS = !m.diffSBS
			m.setView(m.renderDiffView())
		}
	case key.Matches(keyMsg, keys.Space):
		// Toggle the "from" anchor. Pressing space on the already-anchored
		// row clears it; otherwise pin the cursor row as the comparison
		// origin. Force Diff mode so the result is visible.
		if m.compareAnchor == m.cursor {
			m.compareAnchor = -1
		} else {
			m.compareAnchor = m.cursor
		}
		m.mode = HistoryDiff
	case key.Matches(keyMsg, keys.CompareWorking):
		// Toggle the "to" side between the cursor's revision and the
		// on-disk working copy.
		m.vsWorking = !m.vsWorking
		m.mode = HistoryDiff
	case keyMsg.String() == "<":
		if m.hOffset > 0 {
			m.hOffset -= hScrollStep
			if m.hOffset < 0 {
				m.hOffset = 0
			}
			m.reapplyHOffset()
		}
	case keyMsg.String() == ">":
		m.hOffset += hScrollStep
		m.reapplyHOffset()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *HistoryModel) ensureVisible() {
	// Each revision takes two rows (header + description), so the
	// visible-row count is half the panel height.
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height/2)
}

// SetSize takes the App's layout envelope. History fills both panels —
// revisions list on the left, diff/content viewport on the right — so it
// reads LeftW, RightW, and Height.
func (m *HistoryModel) SetSize(d PanelDims) {
	widthChanged := d.RightW != m.rightWidth
	m.leftWidth = d.LeftW
	m.rightWidth = d.RightW
	m.height = d.Height
	m.viewport.Width = d.RightW
	m.viewport.Height = d.Height
	// On a width change we have to re-render the right-pane content at
	// the new width: the cached rawView was computed for the old width
	// and stretching/squeezing it by changing the viewport bounds alone
	// produces wrapped or truncated lines (UTF-8 sequences cut mid-byte
	// surface as � replacement chars).
	if widthChanged {
		switch {
		case m.diffData != nil:
			m.setView(m.renderDiffView())
		case m.content != "":
			m.setView(m.content)
		}
	}
}

func (m *HistoryModel) resetViewport() {
	w := m.rightWidth
	if w <= 0 {
		w = 80
	}
	m.viewport = viewport.New(w, m.height)
}

func (m *HistoryModel) setView(s string) {
	m.rawView = s
	m.viewport.SetContent(applyHOffset(s, m.hOffset))
}

func (m *HistoryModel) reapplyHOffset() {
	m.viewport.SetContent(applyHOffset(m.rawView, m.hOffset))
}

func applyHOffset(s string, offset int) string {
	if offset <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = ansi.TruncateLeft(line, offset, "")
	}
	return strings.Join(lines, "\n")
}

func (m HistoryModel) renderDiffView() string {
	if m.diffData == nil || len(m.diffData.Hunks) == 0 {
		return mutedStyle.Render("  No differences")
	}
	if m.diffSBS {
		return renderSideBySideDiff(m.diffData, m.rightWidth)
	}
	return renderUnifiedDiff(m.diffData.Hunks)
}

// SelectedRevision returns the revision under the cursor, or nil if the
// cursor is on the pseudo working-copy row.
func (m HistoryModel) SelectedRevision() *cvs.Revision {
	idx := m.revisionIndex(m.cursor)
	if idx < 0 || idx >= len(m.revisions) {
		return nil
	}
	return &m.revisions[idx]
}

// Path returns the currently displayed file path (may be "").
func (m HistoryModel) Path() string { return m.path }

// NumRevisions returns the count of selectable rows in the left pane,
// including the pseudo working-copy row when the file is dirty. Used
// by callers to decide whether there's anything to auto-load.
func (m HistoryModel) NumRevisions() int { return m.numRows() }

// ViewLeft renders the revision list. The header (file name) is rendered
// by the panel frame in app_layout.go — this returns rows only.
func (m HistoryModel) ViewLeft() string {
	if m.path == "" {
		return mutedStyle.Render("  No file selected")
	}
	rows := m.numRows()
	if rows == 0 {
		return mutedStyle.Render("  Loading revisions...")
	}

	visibleCount := m.height / 2
	if visibleCount < 1 {
		visibleCount = 1
	}
	end := min(m.offset+visibleCount, rows)

	// Use a guaranteed-distinct prefix character per row: "▌" for the cursor
	// row, " " otherwise. This forces the differential renderer to repaint
	// the prefix cell when the cursor moves, which in turn flushes any
	// stale reverse-video styling on the row left over from a previous
	// frame.
	var lines []string
	for i := m.offset; i < end; i++ {
		isCursor := i == m.cursor
		isAnchor := i == m.compareAnchor
		// Marker glyph at column 0: cursor wins over anchor on the same
		// row (reverse-video makes the anchor's identity clear via the
		// "Compare —" label in the right pane title).
		marker := " "
		switch {
		case isCursor:
			marker = "▌"
		case isAnchor:
			marker = "▸"
		}
		var renderedMarker string
		if isAnchor && !isCursor {
			renderedMarker = lipgloss.NewStyle().Foreground(colorActive).Render(marker)
		} else {
			renderedMarker = marker
		}

		var line1, line2 string
		if m.hasWorkingRow && i == 0 {
			// Pseudo working-copy row.
			line1 = fmt.Sprintf("%s%-6s %-8s %s",
				renderedMarker,
				lipgloss.NewStyle().Foreground(colorActive).Render("WORK"),
				"you",
				"now")
			line2 = renderedMarker + "  " + mutedStyle.Render("(working copy local)")
		} else {
			rev := m.revisions[m.revisionIndex(i)]
			date := rev.Date.Format("Jan 02 06")

			line1 = fmt.Sprintf("%s%-6s %-8s %s", renderedMarker, rev.Number, truncate(rev.Author, 8), date)
			if len(rev.Tags) > 0 {
				line1 += " " + lipgloss.NewStyle().Foreground(colorStale).Render(truncate(rev.Tags[0], 12))
			}
			// HEAD badge: when the file is clean, the first real revision
			// in the list IS the working copy. Append "(working)" to
			// signal that the on-disk content matches it.
			if m.workingMatchesHead && m.revisionIndex(i) == 0 {
				line1 += " " + lipgloss.NewStyle().Foreground(colorActive).Render("(working)")
			}

			msg := strings.SplitN(rev.Message, "\n", 2)[0]
			msg = truncate(msg, m.leftWidth-4)
			delta := ""
			if rev.LinesAdded > 0 || rev.LinesRemoved > 0 {
				delta = fmt.Sprintf(" %s%s",
					lipgloss.NewStyle().Foreground(colorUpdated).Render(fmt.Sprintf("+%d", rev.LinesAdded)),
					lipgloss.NewStyle().Foreground(colorConflict).Render(fmt.Sprintf("-%d", rev.LinesRemoved)))
			}
			line2 = renderedMarker + "  " + mutedStyle.Render(msg) + delta
		}

		if isCursor {
			sel := lipgloss.NewStyle().Reverse(true)
			plain1 := ansi.Strip(line1)
			plain2 := ansi.Strip(line2)
			if w := lipgloss.Width(plain1); w < m.leftWidth {
				plain1 += strings.Repeat(" ", m.leftWidth-w)
			}
			if w := lipgloss.Width(plain2); w < m.leftWidth {
				plain2 += strings.Repeat(" ", m.leftWidth-w)
			}
			line1 = sel.Render(plain1)
			line2 = sel.Render(plain2)
		}
		lines = append(lines, line1, line2)
	}

	// Always pad to exactly m.height lines so renderPanel never has to
	// extend or truncate. Inconsistent line counts between frames are
	// what allow stale rows to persist outside the panel frame.
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// ViewRight renders whatever is currently in the viewport (diff, content,
// blame), or a context-appropriate placeholder while data is in flight.
// "ready" is now a derived condition: data is present iff diffData or
// content is non-empty.
func (m HistoryModel) ViewRight() string {
	if m.path == "" {
		return ""
	}
	if m.diffData == nil && m.content == "" {
		switch m.mode {
		case HistoryDiff:
			return mutedStyle.Render("  Loading diff...")
		case HistoryBlame:
			return mutedStyle.Render("  Loading blame...")
		default:
			return mutedStyle.Render("  Loading...")
		}
	}
	return m.viewport.View()
}
