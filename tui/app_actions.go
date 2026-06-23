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
	// Collect candidates from two sources:
	//   1. statusMap entries under the prefix — covers M/C/?/A/R/U files
	//      that cvs status reported.
	//   2. Files already marked under the prefix — covers entries that
	//      live outside statusMap (e.g. ? files in a freshly-added dir
	//      whose status comes from the Entries-lookup promotion in the
	//      listing). Without this branch, marks made via Space on the
	//      filelist's dir header couldn't be cleared with A — A would
	//      find zero candidates and return.
	seen := make(map[string]bool)
	var candidates []string
	for path, status := range m.statusMap {
		if strings.HasPrefix(path, prefix) && status != "" && !seen[path] {
			seen[path] = true
			candidates = append(candidates, path)
		}
	}
	for path := range m.filelist.marked {
		if strings.HasPrefix(path, prefix) && !seen[path] {
			seen[path] = true
			candidates = append(candidates, path)
		}
	}
	if len(candidates) == 0 {
		return
	}
	// Check if all are already marked
	allMarked := true
	for _, p := range candidates {
		if !m.filelist.marked[p] {
			allMarked = false
			break
		}
	}
	for _, p := range candidates {
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
			var firstErr error
			for _, p := range paths {
				if err := ensureParentDirs(exec, p); err != nil && firstErr == nil {
					firstErr = err
				}
				if r, err := exec.Run("add", p); err != nil {
					if firstErr == nil {
						firstErr = err
					}
				} else if r != nil && !r.Success && firstErr == nil {
					firstErr = fmt.Errorf("cvs add %s exited %d", p, r.ExitCode)
				}
			}
			return actionDoneMsg{paths: paths, err: firstErr}
		}
	case "revert":
		// Capture per-path statuses BEFORE the goroutine: cvs needs a
		// different sequence for C ("Unresolved Conflict") than for M.
		// `cvs update -C` reliably restores an M file from the server,
		// but for C the Entries "Result of merge" marker survives the
		// rewrite and the file keeps reporting as C. `rm <path> && cvs
		// update <path>` deletes the working copy and re-fetches fresh
		// — that path clears the marker for both M and C, at the cost
		// of losing CVS's own `.#file.rev` backup. We already write a
		// `<file>.lazycvs-backup` next to every reverted file (see
		// stagedBulkPrepareBackups below), so no actual recovery info
		// is lost.
		statuses := make(map[string]string, len(paths))
		for _, p := range paths {
			statuses[p] = m.statusMap[p]
		}
		workDir := exec.WorkDir
		stagedBulkPrepareBackups(workDir, paths)
		return func() tea.Msg {
			var firstErr error
			for _, p := range paths {
				err := revertOne(exec, workDir, p, statuses[p])
				if err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return actionDoneMsg{paths: paths, err: firstErr}
		}
	case "update":
		return func() tea.Msg {
			// -d adds dirs the server has and we don't, -P prunes dirs
			// that become empty after the update. Same combo the dry-run
			// uses; keeps the working copy clean without a follow-up
			// stale-dir prompt.
			args := append([]string{"update", "-d", "-P"}, paths...)
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

// restoreFromBackup swaps a file with its <file>.lazycvs-backup sibling
// — used after the user thinks a revert was a mistake and wants the
// pre-revert content back. Accepts either side of the pair: cursor on
// foo.jpg → restore from foo.jpg.lazycvs-backup; cursor on
// foo.jpg.lazycvs-backup → restore foo.jpg from it. The backup file is
// renamed (not deleted) over the working file, so the user sees the
// restored content immediately and there's no leftover backup file
// that would re-appear as ? on the next refresh.
func (m *App) restoreFromBackup(path string) tea.Cmd {
	const suffix = ".lazycvs-backup"
	target, backup := path, path+suffix
	if strings.HasSuffix(path, suffix) {
		target = strings.TrimSuffix(path, suffix)
		backup = path
	}
	absTarget := filepath.Join(m.exec.WorkDir, target)
	absBackup := filepath.Join(m.exec.WorkDir, backup)
	if _, err := os.Stat(absBackup); err != nil {
		notifyCmd := m.setResult(fmt.Sprintf("✗ No backup at %s", backup), false)
		return notifyCmd
	}
	if err := os.Rename(absBackup, absTarget); err != nil {
		notifyCmd := m.setResult(fmt.Sprintf("✗ Restore failed: %v", err), false)
		return notifyCmd
	}
	// The restored file is back in M state from CVS's perspective —
	// reuse the per-path refresh that the action handlers use so the
	// listing picks up the new status and the now-gone backup file.
	notifyCmd := m.setResult(fmt.Sprintf("✓ Restored %s from backup", filepath.Base(target)), true)
	return tea.Batch(notifyCmd, m.refreshStatusForPaths([]string{target, backup}))
}

// revertOne reverts a single file. For M-status it runs `cvs update -C`
// (the conventional revert). For C-status — "Unresolved Conflict" —
// the safer path is `rm <path>` followed by `cvs update <path>`: cvs
// keeps a "Result of merge" marker in CVS/Entries that `update -C`
// doesn't fully clear, so the file keeps reporting as C even after
// the working copy has been overwritten. Removing the working file
// first forces cvs to re-create it cleanly and the marker is wiped
// with it.
func revertOne(exec *cvs.CVSExecutor, workDir, path, status string) error {
	abs := filepath.Join(workDir, path)
	if status == "C" {
		// Best-effort delete: if the file is already gone the next
		// cvs update will still bring it back, which is the goal.
		_ = os.Remove(abs)
		r, err := exec.Run("update", path)
		if err != nil {
			return err
		}
		if r != nil && !r.Success {
			return fmt.Errorf("cvs update %s exited %d", path, r.ExitCode)
		}
		return nil
	}
	r, err := exec.Run("update", "-C", path)
	if err != nil {
		return err
	}
	if r != nil && !r.Success {
		return fmt.Errorf("cvs update -C %s exited %d", path, r.ExitCode)
	}
	return nil
}

// stagedBulkPrepareBackups writes a <path>.lazycvs-backup for every
// path that's still on disk *before* the revert goroutine touches it.
// Mirrors the per-file backup doRevert already writes for the
// dialog-driven single-file revert, but lifts it out of the goroutine
// so the backups are guaranteed in place before any `rm` lands.
func stagedBulkPrepareBackups(workDir string, paths []string) {
	for _, p := range paths {
		abs := filepath.Join(workDir, p)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue // missing on disk → nothing to back up
		}
		_ = os.WriteFile(abs+".lazycvs-backup", data, 0644)
	}
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
		args := append([]string{"update", "-d", "-P"}, paths...)
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
		r, err := exec.Run("update", "-d", "-P", target)
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
		if err := ensureParentDirs(exec, path); err != nil {
			return actionDoneMsg{paths: []string{path}, err: err}
		}
		r, err := exec.Run("add", path)
		if err == nil && r != nil && !r.Success {
			err = fmt.Errorf("cvs add %s exited %d", path, r.ExitCode)
		}
		return actionDoneMsg{paths: []string{path}, err: err}
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
