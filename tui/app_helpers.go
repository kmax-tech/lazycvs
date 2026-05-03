package tui

import (
	"lazycvs/cvs"
	"lazycvs/fs"
	"fmt"
	ioFS "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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

// resolveFileStatus returns the effective status for a file. If the statusMap
// has no entry (empty string), checks whether the parent directory is unknown
// to CVS (no CVS/ subdir) — if so the file is untracked ("?").
func (m *App) resolveFileStatus(path string) string {
	if s := m.statusMap[path]; s != "" {
		return s
	}
	dir := filepath.Dir(path)
	cvsDir := filepath.Join(m.exec.WorkDir, dir, "CVS")
	if _, err := os.Stat(cvsDir); os.IsNotExist(err) {
		return "?"
	}
	return ""
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
		return m.history.path
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

// doUpdateSelected runs cvs update on the tree's selected directory or file.
// Returns updateDoneMsg with success/error; the handler in App.Update shows
// a banner and dispatches the follow-up status refresh. With no node
// selected, falls back to a silent status refresh (no banner).
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

// fileHasLocalChanges reports whether the path has uncommitted modifications
// (M or C) according to the App's statusMap.
func (m *App) fileHasLocalChanges(path string) bool {
	switch m.statusMap[path] {
	case "M", "C":
		return true
	}
	return false
}

func (m *App) autoLoadHistory() tea.Cmd {
	// Find a selected file path from tree or filelist
	var path string
	if f := m.filelist.SelectedFile(); f != nil {
		path = f.Path
	} else if node := m.tree.SelectedNode(); node != nil && !node.IsDir {
		path = node.Path
	}
	if path == "" || path == m.history.path {
		return nil
	}
	return loadHistory(m.exec, path, m.fileHasLocalChanges(path))
}

func (m *App) loadHistoryContent() tea.Cmd {
	path := m.history.path

	m.history.loadGen++
	gen := m.history.loadGen

	if fs.IsBinary(filepath.Join(m.exec.WorkDir, path)) {
		return func() tea.Msg {
			return historyContentMsg{content: "Binary file — cannot display content", gen: gen}
		}
	}

	// Compare mode: combinations of working-copy and revisions on either side.
	if m.history.mode == HistoryCompare {
		toIsWorking := m.history.IsWorkingCopyRow() || m.history.compareToWorking
		fromIsWorking := m.history.CompareFromIsWorking()
		fromRev := m.history.CompareFromRev()
		toRev := m.history.SelectedRevision()

		if toIsWorking || fromIsWorking {
			fullPath := filepath.Join(m.exec.WorkDir, path)
			if _, err := os.Stat(fullPath); os.IsNotExist(err) {
				return func() tea.Msg {
					return historyContentMsg{content: "File does not exist on disk — cannot compare against working copy", gen: gen}
				}
			}
		}

		switch {
		case fromIsWorking && toRev != nil:
			return loadCompareWorking(m.exec, path, toRev.Number, gen)
		case toIsWorking && fromRev != nil:
			return loadCompareWorking(m.exec, path, fromRev.Number, gen)
		case fromRev != nil && toRev != nil:
			return loadCompare(m.exec, path, fromRev.Number, toRev.Number, gen)
		}
		return nil
	}

	// Working-copy pseudo-row: read on-disk content / run plain `cvs diff`.
	if m.history.IsWorkingCopyRow() {
		switch m.history.mode {
		case HistoryContent:
			return loadWorkingContent(m.exec.WorkDir, path, gen)
		case HistoryDiff:
			return loadWorkingDiff(m.exec, path, gen)
		case HistoryBlame:
			return loadBlame(m.exec, path, gen)
		}
		return nil
	}

	// Real revision under cursor.
	rev := m.history.SelectedRevision()
	if rev == nil {
		return nil
	}
	switch m.history.mode {
	case HistoryContent:
		m.history.diffFromRev = ""
		m.history.diffToRev = rev.Number
		if cached, ok := m.history.contentCache[rev.Number]; ok {
			m.history.applyContent(cached)
			return nil
		}
		return loadRevisionContent(m.exec, path, rev.Number, "", rev.Number, gen)
	case HistoryDiff:
		if rev.PrevNumber != "" {
			m.history.diffFromRev = rev.PrevNumber
			m.history.diffToRev = rev.Number
			cacheKey := rev.PrevNumber + ":" + rev.Number
			if cached, ok := m.history.diffCache[cacheKey]; ok {
				m.history.applyDiff(cached)
				return m.prefetchAdjacentDiffs(path)
			}
			return m.withPrefetch(
				loadRevisionDiff(m.exec, path, rev.PrevNumber, rev.Number, gen),
				path,
			)
		}
		m.history.diffFromRev = ""
		m.history.diffToRev = rev.Number
		if cached, ok := m.history.contentCache[rev.Number]; ok {
			m.history.applyContent(cached)
			return nil
		}
		return loadRevisionContent(m.exec, path, rev.Number, "", rev.Number, gen)
	case HistoryBlame:
		return loadBlame(m.exec, path, gen)
	}
	return nil
}

func (m *App) prefetchAdjacentDiffs(path string) tea.Cmd {
	var cmds []tea.Cmd
	for _, delta := range []int{-1, 1} {
		adjIdx := m.history.revisionIndex(m.history.cursor + delta)
		if adjIdx < 0 || adjIdx >= len(m.history.revisions) {
			continue
		}
		adj := m.history.revisions[adjIdx]
		if adj.PrevNumber == "" {
			continue
		}
		cacheKey := adj.PrevNumber + ":" + adj.Number
		if _, ok := m.history.diffCache[cacheKey]; ok {
			continue
		}
		cmds = append(cmds, loadRevisionDiff(m.exec, path, adj.PrevNumber, adj.Number, 0))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *App) withPrefetch(mainCmd tea.Cmd, path string) tea.Cmd {
	if mainCmd == nil {
		return nil
	}
	prefetch := m.prefetchAdjacentDiffs(path)
	if prefetch == nil {
		return mainCmd
	}
	return tea.Batch(mainCmd, prefetch)
}

func refreshStagedFiles(exec *cvs.CVSExecutor, paths []string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"status"}, paths...)
		result, _ := exec.RunReadOnly(args...)
		if result == nil {
			return stagedStatusMsg{paths: paths}
		}
		statuses := cvs.ParseStatus(result.Stdout)
		// `cvs status <path>` reports the basename only ("File: notes.txt")
		// — no "Examining <dir>" prefix to give ParseStatus a directory
		// context. Remap basenames back to the original input paths so the
		// stagedStatusMsg handler keys statusMap correctly.
		byBase := make(map[string]string, len(paths))
		for _, p := range paths {
			byBase[filepath.Base(p)] = p
		}
		for i, s := range statuses {
			if full, ok := byBase[s.Path]; ok {
				statuses[i].Path = full
			}
		}
		return stagedStatusMsg{paths: paths, statuses: statuses}
	}
}

