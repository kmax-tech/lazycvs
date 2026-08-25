package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/config"
	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/fs"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// User-initiated CVS actions: commit / update / revert / add / ignore /
// remove. Each method either dispatches a tea.Cmd that runs the cvs
// binary in the background, or opens a dialog so the user can confirm /
// supply input. The actual cvs invocation happens inside the returned
// closure; the main goroutine is never blocked.

// backupSuffix is the sidecar extension for the safety copies written
// before destructive actions (revert, restore-rev). THE single
// definition — the ignore pattern, the restore flows, and the backup
// writers all derive from it.
const backupSuffix = ".lazycvs-backup"

// writeBackupFile copies path's on-disk content to path+backupSuffix.
// Returns true when a backup was written; a path missing on disk is
// not an error (nothing to protect). Logging is the caller's job —
// single-file flows log per file, bulk flows log one summary.
func writeBackupFile(workDir, path string) bool {
	abs := filepath.Join(workDir, path)
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	return os.WriteFile(abs+backupSuffix, data, 0644) == nil
}

// validatePaths runs every path through the executor's traversal- and
// symlink-escape check. This is the enforcement point for the
// path-validation rule (CLAUDE.md): every action dispatcher that
// accepts working-copy paths calls it before touching cvs or the
// filesystem.
func validatePaths(exec *cvs.CVSExecutor, paths ...string) error {
	for _, p := range paths {
		if err := exec.ValidatePath(p); err != nil {
			return err
		}
	}
	return nil
}

