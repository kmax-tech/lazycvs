package tui

import (
	"lazycvs/cvs"
	"fmt"
	"os"
	"path/filepath"
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
	HistoryCompare
)

type HistoryModel struct {
	path       string
	revisions  []cvs.Revision
	cursor     int
	offset     int
	mode       HistoryMode
	content    string
	viewport   viewport.Model
	leftWidth  int
	rightWidth int
	height     int
	ready      bool

	// hasWorkingCopy is true when the file has uncommitted local changes (M/C).
	// When true, a synthetic "(working copy)" pseudo-row is shown at index 0
	// of the revision list and represents the current on-disk state.
	hasWorkingCopy bool

	// hOffset is the horizontal scroll offset (visible columns) applied to
	// the right-panel viewport. rawView holds the un-shifted rendered string;
	// applyHOffset re-derives viewport content from rawView whenever hOffset
	// or content changes.
	hOffset int
	rawView string

	// Compare mode state
	compareFrom      int             // -1 = no selection, >= 0 = revision index
	compareToWorking bool            // true when comparing against working copy
	compareDiff      *cvs.DiffResult // parsed diff for compare view
	compareFromRev   string          // "from" revision label
	compareToRev     string          // "to" revision label
	compareSBS       bool            // side-by-side toggle for compare

	// Diff mode state (parent-rev diff)
	diffData *cvs.DiffResult // parsed diff between selected rev and its parent
	diffSBS  bool            // side-by-side toggle for diff
}

func NewHistoryModel() HistoryModel {
	return HistoryModel{compareFrom: -1}
}

type historyLoadedMsg struct {
	path           string
	history        *cvs.FileHistory
	hasWorkingCopy bool
}

type historyContentMsg struct {
	content string
}

type historyDiffMsg struct {
	diff *cvs.DiffResult
}

func loadHistory(exec *cvs.CVSExecutor, path string, hasWorkingCopy bool) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.Run("log", path)
		if err != nil && result == nil {
			return historyLoadedMsg{path: path, hasWorkingCopy: hasWorkingCopy}
		}
		history := cvs.ParseLog(result.Stdout)
		return historyLoadedMsg{path: path, history: history, hasWorkingCopy: hasWorkingCopy}
	}
}

// loadWorkingDiff runs `cvs diff -u <file>` (working copy vs HEAD) and reports
// the parsed result via historyDiffMsg, the same channel used for revision diffs.
func loadWorkingDiff(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.Run("diff", "-u", path)
		if result == nil {
			return historyDiffMsg{}
		}
		return historyDiffMsg{diff: cvs.ParseDiff(result.Stdout)}
	}
}

// loadWorkingContent reads the file's current on-disk content directly (no CVS
// command needed) and reports it via historyContentMsg.
func loadWorkingContent(workDir, path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(filepath.Join(workDir, path))
		if err != nil {
			return historyContentMsg{content: "Error reading file: " + err.Error()}
		}
		return historyContentMsg{content: string(data)}
	}
}

func loadRevisionContent(exec *cvs.CVSExecutor, path, rev string) tea.Cmd {
	return func() tea.Msg {
		content, err := catRevisionStdout(exec, path, rev)
		if err != nil {
			return historyContentMsg{content: "Error loading revision: " + err.Error()}
		}
		return historyContentMsg{content: content}
	}
}

func loadRevisionDiff(exec *cvs.CVSExecutor, path, rev1, rev2 string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.Run("diff", "-u", "-r", rev1, "-r", rev2, path)
		if result == nil {
			return historyDiffMsg{}
		}
		return historyDiffMsg{diff: cvs.ParseDiff(result.Stdout)}
	}
}

func loadBlame(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.Run("annotate", path)
		if err != nil && result == nil {
			return historyContentMsg{content: "Error loading blame"}
		}
		return historyContentMsg{content: result.Stdout}
	}
}

type historyCompareMsg struct {
	diff    *cvs.DiffResult
	fromRev string
	toRev   string
}

