package tui

import (
	"fmt"
	"lazycvs/cvs"
	"lazycvs/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// globalBindings is a table of context-free key actions handled in
// handleGlobalKey. Each action receives the App and returns a tea.Cmd (or
// nil). First match wins. Bindings whose behavior depends on focus/tab/mode
// stay in the switch below — only put zero-context entries here.
var globalBindings = []struct {
	binding key.Binding
	action  func(*App) tea.Cmd
}{
	{keys.Quit, func(*App) tea.Cmd { return tea.Quit }},
	{keys.Tab1, func(m *App) tea.Cmd {
		// Switching to Files always feels like "go check what changed";
		// kick off a status refresh so the listing the user lands on is
		// fresh, with the ⟳ banner as proof the refresh ran.
		previous := m.activeTab
		m.activeTab = TabTree
		if previous != TabTree {
			m.setProgress("⟳ Refreshing status…")
			return m.refreshStatusUser()
		}
		return nil
	}},
	{keys.Tab2, func(m *App) tea.Cmd { m.activeTab = TabFavorites; return nil }},
	{keys.Tab3, func(m *App) tea.Cmd {
		m.activeTab = TabStaged
		// Auto-mark the selected file if nothing is staged yet
		if len(m.filelist.marked) == 0 {
			var path string
			if f := m.filelist.SelectedFile(); f != nil && f.Status != "" {
				path = f.Path
			} else if node := m.tree.SelectedNode(); node != nil && !node.IsDir && node.Status != "" {
				path = node.Path
			}
			if path != "" {
				m.filelist.marked[path] = true
			}
		}
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		paths := m.filelist.MarkedFiles()
		if len(paths) > 0 {
			return refreshStagedFiles(m.exec, paths)
		}
		return nil
	}},
	{keys.Tab4, func(m *App) tea.Cmd {
		m.activeTab = TabHistory
		m.focus = PanelLeft
		return m.autoLoadHistory()
	}},
	{keys.FocusL, func(m *App) tea.Cmd { m.focus = PanelLeft; return nil }},
	{keys.FocusR, func(m *App) tea.Cmd {
		if m.activeTab == TabStaged && m.staged.mode == StagedActions {
			return nil // single-panel tab; nothing to the right
		}
		m.focus = PanelRight
		if m.activeTab == TabStaged {
			m.staged.input.Focus()
		}
		return nil
	}},
	{keys.FocusC, func(m *App) tea.Cmd { m.focus = PanelConsole; return nil }},
	{keys.FocusUp, func(m *App) tea.Cmd {
		// Up out of the console row lands on the left pane — the
		// "first" pane of the row above. Up from a pane goes to
		// the left pane (or stays put if already there).
		if m.focus == PanelConsole {
			m.focus = PanelLeft
		}
		return nil
	}},
	{keys.Help, func(m *App) tea.Cmd { m.dialog.OpenHelp(); return nil }},
	{keys.Search, func(m *App) tea.Cmd { return m.search.Open(m.exec.WorkDir) }},
}

// handleStagedInput processes a key when the staged-tab commit input is
// focused. Returns (cmd, true) if the input was focused and consumed the key;
// (nil, false) otherwise so the caller can fall through to the next handler.
func (m *App) handleStagedInput(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !(m.activeTab == TabStaged && m.focus == PanelRight && m.staged.input.Focused()) {
		return nil, false
	}
	switch {
	case key.Matches(msg, keys.Escape):
		// Left arrow used to also blur the input + return to the file
		// list, which broke text-cursor movement inside the message
		// (a user fixing a typo with Left would exit the input). Esc
		// is the explicit "back" key now; Ctrl-h works too via the
		// global pane-focus handler.
		m.focus = PanelLeft
		m.staged.input.Blur()
		m.staged.mode = StagedActions
		return nil, true
	case msg.Type == tea.KeyCtrlE:
		// Ctrl+E: open $EDITOR with the current message + a comment
		// block listing the files. Lets the user write a multi-line
		// commit message that wouldn't fit in the single-line input.
		// On editor close, commitMessageEditedMsg lands and either
		// commits (non-empty message) or aborts (empty).
		commitFiles := m.staged.PathsByStatus("?", "A", "M", "C", "R")
		if len(commitFiles) == 0 {
			return nil, true
		}
		return openCommitEditor(
			m.staged.input.Value(),
			commitFiles,
			m.statusesFor(commitFiles),
		), true
	case key.Matches(msg, keys.Enter):
		// Universal commit: include ? (cvs-add first), A (initial commit),
		// M and C (content commit), R (commit the deletion). The commitMsg
		// handler in App.Update centralizes the conflict-marker check and
		// dispatches doCommit. Both commit paths (Staged Enter, Tree-tab
		// DialogCommit) emit commitMsg so the check runs in exactly one
		// place.
		untracked := m.staged.PathsByStatus("?")
		commitFiles := m.staged.PathsByStatus("?", "A", "M", "C", "R")
		if len(commitFiles) == 0 {
			return nil, true
		}
		message := m.staged.input.Value()
		m.staged.input.SetValue("")
		m.staged.input.Blur()
		m.staged.mode = StagedActions
		m.focus = PanelLeft
		return func() tea.Msg {
			return commitMsg{message: message, untracked: untracked, files: commitFiles}
		}, true
	}
	var cmd tea.Cmd
	m.staged.input, cmd = m.staged.input.Update(msg)
	return cmd, true
}

// handleKeyPriority routes a key to the active overlay (dialog or search) if
// one is open. Returns (cmd, true) when consumed; (nil, false) otherwise.
func (m *App) handleKeyPriority(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.dialog.Active() {
		var cmd tea.Cmd
		m.dialog, cmd = m.dialog.Update(msg)
		return cmd, true
	}
	if m.search.active {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return cmd, true
	}
	return nil, false
}

// handleGlobalKey processes top-level keybindings. Returns (cmd, true) when
// the key was consumed; (nil, false) means the caller should fall through to
// the focused-panel delegate.
func (m *App) handleGlobalKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	// Flat, context-free bindings come from the table.
	for _, b := range globalBindings {
		if key.Matches(msg, b.binding) {
			return b.action(m), true
		}
	}

	// Context-dependent bindings: focus/tab/mode-aware.
	switch {
	case key.Matches(msg, keys.Tab):
		// Cycle focus: Left → Right → Console → Left. The Staged tab in
		// actions mode renders a single panel, so the cycle skips the
		// nonexistent right pane there — same guard FocusR applies.
		singlePanel := m.activeTab == TabStaged && m.staged.mode == StagedActions
		switch m.focus {
		case PanelLeft:
			if singlePanel {
				m.focus = PanelConsole
			} else {
				m.focus = PanelRight
			}
		case PanelRight:
			m.focus = PanelConsole
		case PanelConsole:
			m.focus = PanelLeft
		}
		return nil, true
	case key.Matches(msg, keys.BackTab):
		singlePanel := m.activeTab == TabStaged && m.staged.mode == StagedActions
		switch m.focus {
		case PanelLeft:
			m.focus = PanelConsole
		case PanelRight:
			m.focus = PanelLeft
		case PanelConsole:
			if singlePanel {
				m.focus = PanelLeft
			} else {
				m.focus = PanelRight
			}
		}
		return nil, true
	// Arrow keys and h/l navigate WITHIN the focused pane only. Cross-
	// pane movement is on the explicit Ctrl-modified variants (FocusL/R/
	// C/Up), Tab, or the [ / ] aliases. h/l in the tree pane fall
	// through to TreeModel.Update so they collapse/expand the dir under
	// the cursor (the only in-pane horizontal motion that makes sense
	// for a tree).
	case key.Matches(msg, keys.HideIgnored) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		m.filelist.hideIgnored = !m.filelist.hideIgnored
		m.filelist.cursor = 0
		m.filelist.offset = 0
		m.tree.SetHideIgnored(m.filelist.hideIgnored)
		m.updateFileList()
		return nil, true
	case key.Matches(msg, keys.Status):
		m.setProgress("⟳ Refreshing status…")
		return m.refreshStatusUser(), true
	case key.Matches(msg, keys.Update):
		if m.activeTab == TabTree || m.activeTab == TabFavorites {
			if paths := m.filelist.MarkedFiles(); len(paths) > 0 {
				m.setProgress(fmt.Sprintf("⟳ Updating %d file(s)…", len(paths)))
				return m.doUpdatePaths(paths), true
			}
			if target := m.selectedTarget(); target != "" {
				m.setProgress(fmt.Sprintf("⟳ Updating %s…", target))
			}
			return m.doUpdateSelected(), true
		}
		if m.activeTab == TabStaged {
			// The Staged tab has its own bulk-update handler in
			// delegateKey (cvs update over the staged set) — fall
			// through so it actually receives the key. Consuming it
			// here left the keybar's `u:update` advertising a no-op.
			return nil, false
		}
		// History is read-only with respect to the working copy. `u`
		// would surprise the user by mutating state they're trying to
		// inspect, so silently consume it.
		return nil, true
	case key.Matches(msg, keys.ForceUpdate) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		if paths := m.filelist.MarkedFiles(); len(paths) > 0 {
			m.dialog.OpenForceUpdate(paths)
		} else if target := m.selectedTarget(); target != "" {
			m.dialog.OpenForceUpdate([]string{target})
		}
		return nil, true
	case key.Matches(msg, keys.ListMode) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		// Cycle the filelist's flat → sub → tree view mode regardless of
		// which panel is focused. Previously this only worked when the
		// right pane was focused; users with the cursor on the tree pane
		// pressed `f`, saw nothing happen, and had to switch focus first.
		m.filelist.CycleViewMode()
		return nil, true
	case key.Matches(msg, keys.TreeList) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		// `t` jumps straight to tree mode — same focus-agnostic rule as
		// its sibling `f` above.
		m.filelist.SetViewTree()
		return nil, true
	case key.Matches(msg, keys.ViewMode):
		if m.activeTab != TabTree {
			return nil, false
		}
		if m.treeMode == TreeViewFiles {
			m.treeMode = TreeViewDetails
		} else {
			m.treeMode = TreeViewFiles
		}
		return m.applyTreeMode(), true
	case key.Matches(msg, keys.Escape):
		if m.activeTab == TabStaged {
			if m.staged.mode == StagedCommit {
				m.staged.mode = StagedActions
				m.staged.input.Blur()
				m.focus = PanelLeft
				return nil, true
			}
			m.activeTab = TabTree
			return nil, true
		}
		if m.activeTab == TabHistory {
			// In a comparison? Clear the comparison first; only leave
			// the tab on a second Escape.
			if m.history.HasCompare() || m.history.vsWorking {
				m.history.compareAnchor = -1
				m.history.vsWorking = false
				return m.loadHistoryContent(), true
			}
			m.activeTab = TabTree
			return nil, true
		}
		return nil, true
	}
	return nil, false
}