func loadPreviewDiff(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", path)
		if result == nil {
			return previewMsg{path: path}
		}
		parsed := cvs.ParseDiff(result.Stdout)
		return previewMsg{path: path, diff: parsed}
	}
}

func (m *App) autoLoadPreview() tea.Cmd {
	node := m.tree.SelectedNode()
	if node == nil || node.IsDir {
		m.previewReady = false
		return nil
	}
	if node.Path == m.previewPath && m.previewReady {
		return nil // already loaded
	}
	m.previewPath = node.Path
	m.previewReady = false
	if fs.IsBinary(filepath.Join(m.exec.WorkDir, node.Path)) {
		m.previewDiff = nil
		m.previewReady = true
		return nil
	}
	if node.Status == "M" || node.Status == "C" {
		return loadPreviewDiff(m.exec, node.Path)
	}
	// No diff needed — mark ready with nil diff
	m.previewDiff = nil
	m.previewReady = true
	return nil
}

func (m *App) applyTreeMode() tea.Cmd {
	m.tree.showFiles = (m.treeMode == TreeViewDetails)
	m.tree.rebuildFlat()
	if m.treeMode == TreeViewDetails {
		return m.autoLoadPreview()
	}
	m.updateFileList()
	return nil
}

// diffSide describes one side of an external-diff invocation.
// If working is true, the file is the on-disk working copy (no extraction).
// Otherwise rev is checked out via `cvs update -p [-r rev]` to a temp file
// (rev="" means HEAD).
type diffSide struct {
	rev     string
	working bool
}