func loadCompare(exec *cvs.CVSExecutor, path, rev1, rev2 string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.Run("diff", "-u", "-r", rev1, "-r", rev2, path)
		if result == nil {
			return historyCompareMsg{}
		}
		parsed := cvs.ParseDiff(result.Stdout)
		return historyCompareMsg{diff: parsed, fromRev: rev1, toRev: rev2}
	}
}

func loadCompareWorking(exec *cvs.CVSExecutor, path, rev string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.Run("diff", "-u", "-r", rev, path)
		if result == nil {
			return historyCompareMsg{}
		}
		parsed := cvs.ParseDiff(result.Stdout)
		return historyCompareMsg{diff: parsed, fromRev: rev, toRev: "working copy"}
	}
}

func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	switch msg := msg.(type) {
	case historyLoadedMsg:
		m.path = msg.path
		if msg.history != nil {
			m.revisions = msg.history.Revisions
		} else {
			m.revisions = nil
		}
		m.hasWorkingCopy = msg.hasWorkingCopy
		m.cursor = 0
		m.offset = 0
		m.content = ""
		m.diffData = nil
		m.diffSBS = false
		m.ready = false
		m.ClearCompare()
		m.mode = HistoryDiff // default landing — "what changed" view
		return m, nil

	case historyCompareMsg:
		m.compareDiff = msg.diff
		m.compareFromRev = msg.fromRev
		m.compareToRev = msg.toRev
		m.hOffset = 0 // new content — start at column 0
		w := 80
		if m.rightWidth > 0 {
			w = m.rightWidth
		}
		m.viewport = viewport.New(w, m.height)
		m.setView(m.renderCompare())
		m.ready = true
		return m, nil

	case historyContentMsg:
		m.content = msg.content
		m.diffData = nil // content fallback (e.g. first revision, no parent)
		m.hOffset = 0
		w := 80
		if m.rightWidth > 0 {
			w = m.rightWidth
		}
		m.viewport = viewport.New(w, m.height)
		m.setView(m.content)
		m.ready = true
		return m, nil

	case historyDiffMsg:
		m.diffData = msg.diff
		m.hOffset = 0
		w := 80
		if m.rightWidth > 0 {
			w = m.rightWidth
		}
		m.viewport = viewport.New(w, m.height)
		m.setView(m.renderDiffView())
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Down):
			if m.cursor < m.numRows()-1 {
				m.cursor++
				m.ensureVisible()
			}
			return m, nil
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()
			}
			return m, nil
		case key.Matches(msg, keys.Diff):
			m.mode = HistoryDiff
			m.compareFrom = -1
			return m, nil
		case key.Matches(msg, keys.Blame):
			m.mode = HistoryBlame
			m.compareFrom = -1
			return m, nil
		case key.Matches(msg, keys.Enter):
			m.mode = HistoryContent
			m.compareFrom = -1
			return m, nil
		case key.Matches(msg, keys.Space):
			if m.compareFrom < 0 {
				// Mark first revision for compare
				m.compareFrom = m.cursor
			} else if m.cursor == m.compareFrom {
				// Unmark — exit compare mode entirely so navigation works again
				m.ClearCompare()
			} else {
				// Second selection — trigger compare
				m.compareToWorking = false
				m.mode = HistoryCompare
			}
			return m, nil
		case key.Matches(msg, keys.CompareWorking):
			// Only meaningful when cursor is on a real revision, not the
			// working-copy pseudo-row (which is already the working copy).
			if m.IsWorkingCopyRow() || m.SelectedRevision() == nil {
				return m, nil
			}
			m.compareFrom = m.cursor
			m.compareToWorking = true
			m.mode = HistoryCompare
			return m, nil
		case key.Matches(msg, keys.SideBySide):
			switch {
			case m.mode == HistoryCompare && m.compareDiff != nil:
				m.compareSBS = !m.compareSBS
				m.setView(m.renderCompare())
			case m.mode == HistoryDiff && m.diffData != nil:
				m.diffSBS = !m.diffSBS
				m.setView(m.renderDiffView())
			}
			return m, nil

		case msg.String() == "<":
			if m.hOffset > 0 {
				m.hOffset -= hScrollStep
				if m.hOffset < 0 {
					m.hOffset = 0
				}
				m.reapplyHOffset()
			}
			return m, nil

		case msg.String() == ">":
			m.hOffset += hScrollStep
			m.reapplyHOffset()
			return m, nil
		}

		// Scroll viewport (for other keys like pgup/pgdn)
		if m.ready {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *HistoryModel) ensureVisible() {
	visibleCount := (m.height - 1) / 2
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
	m.leftWidth = leftWidth
	m.rightWidth = rightWidth
	m.height = height
	if m.ready {
		m.viewport.Width = rightWidth
		m.viewport.Height = height
	}
}

