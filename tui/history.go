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

// historyFileUI is the per-file UI state (cursor, mode, scroll). Preserved
// across file switches so navigating away and back keeps the user's place.
type historyFileUI struct {
	cursor  int
	offset  int
	mode    HistoryMode
	diffSBS bool
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

	viewport viewport.Model
	hOffset  int
	rawView  string

	// Per-file UI memory. Saved on path switch, restored on return.
	uiState map[string]historyFileUI
}

func NewHistoryModel() HistoryModel {
	return HistoryModel{
		uiState: make(map[string]historyFileUI),
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

func loadBlame(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.RunReadOnly("annotate", path)
		if err != nil && result == nil {
			return historyContentMsg{path: path, rev: "@blame", content: "Error loading blame"}
		}
		return historyContentMsg{path: path, rev: "@blame", content: cvs.EnsureUTF8(result.Stdout)}
	}
}

// --- per-file UI state ------------------------------------------------------

// SwitchTo loads UI state for path, saving the current path's state first.
// Caller is responsible for then pushing the file's revisions/diff/content
// from the App's caches via the Apply* methods.
func (m *HistoryModel) SwitchTo(path string) {
	if m.path != "" {
		m.uiState[m.path] = historyFileUI{
			cursor: m.cursor, offset: m.offset, mode: m.mode, diffSBS: m.diffSBS,
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
		m.cursor, m.offset, m.mode, m.diffSBS = s.cursor, s.offset, s.mode, s.diffSBS
	} else {
		m.cursor, m.offset = 0, 0
		m.mode = HistoryDiff
		m.diffSBS = false
	}
}

// ApplyRevisions pushes a freshly loaded revisions list. Called by the
// App handler when historyLoadedMsg arrives for the current path.
func (m *HistoryModel) ApplyRevisions(history *cvs.FileHistory) {
	if history != nil {
		m.revisions = history.Revisions
	} else {
		m.revisions = nil
	}
	if m.cursor >= len(m.revisions) {
		m.cursor = 0
		m.offset = 0
	}
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

	switch {
	case key.Matches(keyMsg, keys.Down):
		if m.cursor < len(m.revisions)-1 {
			m.cursor++
			m.ensureVisible()
		}
	case key.Matches(keyMsg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
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
	visibleCount := m.height / 2
	if visibleCount < 1 {
		visibleCount = 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visibleCount {
		m.offset = m.cursor - visibleCount + 1
	}
}

func (m *HistoryModel) SetSize(leftWidth, rightWidth, height int) {
	widthChanged := rightWidth != m.rightWidth
	m.leftWidth = leftWidth
	m.rightWidth = rightWidth
	m.height = height
	m.viewport.Width = rightWidth
	m.viewport.Height = height
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

// SelectedRevision returns the revision under the cursor, or nil.
func (m HistoryModel) SelectedRevision() *cvs.Revision {
	if m.cursor < 0 || m.cursor >= len(m.revisions) {
		return nil
	}
	return &m.revisions[m.cursor]
}

// Path returns the currently displayed file path (may be "").
func (m HistoryModel) Path() string { return m.path }

// NumRevisions returns the count of revisions for the current file.
func (m HistoryModel) NumRevisions() int { return len(m.revisions) }

// ViewLeft renders the revision list. The header (file name) is rendered
// by the panel frame in app_layout.go — this returns rows only.
func (m HistoryModel) ViewLeft() string {
	if m.path == "" {
		return mutedStyle.Render("  No file selected")
	}
	if len(m.revisions) == 0 {
		return mutedStyle.Render("  Loading revisions...")
	}

	visibleCount := m.height / 2
	if visibleCount < 1 {
		visibleCount = 1
	}
	end := min(m.offset+visibleCount, len(m.revisions))

	// Use a guaranteed-distinct prefix character per row: "▌" for the cursor
	// row, " " otherwise. This forces the differential renderer to repaint
	// the prefix cell when the cursor moves, which in turn flushes any
	// stale reverse-video styling on the row left over from a previous
	// frame. Without this, a terminal that diff-skips identical content
	// can leave the previous cursor row visually highlighted.
	var lines []string
	for i := m.offset; i < end; i++ {
		rev := m.revisions[i]
		date := rev.Date.Format("Jan 02 06")

		isCursor := i == m.cursor
		marker := " "
		if isCursor {
			marker = "▌"
		}

		line1 := fmt.Sprintf("%s%-6s %-8s %s", marker, rev.Number, truncate(rev.Author, 8), date)
		if len(rev.Tags) > 0 {
			line1 += " " + lipgloss.NewStyle().Foreground(colorStale).Render(truncate(rev.Tags[0], 12))
		}

		msg := strings.SplitN(rev.Message, "\n", 2)[0]
		msg = truncate(msg, m.leftWidth-4)
		delta := ""
		if rev.LinesAdded > 0 || rev.LinesRemoved > 0 {
			delta = fmt.Sprintf(" %s%s",
				lipgloss.NewStyle().Foreground(colorUpdated).Render(fmt.Sprintf("+%d", rev.LinesAdded)),
				lipgloss.NewStyle().Foreground(colorConflict).Render(fmt.Sprintf("-%d", rev.LinesRemoved)))
		}
		line2 := marker + "  " + mutedStyle.Render(msg) + delta

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
