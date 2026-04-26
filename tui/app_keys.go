package tui

import (
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
	{keys.Tab1, func(m *App) tea.Cmd { m.activeTab = TabTree; return nil }},
	{keys.Tab2, func(m *App) tea.Cmd { m.activeTab = TabFavorites; return nil }},
	{keys.Tab3, func(m *App) tea.Cmd {
		m.activeTab = TabStaged
		m.staged.Refresh(m.filelist.marked, m.statusMap)
		paths := m.filelist.MarkedFiles()
		if len(paths) > 0 {
			return refreshStagedFiles(m.exec, paths)
		}
		return nil
	}},
	{keys.Tab4, func(m *App) tea.Cmd {
		m.activeTab = TabHistory
		return m.autoLoadHistory()
	}},
	{keys.FocusL, func(m *App) tea.Cmd { m.focus = PanelLeft; return nil }},
	{keys.FocusR, func(m *App) tea.Cmd { m.focus = PanelRight; return nil }},
	{keys.FocusC, func(m *App) tea.Cmd { m.focus = PanelConsole; return nil }},
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
	case key.Matches(msg, keys.Escape), msg.Type == tea.KeyLeft:
		m.focus = PanelLeft
		m.staged.input.Blur()
		m.staged.mode = StagedActions
		return nil, true
	case key.Matches(msg, keys.Enter):
		// Universal commit: include ? (cvs-add first), A (initial commit),
		// M and C (content commit), R (commit the deletion). The commitMsg
		// handler in App.Update centralizes the conflict-marker check and
		// dispatches doCommit. Both commit paths (Staged Enter, Tree-tab
		// DialogCommit) emit commitMsg so the check runs in exactly one
		// place.
		untracked := m.staged.PathsByStatus("?")
		commitFiles := m.staged.PathsByStatus("?", "A", "M", "C", "R")
		if m.staged.input.Value() == "" || len(commitFiles) == 0 {
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
		// Cycle focus: Left → Right → Console → Left
		switch m.focus {
		case PanelLeft:
			m.focus = PanelRight
		case PanelRight:
			m.focus = PanelConsole
		case PanelConsole:
			m.focus = PanelLeft
		}
		return nil, true
	case key.Matches(msg, keys.BackTab):
		switch m.focus {
		case PanelLeft:
			m.focus = PanelConsole
		case PanelRight:
			m.focus = PanelLeft
		case PanelConsole:
			m.focus = PanelRight
		}
		return nil, true
	case msg.Type == tea.KeyRight && m.focus == PanelLeft:
		// Staged in actions mode is single-panel — there's no right side
		// to switch to. Consume silently rather than moving focus into a
		// non-rendered area.
		if m.activeTab == TabStaged && m.staged.mode == StagedActions {
			return nil, true
		}
		m.focus = PanelRight
		if m.activeTab == TabStaged {
			m.staged.input.Focus()
		}
		return nil, true
	case msg.Type == tea.KeyLeft && m.focus == PanelRight:
		m.focus = PanelLeft
		if m.activeTab == TabStaged {
			m.staged.input.Blur()
		}
		return nil, true

	// vim-style h/l: tree expand/collapse takes precedence; otherwise switch
	// focus between left and right panels — same effect as the arrow keys.
	case msg.String() == "l" && m.focus == PanelLeft:
		if m.activeTab == TabTree && m.tree.CanExpand() {
			return nil, false // fall through to delegateKey → tree expands
		}
		if m.activeTab == TabStaged && m.staged.mode == StagedActions {
			return nil, true // single-panel; no right to switch to
		}
		m.focus = PanelRight
		if m.activeTab == TabStaged {
			m.staged.input.Focus()
		}
		return nil, true
	case msg.String() == "h" && m.focus == PanelLeft:
		if m.activeTab == TabTree && m.tree.CanCollapse() {
			return nil, false // fall through; tree collapses or moves up
		}
		// No panel left of the left one — silently consume so the key
		// doesn't propagate into the focused list as a no-op nav action.
		return nil, true
	case msg.String() == "h" && m.focus == PanelRight:
		m.focus = PanelLeft
		if m.activeTab == TabStaged {
			m.staged.input.Blur()
		}
		return nil, true
	case msg.String() == "l" && m.focus == PanelRight:
		// No panel right of the right one — silently consume.
		return nil, true
	case key.Matches(msg, keys.Update):
		if m.activeTab == TabTree || m.activeTab == TabFavorites {
			return m.doUpdateSelected(), true
		}
		return m.refreshStatus(), true
	case msg.String() == "U" && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		target := m.selectedTarget()
		if target != "" {
			m.dialog.OpenForceUpdate([]string{target})
		}
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
		if m.activeTab == TabHistory && m.history.HasCompare() {
			m.history.ClearCompare()
			return m.loadHistoryContent(), true
		}
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
			selectedPath = m.history.path
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
		case key.Matches(msg, keys.Diff) && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			m.activeTab = TabHistory
			return loadHistory(m.exec, selectedPath, m.fileHasLocalChanges(selectedPath))
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
		case key.Matches(msg, keys.Commit):
			return m.openCommitDialog()
		case key.Matches(msg, keys.Revert) && selectedPath != "":
			m.dialog.OpenRevert(selectedPath)
			return nil
		case key.Matches(msg, keys.Remove) && selectedPath != "":
			m.dialog.OpenRemove(selectedPath, m.statusMap[selectedPath])
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
		case msg.String() == "p" && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			data, err := os.ReadFile(fullPath)
			if err == nil {
				content := string(data)
				// Limit preview to ~500 lines
				if lines := strings.Split(content, "\n"); len(lines) > 500 {
					content = strings.Join(lines[:500], "\n") + "\n..."
				}
				m.dialog.OpenPreview(selectedPath, content)
			}
			return nil
		case key.Matches(msg, keys.Add) && selectedFile != nil && selectedFile.Status == "?":
			return m.addFile(selectedPath)
		case key.Matches(msg, keys.Ignore) && selectedFile != nil && selectedFile.Status == "?":
			m.dialog.OpenIgnore(selectedPath)
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
					return tea.Batch(cmd, previewCmd)
				}
				m.updateFileList()
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
			case key.Matches(msg, keys.Space), msg.String() == "x":
				if path := m.staged.SelectedPath(); path != "" {
					delete(m.filelist.marked, path)
					m.staged.Refresh(m.filelist.marked, m.statusMap)
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
				// Revert: revert all M files
				if paths := m.staged.PathsByStatus("M"); len(paths) > 0 {
					return m.stagedBulkAction("revert", paths)
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
					return m.stagedBulkAction("update", paths)
				}
			case msg.String() == "U":
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
			prevCursor := m.history.cursor
			prevMode := m.history.mode
			var cmd tea.Cmd
			m.history, cmd = m.history.Update(msg)
			// Auto-load content when cursor moves, mode changes, or Enter
			if m.history.cursor != prevCursor || m.history.mode != prevMode || key.Matches(msg, keys.Enter) {
				return tea.Batch(cmd, m.loadHistoryContent())
			}
			return cmd
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
			var cmd tea.Cmd
			m.history, cmd = m.history.Update(msg)
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