// numRows returns the total selectable rows in the left panel, including the
// working-copy pseudo-row when present.
func (m HistoryModel) numRows() int {
	if m.hasWorkingCopy {
		return len(m.revisions) + 1
	}
	return len(m.revisions)
}

// IsWorkingCopyRow reports whether the cursor is on the working-copy
// pseudo-row.
func (m HistoryModel) IsWorkingCopyRow() bool {
	return m.hasWorkingCopy && m.cursor == 0
}

// revisionIndex maps a row index to the underlying revisions slice index,
// accounting for the working-copy pseudo-row if present. Returns -1 for the
// pseudo-row or out-of-range.
func (m HistoryModel) revisionIndex(row int) int {
	if m.hasWorkingCopy {
		if row == 0 {
			return -1
		}
		row--
	}
	if row < 0 || row >= len(m.revisions) {
		return -1
	}
	return row
}

// SelectedRevision returns the revision under the cursor, or nil if the cursor
// is on the working-copy pseudo-row or out of range.
func (m HistoryModel) SelectedRevision() *cvs.Revision {
	idx := m.revisionIndex(m.cursor)
	if idx < 0 {
		return nil
	}
	return &m.revisions[idx]
}

// CompareFromIsWorking reports whether the user picked the working-copy row as
// the "from" side of a free compare.
func (m HistoryModel) CompareFromIsWorking() bool {
	return m.hasWorkingCopy && m.compareFrom == 0
}

// CompareFromRev returns the revision the user picked as "from" of a free
// compare, or nil if no pick (or the pick is the working-copy row).
func (m HistoryModel) CompareFromRev() *cvs.Revision {
	idx := m.revisionIndex(m.compareFrom)
	if idx < 0 {
		return nil
	}
	return &m.revisions[idx]
}

func (m HistoryModel) HasCompare() bool {
	return m.compareFrom >= 0
}

func (m *HistoryModel) ClearCompare() {
	m.compareFrom = -1
	m.compareToWorking = false
	m.compareDiff = nil
	m.compareFromRev = ""
	m.compareToRev = ""
	m.compareSBS = false
	if m.mode == HistoryCompare {
		// Exit compare mode back to the default diff view (matches the
		// `d` keybinding's landing mode, so users land somewhere familiar).
		m.mode = HistoryDiff
	}
}

// setView stores the raw rendered string and applies the current hOffset
// before pushing it to the viewport. Call this anywhere you'd otherwise call
// m.viewport.SetContent(...) on freshly rendered content.
func (m *HistoryModel) setView(s string) {
	m.rawView = s
	m.viewport.SetContent(applyHOffset(s, m.hOffset))
}

// reapplyHOffset re-derives viewport content from rawView for the current
// hOffset. Cheap — no CVS or rendering work; just slices each line.
func (m *HistoryModel) reapplyHOffset() {
	m.viewport.SetContent(applyHOffset(m.rawView, m.hOffset))
}

// applyHOffset trims the first `offset` visible columns from each line of s,
// preserving ANSI styling. Empty when offset is zero (fast path).
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

