package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Mouse and click/scroll dispatch for the TUI. The handlers translate
// terminal coordinates into panel-and-row indices, then delegate to the
// click* / scroll* methods which know how to update each tab's
// underlying model. Pure routing logic lives here; business logic lives
// in the model methods.

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
			// Staged actions mode is a single full-width panel — every
			// click belongs to it. Splitting at leftW here used to set
			// focus to the nonexistent right panel, where action keys
			// silently died ("clicked the row, pressed c, nothing").
			if x < leftW || (m.activeTab == TabStaged && m.staged.mode == StagedActions) {
				m.focus = PanelLeft
				return m.clickLeft(contentRow)
			}
			m.focus = PanelRight
			m.clickRight(contentRow)
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
	case TabStaged:
		// One header row at the top, then one row per file.
		idx := m.staged.offset + row - 1
		if idx >= 0 && idx < len(m.staged.files) {
			m.staged.cursor = idx
		}
	case TabHistory:
		prevCursor := m.history.cursor
		// Each revision row spans 2 visual lines and there's no inline
		// header (the file name lives in the panel frame title).
		idx := m.history.offset + row/2
		if idx >= 0 && idx < m.history.NumRevisions() {
			m.history.cursor = idx
		}
		if m.history.cursor != prevCursor {
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
	case TabStaged:
		m.staged.cursor = clamp(m.staged.cursor+delta, 0, max(0, len(m.staged.files)-1))
		m.staged.ensureVisible()
	case TabHistory:
		prevCursor := m.history.cursor
		m.history.cursor = clamp(m.history.cursor+delta, 0, max(0, m.history.NumRevisions()-1))
		m.history.ensureVisible()
		if m.history.cursor != prevCursor {
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
	case TabStaged:
		// Staged is single-panel full-width in StagedActions; clicking
		// the "right" half should still scroll the staged list. In
		// StagedCommit the right pane is the single-line input, which
		// doesn't have anything to scroll.
		if m.staged.mode == StagedActions {
			m.staged.cursor = clamp(m.staged.cursor+delta, 0, max(0, len(m.staged.files)-1))
			m.staged.ensureVisible()
		}
	case TabHistory:
		if delta < 0 {
			m.history.viewport.LineUp(3)
		} else {
			m.history.viewport.LineDown(3)
		}
	}
}
