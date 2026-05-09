package tui

import (
	"lazycvs/cvs"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type FileViewMode int

const (
	FileViewFlat FileViewMode = iota // only files in this directory
	FileViewSub                      // files grouped by subdirectories
	FileViewTree                     // expandable subtree
)

// SubDirGroup holds a subdirectory and its files for sub/tree views.
type SubDirGroup struct {
	Name     string
	Path     string
	Files    []cvs.FileEntry
	Counts   StatusCounts
	Expanded bool
}

// fileRow is one navigable row in the file list (either a file or a dir header).
type fileRow struct {
	file        *cvs.FileEntry
	subDir      *SubDirGroup
	isDirHeader bool
}

type FileListModel struct {
	files       []cvs.FileEntry
	subDirs     []SubDirGroup
	cursor      int
	offset      int
	width       int
	height      int
	dir         string
	filter      string // "M", "C", "?", or "" for all
	marked      map[string]bool
	viewMode    FileViewMode
	hideIgnored bool
}

func NewFileListModel() FileListModel {
	return FileListModel{
		marked:      make(map[string]bool),
		hideIgnored: true,
	}
}

func (m *FileListModel) SetFiles(dir string, files []cvs.FileEntry, subDirs []SubDirGroup) {
	m.dir = dir
	m.files = files
	m.subDirs = subDirs
	m.cursor = 0
	m.offset = 0
}

// SetSize takes the App's layout envelope. FileList fills the right panel
// only (in the Tree and Favorites tabs), so it reads RightW and Height;
// LeftW is ignored.
func (m *FileListModel) SetSize(d PanelDims) {
	m.width = d.RightW
	m.height = d.Height
}

// rows builds the flat navigable list based on current mode and filter.
func (m FileListModel) rows() []fileRow {
	// Main directory header — Space on this toggles all visible files
	mainDir := &SubDirGroup{Name: m.dir, Path: m.dir}
	var rows []fileRow
	rows = append(rows, fileRow{subDir: mainDir, isDirHeader: true})

	switch m.viewMode {
	case FileViewFlat:
		for i := range m.files {
			if !m.showFile(&m.files[i]) {
				continue
			}
			rows = append(rows, fileRow{file: &m.files[i]})
		}

	case FileViewSub, FileViewTree:
		// Top-level files first
		for i := range m.files {
			if !m.showFile(&m.files[i]) {
				continue
			}
			rows = append(rows, fileRow{file: &m.files[i]})
		}
		// Subdirectories
		for i := range m.subDirs {
			sd := &m.subDirs[i]
			if m.filter != "" && !m.subDirHasMatch(sd) {
				continue
			}
			rows = append(rows, fileRow{subDir: sd, isDirHeader: true})
			expanded := m.viewMode == FileViewSub || sd.Expanded
			if expanded {
				for j := range sd.Files {
					if !m.showFile(&sd.Files[j]) {
						continue
					}
					rows = append(rows, fileRow{file: &sd.Files[j], subDir: sd})
				}
			}
		}
	}
	return rows
}

func (m FileListModel) showFile(f *cvs.FileEntry) bool {
	if m.hideIgnored && f.Ignored {
		return false
	}
	return m.matchesFilter(f.Status)
}

func (m FileListModel) matchesFilter(status string) bool {
	if m.filter == "" {
		return true
	}
	if m.filter == "*" {
		return status != ""
	}
	return status == m.filter
}

func (m FileListModel) subDirHasMatch(sd *SubDirGroup) bool {
	for _, f := range sd.Files {
		if m.matchesFilter(f.Status) {
			return true
		}
	}
	return false
}

func (m FileListModel) SelectedFile() *cvs.FileEntry {
	rows := m.rows()
	if m.cursor >= len(rows) {
		return nil
	}
	return rows[m.cursor].file // nil for dir headers
}

func (m FileListModel) MarkedFiles() []string {
	var result []string
	for path := range m.marked {
		result = append(result, path)
	}
	return result
}

// ViewModeName returns a short label for the current view mode, used as
// a parenthetical badge in the file-list header so the user can tell at
// a glance which layout `f` cycled to.
func (m FileListModel) ViewModeName() string {
	switch m.viewMode {
	case FileViewSub:
		return "sub"
	case FileViewTree:
		return "tree"
	default:
		return "flat"
	}
}

func (m FileListModel) Update(msg tea.Msg) (FileListModel, tea.Cmd) {
	rows := m.rows()
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Down):
			if m.cursor < len(rows)-1 {
				m.cursor++
				m.ensureVisible()
			}
		case key.Matches(msg, keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.ensureVisible()
			}
		case key.Matches(msg, keys.Top):
			m.cursor = 0
			m.offset = 0
		case key.Matches(msg, keys.Bottom):
			m.cursor = max(0, len(rows)-1)
			m.ensureVisible()
		case key.Matches(msg, keys.PageDown):
			m.cursor = clamp(m.cursor+m.height, 0, max(0, len(rows)-1))
			m.ensureVisible()
		case key.Matches(msg, keys.PageUp):
			m.cursor = clamp(m.cursor-m.height, 0, max(0, len(rows)-1))
			m.ensureVisible()
		case key.Matches(msg, keys.Space):
			if f := m.SelectedFile(); f != nil {
				// Toggle individual file
				if m.marked[f.Path] {
					delete(m.marked, f.Path)
				} else {
					m.marked[f.Path] = true
				}
			} else if m.cursor < len(rows) && rows[m.cursor].isDirHeader {
				sd := rows[m.cursor].subDir
				if sd.Path == m.dir {
					// Main directory header — toggle ALL visible files
					var all []string
					for _, r := range rows {
						if r.file != nil {
							all = append(all, r.file.Path)
						}
					}
					allMarked := true
					for _, p := range all {
						if !m.marked[p] {
							allMarked = false
							break
						}
					}
					for _, p := range all {
						if allMarked {
							delete(m.marked, p)
						} else {
							m.marked[p] = true
						}
					}
				} else {
					// Subdirectory header — toggle files in this subdir
					allMarked := true
					for _, f := range sd.Files {
						if f.Status != "" && !m.marked[f.Path] {
							allMarked = false
							break
						}
					}
					for _, f := range sd.Files {
						if f.Status == "" {
							continue
						}
						if allMarked {
							delete(m.marked, f.Path)
						} else {
							m.marked[f.Path] = true
						}
					}
				}
			}
		case key.Matches(msg, keys.Enter):
			// In tree mode, toggle expand on dir headers
			if m.viewMode == FileViewTree && m.cursor < len(rows) && rows[m.cursor].isDirHeader {
				rows[m.cursor].subDir.Expanded = !rows[m.cursor].subDir.Expanded
			}
		case key.Matches(msg, keys.Filter):
			switch m.filter {
			case "":
				m.filter = "*" // any change
			case "*":
				m.filter = "M"
			case "M":
				m.filter = "C"
			case "C":
				m.filter = "?"
			default:
				m.filter = ""
			}
			m.cursor = 0
			m.offset = 0
		// View mode switching: f cycles flat→sub→tree, t jumps to tree
		case msg.String() == "f":
			switch m.viewMode {
			case FileViewFlat:
				m.viewMode = FileViewSub
			case FileViewSub:
				m.viewMode = FileViewTree
			default:
				m.viewMode = FileViewFlat
			}
			m.cursor = 0
			m.offset = 0
		case msg.String() == "t":
			m.viewMode = FileViewTree
			m.cursor = 0
			m.offset = 0
		case msg.String() == "I":
			m.hideIgnored = !m.hideIgnored
			m.cursor = 0
			m.offset = 0
		}
	}
	return m, nil
}