// launchExternalDiffFor extracts both sides to files (or uses the working
// file directly) and spawns the configured external diff tool. If no tool is
// configured, opens a preview dialog explaining how to set one.
func (m *App) launchExternalDiffFor(path string, left, right diffSide) tea.Cmd {
	cfg := m.cfgMgr.Get()
	if cfg.Editor.DiffTool == "" && cfg.Editor.DiffCommand == "" {
		m.dialog.OpenPreview("external diff", "No diff tool configured.\n\n"+
			"Set [editor] diff_tool in your config, e.g.:\n\n"+
			"  [editor]\n"+
			"  diff_tool = \"meld\"\n\n"+
			"Built-in presets: vscode, emacs, vimdiff, meld,\n"+
			"opendiff, kdiff3, diffuse, bcompare.\n\n"+
			"Or use a custom command template:\n"+
			"  diff_command = \"my-tool $LEFT $RIGHT\"")
		return nil
	}
	executor := m.exec
	return func() tea.Msg {
		leftFile, err := materializeSide(executor, path, left)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		rightFile, err := materializeSide(executor, path, right)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		return launchExternalDiff(cfg, leftFile, rightFile)()
	}
}

// materializeSide returns the absolute file path for a diff side, extracting
// the revision to a temp file if necessary.
func materializeSide(executor *cvs.CVSExecutor, path string, side diffSide) (string, error) {
	if side.working {
		return filepath.Join(executor.WorkDir, path), nil
	}
	return extractRevToTemp(executor, path, side.rev)
}

// launchExternalMergeFor extracts the four sides of a CVS conflict file and
// spawns the configured 3-way merge tool. Returns nil with a help dialog
// open if no merge tool is configured.
func (m *App) launchExternalMergeFor(path string) tea.Cmd {
	cfg := m.cfgMgr.Get()
	if cfg.Editor.MergeTool == "" && cfg.Editor.MergeCommand == "" {
		m.dialog.OpenPreview("merge tool", "No merge tool configured.\n\n"+
			"Set [editor] merge_tool in your config, e.g.:\n\n"+
			"  [editor]\n"+
			"  merge_tool = \"meld\"\n\n"+
			"Built-in 3-way presets: meld, kdiff3, vimdiff,\n"+
			"vscode, opendiff, diffuse, bcompare.\n\n"+
			"Or use a custom command:\n"+
			"  merge_command = \"my-tool $BASE $LOCAL $REMOTE -o $MERGED\"")
		return nil
	}
	executor := m.exec
	return func() tea.Msg {
		base, local, remote, merged, err := extractConflictSides(executor, path)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		return launchExternalMerge(cfg, base, local, remote, merged)()
	}
}

