package tui

import (
	"lazycvs/fs"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// PanelDims is the layout envelope passed to every sub-model's SetSize.
// Each model reads only the fields it cares about: a single-panel model
// (tree, filelist, favorites, console) ignores the side it doesn't fill;
// a two-panel model (staged, history) reads both LeftW and RightW. Zero
// values for unread fields are by design — they're never inspected, so
// no sentinel logic is needed inside the models.
type PanelDims struct {
	LeftW  int
	RightW int
	Height int
}

func (m *App) updateSizes() {
	leftW, rightW, contentH, consoleH := m.layout()
	innerLeftW := leftW - 2
	innerRightW := rightW - 2

	m.tree.SetSize(PanelDims{LeftW: innerLeftW, Height: contentH})
	m.filelist.SetSize(PanelDims{RightW: innerRightW, Height: contentH})
	m.favorites.SetSize(PanelDims{LeftW: innerLeftW, Height: contentH})
	m.staged.SetSize(PanelDims{LeftW: innerLeftW, RightW: innerRightW, Height: contentH})
	m.history.SetSize(PanelDims{LeftW: innerLeftW, RightW: innerRightW, Height: contentH})
	m.console.SetSize(PanelDims{LeftW: m.width - 2, Height: consoleH})
	m.dialog.SetSize(PanelDims{LeftW: m.width, Height: m.height})
	m.search.width = m.width
	m.search.height = m.height
	if m.previewReady {
		m.previewVP.Width = innerRightW
		m.previewVP.Height = contentH
	}
}

func (m App) layout() (leftWidth, rightWidth, contentHeight, consoleHeight int) {
	leftWidth = m.width / 3
	if leftWidth < 25 {
		leftWidth = 25
	}
	if leftWidth > 40 {
		leftWidth = 40
	}
	rightWidth = m.width - leftWidth
	consoleHeight = m.consoleHeight
	// tabbar(1) + keybar(1) + main borders(2) + main padding(1) + console borders(2) + console padding(1)
	overhead := 8
	if m.notification != "" {
		overhead++ // banner takes one extra line above the tab bar
	}
	contentHeight = m.height - consoleHeight - overhead
	if contentHeight < 5 {
		contentHeight = 5
		consoleHeight = m.height - contentHeight - overhead
		if consoleHeight < 3 {
			consoleHeight = 3
		}
	}
	return
}

func (m App) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading..."
	}

	tabBar := m.renderTabBar()
	mainContent := m.renderMainContent()
	console := m.renderConsole()
	keybar := m.renderKeybar()

	rows := []string{}
	if m.notification != "" {
		rows = append(rows, m.renderBanner())
	}
	rows = append(rows, tabBar, mainContent, console)

	// Assemble body (everything except keybar) with explicit join, then
	// enforce exactly m.height lines with the keybar always last.
	body := strings.Join(rows, "\n")
	lines := strings.Split(body, "\n")
	target := m.height - 1 // reserve one line for keybar
	if len(lines) > target {
		lines = lines[:target]
	}
	for len(lines) < target {
		lines = append(lines, "")
	}
	lines = append(lines, keybar)
	view := strings.Join(lines, "\n")

	// Overlay dialog/search if active
	if m.dialog.Active() {
		view = m.overlay(view, m.dialog.View())
	}
	if m.search.active {
		view = m.overlay(view, m.search.View())
	}

	return view
}

