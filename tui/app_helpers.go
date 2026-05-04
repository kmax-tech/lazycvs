package tui

import (
	"fmt"
	"lazycvs/cvs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CVS-ignore handling and the file-list / staging helpers used by the
// Tree, Favorites, and Staged tabs. History-tab logic lives in
// app_history.go; status-loading in app_status.go; mouse routing in
// app_mouse.go; external diff/merge in external.go.

// cvsDefaultIgnore is the built-in CVS ignore list (see cvs(5) "Ignoring files
// via cvsignore"). These patterns are always active unless cleared with "!".
var cvsDefaultIgnore = strings.Fields(`
	RCS SCCS CVS CVS.adm
	RCSLOG cvslog.*
	tags TAGS
	.make.state .nse_depinfo
	*~ #* .#* ,* _$* *$
	*.old *.bak *.BAK *.orig *.rej .del-*
	*.a *.olb *.o *.obj *.so *.exe
	*.Z *.elc *.ln
	core
`)

// globalIgnorePatterns returns the ignore list built from:
//  1. CVS built-in defaults
//  2. ~/.cvsignore
//  3. $CVSIGNORE environment variable
//
// A lone "!" entry resets the list.
func globalIgnorePatterns() []string {
	patterns := append([]string{}, cvsDefaultIgnore...)
	if home, err := os.UserHomeDir(); err == nil {
		patterns = appendIgnoreFile(patterns, filepath.Join(home, ".cvsignore"))
	}
	if env := os.Getenv("CVSIGNORE"); env != "" {
		patterns = appendPatterns(patterns, strings.Fields(env))
	}
	return patterns
}

// loadIgnorePatterns collects all CVS ignore patterns that apply to a directory:
// built-in defaults + ~/.cvsignore + $CVSIGNORE + per-directory .cvsignore.
func loadIgnorePatterns(absDir string) []string {
	patterns := globalIgnorePatterns()
	patterns = appendIgnoreFile(patterns, filepath.Join(absDir, ".cvsignore"))
	return patterns
}

// appendIgnoreFile reads a cvsignore file and appends its patterns. CVS ignore
// files contain space-separated patterns (no comment syntax). "!" resets the
// list.
func appendIgnoreFile(patterns []string, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return patterns
	}
	return appendPatterns(patterns, strings.Fields(string(data)))
}

// appendPatterns adds entries to the pattern list, handling "!" as a reset.
func appendPatterns(patterns, entries []string) []string {
	for _, e := range entries {
		if e == "!" {
			patterns = patterns[:0]
		} else {
			patterns = append(patterns, e)
		}
	}
	return patterns
}

func matchesIgnore(name string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
	}
	return false
}

func skipInListing(name string) bool {
	return name == "CVS" || name == ".cvsignore" || name == ".DS_Store" ||
		strings.HasPrefix(name, ".#") || strings.HasSuffix(name, ".~")
}

func (m *App) updateFileList() {
	var dir string
	switch m.activeTab {
	case TabTree:
		node := m.tree.SelectedNode()
		if node == nil {
			// No node selected (e.g. root has only files, no dirs) —
			// fall back to showing root directory.
			m.updateFileListForDir(".")
			return
		}
		if node.IsDir {
			dir = node.Path
		} else {
			dir = filepath.Dir(node.Path)
			if dir == "." {
				dir = "."
			}
		}
	case TabFavorites:
		dir = m.favorites.SelectedPath()
	default:
		return
	}

	if dir == "" {
		return
	}

	m.updateFileListForDir(dir)
}

