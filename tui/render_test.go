package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kmax-tech/lazycvs/cvs"
)

// Columns are measured in display cells. fmt's %-*s pads by bytes, so
// every umlaut in a file name used to shift the size column one cell
// left — routine in a repo whose expected encoding is Latin-1.
func TestPadCellsCountsCellsNotBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
	}{
		{"ascii", "readme.txt", 20},
		{"umlauts", "Übersicht-Grün.txt", 20},
		{"sharp s", "Straßenbahn.md", 20},
		{"exactly full", "abcde", 5},
		{"needs truncation", "a-very-long-file-name.txt", 10},
		{"truncated with umlauts", "Ümlaut-Datei-mit-langem-Namen.txt", 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := padCells(c.in, c.w)
			if w := lipgloss.Width(got); w != c.w {
				t.Errorf("padCells(%q, %d) is %d cells wide: %q", c.in, c.w, w, got)
			}
		})
	}
}

// Two rows of the same width must line up whatever their names contain
// — the actual symptom: a size column that stepped left on every file
// with an umlaut.
func TestFileRowsAlignAcrossEncodings(t *testing.T) {
	const w = 24
	ascii := padCells("notes.txt", w) + " 1.3 KB"
	umlaut := padCells("Änderungen.txt", w) + " 1.3 KB"

	if lipgloss.Width(ascii) != lipgloss.Width(umlaut) {
		t.Errorf("rows differ in width: %d vs %d\n  %q\n  %q",
			lipgloss.Width(ascii), lipgloss.Width(umlaut), ascii, umlaut)
	}
}

// The selection bar spans the pane and carries no colour blocks: the
// tree used to reverse only as much as the text was long, and reversing
// a coloured count badge painted it as a solid block.
func TestCursorBarSpansPaneWithoutColourBlocks(t *testing.T) {
	colored := lipgloss.NewStyle().Foreground(colorMod).Render("1M") + " file.txt"
	bar := cursorBar(colored, 30)

	if w := lipgloss.Width(bar); w != 30 {
		t.Errorf("bar is %d cells wide, want 30: %q", w, ansi.Strip(bar))
	}
	if n := strings.Count(bar, "\x1b["); n > 2 {
		t.Errorf("bar carries %d escape sequences, want one reverse pair: %q", n, bar)
	}
	if !strings.Contains(ansi.Strip(bar), "1M file.txt") {
		t.Errorf("bar lost its text: %q", ansi.Strip(bar))
	}
}

// Untracked rows are the bulk of a listing; if they share the border's
// colour, the M and C rows that need attention disappear into them.
func TestUntrackedIsDistinctFromChrome(t *testing.T) {
	if colorUntracked == colorBorder || colorUntracked == colorMuted {
		t.Errorf("untracked (%v) must differ from border (%v) and muted (%v)",
			colorUntracked, colorBorder, colorMuted)
	}
	if colorUntracked == colorIgnored {
		t.Errorf("untracked (%v) must stay brighter than ignored (%v)",
			colorUntracked, colorIgnored)
	}
}

// The header and the footer of the file pane must report the same
// total. They used to count differently — the header counted files,
// the footer counted navigable rows, which include the directory
// header — so one listing claimed "14 files" and "10 of 15" at once.
func TestFileListHeaderAndFooterAgree(t *testing.T) {
	fl := NewFileListModel()
	fl.width = 60
	fl.height = 20
	fl.SetFiles(".", []cvs.FileEntry{
		{Path: "a.txt", Status: "M"},
		{Path: "b.txt", Status: "?"},
		{Path: "c.txt"},
	}, nil)

	if got := fl.FileCount(); got != 3 {
		t.Fatalf("FileCount = %d, want 3", got)
	}

	header := ansi.Strip(fl.View())
	if !strings.Contains(header, "3 files") {
		t.Errorf("header does not report 3 files: %q", strings.SplitN(header, "\n", 2)[0])
	}

	// Cursor on the directory header row: a total, no position.
	fl.cursor = 0
	if info := ansi.Strip(fileListInfo(fl)); info != " 3 files " {
		t.Errorf("footer on the header row = %q, want \" 3 files \"", info)
	}

	// Cursor on the second file: position among files, same total.
	fl.cursor = 2
	if info := ansi.Strip(fileListInfo(fl)); info != " 2 of 3 " {
		t.Errorf("footer on the second file = %q, want \" 2 of 3 \"", info)
	}
}

