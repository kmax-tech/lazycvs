package tui

import (
	"fmt"
	"lazycvs/config"
	"lazycvs/cvs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
// `cvs update` (refresh) on the given paths. Marks follow the worklist
// model: the actionDoneMsg/updateDoneMsg handlers consume them on
// success and keep them on failure — nothing is unmarked here at
// dispatch time. `add` keeps its marks even on success (keepMarks) so
// the just-added files stay staged for the follow-up commit.
func (m *App) stagedBulkAction(action string, paths []string) tea.Cmd {
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
			return actionDoneMsg{paths: paths, err: firstErr, keepMarks: true}
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
		stagedBulkPrepareBackups(exec, paths)
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
		m.cmdLog.LogFileOp("mv "+backup+" "+target+"  # restore backup", err)
		notifyCmd := m.setResult(fmt.Sprintf("✗ Restore failed: %v", err), false)
		return notifyCmd
	}
	m.cmdLog.LogFileOp("mv "+backup+" "+target+"  # restore backup", nil)
	// The restored file is back in M state from CVS's perspective —
	// reuse the per-path refresh that the action handlers use so the
	// listing picks up the new status and the now-gone backup file.
	notifyCmd := m.setResult(fmt.Sprintf("✓ Restored %s from backup", filepath.Base(target)), true)
	return tea.Batch(notifyCmd, m.refreshStatusForPaths([]string{target, backup}))
}

// restoreFromBackupBulk is the marked-set companion to
// restoreFromBackup. Each input path is normalized to its
// <file>/<file>.lazycvs-backup pair, then renamed in place. Paths
// that don't have a matching backup are reported in the result
// banner but don't abort the rest — partial restore is better than
// none when the user just wants their data back.
func (m *App) restoreFromBackupBulk(paths []string) tea.Cmd {
	const suffix = ".lazycvs-backup"
	seen := make(map[string]bool, len(paths))
	type pair struct{ target, backup string }
	var pairs []pair
	for _, p := range paths {
		target := p
		backup := p + suffix
		if strings.HasSuffix(p, suffix) {
			target = strings.TrimSuffix(p, suffix)
			backup = p
		}
		if seen[target] {
			continue // user marked both halves of the pair — restore once
		}
		seen[target] = true
		pairs = append(pairs, pair{target: target, backup: backup})
	}

	var restored, missing, failed int
	var firstErr error
	var refreshPaths []string
	for _, p := range pairs {
		absTarget := filepath.Join(m.exec.WorkDir, p.target)
		absBackup := filepath.Join(m.exec.WorkDir, p.backup)
		if _, err := os.Stat(absBackup); err != nil {
			missing++
			continue
		}
		if err := os.Rename(absBackup, absTarget); err != nil {
			m.cmdLog.LogFileOp("mv "+p.backup+" "+p.target+"  # restore backup (bulk)", err)
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.cmdLog.LogFileOp("mv "+p.backup+" "+p.target+"  # restore backup (bulk)", nil)
		restored++
		refreshPaths = append(refreshPaths, p.target, p.backup)
		delete(m.filelist.marked, p.target)
		delete(m.filelist.marked, p.backup)
	}

	var notifyCmd tea.Cmd
	switch {
	case restored == 0 && missing > 0:
		notifyCmd = m.setResult(fmt.Sprintf("✗ No backups found for %d marked path(s)", missing), false)
	case failed > 0:
		notifyCmd = m.setResult(fmt.Sprintf("⚠ Restored %d, %d failed — see Console", restored, failed), false)
	case missing > 0:
		notifyCmd = m.setResult(fmt.Sprintf("✓ Restored %d (skipped %d without backup)", restored, missing), true)
	default:
		notifyCmd = m.setResult(fmt.Sprintf("✓ Restored %d file(s) from backup", restored), true)
	}
	if len(refreshPaths) == 0 {
		return notifyCmd
	}
	return tea.Batch(notifyCmd, m.refreshStatusForPaths(refreshPaths))
}

// restoreBackupsInSubtree walks dirPath relative to the working copy
// and renames every <file>.lazycvs-backup it finds over <file>. Skips
// CVS/ admin dirs. dirPath == "." restores the whole working copy;
// that's allowed but the user explicitly chose the root in the tree
// pane so the scope is on them.
func (m *App) restoreBackupsInSubtree(dirPath string) tea.Cmd {
	const suffix = ".lazycvs-backup"
	abs := filepath.Join(m.exec.WorkDir, dirPath)
	var restored, failed int
	var firstErr error
	var refreshPaths []string
	walkErr := filepath.Walk(abs, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // tolerate unreadable subtrees
		}
		if info.IsDir() {
			if info.Name() == "CVS" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), suffix) {
			return nil
		}
		target := strings.TrimSuffix(p, suffix)
		relTarget := target
		if r, err := filepath.Rel(m.exec.WorkDir, target); err == nil {
			relTarget = r
		}
		if err := os.Rename(p, target); err != nil {
			m.cmdLog.LogFileOp("mv "+relTarget+suffix+" "+relTarget+"  # restore backup (subtree)", err)
			failed++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		m.cmdLog.LogFileOp("mv "+relTarget+suffix+" "+relTarget+"  # restore backup (subtree)", nil)
		restored++
		// Both halves of the pair, expressed relative to WorkDir so
		// the refresh keys line up with statusMap entries.
		if rel, err := filepath.Rel(m.exec.WorkDir, target); err == nil {
			refreshPaths = append(refreshPaths, rel, rel+suffix)
		}
		return nil
	})
	if walkErr != nil && firstErr == nil {
		firstErr = walkErr
	}
	var notifyCmd tea.Cmd
	switch {
	case restored == 0 && failed == 0:
		notifyCmd = m.setResult(fmt.Sprintf("No .lazycvs-backup files under %s", dirPath), false)
	case failed > 0:
		notifyCmd = m.setResult(fmt.Sprintf("⚠ Restored %d, %d failed — see Console", restored, failed), false)
	default:
		notifyCmd = m.setResult(fmt.Sprintf("✓ Restored %d backup(s) under %s", restored, dirPath), true)
	}
	if len(refreshPaths) == 0 {
		return notifyCmd
	}
	return tea.Batch(notifyCmd, m.refreshStatusForPaths(refreshPaths))
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
		if err := os.Remove(abs); err == nil {
			exec.Log.LogFileOp("rm "+path+"  # revert conflict: refetch clean copy", nil)
		}
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
func stagedBulkPrepareBackups(exec *cvs.CVSExecutor, paths []string) {
	n := 0
	for _, p := range paths {
		abs := filepath.Join(exec.WorkDir, p)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue // missing on disk → nothing to back up
		}
		if os.WriteFile(abs+".lazycvs-backup", data, 0644) == nil {
			n++
		}
	}
	if n > 0 {
		// One summary line, not one per file — an 18-file bulk revert
		// shouldn't spend a third of the console on backup notes.
		exec.Log.LogFileOp(fmt.Sprintf("cp %d file(s) → *.lazycvs-backup  # pre-revert safety copies", n), nil)
	}
}

