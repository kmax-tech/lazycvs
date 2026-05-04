package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type StagedMode int

const (
	StagedActions StagedMode = iota // show action menu
	StagedCommit                    // show commit message input
)

type stagedFile struct {
	path   string
	status string
}

type StagedModel struct {
	files      []stagedFile
	cursor     int
	offset     int
	mode       StagedMode
	input      textinput.Model
	leftWidth  int
	rightWidth int
	height     int
}

func NewStagedModel() StagedModel {
	ti := textinput.New()
	ti.Placeholder = "Commit message..."
	ti.CharLimit = 256
	return StagedModel{input: ti}
}

// Refresh rebuilds the staged file list from the marked set.
// resolveStatus returns the effective CVS status for a path.
func (m *StagedModel) Refresh(marked map[string]bool, resolveStatus func(string) string) {
	var files []stagedFile
	for path := range marked {
		files = append(files, stagedFile{path: path, status: resolveStatus(path)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	m.files = files
	if m.cursor >= len(files) {
		m.cursor = max(0, len(files)-1)
	}
}

func (m *StagedModel) SetSize(leftWidth, rightWidth, height int) {
	m.leftWidth = leftWidth
	m.rightWidth = rightWidth
	m.height = height
	m.input.Width = max(10, rightWidth-4)
}

func (m StagedModel) SelectedPath() string {
	if m.cursor >= len(m.files) {
		return ""
	}
	return m.files[m.cursor].path
}

// CountByStatus returns how many staged files have each status.
func (m StagedModel) CountByStatus() map[string]int {
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

// PathsByStatus returns staged file paths matching the given statuses.
func (m StagedModel) PathsByStatus(statuses ...string) []string {
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

func (m *StagedModel) ensureVisible() {
	// One row reserved for the header line.
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height-1)
}

func (m StagedModel) ViewLeft() string {
	if m.mode == StagedCommit {
		return m.viewCommitFiles()
	}

	header := titleStyle.Render(fmt.Sprintf(" staged (%d)", len(m.files)))
	var lines []string
	lines = append(lines, header)

	if len(m.files) == 0 {
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("  No files staged"))
		lines = append(lines, "")
		lines = append(lines, mutedStyle.Render("  Use space in the file list"))
		lines = append(lines, mutedStyle.Render("  to mark files for commit"))
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

func (m StagedModel) viewCommitFiles() string {
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

// ViewRight is only consulted in StagedCommit mode — actions mode is rendered
// single-panel in app_layout.go::renderMainContent.
func (m StagedModel) ViewRight() string {
	if m.mode != StagedCommit {
		return ""
	}
	return m.viewCommit()
}

func (m StagedModel) viewCommit() string {
	mc := m.CountByStatus()
	n := mc["M"] + mc["C"]

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf(" commit (%d files)", n)) + "\n\n")
	b.WriteString("  " + m.input.View() + "\n\n")

	if m.input.Focused() {
		b.WriteString(helpStyle.Render("  enter:commit  esc:cancel") + "\n")
	} else {
		b.WriteString(helpStyle.Render("  enter:edit message") + "\n")
	}

	return b.String()
}