// The header says what state the list is in, in words. "-ign" said it
// in three characters that name neither the setting nor its direction.
func TestFileListHeaderNamesItsState(t *testing.T) {
	fl := NewFileListModel()
	fl.width = 80
	fl.height = 10
	fl.SetFiles("docs", []cvs.FileEntry{{Path: "docs/a.txt", Status: "M"}}, nil)

	head := func() string {
		return strings.SplitN(ansi.Strip(fl.View()), "\n", 2)[0]
	}

	if got := head(); !strings.Contains(got, "ignored:hidden") {
		t.Errorf("default header should say ignored:hidden: %q", got)
	}
	fl.hideIgnored = false
	if got := head(); !strings.Contains(got, "ignored:shown") {
		t.Errorf("header should say ignored:shown once they are: %q", got)
	}
	fl.filter = "M"
	if got := head(); !strings.Contains(got, "filter:M") {
		t.Errorf("header should name the active filter: %q", got)
	}
	if got := head(); strings.Contains(got, "-ign") {
		t.Errorf("header still carries the old shorthand: %q", got)
	}
}

// The console collapses to one line and expands on focus. The viewport's
// scroll offset is computed for the height it had, so expanding without
// re-anchoring scrolls past the end: the panel opens blank, exactly when
// the user went looking for something.
func TestConsoleShowsContentAfterExpanding(t *testing.T) {
	log := cvs.NewCommandLog(10)
	log.Add(cvs.CommandResult{
		Command:  "cvs status",
		ExitCode: 0,
		Duration: 11 * time.Millisecond,
		Stdout:   "M docs/notes.txt\n",
	})
	console := NewConsoleModel(log)

	console.SetSize(PanelDims{LeftW: 80, Height: consoleCollapsed})
	console, _ = console.Update(nil) // refreshes content from the log
	if got := ansi.Strip(console.View()); !strings.Contains(got, "cvs status") {
		t.Fatalf("collapsed console should show the last command, got %q", got)
	}

	console.SetSize(PanelDims{LeftW: 80, Height: 6})
	if got := ansi.Strip(console.View()); !strings.Contains(got, "cvs status") {
		t.Errorf("expanded console is blank: %q", got)
	}
}

// The console opens itself when a command fails, which changes the
// layout on the next frame — but SetSize only runs on a focus change
// or a terminal resize. Rendering from the stored height therefore drew
// one collapsed line inside a six-row panel: exactly the frame a
// failure produced, with the reason for the failure invisible.
func TestConsoleRendersAtTheHeightItIsDrawnAt(t *testing.T) {
	log := cvs.NewCommandLog(10)
	log.Add(cvs.CommandResult{
		Command:  "cvs status",
		Stdout:   "cvs [update aborted]: connect to host failed\n",
		ExitCode: 2,
		Success:  false,
	})
	console := NewConsoleModel(log)
	console.SetSize(PanelDims{LeftW: 100, Height: consoleCollapsed})
	console, _ = console.Update(nil)

	collapsed := ansi.Strip(console.ViewAt(consoleCollapsed))
	if strings.Contains(collapsed, "→") {
		t.Errorf("the collapsed line should be one line, got %q", collapsed)
	}

	// Same model, no SetSize in between — the layout expanded it.
	expanded := ansi.Strip(console.ViewAt(6))
	if !strings.Contains(expanded, "FAILED 2") {
		t.Errorf("expanded console lost the failure: %q", expanded)
	}
	if !strings.Contains(expanded, "connect to host failed") {
		t.Errorf("expanded console does not say why it failed: %q", expanded)
	}
}