// loadRecentChanges asks the repository which files changed under dir
// in the last `days` days: `cvs history -x AMR -a -D <since>`. The
// history database is server-global (CVSROOT/history), so two things
// bound the result: the -D window keeps the query small, and the
// module-path prefix scopes it to the chosen dir including all
// subdirs. Requires history logging on the server (LogHistory in
// CVSROOT/config) — the handler explains when it's unavailable.
func (m *App) loadRecentChanges(dir string, days int) tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		rootRepo, err := os.ReadFile(filepath.Join(exec.WorkDir, "CVS", "Repository"))
		if err != nil {
			return recentChangesMsg{dir: dir, days: days, err: fmt.Errorf("read CVS/Repository: %w", err)}
		}
		modulePrefix := filepath.Clean(strings.TrimSpace(string(rootRepo)) + "/" + dir)
		since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		r, runErr := exec.RunReadOnly("history", "-x", "AMR", "-a", "-D", since)
		if e := cvs.FirstFailure(r, runErr); e != nil {
			out := ""
			if r != nil {
				out = r.Combined
			}
			return recentChangesMsg{dir: dir, days: days, err: e, output: out}
		}
		events := cvs.FilterHistoryByRepoPrefix(cvs.ParseHistory(r.Stdout), modulePrefix)
		sort.Slice(events, func(i, j int) bool { return events[i].Time.After(events[j].Time) })
		return recentChangesMsg{dir: dir, days: days, events: events, modulePrefix: modulePrefix}
	}
}