// renderBanner renders the transient notification line shown above the tab
// bar. Green for success, red for failure. Padded to full width so it spans
// the screen — easier to spot than a short floating message.
func (m App) renderBanner() string {
	bg := colorUpdated // green-ish
	if !m.notificationOK {
		bg = colorConflict // red-ish
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")). // black text on bright bg
		Background(bg).
		Bold(true).
		Width(m.width).
		Padding(0, 1)
	return style.Render(m.notification)
}

func (m App) renderTabBar() string {
	treeName := "Files"
	if m.treeMode == TreeViewDetails {
		treeName = "Detail"
	}
	stagedName := "Staged"
	if n := len(m.filelist.marked); n > 0 {
		stagedName = fmt.Sprintf("Staged(%d)", n)
	}
	names := []string{treeName, "Fav", stagedName, "History"}
	active := lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	inactive := mutedStyle

	var parts []string
	for i, name := range names {
		label := fmt.Sprintf(" %d:%s ", i+1, name)
		if i == m.activeTab {
			parts = append(parts, active.Render(label))
		} else {
			parts = append(parts, inactive.Render(label))
		}
	}
	bar := strings.Join(parts, mutedStyle.Render("│"))

	// Right-aligned warning indicator: number of failed CVS commands since
	// the user last focused the console panel. Tab/Ctrl+J resets it.
	warn := ""
	if n := m.cmdLog.UnreadErrors(); n > 0 {
		warnStyle := lipgloss.NewStyle().Foreground(colorConflict).Bold(true)
		warn = warnStyle.Render(fmt.Sprintf(" ⚠ %d ", n))
	}

	// Pad bar so the warn indicator sits flush right.
	pad := m.width - lipgloss.Width(bar) - lipgloss.Width(warn)
	if pad > 0 {
		bar += strings.Repeat(" ", pad)
	}
	return bar + warn
}

func renderPanel(title, content string, width, height int, active bool, info string) string {
	if width < 4 {
		width = 4
	}
	if height < 1 {
		height = 1
	}
	bc := colorBorder
	if active {
		bc = colorActive
	}
	bClr := lipgloss.NewStyle().Foreground(bc)
	innerW := width - 2

	// Top: ╭─title──────────╮
	titleW := lipgloss.Width(title)
	maxTitleW := width - 3 // space for ╭─ and ╮
	if titleW > maxTitleW && maxTitleW > 0 {
		title = ansi.Truncate(title, maxTitleW, "…")
		titleW = lipgloss.Width(title)
	}
	topFill := max(0, width-3-titleW)
	top := bClr.Render("╭─") + title + bClr.Render(strings.Repeat("─", topFill)+"╮")

	// Content lines
	contentLines := strings.Split(content, "\n")
	var lines []string
	lines = append(lines, top)

	// Top padding line
	lines = append(lines, bClr.Render("│")+strings.Repeat(" ", innerW)+bClr.Render("│"))

	for i := 0; i < height; i++ {
		var cl string
		if i < len(contentLines) {
			cl = contentLines[i]
		}
		lw := lipgloss.Width(cl)
		if lw > innerW {
			cl = ansi.Truncate(cl, innerW, "…")
			lw = lipgloss.Width(cl)
		}
		pad := innerW - lw
		if pad > 0 {
			cl += strings.Repeat(" ", pad)
		}
		lines = append(lines, bClr.Render("│")+cl+bClr.Render("│"))
	}

	// Bottom: ╰──────info─╯ or ╰──────╯
	if info != "" {
		infoStr := bClr.Render(info)
		infoW := lipgloss.Width(infoStr)
		botFill := max(0, width-2-infoW)
		bottom := bClr.Render("╰"+strings.Repeat("─", botFill)) + infoStr + bClr.Render("╯")
		lines = append(lines, bottom)
	} else {
		lines = append(lines, bClr.Render("╰"+strings.Repeat("─", innerW)+"╯"))
	}

	return strings.Join(lines, "\n")
}

func (m App) renderMainContent() string {
	leftW, rightW, contentH, _ := m.layout()

	// Left panel title (context-specific)
	var leftTitle string
	switch m.activeTab {
	case TabTree:
		leftTitle = "Tree"
	case TabFavorites:
		leftTitle = "Favorites"
	case TabStaged:
		leftTitle = fmt.Sprintf("Staged (%d)", len(m.staged.files))
	case TabHistory:
		if p := m.history.Path(); p != "" {
			leftTitle = "Revisions — " + p
		} else {
			leftTitle = "Revisions"
		}
	}
	lt := " " + lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render(leftTitle) + " "

	// Staged tab in actions mode is single-panel: the right side would be a
	// static action overview duplicating the keybar, so we render the file
	// list full-width instead. The right panel reappears in commit mode
	// (StagedCommit) below the regular two-panel branch.
	if m.activeTab == TabStaged && m.staged.mode == StagedActions {
		info := ""
		if len(m.staged.files) > 0 {
			info = fmt.Sprintf(" %d of %d ", m.staged.cursor+1, len(m.staged.files))
		}
		return renderPanel(lt, m.staged.ViewLeft(), m.width, contentH, m.focus == PanelLeft, info)
	}

	// Left panel info
	var leftInfo string
	switch m.activeTab {
	case TabTree:
		if len(m.tree.flat) > 0 {
			leftInfo = fmt.Sprintf(" %d of %d ", m.tree.cursor+1, len(m.tree.flat))
		}
	case TabFavorites:
		if len(m.favorites.favorites) > 0 {
			leftInfo = fmt.Sprintf(" %d of %d ", m.favorites.cursor+1, len(m.favorites.favorites))
		}
	case TabStaged:
		if len(m.staged.files) > 0 {
			leftInfo = fmt.Sprintf(" %d of %d ", m.staged.cursor+1, len(m.staged.files))
		}
	case TabHistory:
		if m.history.NumRevisions() > 0 {
			leftInfo = fmt.Sprintf(" %d of %d ", m.history.cursor+1, m.history.NumRevisions())
		}
	}

	// Right panel title & info
	var rightTitle string
	var rightInfo string
	switch m.activeTab {
	case TabTree:
		if m.treeMode == TreeViewDetails {
			node := m.tree.SelectedNode()
			if node != nil {
				if node.IsDir {
					rightTitle = node.Path + "/"
				} else if node.Status != "" {
					rightTitle = node.Status + " " + node.Path
				} else {
					rightTitle = node.Path
				}
			} else {
				rightTitle = "Preview"
			}
		} else {
			rightTitle = "Files"
			if m.filelist.dir != "" {
				rightTitle = "Files — " + m.filelist.dir
			}
			files := m.filelist.rows()
			if len(files) > 0 {
				rightInfo = fmt.Sprintf(" %d of %d ", m.filelist.cursor+1, len(files))
			}
		}
	case TabFavorites:
		rightTitle = "Files"
		if m.filelist.dir != "" {
			rightTitle = "Files — " + m.filelist.dir
		}
		files := m.filelist.rows()
		if len(files) > 0 {
			rightInfo = fmt.Sprintf(" %d of %d ", m.filelist.cursor+1, len(files))
		}
	case TabStaged:
		rightTitle = "Commit"
	case TabHistory:
		switch m.history.mode {
		case HistoryContent:
			if m.history.diffToRev != "" {
				rightTitle = fmt.Sprintf("Content — %s", m.history.diffToRev)
			} else {
				rightTitle = "Content"
			}
		case HistoryDiff:
			from := m.history.diffFromRev
			to := prettyRev(m.history.diffToRev)
			verb := "Diff"
			if m.history.HasCompare() {
				verb = "Compare"
			}
			switch {
			case from != "" && to != "":
				rightTitle = fmt.Sprintf("%s — %s ↔ %s", verb, from, to)
			case to != "":
				rightTitle = fmt.Sprintf("Content — %s (initial)", to)
			default:
				rightTitle = verb
			}
		case HistoryBlame:
			rightTitle = "Blame"
		}
	}

	// Content
	var leftContent, rightContent string
	switch m.activeTab {
	case TabTree:
		leftContent = m.tree.View()
		if m.treeMode == TreeViewDetails {
			rightContent = m.renderPreview()
		} else {
			rightContent = m.filelist.View()
		}
	case TabFavorites:
		leftContent = m.favorites.View()
		rightContent = m.filelist.View()
	case TabStaged:
		leftContent = m.staged.ViewLeft()
		rightContent = m.staged.ViewRight()
	case TabHistory:
		leftContent = m.history.ViewLeft()
		rightContent = m.history.ViewRight()
	}

	rt := " " + lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render(rightTitle) + " "
	left := renderPanel(lt, leftContent, leftW, contentH, m.focus == PanelLeft, leftInfo)
	right := renderPanel(rt, rightContent, rightW, contentH, m.focus == PanelRight, rightInfo)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m App) renderConsole() string {
	_, _, _, consoleH := m.layout()
	consoleTitle := "Console"
	if fl := m.console.FilterLabel(); fl != "" {
		consoleTitle += " [" + fl + "]"
	}
	title := " " + lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render(consoleTitle) + " "
	info := ""
	if m.console.lastLen > 0 {
		info = fmt.Sprintf(" %d cmds ", m.console.lastLen)
	}
	return renderPanel(title, m.console.View(), m.width, consoleH, m.focus == PanelConsole, info)
}

func (m App) renderKeybar() string {
	focus := helpStyle.Render("Switch panel: ") + keyStyle.Render("<tab>") +
		helpStyle.Render("  Keybindings: ") + keyStyle.Render("?") +
		helpStyle.Render("  Cancel: ") + keyStyle.Render("<esc>")

	// Conflict-only hint: append M:merge when the selected file has C status.
	mergeHint := ""
	if path := m.selectedFilePath(); path != "" && m.statusMap[path] == "C" {
		mergeHint = "  " + keyHelp(keys.Merge)
	}

	var actions string
	switch m.activeTab {
	case TabTree:
		actions = keyHelp(keys.ViewMode, keys.Diff, keys.Commit, keys.MarkAll, keys.Ignore, keys.Edit, keys.Status, keys.Update) +
			"  " + keyStyle.Render("f") + ":view  " + keyStyle.Render("t") + ":tree" + mergeHint
	case TabFavorites:
		actions = keyHelp(keys.Diff, keys.Commit, keys.MarkAll, keys.Ignore, keys.Edit, keys.Status, keys.Update) +
			"  " + keyStyle.Render("f") + ":view  " + keyStyle.Render("t") + ":tree" + mergeHint
	case TabStaged:
		// `c` covers both add+commit (for ?-files) and plain commit, so a:add
		// is no longer offered separately in the staged tab.
		actions = keyHelp(keys.Commit, keys.Revert, keys.Ignore, keys.Update) + "  " +
			keyStyle.Render("space") + ":unstage  " +
			keyHelp(keys.Diff) + mergeHint
	case TabHistory:
		hScroll := keyStyle.Render("</>") + ":scroll"
		spAnchor := keyStyle.Render("space") + ":anchor"
		if m.history.HasCompare() || m.history.vsWorking {
			// In a comparison: surface Esc as the way out, and keep
			// the toggle for working-copy on the keybar.
			actions = keyHelp(keys.SideBySide, keys.Blame, keys.CompareWorking, keys.Escape) +
				"  " + spAnchor + "  " + hScroll
		} else if m.history.mode == HistoryDiff {
			actions = keyHelp(keys.SideBySide, keys.Blame, keys.Edit, keys.EditDiff, keys.CompareWorking) +
				"  " + spAnchor + "  " + hScroll
		} else {
			actions = keyHelp(keys.Diff, keys.Blame, keys.Edit, keys.EditDiff, keys.CompareWorking) +
				"  " + spAnchor + "  " + hScroll
		}
	}

	gap := m.width - lipgloss.Width(focus) - lipgloss.Width(actions) - 2
	if gap < 1 {
		gap = 1
	}
	return " " + actions + strings.Repeat(" ", gap) + focus
}

func (m App) renderPreview() string {
	node := m.tree.SelectedNode()
	if node == nil {
		return mutedStyle.Render("  No selection")
	}
	if node.IsDir {
		if !node.Counts.IsZero() {
			return "  " + renderCounts(node.Counts)
		}
		return mutedStyle.Render("  No changes in this directory")
	}
	if fs.IsBinary(filepath.Join(m.exec.WorkDir, node.Path)) {
		return mutedStyle.Render("  Binary file — cannot display")
	}
	if !m.previewReady || m.previewPath != node.Path {
		return mutedStyle.Render("  Loading...")
	}
	if m.previewDiff == nil || len(m.previewDiff.Hunks) == 0 {
		return mutedStyle.Render("  No local changes")
	}
	return m.previewVP.View()
}

func (m App) overlay(bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	fgWidth := 0
	for _, line := range fgLines {
		if w := lipgloss.Width(line); w > fgWidth {
			fgWidth = w
		}
	}

	startY := (m.height - len(fgLines)) / 2
	startX := (m.width - fgWidth) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	for i, fgLine := range fgLines {
		row := startY + i
		if row >= len(bgLines) {
			break
		}
		bgLine := bgLines[row]
		// ANSI-aware overlay — truncate background at visual position
		if startX < lipgloss.Width(bgLine) {
			before := ansi.Truncate(bgLine, startX, "")
			bgLines[row] = before + fgLine
		} else {
			bgLines[row] = strings.Repeat(" ", startX) + fgLine
		}
	}

	return strings.Join(bgLines, "\n")
}
