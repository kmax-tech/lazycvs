package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/fs"

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
	// tabbar(1) + keybar(2: nav row + context row) + main borders(2) +
	// main padding(1) + console borders(2) + console padding(1)
	overhead := 9
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
	// At least 3 rows are needed for any meaningful layout (1 row of
	// content + 2-row keybar). Below that we'd produce a negative
	// slice index a few lines down and panic — return the placeholder
	// instead so the initial WindowSizeMsg or a single-line terminal
	// degrades gracefully.
	if m.width == 0 || m.height < 3 {
		return "Loading..."
	}

	// Bootstrap mode: skip the full TUI chrome and render only the
	// centered DialogCheckout overlay. The dialog renderer below
	// (renderDialog) draws on top of the rest; here we just provide
	// an empty backdrop so the dialog has clean space.
	if m.setupMode {
		dialog := m.dialog.View()
		// Centre the dialog roughly in the terminal — same trick the
		// modal overlay uses normally, but without any other content.
		body := lipgloss.Place(m.width, m.height-1,
			lipgloss.Center, lipgloss.Center, dialog)
		footer := " " + helpStyle.Render("lazycvs init — bootstrap mode")
		return body + "\n" + footer
	}

	tabBar := m.renderTabBar()
	mainContent := m.renderMainContent()
	console := m.renderConsole()
	keybar := m.renderKeybar() // two lines

	rows := []string{}
	if m.notification != "" {
		rows = append(rows, m.renderBanner())
	}
	rows = append(rows, tabBar, mainContent, console)

	// Assemble body (everything except keybar) with explicit join, then
	// enforce exactly m.height lines with the keybar always last. The
	// keybar itself is now two lines (nav row + context row).
	body := strings.Join(rows, "\n")
	lines := strings.Split(body, "\n")
	target := m.height - 2 // reserve two lines for keybar
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
	bg := colorUpdated // green-ish: success
	switch {
	case m.bannerInProgress():
		bg = colorStale // yellow-ish: running
	case !m.notificationOK:
		bg = colorConflict // red-ish: failure
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
		if Tab(i) == m.activeTab {
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
	wcRoot := filepath.Base(m.exec.WorkDir)
	// fullDirPath joins the working-copy basename with the file-list's
	// relative dir so the title always says "where in this checkout am
	// I". Without it the title only showed the immediate dir name and
	// couldn't disambiguate when several working copies have the same
	// leaf names.
	fullDirPath := func(rel string) string {
		if rel == "" || rel == "." {
			return wcRoot
		}
		return wcRoot + "/" + rel
	}
	switch m.activeTab {
	case TabTree:
		if m.treeMode == TreeViewDetails {
			node := m.tree.SelectedNode()
			if node != nil {
				if node.IsDir {
					rightTitle = fullDirPath(node.Path) + "/"
				} else if node.Status != "" {
					rightTitle = node.Status + " " + fullDirPath(node.Path)
				} else {
					rightTitle = fullDirPath(node.Path)
				}
				if !node.IsDir {
					if issue := m.encodingIssueFor(node.Path); issue != cvs.EncodingOK {
						rightTitle += "  ⚠ " + issue.String()
					}
				}
			} else {
				rightTitle = "Preview"
			}
		} else {
			rightTitle = "Files — " + fullDirPath(m.filelist.dir)
			files := m.filelist.rows()
			if len(files) > 0 {
				rightInfo = fmt.Sprintf(" %d of %d ", m.filelist.cursor+1, len(files))
			}
		}
	case TabFavorites:
		rightTitle = "Files — " + fullDirPath(m.filelist.dir)
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
	// Echo the working-copy root so the cvs invocations in the log
	// are unambiguous — every command runs with cmd.Dir = WorkDir,
	// and with multiple checkouts on disk the path args (relative
	// to WorkDir) alone don't tell you which one was acted on.
	if m.exec != nil && m.exec.WorkDir != "" {
		consoleTitle += " — " + filepath.Base(m.exec.WorkDir)
	}
	title := " " + lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render(consoleTitle) + " "
	info := ""
	if m.console.lastLen > 0 {
		info = fmt.Sprintf(" %d cmds ", m.console.lastLen)
	}
	return renderPanel(title, m.console.View(), m.width, consoleH, m.focus == PanelConsole, info)
}

// renderKeybar returns two lines, both assembled from the keymap
// registry (keymap.go) — the single table that also feeds the help
// modal, so the two can no longer disagree.
//
//	Line 1 — the nav segment: the same on every tab, a stable map of
//	how to move around.
//
//	Line 2 — the active tab's action segments, separated by │. Which
//	segments exist and in what order is barSegmentOrder; which keys
//	are live inside them is the entries' own applies predicate (a
//	merge key on a conflict row, the History mode variants).
//
// The commit input is the one hand-written row: while the message
// field has focus the keys belong to the text input, not to the file
// list, so no registry entry applies.
func (m App) renderKeybar() string {
	sep := mutedStyle.Render(" │ ")
	ctx := m.keyContext()

	nav := strings.Join(barItems(barEntries(ctx, "nav")), sep)
	if m.width > 2 {
		nav = ansi.Truncate(nav, m.width-1, "…")
	}

	actions := m.actionRow(ctx)
	return " " + nav + "\n " + actions
}

// barItems renders entries as "key:description" in registry order.
func barItems(entries []keyEntry) []string {
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		k, d := e.barText()
		items = append(items, keyStyle.Render(k)+":"+d)
	}
	return items
}

// actionRow builds keybar line 2 for the current context and width.
//
// Two things distinguish it from a static toolbar. It shows only keys
// that apply to the row under the cursor (an untracked file offers add
// and ignore, a conflict offers the merge tool), and when the keys
// exceed the terminal width it *drops* the lowest-ranked ones and says
// how many, instead of cutting the row mid-word. The old behaviour hid
// the entire [View] group on an 80-column terminal behind a single "…"
// with nothing to indicate what was missing.
func (m App) actionRow(ctx keyContext) string {
	sep := mutedStyle.Render(" │ ")

	// The commit input is not a key catalogue: while it has focus the
	// keys belong to the text field.
	if ctx.stagedInput {
		row := keyStyle.Render("enter") + ":commit" + sep +
			keyStyle.Render("C-e") + ":editor" + sep +
			keyStyle.Render("esc") + ":back"
		return truncateRow(row, m.width)
	}

	// Rule 1 made visible: with a worklist, actions address the marked
	// set, and the row says so before naming a single key.
	prefix := ""
	if ctx.marked > 0 {
		prefix = lipgloss.NewStyle().Foreground(colorMod).Bold(true).
			Render(fmt.Sprintf("%d marked ▸ ", ctx.marked))
	}

	type unit struct {
		segment string
		text    string
		keep    int // segment rank; higher goes first when space runs out
		bar     int // rank within the segment
	}
	var units []unit
	for _, id := range barSegmentOrder(ctx.tab) {
		entries := barEntries(ctx, id)
		for i, item := range barItems(entries) {
			units = append(units, unit{
				segment: entries[i].barSegment,
				text:    item,
				keep:    barSegmentKeep(entries[i].barSegment),
				bar:     entries[i].bar,
			})
		}
	}

	// Drop order: least deserving segment first, lowest rank within it.
	// Display order is untouched — dropping [View] must not reshuffle
	// what remains.
	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ua, ub := units[order[a]], units[order[b]]
		if ua.keep != ub.keep {
			return ua.keep > ub.keep
		}
		return ua.bar > ub.bar
	})

	render := func(dropped map[int]bool) string {
		var b strings.Builder
		current := ""
		for i, u := range units {
			if dropped[i] {
				continue
			}
			switch {
			case u.segment != current:
				if current != "" {
					b.WriteString(sep)
				}
				b.WriteString(mutedStyle.Render(barSegmentLabel(u.segment, ctx)))
				current = u.segment
			default:
				b.WriteString("  ")
			}
			b.WriteString(u.text)
		}
		return b.String()
	}

	// A blank row reads as a broken UI. When nothing applies — an empty
	// Staged tab, a cursor on a row with no actions — say so and point
	// at the one key that always works.
	if len(units) == 0 {
		return truncateRow(prefix+mutedStyle.Render("no actions for this row — ")+
			keyStyle.Render("?")+mutedStyle.Render(" lists every key"), m.width)
	}

	budget := m.width - 1
	dropped := map[int]bool{}
	for n := 0; ; n++ {
		marker := ""
		if n > 0 {
			marker = mutedStyle.Render(fmt.Sprintf("  +%d (?)", n))
		}
		row := prefix + render(dropped) + marker
		if n == len(units) || m.width <= 2 || lipgloss.Width(row) <= budget {
			return truncateRow(row, m.width)
		}
		dropped[order[n]] = true
	}
}

// truncateRow is the last-resort guard for a terminal too narrow for
// even one key.
func truncateRow(row string, width int) string {
	if width > 2 {
		return ansi.Truncate(row, width-1, "…")
	}
	return row
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
