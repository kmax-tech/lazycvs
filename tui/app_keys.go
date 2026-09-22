package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/fs"
	"path/filepath"
	"time"

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
	{keys.Quit, func(m *App) tea.Cmd {
		// Marks and a typed commit message are work the user cannot
		// get back after the process exits. One confirming keystroke
		// is cheaper than losing a twenty-file worklist to a stray q.
		if pending := m.unsavedWork(); pending != "" && !m.quitPending {
			m.quitPending = true
			return m.setResult(pending+" — press q again to quit", false)
		}
		return tea.Quit
	}},
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
		// Looking at the marked set must not change it. Entering this
		// tab used to mark the file under the cursor whenever nothing
		// was marked yet — a silent edit to the worklist, triggered by
		// navigation, that the next bulk action then acted on.
		m.activeTab = TabMarked
		m.markedTab.Refresh(m.filelist.marked, m.resolveFileStatus)
		paths := m.filelist.MarkedFiles()
		if len(paths) > 0 {
			return refreshMarkedFiles(m.exec, paths)
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
		if m.activeTab == TabMarked && m.markedTab.mode == MarkedActions {
			return nil // single-panel tab; nothing to the right
		}
		m.focus = PanelRight
		if m.activeTab == TabMarked {
			m.markedTab.input.Focus()
		}
		return nil
	}},
	{keys.FocusC, func(m *App) tea.Cmd {
		// Coming from a failure banner, the console opens on the
		// errors filter: "see Console" is only useful if the console
		// then shows the failure rather than the fifty successful
		// commands that came after it.
		if m.bannerPointsToConsole() {
			m.focusConsoleOnErrors()
		} else {
			m.focus = PanelConsole
		}
		return nil
	}},
	{keys.FocusUp, func(m *App) tea.Cmd {
		// Up out of the console row lands on the left pane — the
		// "first" pane of the row above. Up from a pane goes to
		// the left pane (or stays put if already there).
		if m.focus == PanelConsole {
			m.focus = PanelLeft
		}
		return nil
	}},
	{keys.Help, func(m *App) tea.Cmd {
		m.dialog.OpenHelp(helpSectionFor(m.keyContext()))
		return nil
	}},
	{keys.Search, func(m *App) tea.Cmd { return m.search.Open(m.exec.WorkDir) }},
}