func (m *App) updateFileListForDir(dir string) {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}

	absDir := filepath.Join(m.exec.WorkDir, dir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return
	}

	var files []cvs.FileEntry
	var subDirs []SubDirGroup
	ignorePatterns := loadIgnorePatterns(absDir)

	for _, e := range entries {
		name := e.Name()
		if skipInListing(name) {
			continue
		}

		path := prefix + name
		if dir == "." {
			path = name
		}

		if e.IsDir() {
			sg := SubDirGroup{
				Name: name,
				Path: path,
			}
			if node := m.tree.findNode(path); node != nil {
				sg.Counts = node.Counts
			}
			subAbsDir := filepath.Join(absDir, name)
			subEntries, err := os.ReadDir(subAbsDir)
			subIgnore := loadIgnorePatterns(subAbsDir)
			if err == nil {
				for _, se := range subEntries {
					sn := se.Name()
					if skipInListing(sn) || se.IsDir() {
						continue
					}
					sp := path + "/" + sn
					var sz int64
					if info, err := se.Info(); err == nil {
						sz = info.Size()
					}
					st := m.resolveFileStatus(sp)
					ignored := st == "" && matchesIgnore(sn, subIgnore)
					sg.Files = append(sg.Files, cvs.FileEntry{Path: sp, Status: st, Size: sz, Ignored: ignored})
				}
			}
			subDirs = append(subDirs, sg)
			continue
		}

		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		status := m.resolveFileStatus(path)
		ignored := status == "" && matchesIgnore(name, ignorePatterns)
		files = append(files, cvs.FileEntry{Path: path, Status: status, Size: size, Ignored: ignored})
	}

	m.filelist.SetFiles(dir, files, subDirs)
}

func (m *App) openCommitDialog() tea.Cmd {
	marked := m.filelist.MarkedFiles()
	if len(marked) > 0 {
		statuses := make(map[string]string, len(marked))
		for _, p := range marked {
			statuses[p] = m.resolveFileStatus(p)
		}
		m.dialog.OpenCommit(marked, statuses)
		return nil
	}
	// If nothing marked, commit the selected modified file
	if f := m.filelist.SelectedFile(); f != nil {
		s := m.resolveFileStatus(f.Path)
		if isCommittable(s) {
			m.dialog.OpenCommit([]string{f.Path}, map[string]string{f.Path: s})
			return nil
		}
	}
	return nil
}

func (m *App) toggleDirFiles(dirPath string) {
	prefix := dirPath + "/"
	if dirPath == "." {
		prefix = ""
	}
	// Collect all changed files under this directory
	var changed []string
	for path, status := range m.statusMap {
		if strings.HasPrefix(path, prefix) && status != "" {
			changed = append(changed, path)
		}
	}
	if len(changed) == 0 {
		return
	}
	// Check if all are already marked
	allMarked := true
	for _, p := range changed {
		if !m.filelist.marked[p] {
			allMarked = false
			break
		}
	}
	for _, p := range changed {
		if allMarked {
			delete(m.filelist.marked, p)
		} else {
			m.filelist.marked[p] = true
		}
	}
}

func (m *App) stagedBulkAction(action string, paths []string) tea.Cmd {
	if action != "add" {
		for _, p := range paths {
			delete(m.filelist.marked, p)
		}
	}
	m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
	exec := m.exec
	switch action {
	case "add":
		return func() tea.Msg {
			for _, p := range paths {
				ensureParentDirs(exec, p)
				exec.Run("add", p)
			}
			return actionDoneMsg{paths: paths}
		}
	case "revert":
		return func() tea.Msg {
			for _, p := range paths {
				exec.Run("update", "-C", p)
			}
			return actionDoneMsg{paths: paths}
		}
	case "update":
		return func() tea.Msg {
			args := append([]string{"update"}, paths...)
			r, err := exec.Run(args...)
			if err == nil && r != nil && !r.Success {
				err = fmt.Errorf("cvs update exited %d", r.ExitCode)
			}
			return updateDoneMsg{paths: paths, err: err}
		}
	}
	return nil
}

func (m *App) stagedBulkIgnore(paths []string) tea.Cmd {
	for _, p := range paths {
		delete(m.filelist.marked, p)
		dir := filepath.Dir(p)
		ignPath := filepath.Join(m.exec.WorkDir, dir, ".cvsignore")
		f, err := os.OpenFile(ignPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(base(p) + "\n")
			f.Close()
		}
	}
	m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
	return m.refreshStatusForPaths(paths)
}

// selectedFilePath returns the path of the file under cursor in the active
// tab, or "" if no file is selected (cursor on a dir, no selection, etc.).
// Mirrors the resolution logic in delegateKey so keybar hints stay in sync.
func (m App) selectedFilePath() string {
	switch m.activeTab {
	case TabStaged:
		return m.staged.SelectedPath()
	case TabHistory:
		return m.history.Path()
	case TabTree:
		if m.focus == PanelRight && m.treeMode != TreeViewDetails {
			if f := m.filelist.SelectedFile(); f != nil {
				return f.Path
			}
			return ""
		}
		if node := m.tree.SelectedNode(); node != nil && !node.IsDir {
			return node.Path
		}
		return ""
	case TabFavorites:
		if f := m.filelist.SelectedFile(); f != nil {
			return f.Path
		}
	}
	return ""
}

