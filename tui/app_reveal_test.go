package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// revealPath is the single navigation both the fuzzy finder's pick and
// the command-line path argument go through. A *file* argument has to
// land on the file itself, not merely in its directory: the tree pane
// carries directories only (flatten skips files unless showFiles), so
// handing the file path to the tree alone leaves the cursor at the root.
func newRevealTestApp(t *testing.T) *App {
	t.Helper()
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.tree, _ = app.tree.Update(treeLoadedMsg{nodes: scanDir(app.exec.WorkDir, ".")})
	return app
}

func TestRevealPathFocusesFile(t *testing.T) {
	app := newRevealTestApp(t)

	app.revealPath("sub/deep/modified.txt")

	sel := app.filelist.SelectedFile()
	if sel == nil {
		t.Fatal("no file selected in the right pane")
	}
	if sel.Path != "sub/deep/modified.txt" {
		t.Errorf("filelist cursor = %q, want sub/deep/modified.txt", sel.Path)
	}
	if app.focus != PanelRight {
		t.Errorf("focus = %v, want PanelRight (so e/E/c/D act on the file)", app.focus)
	}
}

func TestRevealPathSelectsDirectory(t *testing.T) {
	app := newRevealTestApp(t)

	app.revealPath("sub")

	node := app.tree.SelectedNode()
	if node == nil {
		t.Fatal("no node selected in the tree")
	}
	if node.Path != "sub" {
		t.Errorf("tree cursor = %q, want sub", node.Path)
	}
	if app.focus != PanelLeft {
		t.Errorf("focus = %v, want PanelLeft", app.focus)
	}
}

// The path from the command line (`lazycvs path/to/file`) reaches the
// App as initialPath and is resolved when the tree finishes loading.
// Before revealPath was shared, that branch asked the tree to select
// the file directly and the cursor silently fell back to the root.
func TestInitialPathLandsOnFile(t *testing.T) {
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.initialPath = "sub/deep/modified.txt"

	next, _ := app.Update(treeLoadedMsg{nodes: scanDir(app.exec.WorkDir, ".")})
	got, ok := next.(App)
	if !ok {
		t.Fatalf("Update returned %T, want App", next)
	}

	sel := got.filelist.SelectedFile()
	if sel == nil {
		t.Fatal("startup with a file argument selected no file")
	}
	if sel.Path != "sub/deep/modified.txt" {
		t.Errorf("filelist cursor = %q, want sub/deep/modified.txt", sel.Path)
	}
	if got.initialPath != "" {
		t.Errorf("initialPath = %q, want it consumed", got.initialPath)
	}
}

// `d` used to throw the user into the History tab — a different
// question (who changed this, and when) with its own navigation. It
// now shows the diff in the pane beside the file, and a second d
// closes it again, leaving the cursor where it started.
func TestDiffKeyTogglesTheDiffPaneInPlace(t *testing.T) {
	app := newRevealTestApp(t)
	app.activeTab = TabTree
	app.tree.RefreshStatus(app.statusMap, app.staleDirs)
	app.updateFileListForDir(".")
	if !app.filelist.FocusOnFile("top.txt") {
		t.Fatal("fixture does not list top.txt")
	}
	app.focus = PanelRight

	press := func() {
		app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	}

	press()
	if app.activeTab != TabTree {
		t.Fatalf("d left the Files tab: activeTab = %v", app.activeTab)
	}
	if app.treeMode != TreeViewDetails {
		t.Fatal("d did not open the diff layout")
	}
	if app.previewPath != "top.txt" {
		t.Errorf("diff pane shows %q, want top.txt", app.previewPath)
	}
	if node := app.tree.SelectedNode(); node == nil || node.Path != "top.txt" {
		t.Errorf("cursor did not follow the file into the diff layout: %+v", node)
	}
	if app.focus != PanelLeft {
		t.Errorf("focus = %v, want PanelLeft so j/k moves between files", app.focus)
	}

	press()
	if app.treeMode != TreeViewFiles {
		t.Fatal("a second d did not close the diff layout")
	}
	if f := app.filelist.SelectedFile(); f == nil || f.Path != "top.txt" {
		t.Errorf("closing the diff lost the file: %+v", f)
	}
	if app.focus != PanelRight {
		t.Errorf("focus = %v, want PanelRight (back on the file list)", app.focus)
	}
}

// Esc is the other way out of a view the user opened deliberately.
func TestEscapeClosesTheDiffPane(t *testing.T) {
	app := newRevealTestApp(t)
	app.activeTab = TabTree
	app.staged = NewStagedModel()
	app.history = NewHistoryModel()
	app.updateFileListForDir(".")
	app.filelist.FocusOnFile("top.txt")
	app.focus = PanelRight

	app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if app.treeMode != TreeViewDetails {
		t.Fatal("d did not open the diff layout")
	}

	if _, handled := app.handleGlobalKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled {
		t.Fatal("esc should be consumed")
	}
	if app.treeMode != TreeViewFiles {
		t.Error("esc did not close the diff layout")
	}
}