func (m *App) delegateKey(msg tea.KeyMsg) tea.Cmd {
	// File action keys (work when a file is focused in tree or filelist)
	if m.focus == PanelLeft || m.focus == PanelRight {
		var selectedFile *cvs.FileEntry
		var selectedPath string

		if m.activeTab == TabStaged {
			selectedPath = m.staged.SelectedPath()
		} else if m.activeTab == TabHistory {
			// In History the "selected file" is the file whose log is loaded —
			// regardless of which row the cursor is on, file-actions like E
			// (external diff) and e (edit) operate on this file.
			selectedPath = m.history.Path()
		} else if m.focus == PanelRight && !(m.activeTab == TabTree && m.treeMode == TreeViewDetails) {
			selectedFile = m.filelist.SelectedFile()
			if selectedFile != nil {
				selectedPath = selectedFile.Path
			}
		} else if m.activeTab == TabTree {
			node := m.tree.SelectedNode()
			if node != nil && !node.IsDir {
				selectedPath = node.Path
			}
		}

		switch {
		case key.Matches(msg, keys.Diff) && m.activeTab != TabHistory && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			m.activeTab = TabHistory
			return m.openHistoryFor(selectedPath)
		case key.Matches(msg, keys.EditDiff) && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			return m.launchExternalDiffForCurrent(selectedPath)
		case key.Matches(msg, keys.Merge) && selectedPath != "" && m.statusMap[selectedPath] == "C":
			return m.launchExternalMergeFor(selectedPath)
		case key.Matches(msg, keys.Edit) && selectedPath != "":
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			return openEditor(fullPath)
		case key.Matches(msg, keys.Open) && selectedPath != "":
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			return openInOS(fullPath)
		case key.Matches(msg, keys.Commit) && m.activeTab != TabHistory && m.activeTab != TabStaged:
			// Staged tab handles `c` further down: it switches to the
			// in-pane commit input instead of the modal dialog. Without
			// this gate the dialog opened on top of the Staged tab and
			// the in-pane path was unreachable.
			return m.openCommitDialog()
		case key.Matches(msg, keys.Revert) && selectedPath != "" && m.activeTab != TabHistory && m.activeTab != TabStaged:
			// Staged tab handles `r` further down as a bulk revert
			// over every M/C file at once — falling through to the
			// per-file dialog there would force the user to confirm
			// 18 times for an 18-file conflict set.
			m.dialog.OpenRevert(selectedPath)
			return nil
		case key.Matches(msg, keys.Remove) && selectedPath != "" && m.activeTab != TabHistory:
			// If files are marked, treat D as a bulk remove and surface
			// every path in the dialog so the user can review before
			// confirming. Otherwise fall back to the single-file flow on
			// the cursor row.
			paths := m.filelist.MarkedFiles()
			if len(paths) == 0 {
				paths = []string{selectedPath}
			}
			m.dialog.OpenRemove(paths, m.statusesFor(paths))
			return nil
		case key.Matches(msg, keys.RestoreBackup) && m.activeTab != TabHistory:
			// Three modes, in priority order:
			//   1. Marked files: explicit "restore these" set.
			//   2. Cursor on a dir tree node: scan its subtree for
			//      .lazycvs-backup files and restore each. This is
			//      the typical "undo bulk revert" path — the user
			//      doesn't have to mark anything, just navigate to
			//      the affected dir and press B once.
			//   3. Cursor on a file: single-pair restore.
			if paths := m.filelist.MarkedFiles(); len(paths) > 0 {
				return m.restoreFromBackupBulk(paths)
			}
			if m.activeTab == TabTree && m.focus == PanelLeft {
				if node := m.tree.SelectedNode(); node != nil && node.IsDir {
					return m.restoreBackupsInSubtree(node.Path)
				}
			}
			if selectedPath != "" {
				return m.restoreFromBackup(selectedPath)
			}
			return nil
		case key.Matches(msg, keys.Space) && m.focus == PanelLeft && (m.activeTab == TabTree || m.activeTab == TabFavorites):
			node := m.tree.SelectedNode()
			if node != nil {
				if node.IsDir {
					m.toggleDirFiles(node.Path)
				} else if node.Status != "" {
					if m.filelist.marked[node.Path] {
						delete(m.filelist.marked, node.Path)
					} else {
						m.filelist.marked[node.Path] = true
					}
				}
			}
			return nil
		case key.Matches(msg, keys.MarkAll) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
			// Mark every changed file in the directory subtree the user
			// is currently looking at. Resolution order:
			//  - cursor on a directory tree node → that subtree
			//  - otherwise → the directory shown in the right-pane file
			//    list (works from either panel and from the Favorites tab)
			target := ""
			if node := m.tree.SelectedNode(); node != nil && node.IsDir {
				target = node.Path
			} else if m.filelist.dir != "" {
				target = m.filelist.dir
			}
			if target != "" {
				m.toggleDirFiles(target)
			}
			return nil
		case key.Matches(msg, keys.FavAdd) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
			return m.addFavorite()
		case key.Matches(msg, keys.FavDel) && m.activeTab == TabFavorites:
			return m.removeFavorite()
		case key.Matches(msg, keys.Preview) && m.activeTab != TabHistory && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			data, err := os.ReadFile(fullPath)
			if err == nil {
				content := cvs.EnsureUTF8(string(data))
				// Limit preview to ~500 lines
				if lines := strings.Split(content, "\n"); len(lines) > 500 {
					content = strings.Join(lines[:500], "\n") + "\n..."
				}
				m.dialog.OpenPreview(selectedPath, content)
			}
			return nil
		case key.Matches(msg, keys.Add) && selectedFile != nil && selectedFile.Status == "?" && m.activeTab != TabHistory:
			m.setProgress(fmt.Sprintf("⟳ Adding %s…", filepath.Base(selectedPath)))
			return m.addFile(selectedPath)
		case key.Matches(msg, keys.Ignore) && m.activeTab != TabHistory:
			// If any marked files are untracked, bulk-ignore them
			// (write each to its directory's .cvsignore). Otherwise
			// fall back to the single-file ignore dialog on the
			// cursor row, but only if it's actually a "?" — ignore
			// has no meaning for tracked files.
			marked := m.filelist.MarkedFiles()
			var untracked []string
			for _, p := range marked {
				if m.statusMap[p] == "?" {
					untracked = append(untracked, p)
				}
			}
			if len(untracked) > 0 {
				return m.stagedBulkIgnore(untracked)
			}
			if selectedPath != "" && m.statusMap[selectedPath] == "?" {
				m.dialog.OpenIgnore(selectedPath)
			}
			return nil
		}
	}

	// Delegate navigation keys to the focused panel
	switch m.focus {
	case PanelLeft:
		switch m.activeTab {
		case TabTree:
			var cmd tea.Cmd
			m.tree, cmd = m.tree.Update(msg)
			if key.Matches(msg, keys.Enter) || key.Matches(msg, keys.Down) || key.Matches(msg, keys.Up) {
				if m.treeMode == TreeViewDetails {
					previewCmd := m.autoLoadPreview()
					if key.Matches(msg, keys.Enter) {
						// yazi-style: Enter selects + crosses into the
						// preview pane so the next keypress acts on
						// what the user just opened.
						m.focus = PanelRight
					}
					return tea.Batch(cmd, previewCmd)
				}
				m.updateFileList()
				if key.Matches(msg, keys.Enter) {
					// In Files layout, Enter on a tree dir moved
					// expand state + filled the file list; jump the
					// cursor across so the user can act on those
					// files without an extra Tab / Ctrl-l keystroke.
					m.focus = PanelRight
				}
			}
			return cmd
		case TabFavorites:
			var cmd tea.Cmd
			m.favorites, cmd = m.favorites.Update(msg)
			if key.Matches(msg, keys.Enter) {
				m.updateFileList()
			}
			return cmd
		case TabStaged:
			switch {
			case key.Matches(msg, keys.Down):
				if m.staged.cursor < len(m.staged.files)-1 {
					m.staged.cursor++
					m.staged.ensureVisible()
				}
			case key.Matches(msg, keys.Up):
				if m.staged.cursor > 0 {
					m.staged.cursor--
					m.staged.ensureVisible()
				}
			case key.Matches(msg, keys.Top):
				m.staged.cursor = 0
				m.staged.ensureVisible()
			case key.Matches(msg, keys.Bottom):
				m.staged.cursor = max(0, len(m.staged.files)-1)
				m.staged.ensureVisible()
			case key.Matches(msg, keys.PageDown):
				m.staged.cursor = clamp(m.staged.cursor+m.staged.height-1, 0, max(0, len(m.staged.files)-1))
				m.staged.ensureVisible()
			case key.Matches(msg, keys.PageUp):
				m.staged.cursor = clamp(m.staged.cursor-(m.staged.height-1), 0, max(0, len(m.staged.files)-1))
				m.staged.ensureVisible()
			case key.Matches(msg, keys.Space):
				if path := m.staged.SelectedPath(); path != "" {
					delete(m.filelist.marked, path)
					m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
				}
			case key.Matches(msg, keys.Commit):
				// Commit: switch to commit mode for everything that can end
				// up in a commit. ? files get cvs-added first as part of
				// the same operation; A files get an initial commit;
				// M/C files get a content commit.
				if paths := m.staged.PathsByStatus("?", "A", "M", "C"); len(paths) > 0 {
					m.staged.mode = StagedCommit
					m.focus = PanelRight
					m.staged.input.Focus()
				}
			case key.Matches(msg, keys.Revert):
				// Bulk-revert every M *and* C file in the staged set.
				// Opens a confirmation dialog first — the action is
				// destructive (cvs update -C / rm + cvs update,
				// depending on per-path status), and confirmations are
				// the standard pattern for the equivalent file-action
				// `r` in other tabs. The dialog dispatch carries the
				// status map across so the action handler picks the
				// right cvs sequence per file.
				if paths := m.staged.PathsByStatus("M", "C"); len(paths) > 0 {
					statuses := make(map[string]string, len(paths))
					for _, p := range paths {
						statuses[p] = m.statusMap[p]
					}
					m.dialog.OpenRevertBulk(paths, statuses)
					return nil
				}
			case key.Matches(msg, keys.Ignore):
				// Ignore: add all ? files to .cvsignore
				if paths := m.staged.PathsByStatus("?"); len(paths) > 0 {
					return m.stagedBulkIgnore(paths)
				}
			case key.Matches(msg, keys.Update):
				// Update: cvs update selected files
				if len(m.staged.files) > 0 {
					var paths []string
					for _, f := range m.staged.files {
						paths = append(paths, f.path)
					}
					m.setProgress(fmt.Sprintf("⟳ Updating %d file(s)…", len(paths)))
					return m.stagedBulkAction("update", paths)
				}
			case key.Matches(msg, keys.ForceUpdate):
				// Force update: cvs update -C (needs confirmation)
				if paths := m.staged.PathsByStatus("M", "C"); len(paths) > 0 {
					m.dialog.OpenForceUpdate(paths)
				}
			case key.Matches(msg, keys.Enter):
				m.staged.mode = StagedCommit
				m.focus = PanelRight
				m.staged.input.Focus()
			}
			return nil
		case TabHistory:
			return m.delegateHistoryKey(msg)
		}
	case PanelRight:
		switch m.activeTab {
		case TabTree:
			if m.treeMode == TreeViewDetails {
				if m.previewReady {
					var cmd tea.Cmd
					m.previewVP, cmd = m.previewVP.Update(msg)
					return cmd
				}
				return nil
			}
			var cmd tea.Cmd
			m.filelist, cmd = m.filelist.Update(msg)
			return cmd
		case TabFavorites:
			var cmd tea.Cmd
			m.filelist, cmd = m.filelist.Update(msg)
			return cmd
		case TabStaged:
			// Input handling is done by the priority check above
			// If we get here, input is not focused — Enter focuses it
			if key.Matches(msg, keys.Enter) {
				m.staged.input.Focus()
			}
			return nil
		case TabHistory:
			// Mode-toggle keys (p side-by-side, d diff, b blame, w
			// vs-working, space anchor, Enter content) work from
			// either panel — they change *what* is shown, which is a
			// cross-panel concern. Everything else scrolls the
			// right-pane viewport.
			if key.Matches(msg, keys.SideBySide) || key.Matches(msg, keys.Diff) ||
				key.Matches(msg, keys.Blame) || key.Matches(msg, keys.CompareWorking) ||
				key.Matches(msg, keys.Space) || key.Matches(msg, keys.Enter) {
				return m.delegateHistoryKey(msg)
			}
			var cmd tea.Cmd
			m.history.viewport, cmd = m.history.viewport.Update(msg)
			return cmd
		}
	case PanelConsole:
		// Console resize
		switch msg.String() {
		case "+", "=":
			maxH := m.height / 2
			m.consoleHeight = min(m.consoleHeight+3, maxH)
			m.updateSizes()
			return nil
		case "-", "_":
			m.consoleHeight = max(m.consoleHeight-3, 3)
			m.updateSizes()
			return nil
		}
		var cmd tea.Cmd
		m.console, cmd = m.console.Update(msg)
		return cmd
	}

	return nil
}