// selectedTarget returns the path of the currently focused file or directory.
func (m *App) selectedTarget() string {
	if m.focus == PanelRight {
		if f := m.filelist.SelectedFile(); f != nil {
			return f.Path
		}
	}
	if node := m.tree.SelectedNode(); node != nil {
		return node.Path
	}
	return ""
}

// doUpdatePaths runs `cvs update -d` on the given paths. Returns
// updateDoneMsg with success/error; the handler in App.Update shows a
// banner and dispatches the follow-up status refresh.
func (m *App) doUpdatePaths(paths []string) tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		args := append([]string{"update", "-d"}, paths...)
		r, err := exec.Run(args...)
		if err == nil && r != nil && !r.Success {
			err = fmt.Errorf("cvs update exited %d", r.ExitCode)
		}
		return updateDoneMsg{paths: paths, err: err}
	}
}

// doUpdateSelected runs cvs update on the tree's selected directory or
// file. With no node selected, falls back to a silent status refresh
// (no banner).
func (m *App) doUpdateSelected() tea.Cmd {
	node := m.tree.SelectedNode()
	if node == nil {
		m.statusEpoch++
		return backgroundDirScan(m.exec, m.statusEpoch, "")
	}
	target := node.Path
	exec := m.exec
	return func() tea.Msg {
		r, err := exec.Run("update", "-d", target)
		if err == nil && r != nil && !r.Success {
			err = fmt.Errorf("cvs update exited %d", r.ExitCode)
		}
		return updateDoneMsg{paths: []string{target}, err: err}
	}
}

func (m *App) addFile(path string) tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		ensureParentDirs(exec, path)
		exec.Run("add", path)
		return actionDoneMsg{paths: []string{path}}
	}
}

// blockedByConflict filters paths to those whose status is "C" AND whose
// on-disk content still contains the three-way merge markers. CVS would
// reject a commit on these (`had a conflict and has not been modified`),
// so we surface the problem at the UI layer instead of failing into the
// console panel.
//
// statusGetter looks up status by path (typically m.statusMap or the
// commit-dialog's commitStatuses). Paths whose status isn't C are skipped
// without touching the disk — no read I/O for non-conflict files.
func (m *App) blockedByConflict(paths []string, statusGetter func(string) string) []string {
	var blocked []string
	for _, p := range paths {
		if statusGetter(p) != "C" {
			continue
		}
		if cvs.HasConflictMarkers(filepath.Join(m.exec.WorkDir, p)) {
			blocked = append(blocked, p)
		}
	}
	return blocked
}

func (m *App) handleIgnore(msg ignoreMsg) tea.Cmd {
	switch msg.choice {
	case 1:
		// Local .cvsignore
		dir := filepath.Dir(msg.path)
		ignPath := filepath.Join(m.exec.WorkDir, dir, ".cvsignore")
		f, err := os.OpenFile(ignPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(base(msg.path) + "\n")
			f.Close()
		}
	case 2:
		// Global pattern → ~/.cvsignore
		e := ext(msg.path)
		if e != "" {
			appendToGlobalCvsignore("*" + e)
		}
	case 3:
		// Global exact → ~/.cvsignore
		appendToGlobalCvsignore(base(msg.path))
	}
	return m.refreshStatusForPaths([]string{msg.path})
}

func appendToGlobalCvsignore(pattern string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".cvsignore")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	// CVS ignore files are space-separated, but one pattern per line is safe
	f.WriteString(pattern + "\n")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func openInOS(path string) tea.Cmd {
	return func() tea.Msg {
		// macOS: open, Linux: xdg-open
		bin := "open"
		if _, err := os.Stat("/usr/bin/xdg-open"); err == nil {
			bin = "xdg-open"
		}
		cmd := exec.Command(bin, path)
		cmd.Start()
		return nil
	}
}
