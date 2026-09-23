package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// The Marked tab: the files the user picked with `space`, and the
// commit input that acts on them. CVS has no index, so this is a
// selection, not a staging area — the name says so since issue 044,
// and the identifiers since the rename that followed it.
//
// MarkedTabModel rather than MarkedModel because FileListModel.marked
// is the set of marks itself: one word for one thing, and these are
// two things — the marks, and the panel that shows them.
type MarkedMode int

const (
	MarkedActions MarkedMode = iota // show action menu
	MarkedCommit                    // show commit message input
)

type markedFile struct {
	path   string
	status string
}

type MarkedTabModel struct {
	files      []markedFile
	cursor     int
	offset     int
	mode       MarkedMode
	input      textinput.Model
	leftWidth  int
	rightWidth int
	height     int
}

func NewMarkedTabModel() MarkedTabModel {
	ti := textinput.New()
	ti.Placeholder = "Commit message..."
	ti.CharLimit = 256
	return MarkedTabModel{input: ti}
}

// Refresh rebuilds the tab's list from the marked set.
// resolveStatus returns the effective CVS status for a path.
func (m *MarkedTabModel) Refresh(marked map[string]bool, resolveStatus func(string) string) {
	var files []markedFile
	for path := range marked {
		files = append(files, markedFile{path: path, status: resolveStatus(path)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	m.files = files
	if m.cursor >= len(files) {
		m.cursor = max(0, len(files)-1)
	}
}

// SetSize takes the App's layout envelope. The Marked tab fills both panels —
// file list on the left, commit form on the right — so it reads LeftW,
// RightW, and Height.
func (m *MarkedTabModel) SetSize(d PanelDims) {
	m.leftWidth = d.LeftW
	m.rightWidth = d.RightW
	m.height = d.Height
	m.input.Width = max(10, d.RightW-4)
}

// SelectedFile returns the marked row under the cursor, or nil when
// the list is empty. The keymap context needs the status alongside the
// path, and reading it here keeps that lookup off the status map.
func (m MarkedTabModel) SelectedFile() *markedFile {
	if m.cursor >= len(m.files) {
		return nil
	}
	return &m.files[m.cursor]
}

func (m MarkedTabModel) SelectedPath() string {
	if m.cursor >= len(m.files) {
		return ""
	}
	return m.files[m.cursor].path
}

// CountByStatus returns how many marked files have each status.
func (m MarkedTabModel) CountByStatus() map[string]int {
	counts := make(map[string]int)
	for _, f := range m.files {
		if f.status == "" {
			counts["clean"]++
		} else {
			counts[f.status]++
		}
	}
	return counts
}

// CommittablePaths returns the marked paths isCommittable accepts —
// THE single definition of "what can go into a commit" (?, A, M, C,
// R). Every commit gate uses this instead of restating the status
// list; a restated list already drifted once (a c-gate without R made
// deletion-only sets read as "nothing committable").
func (m MarkedTabModel) CommittablePaths() []string {
	var paths []string
	for _, f := range m.files {
		if isCommittable(f.status) {
			paths = append(paths, f.path)
		}
	}
	return paths
}

// PathsByStatus returns marked file paths matching the given statuses.
func (m MarkedTabModel) PathsByStatus(statuses ...string) []string {
	set := make(map[string]bool)
	for _, s := range statuses {
		set[s] = true
	}
	var paths []string
	for _, f := range m.files {
		if set[f.status] {
			paths = append(paths, f.path)
		}
	}
	return paths
}

func (m *MarkedTabModel) ensureVisible() {
	// One row reserved for the header line.
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height-1)
}

func (m MarkedTabModel) ViewLeft() string {
	if m.mode == MarkedCommit {
		return m.viewCommitFiles()
	}

	header := titleStyle.Render(fmt.Sprintf(" marked (%d)", len(m.files)))
	var lines []string
	lines = append(lines, header)

	if len(m.files) == 0 {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("  Nothing marked"))
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("  Press space on a file in the"))
		lines = append(lines, mutedStyle.Render("  Files tab to mark it. Marked"))
		lines = append(lines, mutedStyle.Render("  files are what c, r, i and u"))
		lines = append(lines, mutedStyle.Render("  act on."))
		for len(lines) < m.height {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	end := min(m.offset+m.height-1, len(m.files))
	for i := m.offset; i < end; i++ {
		f := m.files[i]
		status := "  "
		if f.status != "" {
			status = lipgloss.NewStyle().
				Width(2).
				Foreground(statusColor(f.status)).
				Render(f.status)
		}
		line := fmt.Sprintf("  %s  %s", status, f.path)
		if i == m.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m MarkedTabModel) viewCommitFiles() string {
	counts := m.CountByStatus()
	// Universal commit candidates: ?, A, M, C, R. ? files get cvs-added first.
	committable := counts["?"] + counts["A"] + counts["M"] + counts["C"] + counts["R"]
	header := titleStyle.Render(fmt.Sprintf(" commit (%d of %d)", committable, len(m.files)))
	var lines []string
	lines = append(lines, header)

	// Show committable files first.
	for _, f := range m.files {
		if isCommittable(f.status) {
			status := lipgloss.NewStyle().
				Width(2).
				Foreground(statusColor(f.status)).
				Render(f.status)
			line := fmt.Sprintf("  %s  %s", status, f.path)
			if f.status == "?" {
				line += mutedStyle.Render("  (will be added first)")
			}
			lines = append(lines, line)
		}
	}

	// Show skipped files (U / clean — nothing to commit there).
	skipped := false
	for _, f := range m.files {
		if isCommittable(f.status) {
			continue
		}
		if !skipped {
			lines = append(lines, "")
			skipped = true
		}
		status := "  "
		if f.status != "" {
			status = lipgloss.NewStyle().
				Width(2).
				Foreground(statusColor(f.status)).
				Render(f.status)
		}
		hint := "unchanged"
		if f.status == "U" {
			hint = "needs update"
		}
		line := mutedStyle.Render(fmt.Sprintf("  %s  %s  (skip: %s)", status, f.path, hint))
		lines = append(lines, line)
	}

	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// isCommittable reports whether `cvs commit` will produce a meaningful change
// for a file in the given status. ? files are added first then committed; A
// becomes an initial revision; M/C produce a content commit; R commits the
// deletion. U and clean files have nothing to commit.
func isCommittable(status string) bool {
	switch status {
	case "?", "A", "M", "C", "R":
		return true
	}
	return false
}

// ViewRight is only consulted in MarkedCommit mode — actions mode is rendered
// single-panel in app_layout.go::renderMainContent.
func (m MarkedTabModel) ViewRight() string {
	if m.mode != MarkedCommit {
		return ""
	}
	return m.viewCommit()
}

func (m MarkedTabModel) viewCommit() string {
	mc := m.CountByStatus()
	n := mc["M"] + mc["C"]

	var b strings.Builder
	b.WriteString(titleStyle.Render(" commit ("+countLabel(n, "file")+")") + "\n\n")
	b.WriteString("  " + m.input.View() + "\n\n")

	if m.input.Focused() {
		b.WriteString(helpStyle.Render("  enter:commit  esc:cancel") + "\n")
	} else {
		b.WriteString(helpStyle.Render("  enter:edit message") + "\n")
	}

	return b.String()
}