// formatRecentChanges renders the H-view body: newest first, one line
// per event, file paths relative to the chosen dir.
func formatRecentChanges(msg recentChangesMsg) string {
	if len(msg.events) == 0 {
		return fmt.Sprintf("No recorded changes in the last %d day(s).", msg.days)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d change(s), newest first.   M=commit  A=add  R=remove\n\n", len(msg.events))
	for _, e := range msg.events {
		rel := strings.TrimPrefix(e.RepoDir, msg.modulePrefix)
		rel = strings.TrimPrefix(rel, "/")
		p := e.File
		if rel != "" {
			p = rel + "/" + e.File
		}
		fmt.Fprintf(&b, "%s  %s %-6s %-10s %s\n",
			e.Time.Local().Format("2006-01-02 15:04"), e.Code, e.Rev, e.User, p)
	}
	return b.String()
}

// addFavorite pins the directory the user is looking at: the tree
// cursor's dir (or the parent dir when the cursor is on a file),
// falling back to the file-list's current dir. The entry is written
// through ConfigManager so it survives restarts, then mirrored into
// the in-memory model.
func (m *App) addFavorite() tea.Cmd {
	dir := ""
	if m.activeTab == TabTree {
		if node := m.tree.SelectedNode(); node != nil {
			if node.IsDir {
				dir = node.Path
			} else {
				dir = filepath.Dir(node.Path)
			}
		}
	}
	if dir == "" || dir == "." {
		dir = m.filelist.dir
	}
	if dir == "" || dir == "." {
		// The working-copy root is always one keypress away (Tab 1);
		// favoriting it would just duplicate that.
		return m.setResult("✗ Select a directory to add as favorite", false)
	}
	if m.favorites.Contains(dir) {
		return m.setResult(fmt.Sprintf("Already a favorite: %s", dir), false)
	}
	fav := config.FavoriteDir{Name: filepath.Base(dir), Path: dir}
	if err := m.cfgMgr.Update(func(c *config.Config) {
		c.Favorites.Dirs = append(c.Favorites.Dirs, fav)
	}); err != nil {
		return m.setResult(fmt.Sprintf("✗ Save favorites: %v", err), false)
	}
	m.favorites.Add(fav)
	m.favorites.UpdateCounts(m.statusMap)
	return m.setResult(fmt.Sprintf("✓ Added %s to favorites", dir), true)
}

// removeFavorite unpins the favorite under the cursor (Favorites tab)
// and persists the shrunken list.
func (m *App) removeFavorite() tea.Cmd {
	path := m.favorites.RemoveSelected()
	if path == "" {
		return nil
	}
	if err := m.cfgMgr.Update(func(c *config.Config) {
		kept := c.Favorites.Dirs[:0]
		for _, d := range c.Favorites.Dirs {
			if d.Path != path {
				kept = append(kept, d)
			}
		}
		c.Favorites.Dirs = kept
	}); err != nil {
		return m.setResult(fmt.Sprintf("✗ Save favorites: %v", err), false)
	}
	m.updateFileList()
	return m.setResult(fmt.Sprintf("✓ Removed %s from favorites", path), true)
}

func (m *App) stagedBulkIgnore(paths []string) tea.Cmd {
	written := 0
	for _, p := range paths {
		dir := filepath.Dir(p)
		ignPath := filepath.Join(m.exec.WorkDir, dir, ".cvsignore")
		f, err := os.OpenFile(ignPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(base(p) + "\n")
			f.Close()
			written++
			// Worklist model: only successfully-ignored paths are
			// consumed; a failed write keeps its mark for a retry.
			delete(m.filelist.marked, p)
		}
	}
	if written > 0 {
		m.cmdLog.LogFileOp(fmt.Sprintf("echo %d name(s) >> .cvsignore  # ignore bulk", written), nil)
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
		return actionDoneMsg{paths: []string{path}, err: err, keepMarks: true}
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
		m.cmdLog.LogFileOp("echo "+base(msg.path)+" >> "+filepath.Join(dir, ".cvsignore"), err)
	case 2:
		// Global pattern → ~/.cvsignore
		e := ext(msg.path)
		if e != "" {
			err := appendToGlobalCvsignore("*" + e)
			m.cmdLog.LogFileOp("echo *"+e+" >> ~/.cvsignore", err)
		}
	case 3:
		// Global exact → ~/.cvsignore
		err := appendToGlobalCvsignore(base(msg.path))
		m.cmdLog.LogFileOp("echo "+base(msg.path)+" >> ~/.cvsignore", err)
	}
	return m.refreshStatusForPaths([]string{msg.path})
}

func appendToGlobalCvsignore(pattern string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".cvsignore")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	// CVS ignore files are space-separated, but one pattern per line is safe
	_, err = f.WriteString(pattern + "\n")
	return err
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
			err := os.Rename(abs, abs+".moved-by-lazycvs")
			m.cmdLog.LogFileOp("mv "+p+" "+p+".moved-by-lazycvs  # in-the-way", err)
		case inTheWayDelete:
			err := os.Remove(abs)
			m.cmdLog.LogFileOp("rm "+p+"  # in-the-way", err)
		}
	}
	// Re-run cvs update on the now-unblocked paths so the server
	// versions actually land. doUpdatePaths reports back via
	// updateDoneMsg — if a new "in the way" appears (rare), the
	// dialog re-opens.
	return m.doUpdatePaths(msg.paths)
}
