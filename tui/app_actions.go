package tui

import (
	"fmt"
	"lazycvs/cvs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// User-initiated CVS actions: commit / update / revert / add / ignore /
// remove. Each method either dispatches a tea.Cmd that runs the cvs
// binary in the background, or opens a dialog so the user can confirm /
// supply input. The actual cvs invocation happens inside the returned
// closure; the main goroutine is never blocked.

func (m *App) openCommitDialog() tea.Cmd {
	marked := m.filelist.MarkedFiles()
	if len(marked) > 0 {
		m.dialog.OpenCommit(marked, m.statusesFor(marked))
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

// toggleDirFiles toggles the marked state of every changed file under
// dirPath. If all are already marked, it unmarks them; otherwise it
// marks any that aren't yet marked.
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

// stagedBulkAction runs `cvs add`, `cvs update -C` (revert), or
// `cvs update` (refresh) on the given paths. Marked entries for non-add
// actions are unmarked synchronously; the cvs invocation happens in a
// background goroutine.
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
			var inTheWay []string
			if r != nil {
				inTheWay = cvs.ParseInTheWay(r.Stderr)
			}
			return updateDoneMsg{paths: paths, err: err, inTheWay: inTheWay}
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

// doUpdatePaths runs `cvs update -d` on the given paths. Returns
// updateDoneMsg with success/error and any in-the-way paths CVS
// reported; the handler in App.Update shows a banner, dispatches the
// follow-up status refresh, and (if needed) opens DialogInTheWay.
func (m *App) doUpdatePaths(paths []string) tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		args := append([]string{"update", "-d"}, paths...)
		r, err := exec.Run(args...)
		if err == nil && r != nil && !r.Success {
			err = fmt.Errorf("cvs update exited %d", r.ExitCode)
		}
		var inTheWay []string
		if r != nil {
			inTheWay = cvs.ParseInTheWay(r.Stderr)
		}
		return updateDoneMsg{paths: paths, err: err, inTheWay: inTheWay}
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
		var inTheWay []string
		if r != nil {
			inTheWay = cvs.ParseInTheWay(r.Stderr)
		}
		return updateDoneMsg{paths: []string{target}, err: err, inTheWay: inTheWay}
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

// handleIgnore writes a pattern into the appropriate .cvsignore file
// based on the user's choice from the ignore dialog: 1 = local, 2 =
// global pattern by extension, 3 = global exact match.
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

// handleInTheWayResolve performs the user's chosen resolution for paths
// CVS reported as "move away" / "in the way" — local files that
// blocked the server's version from being pulled by `cvs update`.
//
//	inTheWayMoveAside — rename each local file to "<name>.moved-by-lazycvs"
//	                    so the server version can come down on the next
//	                    update; the local content is preserved as a sibling
//	inTheWayDelete    — os.Remove each local file outright
//	inTheWayKeep      — no-op; user wants to keep their local files and
//	                    skip the server-side additions
//
// After move-aside or delete, dispatches `cvs update -d` on the freed
// paths so the previously blocked files actually arrive.
func (m *App) handleInTheWayResolve(msg inTheWayResolveMsg) tea.Cmd {
	if len(msg.paths) == 0 || msg.choice == inTheWayKeep {
		return nil
	}
	for _, p := range msg.paths {
		abs := filepath.Join(m.exec.WorkDir, p)
		switch msg.choice {
		case inTheWayMoveAside:
			os.Rename(abs, abs+".moved-by-lazycvs")
		case inTheWayDelete:
			os.Remove(abs)
		}
	}
	// Re-run cvs update on the now-unblocked paths so the server
	// versions actually land. doUpdatePaths reports back via
	// updateDoneMsg — if a new "in the way" appears (rare), the
	// dialog re-opens.
	return m.doUpdatePaths(msg.paths)
}