func (m HistoryModel) renderCompare() string {
	if m.compareDiff == nil || len(m.compareDiff.Hunks) == 0 {
		return "No differences"
	}
	if m.compareSBS {
		return renderSideBySideDiff(m.compareDiff, m.rightWidth)
	}
	return renderUnifiedDiff(m.compareDiff.Hunks)
}

func (m HistoryModel) renderDiffView() string {
	if m.diffData == nil || len(m.diffData.Hunks) == 0 {
		return "No differences"
	}
	if m.diffSBS {
		return renderSideBySideDiff(m.diffData, m.rightWidth)
	}
	return renderUnifiedDiff(m.diffData.Hunks)
}

func (m HistoryModel) ViewLeft() string {
	if m.path == "" {
		return mutedStyle.Render("  No file selected")
	}

	header := titleStyle.Render(fmt.Sprintf(" revisions — %s", m.path))
	if m.compareFrom >= 0 {
		fromLabel := "(working)"
		if !m.CompareFromIsWorking() {
			if r := m.CompareFromRev(); r != nil {
				fromLabel = r.Number
			}
		}
		header += mutedStyle.Render(fmt.Sprintf("  compare: %s ↔ ?", fromLabel))
	}
	var lines []string
	lines = append(lines, header)

	rows := m.numRows()
	if rows == 0 {
		lines = append(lines, mutedStyle.Render("  No revisions"))
		return strings.Join(lines, "\n")
	}

	visibleCount := (m.height - 1) / 2 // 2 lines per row, 1 for header
	end := min(m.offset+visibleCount, rows)
	for i := m.offset; i < end; i++ {
		var line1, line2 string

		if m.hasWorkingCopy && i == 0 {
			// Working-copy pseudo-row
			prefix := " "
			if i == m.compareFrom {
				prefix = lipgloss.NewStyle().Foreground(colorActive).Render("▸")
			}
			line1 = fmt.Sprintf("%s%-6s %-8s %s",
				prefix,
				lipgloss.NewStyle().Foreground(colorActive).Render("WORK"),
				"you",
				"now")
			line2 = "   " + mutedStyle.Render("uncommitted local changes")
		} else {
			rev := m.revisions[m.revisionIndex(i)]
			date := rev.Date.Format("Jan 02 06")

			prefix := " "
			if i == m.compareFrom {
				prefix = lipgloss.NewStyle().Foreground(colorActive).Render("▸")
			}
			line1 = fmt.Sprintf("%s%-6s %-8s %s",
				prefix, rev.Number, truncate(rev.Author, 8), date)

			if len(rev.Tags) > 0 {
				tag := truncate(rev.Tags[0], 12)
				line1 += " " + lipgloss.NewStyle().Foreground(colorStale).Render(tag)
			}

			msg := strings.SplitN(rev.Message, "\n", 2)[0]
			msg = truncate(msg, m.leftWidth-4)
			delta := ""
			if rev.LinesAdded > 0 || rev.LinesRemoved > 0 {
				delta = fmt.Sprintf(" %s%s",
					lipgloss.NewStyle().Foreground(colorUpdated).Render(fmt.Sprintf("+%d", rev.LinesAdded)),
					lipgloss.NewStyle().Foreground(colorConflict).Render(fmt.Sprintf("-%d", rev.LinesRemoved)))
			}
			line2 = "   " + mutedStyle.Render(msg) + delta
		}

		if i == m.cursor {
			line1 = lipgloss.NewStyle().Reverse(true).Render(line1)
		}
		lines = append(lines, line1)
		lines = append(lines, line2)
	}

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m HistoryModel) ViewRight() string {
	if !m.ready {
		switch m.mode {
		case HistoryCompare:
			return mutedStyle.Render("  Loading compare...")
		case HistoryDiff:
			return mutedStyle.Render("  Loading diff...")
		case HistoryBlame:
			return mutedStyle.Render("  Select a revision (blame mode)")
		default:
			return mutedStyle.Render("  Select a revision (content mode)")
		}
	}
	return m.viewport.View()
}