// launchExternalDiffForCurrent picks left/right based on the active tab and,
// when on TabHistory, the history mode + cursor. The selectedPath argument is
// the path under cursor in Tree/Fav/Staged; for TabHistory we use m.history.path.
func (m *App) launchExternalDiffForCurrent(selectedPath string) tea.Cmd {
	if m.activeTab != TabHistory {
		// Working copy vs HEAD — what most users want from outside History.
		return m.launchExternalDiffFor(selectedPath,
			diffSide{rev: ""},          // HEAD
			diffSide{working: true})    // local file
	}

	path := m.history.path
	if path == "" {
		return nil
	}

	if m.history.mode == HistoryCompare {
		// from-rev (or working) vs to-rev (or working).
		left := diffSide{working: m.history.CompareFromIsWorking()}
		if !left.working {
			if r := m.history.CompareFromRev(); r != nil {
				left.rev = r.Number
			} else {
				return nil
			}
		}
		right := diffSide{working: m.history.IsWorkingCopyRow() || m.history.compareToWorking}
		if !right.working {
			if r := m.history.SelectedRevision(); r != nil {
				right.rev = r.Number
			} else {
				return nil
			}
		}
		return m.launchExternalDiffFor(path, left, right)
	}

	// Diff / Content / Blame at the cursor row.
	if m.history.IsWorkingCopyRow() {
		// HEAD vs working copy — same as the non-history default.
		return m.launchExternalDiffFor(path,
			diffSide{rev: ""},
			diffSide{working: true})
	}
	rev := m.history.SelectedRevision()
	if rev == nil {
		return nil
	}
	if rev.PrevNumber == "" {
		// First revision has no parent — nothing to diff against.
		return nil
	}
	return m.launchExternalDiffFor(path,
		diffSide{rev: rev.PrevNumber},
		diffSide{rev: rev.Number})
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

func (m *App) handleConflictResolve(msg conflictResolveMsg) tea.Cmd {
	fullPath := filepath.Join(m.exec.WorkDir, msg.path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil
	}
	resolved := cvs.ResolveConflict(string(data), msg.choice, 0) // 0 = all regions
	os.WriteFile(fullPath, []byte(resolved), 0644)
	return m.refreshStatusForPaths([]string{msg.path})
}

func (m *App) handleMouse(msg tea.MouseMsg) tea.Cmd {
	leftW, _, contentH, consoleH := m.layout()
	x, y := msg.X, msg.Y

	// Layout: [banner] tabBar mainContent console keybar
	tabBarY := 0
	if m.notification != "" {
		tabBarY = 1
	}

	// Tab bar click
	if y == tabBarY && msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		if tab := m.tabAtX(x); tab >= 0 {
			m.activeTab = tab
			m.updateFileList()
		}
		return nil
	}

	mainTop := tabBarY + 1               // top border of main content
	mainBottom := mainTop + contentH + 1 // bottom border
	consoleTop := mainBottom + 1         // top border of console
	consoleBottom := consoleTop + consoleH + 1

	// Main content area (between borders)
	if y > mainTop && y < mainBottom {
		contentRow := y - mainTop - 1

		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if x < leftW {
				m.focus = PanelLeft
				return m.clickLeft(contentRow)
			} else {
				m.focus = PanelRight
				m.clickRight(contentRow)
			}
			return nil
		}

		// Scroll wheel — scroll the panel under the mouse, regardless of focus
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp {
				delta = -3
			}
			if x < leftW {
				return m.scrollLeft(delta)
			} else {
				m.scrollRight(delta)
			}
			return nil
		}
		return nil
	}

	// Console area
	if y > consoleTop && y < consoleBottom {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.focus = PanelConsole
			return nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.console.viewport.LineUp(3)
			return nil
		}
		if msg.Button == tea.MouseButtonWheelDown {
			m.console.viewport.LineDown(3)
			return nil
		}
	}

	return nil
}

// tabAtX returns the tab index for a given X coordinate, or -1 if outside.
func (m *App) tabAtX(x int) int {
	treeName := "Files"
	if m.treeMode == TreeViewDetails {
		treeName = "Detail"
	}
	stagedName := "Staged"
	if n := len(m.filelist.marked); n > 0 {
		stagedName = fmt.Sprintf("Staged(%d)", n)
	}
	names := []string{treeName, "Fav", stagedName, "History"}
	pos := 0
	for i, name := range names {
		label := fmt.Sprintf(" %d:%s ", i+1, name)
		end := pos + len(label)
		if x >= pos && x < end {
			return i
		}
		pos = end + 1 // +1 for the │ separator
	}
	return -1
}

func (m *App) clickLeft(row int) tea.Cmd {
	switch m.activeTab {
	case TabTree:
		idx := m.tree.offset + row
		if idx >= 0 && idx < len(m.tree.flat) {
			m.tree.cursor = idx
			m.updateFileList()
		}
	case TabFavorites:
		idx := row - 1 // header row
		if idx >= 0 && idx < len(m.favorites.favorites) {
			m.favorites.cursor = idx
			m.updateFileList()
		}
	case TabHistory:
		prevCursor := m.history.cursor
		idx := m.history.offset + row - 1 // header row
		if idx >= 0 && idx < m.history.numRows() {
			m.history.cursor = idx
		}
		if m.history.cursor != prevCursor {
			// DISABLED: auto-compare on click (bug analysis)
			return m.loadHistoryContent()
		}
	}
	return nil
}