func (m *FileListModel) ensureVisible() {
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height)
}

func (m FileListModel) View() string {
	rows := m.rows()

	if len(rows) <= 1 { // only main dir header, no files
		var lines []string
		lines = append(lines, mutedStyle.Render("  No files"))
		for len(lines) < m.height {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}

	var lines []string
	end := min(m.offset+m.height, len(rows))
	for i := m.offset; i < end; i++ {
		row := rows[i]
		var line string

		if row.isDirHeader {
			// Directory header row
			sd := row.subDir
			if sd.Path == m.dir {
				// Main directory header
				info := sd.Name + "/"
				if mn := m.ViewModeName(); mn != "" {
					info += fmt.Sprintf(" (%s)", mn)
				}
				if m.filter != "" {
					info += fmt.Sprintf(" [%s]", m.filter)
				}
				if m.hideIgnored {
					info += " -ign"
				}
				// Count visible files
				fileCount := 0
				for _, r := range rows {
					if r.file != nil {
						fileCount++
					}
				}
				info += fmt.Sprintf(" — %d files", fileCount)
				if len(m.marked) > 0 {
					info += fmt.Sprintf(", %d marked", len(m.marked))
				}
				line = " " + titleStyle.Render(info)
			} else {
				// Subdirectory header
				icon := " "
				if m.viewMode == FileViewTree {
					if sd.Expanded {
						icon = "v"
					} else {
						icon = ">"
					}
				}
				name := sd.Name + "/"
				counts := ""
				if !sd.Counts.IsZero() {
					counts = "  " + renderCounts(sd.Counts)
				}
				line = fmt.Sprintf(" %s %s%s", icon, name, counts)
			}
		} else {
			// File row
			f := row.file
			mark := " "
			if m.marked[f.Path] {
				mark = "*"
			}

			status := "  "
			if f.Ignored {
				status = lipgloss.NewStyle().
					Width(2).
					Foreground(colorIgnored).
					Render("I")
			} else if f.Status != "" {
				status = lipgloss.NewStyle().
					Width(2).
					Foreground(statusColor(f.Status)).
					Render(f.Status)
			}

			name := f.Path
			if m.dir != "" && m.dir != "." {
				name = strings.TrimPrefix(name, m.dir+"/")
			}
			// Indent files inside a subdirectory
			if row.subDir != nil {
				name = strings.TrimPrefix(name, row.subDir.Name+"/")
				name = "  " + name
			}

			// Right-hand column: size, or "(server)" for entries that
			// CVS reports a status for but which don't exist locally
			// yet (e.g. files newly added on the server). 8 chars
			// matches the formatSize column width.
			size := ""
			switch {
			case f.ServerOnly:
				size = "(server)"
			case f.Size > 0:
				size = formatSize(f.Size)
			}

			nameWidth := max(1, m.width-20)
			line = fmt.Sprintf(" %s %s  %-*s %8s",
				mark, status, nameWidth, truncate(name, nameWidth), size)
		}

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

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 4 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