// The file pane answers two questions — "what changed here" and "let me
// walk the directories" — so it has two views. The old third one, sub,
// was the tree with every subdirectory unfolded: the same code path,
// differing in one boolean. It is a fold state now, not a mode.
func TestFileListViewsAndFolding(t *testing.T) {
	newList := func() FileListModel {
		fl := NewFileListModel()
		fl.width = 60
		fl.height = 20
		fl.SetFiles(".", []cvs.FileEntry{{Path: "top.txt", Status: "M"}},
			[]SubDirGroup{{
				Name:  "docs",
				Path:  "docs",
				Files: []cvs.FileEntry{{Path: "docs/a.txt", Status: "M"}, {Path: "docs/b.txt"}},
			}})
		return fl
	}

	t.Run("f toggles between exactly two views", func(t *testing.T) {
		fl := newList()
		if fl.viewMode != FileViewFlat {
			t.Fatalf("default view = %v, want flat", fl.viewMode)
		}
		fl.ToggleViewMode()
		if fl.viewMode != FileViewTree {
			t.Fatalf("after one press = %v, want tree", fl.viewMode)
		}
		fl.ToggleViewMode()
		if fl.viewMode != FileViewFlat {
			t.Errorf("after two presses = %v, want flat again", fl.viewMode)
		}
	})

	t.Run("tree starts folded and ~ unfolds everything", func(t *testing.T) {
		fl := newList()
		fl.ToggleViewMode()

		if got := fl.FileCount(); got != 1 {
			t.Errorf("folded tree shows %d files, want 1 (the top-level one)", got)
		}
		fl.ToggleExpandAll()
		if got := fl.FileCount(); got != 3 {
			t.Errorf("unfolded tree shows %d files, want 3", got)
		}
		fl.ToggleExpandAll()
		if got := fl.FileCount(); got != 1 {
			t.Errorf("folding again shows %d files, want 1", got)
		}
	})

	t.Run("the fold state survives a directory change", func(t *testing.T) {
		fl := newList()
		fl.ToggleViewMode()
		fl.ToggleExpandAll()

		// Walking into another directory must not refold what the user
		// just unfolded — the one thing the old sub mode did well.
		fl.SetFiles("src", nil, []SubDirGroup{{
			Name:  "deep",
			Path:  "src/deep",
			Files: []cvs.FileEntry{{Path: "src/deep/x.go", Status: "M"}},
		}})
		if got := fl.FileCount(); got != 1 {
			t.Errorf("new directory shows %d files, want its subdir unfolded", got)
		}
	})

	t.Run("the header names the fold state", func(t *testing.T) {
		fl := newList()
		if got := fl.ViewModeName(); got != "flat" {
			t.Errorf("chip = %q, want flat", got)
		}
		fl.ToggleViewMode()
		if got := fl.ViewModeName(); got != "tree" {
			t.Errorf("chip = %q, want tree", got)
		}
		fl.ToggleExpandAll()
		if got := fl.ViewModeName(); got != "tree · all open" {
			t.Errorf("chip = %q, want the open state named", got)
		}
	})
}

// A key that means nothing here is not offered here: folding
// subdirectories needs subdirectories on screen.
func TestFoldKeyOnlyOfferedInTheTreeView(t *testing.T) {
	flat := keyContext{tab: TabTree, focus: PanelRight, listMode: FileViewFlat}
	tree := keyContext{tab: TabTree, focus: PanelRight, listMode: FileViewTree}
	onTreePane := keyContext{tab: TabTree, focus: PanelLeft, listMode: FileViewTree}

	if liveKeys(flat)["~"] {
		t.Error("~ offered in the flat view, where there is nothing to fold")
	}
	if !liveKeys(tree)["~"] {
		t.Error("~ not offered in the tree view")
	}
	if liveKeys(onTreePane)["~"] {
		t.Error("~ offered while the tree pane has focus — that is its own collapse-all")
	}
}