// revertPathsCmd is the async bulk revert shared by the staged-tab
// dispatch and the confirmation-dialog dispatch — one loop, one
// backup pass, so the two flows can't drift apart. statuses decides
// the per-path cvs sequence (see revertOne).
func revertPathsCmd(exec *cvs.CVSExecutor, paths []string, statuses map[string]string) tea.Cmd {
	if err := validatePaths(exec, paths...); err != nil {
		return func() tea.Msg { return actionDoneMsg{paths: paths, err: err} }
	}
	stagedBulkPrepareBackups(exec, paths)
	return func() tea.Msg {
		var firstErr error
		for _, p := range paths {
			if err := revertOne(exec, exec.WorkDir, p, statuses[p]); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return actionDoneMsg{paths: paths, err: firstErr}
	}
}

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
			if err := validatePaths(exec, paths...); err != nil {
				return actionDoneMsg{paths: paths, err: err, keepMarks: true}
			}
			var firstErr error
			for _, p := range paths {
				if err := ensureParentDirs(exec, p); err != nil && firstErr == nil {
					firstErr = err
				}
				if r, err := exec.Run(addArgs(exec.WorkDir, p)...); err != nil {
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
			// resolveFileStatus, not the raw statusMap: the map can lack
			// entries the resolver derives (Entries promotion), and the
			// C-vs-M revert sequence depends on getting this right.
			statuses[p] = m.resolveFileStatus(p)
		}
		return revertPathsCmd(exec, paths, statuses)
	case "update":
		return func() tea.Msg {
			if err := validatePaths(exec, paths...); err != nil {
				return updateDoneMsg{paths: paths, err: err}
			}
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
	target, backup := path, path+backupSuffix
	if strings.HasSuffix(path, backupSuffix) {
		target = strings.TrimSuffix(path, backupSuffix)
		backup = path
	}
	if err := validatePaths(m.exec, target, backup); err != nil {
		return m.setResult(fmt.Sprintf("✗ %v", err), false)
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
	seen := make(map[string]bool, len(paths))
	type pair struct{ target, backup string }
	var pairs []pair
	for _, p := range paths {
		target := p
		backup := p + backupSuffix
		if strings.HasSuffix(p, backupSuffix) {
			target = strings.TrimSuffix(p, backupSuffix)
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
	if err := validatePaths(m.exec, dirPath); err != nil {
		return m.setResult(fmt.Sprintf("✗ %v", err), false)
	}
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
		if !strings.HasSuffix(info.Name(), backupSuffix) {
			return nil
		}
		target := strings.TrimSuffix(p, backupSuffix)
		relTarget := target
		if r, err := filepath.Rel(m.exec.WorkDir, target); err == nil {
			relTarget = r
		}
		if err := os.Rename(p, target); err != nil {
			m.cmdLog.LogFileOp("mv "+relTarget+backupSuffix+" "+relTarget+"  # restore backup (subtree)", err)
			failed++
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		m.cmdLog.LogFileOp("mv "+relTarget+backupSuffix+" "+relTarget+"  # restore backup (subtree)", nil)
		restored++
		// Both halves of the pair, expressed relative to WorkDir so
		// the refresh keys line up with statusMap entries.
		if rel, err := filepath.Rel(m.exec.WorkDir, target); err == nil {
			refreshPaths = append(refreshPaths, rel, rel+backupSuffix)
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
		if writeBackupFile(exec.WorkDir, p) {
			n++
		}
	}
	if n > 0 {
		// One summary line, not one per file — an 18-file bulk revert
		// shouldn't spend a third of the console on backup notes.
		exec.Log.LogFileOp(fmt.Sprintf("cp %d file(s) → *.lazycvs-backup  # pre-revert safety copies", n), nil)
	}
}

// addArgs builds the `cvs add` argument list for a file, inserting -kb
// when the on-disk content is binary. Without -kb, CVS applies keyword
// expansion and line-ending conversion on every future checkout, which
// silently corrupts PDFs, images, zips, … (Directories never get -kb;
// use plain "add" for those.)
func addArgs(workDir, path string) []string {
	if fs.IsBinary(filepath.Join(workDir, path)) {
		return []string{"add", "-kb", path}
	}
	return []string{"add", path}
}

// recentCacheTTL bounds how stale a cached `cvs history` fetch may be
// before H re-queries the server. Within the window, re-pressing H —
// on ANY directory — renders instantly from the cache.
const recentCacheTTL = 5 * time.Minute

// buildRecentEntries scopes the repo-global event list to dir (incl.
// subdirs) and the display window, and attaches the two path forms
// the dialog needs. Pure — shared by the fresh-fetch path and the
// cache path. The stored list deliberately reaches further back than
// the window (full local history); the window is display-only.
func buildRecentEntries(events []cvs.HistoryEvent, root, dir string, windowStart time.Time) []recentEntry {
	modulePrefix := filepath.Clean(root + "/" + dir)
	filtered := cvs.FilterHistoryByRepoPrefix(events, modulePrefix)
	entries := make([]recentEntry, 0, len(filtered))
	for _, e := range filtered {
		if e.Time.Before(windowStart) {
			continue
		}
		full := e.RepoDir + "/" + e.File
		entries = append(entries, recentEntry{
			event:  e,
			label:  strings.TrimPrefix(full, modulePrefix+"/"),
			wcPath: strings.TrimPrefix(full, root+"/"),
		})
	}
	return entries
}

// historyStorePath returns the on-disk location of the persistent
// history store for this working copy's CVSROOT (stores are shared
// between checkouts of the same repository), plus the root string
// used as consistency key. Empty when CVS/Root is unreadable.
func historyStorePath(exec *cvs.CVSExecutor) (string, string) {
	data, err := os.ReadFile(filepath.Join(exec.WorkDir, "CVS", "Root"))
	if err != nil {
		return "", ""
	}
	cvsroot := strings.TrimSpace(string(data))
	h := fnv.New32a()
	h.Write([]byte(cvsroot))
	name := fmt.Sprintf("history-%08x.json", h.Sum32())
	return filepath.Join(filepath.Dir(config.DefaultConfigPath()), name), cvsroot
}

func recentTitle(dir string, days int) string {
	return fmt.Sprintf("Changes — %s (last %dd)", dir, days)
}

// openRecentFromCache serves H from the cached repo-global fetch —
// no cvs round trip, the dialog opens in the same frame.
func (m *App) openRecentFromCache(dir string, days int) tea.Cmd {
	root, err := moduleRoot(m.exec)
	if err != nil {
		return m.setResult(fmt.Sprintf("✗ %v", err), false)
	}
	windowStart := time.Now().AddDate(0, 0, -days)
	m.dialog.OpenRecent(recentTitle(dir, days), days, buildRecentEntries(m.recentEvents, root, dir, windowStart))
	m.dialog.SetRecentMeta(dir, m.recentFetched)
	return nil
}

// recentSkewOverlap is subtracted from the last-fetch time on
// incremental refreshes so client/server clock skew can't hide
// events; MergeHistory dedups whatever the overlap re-delivers.
const recentSkewOverlap = 10 * time.Minute

// loadRecentChanges asks the repository which files changed:
// `cvs history -x AMR -a -D <since>`. With a prior fetch in hand the
// query is INCREMENTAL — since = last fetch minus a skew overlap, so
// the server only ships events that are actually new, and the result
// merges into the cached window. Only the first fetch (or a changed
// day window) pulls the full `days` range. The query is deliberately
// repo-global (only -D bounds it): one result serves every directory
// via the client-side prefix filter. Requires history logging on the
// server (LogHistory in CVSROOT/config) — the handler explains when
// it's unavailable.
// loadRecentStore seeds the in-memory cache from the persistent
// store — a synchronous local JSON read, cheap enough for the key
// handler. Best-effort: mismatched CVSROOT or a missing file leave
// the cache empty and the caller falls back to a full fetch.
func (m *App) loadRecentStore() {
	storePath, cvsroot := historyStorePath(m.exec)
	if storePath == "" {
		return
	}
	st, err := cvs.LoadHistoryStore(storePath)
	if err != nil || st.CVSRoot != cvsroot {
		return
	}
	m.recentEvents = st.Events
	m.recentFetched = st.LastFetch
	m.recentCoverage = st.CoverageStart
}

func (m *App) loadRecentChanges(dir string, days int, background bool) tea.Cmd {
	exec := m.exec
	prior := m.recentEvents // snapshot; App only ever replaces the slice
	lastFetch := m.recentFetched
	coverage := m.recentCoverage
	return func() tea.Msg {
		root, err := moduleRoot(exec)
		if err != nil {
			return recentChangesMsg{dir: dir, days: days, err: err}
		}
		// Session cache empty (fresh start or invalidated)? Seed from
		// the persistent store, so even the first H of a session only
		// fetches the delta since the previous run.
		storePath, cvsroot := historyStorePath(exec)
		if prior == nil && storePath != "" {
			if st, err := cvs.LoadHistoryStore(storePath); err == nil && st.CVSRoot == cvsroot {
				prior = st.Events
				lastFetch = st.LastFetch
				coverage = st.CoverageStart
			}
		}

		windowStart := time.Now().AddDate(0, 0, -days)
		newCoverage := coverage
		var sinceArg string
		if prior == nil || coverage.IsZero() || windowStart.Before(coverage) {
			// Nothing local (or the window reaches further back than the
			// local history is complete) — deep fetch of the full window.
			sinceArg = windowStart.Format("2006-01-02")
			if newCoverage.IsZero() || windowStart.Before(newCoverage) {
				newCoverage = windowStart
			}
		} else {
			// Incremental: only events since the last fetch, minus a skew
			// overlap. Datetime precision; "GMT" because the venerable
			// getdate parser in cvs 1.11 understands it and server-side
			// event stamps are UTC.
			sinceArg = lastFetch.Add(-recentSkewOverlap).UTC().Format("2006-01-02 15:04") + " GMT"
		}
		r, runErr := exec.RunReadOnly("history", "-x", "AMR", "-a", "-D", sinceArg)
		if e := cvs.FirstFailure(r, runErr); e != nil {
			out := ""
			if r != nil {
				out = r.Combined
			}
			return recentChangesMsg{dir: dir, days: days, background: background, err: e, output: out}
		}
		// Zero cutoff: the local history keeps everything it has ever
		// seen (bounded by the store cap); the display window filters.
		events := cvs.MergeHistory(prior, cvs.ParseHistory(r.Stdout), time.Time{})
		if storePath != "" {
			_ = (&cvs.HistoryStore{
				CVSRoot:       cvsroot,
				LastFetch:     time.Now(),
				CoverageStart: newCoverage,
				Events:        events,
			}).Save(storePath)
		}
		return recentChangesMsg{
			dir: dir, days: days, raw: events, coverage: newCoverage,
			background: background,
			entries:    buildRecentEntries(events, root, dir, windowStart),
		}
	}
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
		if err := validatePaths(exec, paths...); err != nil {
			return updateDoneMsg{paths: paths, err: err}
		}
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
		if err := validatePaths(exec, path); err != nil {
			return actionDoneMsg{paths: []string{path}, err: err, keepMarks: true}
		}
		if err := ensureParentDirs(exec, path); err != nil {
			return actionDoneMsg{paths: []string{path}, err: err}
		}
		r, err := exec.Run(addArgs(exec.WorkDir, path)...)
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