func (m *App) clickRight(row int) {
	switch m.activeTab {
	case TabTree:
		if m.treeMode == TreeViewDetails {
			return // no click for preview
		}
		idx := m.filelist.offset + row - 1
		files := m.filelist.rows()
		if idx >= 0 && idx < len(files) {
			m.filelist.cursor = idx
		}
	case TabFavorites:
		idx := m.filelist.offset + row - 1 // header row
		files := m.filelist.rows()
		if idx >= 0 && idx < len(files) {
			m.filelist.cursor = idx
		}
	}
}

func (m *App) scrollLeft(delta int) tea.Cmd {
	switch m.activeTab {
	case TabTree:
		m.tree.cursor = clamp(m.tree.cursor+delta, 0, max(0, len(m.tree.flat)-1))
		m.tree.ensureVisible()
		m.updateFileList()
	case TabFavorites:
		m.favorites.cursor = clamp(m.favorites.cursor+delta, 0, max(0, len(m.favorites.favorites)-1))
	case TabHistory:
		prevCursor := m.history.cursor
		m.history.cursor = clamp(m.history.cursor+delta, 0, max(0, m.history.numRows()-1))
		m.history.ensureVisible()
		if m.history.cursor != prevCursor {
			// DISABLED: auto-compare on scroll (bug analysis)
			return m.loadHistoryContent()
		}
	}
	return nil
}

func (m *App) scrollRight(delta int) {
	switch m.activeTab {
	case TabTree:
		if m.treeMode == TreeViewDetails {
			if m.previewReady {
				if delta < 0 {
					m.previewVP.LineUp(3)
				} else {
					m.previewVP.LineDown(3)
				}
			}
			return
		}
		files := m.filelist.rows()
		m.filelist.cursor = clamp(m.filelist.cursor+delta, 0, max(0, len(files)-1))
		m.filelist.ensureVisible()
	case TabFavorites:
		files := m.filelist.rows()
		m.filelist.cursor = clamp(m.filelist.cursor+delta, 0, max(0, len(files)-1))
		m.filelist.ensureVisible()
	case TabHistory:
		if m.history.ready {
			if delta < 0 {
				m.history.viewport.LineUp(3)
			} else {
				m.history.viewport.LineDown(3)
			}
		}
	}
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

func loadDirStatus(executor *cvs.CVSExecutor, dir string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		args := []string{"status", "-l"}
		if dir != "." {
			args = append(args, dir)
		}
		result, _ := executor.RunReadOnly(args...)
		if result == nil {
			return dirStatusMsg{dir: dir, epoch: epoch}
		}
		return dirStatusMsg{dir: dir, statuses: cvs.ParseStatus(result.Stdout), epoch: epoch}
	}
}

// backgroundDirScan walks the working directory, collects all CVS-managed
// directories, and returns a tea.Batch that runs loadDirStatus for each one.
// Because loadDirStatus uses RunReadOnly (shared lock), all directory scans
// run concurrently. Results arrive as individual dirStatusMsg messages and
// render progressively.
func backgroundDirScan(executor *cvs.CVSExecutor, epoch uint64, scope string) tea.Cmd {
	root := executor.WorkDir
	if scope != "" {
		root = filepath.Join(executor.WorkDir, scope)
	}
	var dirs []string
	filepath.WalkDir(root, func(path string, d ioFS.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "CVS" || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		cvsDir := filepath.Join(path, "CVS")
		if _, err := os.Stat(cvsDir); err != nil {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(executor.WorkDir, path)
		if rel == "" {
			rel = "."
		}
		dirs = append(dirs, rel)
		return nil
	})
	cmds := make([]tea.Cmd, len(dirs))
	for i, dir := range dirs {
		cmds[i] = loadDirStatus(executor, dir, epoch)
	}
	return tea.Batch(cmds...)
}

func cvsStatusCode(status string) string {
	switch status {
	case "Locally Modified":
		return "M"
	case "Locally Added":
		return "A"
	case "Locally Removed":
		return "R"
	case "Needs Update", "Needs Patch":
		return "U"
	case "Needs Merge":
		return "M"
	case "File had conflicts on merge", "Unresolved Conflict":
		return "C"
	case "Unknown":
		// `cvs status` calls untracked files "Unknown" while `cvs update -n`
		// uses "?". Map both to the same code so the TUI shows ? consistently.
		return "?"
	default:
		return ""
	}
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
