package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/cvs"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type FileViewMode int

const (
	// Two views, because there are two questions: "what changed here"
	// (flat) and "let me walk the directories" (tree). The old third
	// mode, sub, was tree with every subdirectory unfolded — a state of
	// the tree wearing the costume of a mode. It lives on as
	// expandAll.
	FileViewFlat FileViewMode = iota // this dir's files + changed files below (dir-relative paths)
	FileViewTree                     // subdirectories, foldable
)

// SubDirGroup holds a subdirectory and its files for sub/tree views.
type SubDirGroup struct {
	Name     string
	Path     string
	Files    []cvs.FileEntry
	Counts   StatusCounts
	Expanded bool
	Ignored  bool // matches a .cvsignore pattern in the parent
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
	// expandAll is the fold state new directories inherit. Without it,
	// every directory change would refold what the user just unfolded —
	// which is the one thing the old sub mode did well.
	expandAll bool
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
	for i := range m.subDirs {
		m.subDirs[i].Expanded = m.expandAll
	}
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

// FileCount is how many file rows the listing shows — the number the
// panel header and the panel footer both report. They used to count
// separately: the header counted files, the footer counted navigable
// rows (one more, because of the directory header), so a fourteen-file
// listing said "14 files" at the top and "10 of 15" at the bottom.
func (m FileListModel) FileCount() int {
	n := 0
	for _, r := range m.rows() {
		if r.file != nil {
			n++
		}
	}
	return n
}

// CursorFileIndex is the cursor's 1-based position among the file rows,
// and false when the cursor sits on a directory header (which is not a
// file and has no position among them).
func (m FileListModel) CursorFileIndex() (int, bool) {
	n := 0
	for i, r := range m.rows() {
		if r.file == nil {
			continue
		}
		n++
		if i == m.cursor {
			return n, true
		}
	}
	return 0, false
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
		// Changed files below subdirs show up too, as dir-relative
		// paths (no subDir ref → the renderer keeps the full relative
		// path). The dir badges count the whole subtree, so flat must
		// offer a row for every counted change or the user sees "1M"
		// with nothing to select. Clean subdir files stay hidden —
		// browsing the tree structure is what sub/tree modes are for.
		for i := range m.subDirs {
			sd := &m.subDirs[i]
			if m.hideIgnored && sd.Ignored {
				continue
			}
			for j := range sd.Files {
				f := &sd.Files[j]
				if f.Status == "" || !m.showFile(f) {
					continue
				}
				rows = append(rows, fileRow{file: f})
			}
		}

	case FileViewTree:
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
			if m.hideIgnored && sd.Ignored {
				continue
			}
			if m.filter != "" && !m.subDirHasMatch(sd) {
				continue
			}
			rows = append(rows, fileRow{subDir: sd, isDirHeader: true})
			if sd.Expanded {
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

// FocusOnFile positions the cursor on the row for the given path (a
// path relative to the working copy). Returns true on success; false
// if the file isn't in the current view (e.g. wrong mode, filtered
// out, or not in the directory currently shown). On success the
// caller should also expand-and-scroll: ensureVisible is called here.
func (m *FileListModel) FocusOnFile(path string) bool {
	rows := m.rows()
	for i, r := range rows {
		if r.file != nil && r.file.Path == path {
			m.cursor = i
			m.ensureVisible()
			return true
		}
	}
	return false
}

func (m FileListModel) MarkedFiles() []string {
	var result []string
	for path := range m.marked {
		result = append(result, path)
	}
	return result
}

// ToggleViewMode switches between the two views. It used to cycle
// through three, so reaching the one you wanted took up to two presses
// and a separate `t` key existed to shortcut the cycle.
func (m *FileListModel) ToggleViewMode() {
	if m.viewMode == FileViewFlat {
		m.viewMode = FileViewTree
	} else {
		m.viewMode = FileViewFlat
	}
	m.cursor = 0
	m.offset = 0
}

// ToggleExpandAll folds or unfolds every subdirectory at once — the
// old sub mode as a gesture. The choice sticks for the session, so the
// next directory opens the way this one does.
func (m *FileListModel) ToggleExpandAll() {
	m.expandAll = !m.expandAll
	for i := range m.subDirs {
		m.subDirs[i].Expanded = m.expandAll
	}
	m.cursor = 0
	m.offset = 0
}

// ViewModeName returns a short label for the current view mode, used as
// a parenthetical badge in the file-list header so the user can tell at
// a glance which layout `f` cycled to.
// ViewModeName is the header chip: the view, plus its fold state when
// that is not the default.
func (m FileListModel) ViewModeName() string {
	if m.viewMode != FileViewTree {
		return "flat"
	}
	if m.expandAll {
		return "tree · all open"
	}
	return "tree"
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
					// Main directory header — toggle every visible file
					// that has a non-clean status. Clean files are skipped
					// (cvs commit would ignore them anyway, and including
					// them confused users who pressed Space-on-header to
					// "select all" and ended up with a wall of marks on
					// untouched files). Mirrors the subdir-header branch
					// below and the global `A`/toggleDirFiles semantic.
					var all []string
					for _, r := range rows {
						if r.file != nil && r.file.Status != "" {
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
		}
		// View-mode keys (f cycle, t tree-jump, I hide-ignored) are
		// handled at the App level so they work from either panel and
		// stay in sync with the tree pane — do NOT re-add them here or
		// the copies shadow-drift (see issues/021).
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
				// State as named chips. The old header read
				// "./ (flat) -ign — 14 files": "-ign" is not a word,
				// and nothing said which way it pointed.
				chips := []string{m.ViewModeName()}
				if m.hideIgnored {
					chips = append(chips, "ignored:hidden")
				} else {
					chips = append(chips, "ignored:shown")
				}
				if m.filter != "" {
					chips = append(chips, "filter:"+m.filter)
				}
				info := sd.Name + "/ · " + strings.Join(chips, " · ") +
					fmt.Sprintf(" — %d files", m.FileCount())
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
				if sd.Ignored {
					line = ignoredStyle.Render(line)
				}
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
			if f.IsDir {
				// Untracked directory surfaced from the status scan
				// (cvs reports whole unknown dirs as one "?" line).
				name += "/"
			}

			// Right-hand column: size, or "(server)" for entries that
			// CVS reports a status for but which don't exist locally
			// yet (e.g. files newly added on the server). 8 chars
			// matches the formatSize column width.
			size := ""
			switch {
			case f.IsDir:
				// no size for dirs
			case f.ServerOnly:
				size = "(server)"
			case f.Size > 0:
				size = formatSize(f.Size)
			}

			// Encoding warning flag (display-only): one of the two gap
			// spaces between status and name becomes a yellow "!" for
			// files with encoding issues, so the total row width and
			// nameWidth arithmetic stay unchanged. ASCII "!" (matching
			// the "! stale" tree badge) rather than "⚠", whose ambiguous
			// glyph width could misalign the fixed column.
			encFlag := " "
			if f.EncodingIssue != cvs.EncodingOK {
				encFlag = lipgloss.NewStyle().Foreground(colorStale).Render("!")
			}

			// Cells, not bytes: %-*s pads by byte count, so a name
			// with an umlaut pushed the size column one cell left per
			// non-ASCII byte. ansi.Truncate and lipgloss.Width both
			// count display cells.
			nameWidth := max(1, m.width-20)
			line = fmt.Sprintf(" %s %s%s %s %8s",
				mark, status, encFlag, padCells(name, nameWidth), size)
		}

		if i == m.cursor {
			// One reversed bar across the pane, built from plain text:
			// reversing a coloured status badge paints it as a block,
			// and a short bar ends ragged mid-row. See tree.go.
			line = cursorBar(line, m.width)
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