// delegateHistoryKey runs a key through HistoryModel.Update and, if the
// observable state changed (cursor, mode, anchor, vs-working) or the
// key was Enter, also dispatches loadHistoryContent so the right pane
// reflects the new selection. Shared by both panels because the
// History tab's mode-toggle keys (p/d/b/w/space/Enter) are valid no
// matter which side has focus.
func (m *App) delegateHistoryKey(msg tea.KeyMsg) tea.Cmd {
	// `R` (Restore) intercepts before HistoryModel.Update so the
	// confirmation dialog (or direct dispatch on a clean working
	// copy) lands consistently regardless of focus.
	if key.Matches(msg, keys.Restore) {
		return m.openRestoreRevDialog()
	}
	prevCursor := m.history.cursor
	prevMode := m.history.mode
	prevAnchor := m.history.compareAnchor
	prevWorking := m.history.vsWorking
	var cmd tea.Cmd
	m.history, cmd = m.history.Update(msg)
	if m.history.cursor != prevCursor ||
		m.history.mode != prevMode ||
		m.history.compareAnchor != prevAnchor ||
		m.history.vsWorking != prevWorking ||
		key.Matches(msg, keys.Enter) {
		contentCmd := m.loadHistoryContent()
		return tea.Batch(cmd, contentCmd)
	}
	return cmd
}

// openRestoreRevDialog gathers the selected revision, the History
// path, and the current status; on a clean file dispatches the
// restore directly (no need to interrupt the user), otherwise opens
// the confirmation dialog so the user can opt in to overwriting M/C/A
// state. No-op when the cursor is on the working pseudo-row.
func (m *App) openRestoreRevDialog() tea.Cmd {
	rev := m.history.SelectedRevision()
	path := m.history.Path()
	if rev == nil || path == "" || m.history.IsWorkingCopyRow() {
		return nil
	}
	status := m.statusMap[path]
	switch status {
	case "M", "C", "A":
		m.dialog.OpenRestoreRev(path, rev.Number, status)
		return nil
	default:
		// Clean working copy — just dispatch.
		return func() tea.Msg {
			return restoreRevMsg{path: path, rev: rev.Number}
		}
	}
}
