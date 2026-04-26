package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"lazycvs/fs"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *App) updateFileList() {
	var dir string
	switch m.activeTab {
	case TabTree:
		node := m.tree.SelectedNode()
		if node == nil {
			return
		}
		if node.IsDir {
			dir = node.Path
		} else {
			// Show parent directory files
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

	for _, e := range entries {
		name := e.Name()
		if name == "CVS" || name == ".cvsignore" || name == ".DS_Store" || strings.HasPrefix(name, ".#") {
			continue
		}

		path := prefix + name
		if dir == "." {
			path = name
		}

		if e.IsDir() {
			sg := SubDirGroup{
				Name:   name,
				Path:   path,
				Counts: countsByPrefix(path+"/", m.statusMap),
			}
			// Scan subdir for files (one level deep)
			subAbsDir := filepath.Join(absDir, name)
			subEntries, err := os.ReadDir(subAbsDir)
			if err == nil {
				for _, se := range subEntries {
					sn := se.Name()
					if sn == "CVS" || sn == ".cvsignore" || sn == ".DS_Store" || strings.HasPrefix(sn, ".#") || se.IsDir() {
						continue
					}
					sp := path + "/" + sn
					var sz int64
					if info, err := se.Info(); err == nil {
						sz = info.Size()
					}
					st := ""
					if s, ok := m.statusMap[sp]; ok {
						st = s
					}
					sg.Files = append(sg.Files, cvs.FileEntry{Path: sp, Status: st, Size: sz})
				}
			}
			subDirs = append(subDirs, sg)
			continue
		}

		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		status := ""
		if s, ok := m.statusMap[path]; ok {
			status = s
		}
		files = append(files, cvs.FileEntry{Path: path, Status: status, Size: size})
	}

	m.filelist.SetFiles(dir, files, subDirs)
}

func (m *App) openCommitDialog() tea.Cmd {
	marked := m.filelist.MarkedFiles()
	if len(marked) > 0 {
		statuses := make(map[string]string, len(marked))
		for _, p := range marked {
			statuses[p] = m.statusMap[p]
		}
		m.dialog.OpenCommit(marked, statuses)
		return nil
	}
	// If nothing marked, commit the selected modified file
	if f := m.filelist.SelectedFile(); f != nil && f.Status == "M" {
		m.dialog.OpenCommit([]string{f.Path}, map[string]string{f.Path: f.Status})
		return nil
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
	// `add` transitions the file from ? to A — the user typically wants to
	// commit it immediately afterwards, so keep it marked. revert/update
	// "finish" the file's role in the staged set, so we drop it from marked
	// to make the Staged view reflect what's still pending.
	if action != "add" {
		for _, p := range paths {
			delete(m.filelist.marked, p)
		}
	}
	// Optimistic statusMap update — done before the async cvs run so other
	// tabs already see the expected outcome when the user navigates away.
	switch action {
	case "add":
		m.applyOptimisticStatus(paths, "A") // ? → A
	case "revert":
		m.applyOptimisticStatus(paths, "") // M → clean
	case "update":
		// Outcome is uncertain (could be clean, M, or C with new conflicts).
		// Skip the optimistic update and let the async refresh decide.
	}
	m.staged.Refresh(m.filelist.marked, m.statusMap)
	exec := m.exec
	switch action {
	case "add":
		return func() tea.Msg {
			for _, p := range paths {
				exec.Run("add", p)
			}
			result, _ := exec.DryRunUpdate()
			return statusRefreshedMsg{result: result}
		}
	case "revert":
		return func() tea.Msg {
			for _, p := range paths {
				exec.Run("update", "-C", p)
			}
			result, _ := exec.DryRunUpdate()
			return statusRefreshedMsg{result: result}
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
	m.staged.Refresh(m.filelist.marked, m.statusMap)
	return m.refreshStatus()
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
func (m *App) doUpdateSelected() tea.Cmd {
	node := m.tree.SelectedNode()
	if node == nil {
		return m.refreshStatus()
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
	// Optimistic: ? → A. Status map flips immediately so the Tree marker
	// is up-to-date even before the async cvs add finishes.
	m.applyOptimisticStatus([]string{path}, "A")
	exec := m.exec
	return func() tea.Msg {
		exec.Run("add", path)
		// Reconcile via DryRunUpdate so the rest of the repo state stays
		// consistent (the previous version of this function returned an
		// empty statusRefreshedMsg, which silently skipped the rebuild).
		result, _ := exec.DryRunUpdate()
		return statusRefreshedMsg{result: result}
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

// applyOptimisticStatus updates statusMap for the given paths to the expected
// new status, then rebuilds tree/file-list/favorites views. Use right after
// dispatching an action whose CVS command is still running but whose outcome
// is predictable, so the UI reflects the new state immediately. The async
// status refresh that follows reconciles any remaining discrepancy.
//
// newStatus="" clears the entry (file becomes clean).
func (m *App) applyOptimisticStatus(paths []string, newStatus string) {
	for _, p := range paths {
		if newStatus == "" {
			delete(m.statusMap, p)
		} else {
			m.statusMap[p] = newStatus
		}
	}
	m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
	m.tree.rebuildFlat()
	m.favorites.UpdateCounts(m.statusMap)
	m.updateFileList()
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

	if fs.IsBinary(filepath.Join(m.exec.WorkDir, path)) {
		return func() tea.Msg {
			return historyContentMsg{content: "Binary file — cannot display content"}
		}
	}

	// Compare mode: combinations of working-copy and revisions on either side.
	if m.history.mode == HistoryCompare {
		toIsWorking := m.history.IsWorkingCopyRow() || m.history.compareToWorking
		fromIsWorking := m.history.CompareFromIsWorking()
		fromRev := m.history.CompareFromRev()
		toRev := m.history.SelectedRevision()

		switch {
		case fromIsWorking && toRev != nil:
			return loadCompareWorking(m.exec, path, toRev.Number)
		case toIsWorking && fromRev != nil:
			return loadCompareWorking(m.exec, path, fromRev.Number)
		case fromRev != nil && toRev != nil:
			return loadCompare(m.exec, path, fromRev.Number, toRev.Number)
		}
		return nil
	}

	// Working-copy pseudo-row: read on-disk content / run plain `cvs diff`.
	if m.history.IsWorkingCopyRow() {
		switch m.history.mode {
		case HistoryContent:
			return loadWorkingContent(m.exec.WorkDir, path)
		case HistoryDiff:
			return loadWorkingDiff(m.exec, path)
		case HistoryBlame:
			// Blame on uncommitted state isn't meaningful — fall back to HEAD blame.
			return loadBlame(m.exec, path)
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
		return loadRevisionContent(m.exec, path, rev.Number)
	case HistoryDiff:
		if rev.PrevNumber != "" {
			return loadRevisionDiff(m.exec, path, rev.PrevNumber, rev.Number)
		}
		return loadRevisionContent(m.exec, path, rev.Number)
	case HistoryBlame:
		return loadBlame(m.exec, path)
	}
	return nil
}

func refreshStagedFiles(exec *cvs.CVSExecutor, paths []string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"status"}, paths...)
		result, _ := exec.Run(args...)
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
		result, _ := exec.Run("diff", "-u", path)
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
		// Global pattern
		e := ext(msg.path)
		if e != "" {
			m.cfgMgr.Update(func(cfg *config.Config) {
				cfg.Ignore.GlobalPatterns = append(cfg.Ignore.GlobalPatterns, "*"+e)
			})
		}
	case 3:
		// Global exact
		m.cfgMgr.Update(func(cfg *config.Config) {
			cfg.Ignore.GlobalPatterns = append(cfg.Ignore.GlobalPatterns, base(msg.path))
		})
	}
	return m.refreshStatus()
}

func (m *App) handleConflictResolve(msg conflictResolveMsg) tea.Cmd {
	fullPath := filepath.Join(m.exec.WorkDir, msg.path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil
	}
	resolved := cvs.ResolveConflict(string(data), msg.choice, 0) // 0 = all regions
	os.WriteFile(fullPath, []byte(resolved), 0644)
	return m.refreshStatus()
}

func (m *App) handleMouse(msg tea.MouseMsg) tea.Cmd {
	leftW, _, contentH, consoleH := m.layout()
	x, y := msg.X, msg.Y

	// Layout: main panel (0..contentH+1), console (contentH+2..contentH+consoleH+3), keybar
	mainTop := 0                         // top border of main content
	mainBottom := mainTop + contentH + 1 // bottom border
	consoleTop := mainBottom + 1         // top border of console
	consoleBottom := consoleTop + consoleH + 1

	// Main content area (between borders)
	if y > mainTop && y < mainBottom {
		contentRow := y - mainTop - 1

		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if x < leftW {
				m.focus = PanelLeft
				m.clickLeft(contentRow)
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
				m.scrollLeft(delta)
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

func (m *App) clickLeft(row int) {
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
		idx := m.history.offset + row - 1 // header row
		if idx >= 0 && idx < len(m.history.revisions) {
			m.history.cursor = idx
		}
	}
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

func (m *App) scrollLeft(delta int) {
	switch m.activeTab {
	case TabTree:
		m.tree.cursor = clamp(m.tree.cursor+delta, 0, max(0, len(m.tree.flat)-1))
		m.tree.ensureVisible()
		m.updateFileList()
	case TabFavorites:
		m.favorites.cursor = clamp(m.favorites.cursor+delta, 0, max(0, len(m.favorites.favorites)-1))
	case TabHistory:
		m.history.cursor = clamp(m.history.cursor+delta, 0, max(0, len(m.history.revisions)-1))
		m.history.ensureVisible()
	}
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

func loadDirStatus(executor *cvs.CVSExecutor, dir string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"status", "-l"}
		if dir != "." {
			args = append(args, dir)
		}
		result, _ := executor.Run(args...)
		if result == nil {
			return dirStatusMsg{dir: dir}
		}
		return dirStatusMsg{dir: dir, statuses: cvs.ParseStatus(result.Stdout)}
	}
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