// handleCommitInput processes a key when the Marked tab's commit input is
// focused. Returns (cmd, true) if the input was focused and consumed the key;
// (nil, false) otherwise so the caller can fall through to the next handler.
func (m *App) handleCommitInput(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !(m.activeTab == TabMarked && m.focus == PanelRight && m.markedTab.input.Focused()) {
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
		m.markedTab.input.Blur()
		m.markedTab.mode = MarkedActions
		return nil, true
	case msg.Type == tea.KeyCtrlE:
		// Ctrl+E: open $EDITOR with the current message + a comment
		// block listing the files. Lets the user write a multi-line
		// commit message that wouldn't fit in the single-line input.
		// On editor close, commitMessageEditedMsg lands and either
		// commits (non-empty message) or aborts (empty).
		commitFiles := m.markedTab.CommittablePaths()
		if len(commitFiles) == 0 {
			return m.setResult("Nothing committable marked — statuses must be ?, A, M, C or R", false), true
		}
		return openCommitEditor(
			m.markedTab.input.Value(),
			commitFiles,
			m.statusesFor(commitFiles),
		), true
	case key.Matches(msg, keys.Enter):
		// Universal commit: include ? (cvs-add first), A (initial commit),
		// M and C (content commit), R (commit the deletion). The commitMsg
		// handler in App.Update centralizes the conflict-marker check and
		// dispatches doCommit. Both commit paths (Marked-tab Enter, Tree-tab
		// DialogCommit) emit commitMsg so the check runs in exactly one
		// place.
		untracked := m.markedTab.PathsByStatus("?")
		commitFiles := m.markedTab.CommittablePaths()
		if len(commitFiles) == 0 {
			// Silent-swallow used to read as "commit is broken" — the
			// input ate the Enter and nothing happened, nothing said why.
			return m.setResult("Nothing committable marked — statuses must be ?, A, M, C or R", false), true
		}
		message := m.markedTab.input.Value()
		m.markedTab.input.SetValue("")
		m.markedTab.input.Blur()
		m.markedTab.mode = MarkedActions
		m.focus = PanelLeft
		return func() tea.Msg {
			return commitMsg{message: message, untracked: untracked, files: commitFiles}
		}, true
	}
	var cmd tea.Cmd
	m.markedTab.input, cmd = m.markedTab.input.Update(msg)
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
		// Cycle focus: Left → Right → Console → Left. The Marked tab in
		// actions mode renders a single panel, so the cycle skips the
		// nonexistent right pane there — same guard FocusR applies.
		singlePanel := m.activeTab == TabMarked && m.markedTab.mode == MarkedActions
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
		singlePanel := m.activeTab == TabMarked && m.markedTab.mode == MarkedActions
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
	// Horizontal keys (h/l/←/→) run on a Miller-column axis handled in
	// delegateKey: they fold/expand inside the tree first and cross the
	// pane boundary once there's no in-pane meaning left (→ from a
	// fully-expanded tree row enters the right pane; ← from any right
	// pane returns left). The Ctrl-modified variants (FocusL/R/C/Up) and
	// Tab remain the explicit, unconditional pane jumps.
	case key.Matches(msg, keys.HideIgnored) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		m.filelist.hideIgnored = !m.filelist.hideIgnored
		m.filelist.cursor = 0
		m.filelist.offset = 0
		m.tree.SetHideIgnored(m.filelist.hideIgnored)
		m.updateFileList()
		return nil, true
	case key.Matches(msg, keys.ClearMarks) && m.focus != PanelConsole:
		// Deselect all — drops the whole worklist in one stroke, from
		// any tab. The console keeps its own `x` (clear log); both are
		// the same "clear this" gesture in their context.
		if len(m.filelist.marked) > 0 {
			m.filelist.marked = make(map[string]bool)
			m.markedTab.Refresh(m.filelist.marked, m.resolveFileStatus)
		}
		return nil, true
	case key.Matches(msg, keys.Status):
		m.setProgress("⟳ Refreshing status…")
		return m.refreshStatusUser(), true
	case key.Matches(msg, keys.Update):
		if m.activeTab == TabTree || m.activeTab == TabFavorites {
			if paths := m.filelist.MarkedFiles(); len(paths) > 0 {
				m.setProgress(fmt.Sprintf("⟳ Updating %s…", countLabel(len(paths), "file")))
				return m.doUpdatePaths(paths), true
			}
			if target := m.selectedTarget(); target != "" {
				m.setProgress(fmt.Sprintf("⟳ Updating %s…", target))
			}
			return m.doUpdateSelected(), true
		}
		if m.activeTab == TabMarked {
			// The Marked tab has its own bulk-update handler in
			// delegateKey (cvs update over the marked set) — fall
			// through so it actually receives the key. Consuming it
			// here left the keybar's `u:update` advertising a no-op.
			return nil, false
		}
		// History is read-only with respect to the working copy: `u`
		// there would mutate the very state the user is inspecting.
		// Refusing is right, refusing silently is not — a key that
		// does nothing and says nothing reads as a broken app, which
		// is the same complaint issue 021 collected about dead keys.
		return m.setResult("History is read-only — press 1 to update files", false), true
	case key.Matches(msg, keys.ForceUpdate) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		if paths := m.filelist.MarkedFiles(); len(paths) > 0 {
			m.dialog.OpenForceUpdate(paths)
		} else if target := m.selectedTarget(); target != "" {
			m.dialog.OpenForceUpdate([]string{target})
		}
		return nil, true
	case key.Matches(msg, keys.FileView) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
		// Switch the file pane between its two views, from either
		// panel. It used to cycle through three, so landing on the one
		// you wanted took up to two presses — and `t` existed purely
		// to shortcut that cycle.
		m.filelist.ToggleViewMode()
		return nil, true
	case key.Matches(msg, keys.CollapseAll) && m.focus == PanelRight &&
		(m.activeTab == TabTree || m.activeTab == TabFavorites) &&
		m.filelist.viewMode == FileViewTree:
		// In the file pane, ~ folds or unfolds every subdirectory at
		// once — the same "all of it" gesture the tree pane gives this
		// key, applied to the column the cursor is in. This is the old
		// sub mode: tree-with-everything-open, which used to pretend
		// to be a third view.
		m.filelist.ToggleExpandAll()
		return nil, true
	case key.Matches(msg, keys.Escape):
		if m.activeTab == TabTree && m.filesLayout == LayoutDiff {
			// Esc closes the diff, the same way d does — the two ways
			// out of a view the user opened deliberately.
			if path := m.selectedFilePath(); path != "" {
				return m.toggleDiffView(path), true
			}
		}
		if m.activeTab == TabMarked {
			if m.markedTab.mode == MarkedCommit {
				m.markedTab.mode = MarkedActions
				m.markedTab.input.Blur()
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

		if m.activeTab == TabMarked {
			selectedPath = m.markedTab.SelectedPath()
		} else if m.activeTab == TabHistory {
			// In History the "selected file" is the file whose log is loaded —
			// regardless of which row the cursor is on, file-actions like E
			// (external diff) and e (edit) operate on this file.
			selectedPath = m.history.Path()
		} else if m.focus == PanelRight && !(m.activeTab == TabTree && m.filesLayout == LayoutDiff) {
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
			// The diff of the file under the cursor, in the pane next
			// to it. Pressing d again on the same file closes it. From
			// the Favorites or Marked tab this lands in the Files tab,
			// which is where the diff layout lives.
			return m.toggleDiffView(selectedPath)
		case key.Matches(msg, keys.EditDiff) && selectedPath != "" && !fs.IsBinary(filepath.Join(m.exec.WorkDir, selectedPath)):
			return m.launchExternalDiffForCurrent(selectedPath)
		case key.Matches(msg, keys.Merge) && selectedPath != "" && m.statusMap[selectedPath] == "C":
			// A real conflict outranks a revision merge: CVS's pre-merge
			// backup gives a better LOCAL side than any snapshot, and an
			// unresolved conflict is the more urgent thing to fix.
			return m.launchExternalMergeFor(selectedPath)
		case key.Matches(msg, keys.Merge) && m.activeTab == TabHistory:
			// Merge the selected revision pair into the working copy —
			// `cvs update -j from -j to`, resolved in the merge tool.
			return m.launchHistoryMergeForCurrent()
		case key.Matches(msg, keys.Edit) && selectedPath != "":
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			return openEditor(fullPath)
		case key.Matches(msg, keys.Open) && selectedPath != "":
			fullPath := filepath.Join(m.exec.WorkDir, selectedPath)
			return openInOS(fullPath)
		// Picker-vocabulary Ctrl layer (fzf/yazi-style: C-o/C-r/C-y act on
		// the hit under the cursor). C-e is folded into keys.Edit above.
		case key.Matches(msg, keys.AltOpen) && selectedPath != "":
			return m.launchAltOpenFor(selectedPath)
		case key.Matches(msg, keys.Reveal) && selectedPath != "":
			return revealInOS(filepath.Join(m.exec.WorkDir, selectedPath))
		case key.Matches(msg, keys.CopyPath) && selectedPath != "":
			return copyPathCmd(filepath.Join(m.exec.WorkDir, selectedPath))
		case key.Matches(msg, keys.Commit) && m.activeTab == TabMarked:
			// The Marked tab's commit works from either panel: switch to the
			// in-pane commit input. When nothing qualifies, SAY so —
			// this used to be a silent no-op and read as "commit is
			// broken".
			if paths := m.markedTab.CommittablePaths(); len(paths) > 0 {
				m.markedTab.mode = MarkedCommit
				m.focus = PanelRight
				m.markedTab.input.Focus()
				return nil
			}
			return m.setResult(fmt.Sprintf("Nothing committable in %s marked — need status ?, A, M, C or R", countLabel(len(m.markedTab.files), "file")), false)
		case key.Matches(msg, keys.Commit) && m.activeTab != TabHistory && m.activeTab != TabMarked:
			return m.openCommitDialog()
		case key.Matches(msg, keys.Revert) && m.activeTab != TabHistory && m.activeTab != TabMarked:
			// Marked files → ONE bulk confirmation over every revertable
			// (M/C) marked path, exactly like the Marked tab and like
			// `D` (remove). Only without marks does `r` fall back to the
			// single-file dialog on the cursor row — previously the
			// cursor file was reverted and the rest of the marked set
			// silently ignored.
			marked := m.filelist.MarkedFiles()
			var revertable []string
			statuses := make(map[string]string, len(marked))
			for _, p := range marked {
				if s := m.resolveFileStatus(p); s == "M" || s == "C" {
					revertable = append(revertable, p)
					statuses[p] = s
				}
			}
			if len(revertable) > 0 {
				m.dialog.OpenRevertBulk(revertable, statuses)
				return nil
			}
			if len(marked) > 0 {
				return m.setResult(fmt.Sprintf("Nothing revertable in %s marked — need status M or C", countLabel(len(marked), "file")), false)
			}
			if selectedPath != "" {
				m.dialog.OpenRevert(selectedPath)
			}
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
		case key.Matches(msg, keys.RestoreBackup) && m.activeTab == TabHistory:
			// Only the loaded file's backup. The marked-set and subtree
			// flows below read the file panels' selection, which History
			// doesn't show — following them here would restore files the
			// user can't see. This is also where the pair merge (M) just
			// wrote its pre-merge safety copy, so B must work without
			// leaving the tab. Invalidate the History cache so the
			// vs-working pane reflects the restored content.
			if selectedPath != "" {
				return tea.Batch(m.restoreFromBackup(selectedPath),
					m.invalidateHistoryCache([]string{selectedPath}))
			}
			return nil
		case key.Matches(msg, keys.RestoreBackup):
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
		case key.Matches(msg, keys.Recent) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
			// H = recent repository activity ("what did anyone change
			// here lately?") for the dir under the cursor, subdirs
			// included. Root selected → whole module.
			target := "."
			if m.activeTab == TabFavorites {
				if p := m.favorites.SelectedPath(); p != "" {
					target = p
				}
			} else if node := m.tree.SelectedNode(); node != nil {
				if node.IsDir {
					target = node.Path
				} else if d := filepath.Dir(node.Path); d != "" {
					target = d
				}
			}
			days := m.cfgMgr.Get().CVS.HistoryDays
			if days <= 0 {
				days = 7
			}
			// Serve locally whenever the local history covers the
			// window: instantly and silently within the TTL, and
			// stale-while-revalidate after it — show the local events
			// NOW, fetch the delta in the background, and patch the
			// open dialog when it lands. Only a cold store (or a
			// window wider than its coverage) blocks on the server.
			if m.recentEvents == nil {
				m.loadRecentStore()
			}
			windowStart := time.Now().AddDate(0, 0, -days)
			covered := m.recentEvents != nil && !m.recentCoverage.IsZero() &&
				!windowStart.Before(m.recentCoverage)
			if covered && time.Since(m.recentFetched) < recentCacheTTL {
				return m.openRecentFromCache(target, days)
			}
			if covered {
				return tea.Batch(
					m.openRecentFromCache(target, days),
					m.loadRecentChanges(target, days, true),
				)
			}
			m.setProgress(fmt.Sprintf("⟳ Loading repo changes for %s (last %d days)…", target, days))
			return m.loadRecentChanges(target, days, false)
		case key.Matches(msg, keys.FavAdd) && (m.activeTab == TabTree || m.activeTab == TabFavorites):
			return m.addFavorite()
		case key.Matches(msg, keys.FavDel) && m.activeTab == TabFavorites:
			return m.removeFavorite()
		case key.Matches(msg, keys.Add) && selectedPath != "" &&
			m.resolveFileStatus(selectedPath) == "?" && m.activeTab != TabHistory:
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
				return m.markedBulkIgnore(untracked)
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
			// Miller-column axis (ranger/yazi): → means "deeper". While
			// the cursor dir can still expand INTO something, the tree
			// consumes it; once there's nothing left to unfold (file,
			// already-expanded dir, or a leaf dir with no subdirs) the
			// same key crosses into the right pane — a files-only dir
			// crosses on the FIRST press, no empty expand step. ← stays
			// inside the tree — fold / walk up never leaves the leftmost
			// column.
			if key.Matches(msg, keys.Right) && (!m.tree.CanExpand() || !m.tree.SelectedHasSubdirs()) {
				m.focus = PanelRight
				if m.filesLayout == LayoutDiff {
					return m.autoLoadDiff()
				}
				return nil
			}
			var cmd tea.Cmd
			m.tree, cmd = m.tree.Update(msg)
			if key.Matches(msg, keys.Enter) || key.Matches(msg, keys.Down) || key.Matches(msg, keys.Up) {
				if m.filesLayout == LayoutDiff {
					diffCmd := m.autoLoadDiff()
					if key.Matches(msg, keys.Enter) {
						// yazi-style: Enter selects + crosses into the
						// diff pane so the next keypress acts on
						// what the user just opened.
						m.focus = PanelRight
					}
					return tea.Batch(cmd, diffCmd)
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
			// → crosses into the file list (the favorites list has no
			// in-pane horizontal motion).
			if key.Matches(msg, keys.Right) {
				m.focus = PanelRight
				return nil
			}
			var cmd tea.Cmd
			m.favorites, cmd = m.favorites.Update(msg)
			if key.Matches(msg, keys.Enter) {
				m.updateFileList()
			}
			return cmd
		case TabMarked:
			switch {
			case key.Matches(msg, keys.Down):
				if m.markedTab.cursor < len(m.markedTab.files)-1 {
					m.markedTab.cursor++
					m.markedTab.ensureVisible()
				}
			case key.Matches(msg, keys.Up):
				if m.markedTab.cursor > 0 {
					m.markedTab.cursor--
					m.markedTab.ensureVisible()
				}
			case key.Matches(msg, keys.Top):
				m.markedTab.cursor = 0
				m.markedTab.ensureVisible()
			case key.Matches(msg, keys.Bottom):
				m.markedTab.cursor = max(0, len(m.markedTab.files)-1)
				m.markedTab.ensureVisible()
			case key.Matches(msg, keys.PageDown):
				m.markedTab.cursor = clamp(m.markedTab.cursor+m.markedTab.height-1, 0, max(0, len(m.markedTab.files)-1))
				m.markedTab.ensureVisible()
			case key.Matches(msg, keys.PageUp):
				m.markedTab.cursor = clamp(m.markedTab.cursor-(m.markedTab.height-1), 0, max(0, len(m.markedTab.files)-1))
				m.markedTab.ensureVisible()
			case key.Matches(msg, keys.Space):
				if path := m.markedTab.SelectedPath(); path != "" {
					delete(m.filelist.marked, path)
					m.markedTab.Refresh(m.filelist.marked, m.resolveFileStatus)
				}
			case key.Matches(msg, keys.Revert):
				// Bulk-revert every M *and* C file in the marked set.
				// Opens a confirmation dialog first — the action is
				// destructive (cvs update -C / rm + cvs update,
				// depending on per-path status), and confirmations are
				// the standard pattern for the equivalent file-action
				// `r` in other tabs. The dialog dispatch carries the
				// status map across so the action handler picks the
				// right cvs sequence per file.
				if paths := m.markedTab.PathsByStatus("M", "C"); len(paths) > 0 {
					statuses := make(map[string]string, len(paths))
					for _, p := range paths {
						statuses[p] = m.resolveFileStatus(p)
					}
					m.dialog.OpenRevertBulk(paths, statuses)
					return nil
				}
			case key.Matches(msg, keys.Ignore):
				// Ignore: add all ? files to .cvsignore
				if paths := m.markedTab.PathsByStatus("?"); len(paths) > 0 {
					return m.markedBulkIgnore(paths)
				}
			case key.Matches(msg, keys.Update):
				// Update: cvs update selected files
				if len(m.markedTab.files) > 0 {
					var paths []string
					for _, f := range m.markedTab.files {
						paths = append(paths, f.path)
					}
					m.setProgress(fmt.Sprintf("⟳ Updating %s…", countLabel(len(paths), "file")))
					return m.markedBulkAction("update", paths)
				}
			case key.Matches(msg, keys.ForceUpdate):
				// Force update: cvs update -C (needs confirmation)
				if paths := m.markedTab.PathsByStatus("M", "C"); len(paths) > 0 {
					m.dialog.OpenForceUpdate(paths)
				}
			case key.Matches(msg, keys.Enter):
				m.markedTab.mode = MarkedCommit
				m.focus = PanelRight
				m.markedTab.input.Focus()
			}
			return nil
		case TabHistory:
			// → crosses into the diff/content viewport.
			if key.Matches(msg, keys.Right) {
				m.focus = PanelRight
				return nil
			}
			return m.delegateHistoryKey(msg)
		}
	case PanelRight:
		// Miller-column axis: ← steps back into the left column from any
		// right pane — no right pane assigns an in-pane meaning to ←.
		// (The commit input never reaches here while focused;
		// handleCommitInput consumes its keys, so cursor movement inside
		// the message text is unaffected.)
		if key.Matches(msg, keys.Left) {
			m.focus = PanelLeft
			return nil
		}
		switch m.activeTab {
		case TabTree:
			if m.filesLayout == LayoutDiff {
				if m.diffReady {
					var cmd tea.Cmd
					m.diffVP, cmd = m.diffVP.Update(msg)
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
		case TabMarked:
			// Input handling is done by the priority check above
			// If we get here, input is not focused — Enter focuses it
			if key.Matches(msg, keys.Enter) {
				m.markedTab.input.Focus()
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
		// Console resize. +/- set the *expanded* height for the rest
		// of the session (the panel still collapses when it loses
		// focus with nothing to report); starting from the height
		// currently in effect keeps the first press a small step
		// rather than a jump back to the configured size.
		switch msg.String() {
		case "+", "=":
			m.consoleOverride = min(m.consoleH()+3, max(consoleCollapsed, m.height/2))
			m.updateSizes()
			return nil
		case "-", "_":
			m.consoleOverride = max(m.consoleH()-3, 3)
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
