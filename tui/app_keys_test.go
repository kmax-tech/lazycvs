package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kmax-tech/lazycvs/cvs"
)

// Miller-column crossing: → from the tree with nothing to expand lands
// in the right pane; ← from the right pane comes back. Uses the listing
// fixture (empty tree → CanExpand is false, so → must cross).
func TestHorizontalKeysCrossPanes(t *testing.T) {
	app := newListingTestApp(t)
	app.activeTab = TabTree
	app.focus = PanelLeft

	right := tea.KeyMsg{Type: tea.KeyRight}
	left := tea.KeyMsg{Type: tea.KeyLeft}

	app.delegateKey(right)
	if app.focus != PanelRight {
		t.Fatalf("→ with nothing to expand: focus = %v, want PanelRight", app.focus)
	}

	app.delegateKey(left)
	if app.focus != PanelLeft {
		t.Fatalf("← from right pane: focus = %v, want PanelLeft", app.focus)
	}

	// h behaves like ← (keys.Left binds both).
	app.focus = PanelRight
	app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if app.focus != PanelLeft {
		t.Fatalf("h from right pane: focus = %v, want PanelLeft", app.focus)
	}
}

// While the cursor dir can still expand, → must stay in the tree and
// expand it instead of crossing panes.
func TestRightExpandsBeforeCrossing(t *testing.T) {
	app := newListingTestApp(t)
	app.activeTab = TabTree
	app.focus = PanelLeft
	dir := &TreeNode{Path: "sub", IsDir: true, Children: []*TreeNode{{Path: "sub/deep", IsDir: true}}}
	app.tree.root = []*TreeNode{dir}
	app.tree.rebuildFlat()

	app.delegateKey(tea.KeyMsg{Type: tea.KeyRight})
	if app.focus != PanelLeft {
		t.Fatalf("→ on expandable dir crossed panes; focus = %v, want PanelLeft", app.focus)
	}
	if !dir.Expanded {
		t.Fatal("→ on collapsed dir did not expand it")
	}

	// Second → on the now-expanded dir crosses.
	app.delegateKey(tea.KeyMsg{Type: tea.KeyRight})
	if app.focus != PanelRight {
		t.Fatalf("→ on expanded dir: focus = %v, want PanelRight", app.focus)
	}
}

// A leaf dir (files only, no subdirs) must cross into the right pane
// on the FIRST → — not "expand" an empty node and require a second
// press. Uses the fixture's sub/deep dir, which contains only files.
func TestRightOnLeafDirCrossesImmediately(t *testing.T) {
	app := newListingTestApp(t)
	app.activeTab = TabTree
	app.focus = PanelLeft
	// Collapsed, unloaded node for a dir that has no subdirs on disk.
	app.tree.workDir = app.exec.WorkDir
	app.tree.root = []*TreeNode{{Path: "sub/deep", IsDir: true}}
	app.tree.rebuildFlat()

	app.delegateKey(tea.KeyMsg{Type: tea.KeyRight})
	if app.focus != PanelRight {
		t.Fatalf("→ on leaf dir: focus = %v, want PanelRight on first press", app.focus)
	}
}

// B in the History tab restores the loaded file's .lazycvs-backup in
// place — the tab where the pair merge (M) just wrote that backup.
// Regression test: the RestoreBackup case used to be gated with
// activeTab != TabHistory, so B was a dead key exactly where the
// USER_GUIDE promised it ("B restores it").
func TestRestoreBackupWorksInHistoryTab(t *testing.T) {
	app := newListingTestApp(t)
	app.histRevisions = make(map[string]*cvs.FileHistory)
	app.histDiffs = make(map[string]map[string]*cvs.DiffResult)
	app.histContents = make(map[string]map[string]string)
	app.histPending = make(map[string]bool)
	app.histStreams = make(map[uint64]chan tea.Msg)
	app.activeTab = TabHistory
	app.focus = PanelLeft
	app.history = NewHistoryModel()
	app.history.path = "top.txt"

	backup := filepath.Join(app.exec.WorkDir, "top.txt"+backupSuffix)
	if err := os.WriteFile(backup, []byte("pre-merge content"), 0644); err != nil {
		t.Fatal(err)
	}

	if cmd := app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("B")}); cmd == nil {
		t.Fatal("B in History produced no command — restore did not run")
	}
	data, err := os.ReadFile(filepath.Join(app.exec.WorkDir, "top.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pre-merge content" {
		t.Errorf("working file = %q, want the backup content", data)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Error("backup file still present after restore")
	}
}

// `a` (add) used to require a FileEntry, which only the file list
// produces — so with the cursor on an untracked file in the *tree*
// pane the key did nothing, while the keybar offered it. The keybar
// promises by status now, so the handler has to resolve by path.
func TestAddWorksFromTreePane(t *testing.T) {
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.tree, _ = app.tree.Update(treeLoadedMsg{nodes: scanDir(app.exec.WorkDir, ".")})
	app.activeTab = TabTree
	app.focus = PanelLeft

	// An untracked file the tree can put a cursor on.
	untracked := "fresh.txt"
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, untracked), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	app.statusMap[untracked] = "?"
	app.tree.showFiles = true
	app.tree, _ = app.tree.Update(treeLoadedMsg{nodes: scanDir(app.exec.WorkDir, ".")})
	// The tree carries statuses the same way the app feeds them in.
	app.tree.RefreshStatus(app.statusMap, app.staleDirs)
	found := false
	for i, f := range app.tree.flat {
		if f.node.Path == untracked {
			app.tree.cursor, found = i, true
			break
		}
	}
	if !found {
		t.Fatalf("tree does not list %s as a row", untracked)
	}

	ctx := app.keyContext()
	if ctx.status != "?" {
		t.Fatalf("context status = %q, want ? (the keybar offers a:add on it)", ctx.status)
	}
	if !liveKeys(ctx)["a"] {
		t.Fatal("keybar does not offer a:add on an untracked tree row")
	}
	if cmd := app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); cmd == nil {
		t.Error("a produced no command — the offered key is dead in the tree pane")
	}
}

// A key that does nothing and says nothing reads as a broken app —
// the complaint issue 021 collected. History is read-only on purpose,
// so `u` refuses out loud.
func TestUpdateInHistoryExplainsItself(t *testing.T) {
	app := newListingTestApp(t)
	app.history = NewHistoryModel()
	app.markedTab = NewMarkedTabModel()
	app.activeTab = TabHistory

	cmd, handled := app.handleGlobalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if !handled {
		t.Fatal("u in History should be consumed, not passed on to the panes")
	}
	if cmd == nil {
		t.Fatal("u in History produced no feedback — silent refusal")
	}
	if !strings.Contains(app.notification, "read-only") {
		t.Errorf("banner should explain the refusal, got %q", app.notification)
	}
}

// Marks and a typed commit message are work a quit destroys. One
// confirming keystroke is cheaper than losing a twenty-file worklist.
func TestQuitGuardsUnsavedWork(t *testing.T) {
	newApp := func(t *testing.T) *App {
		app := newListingTestApp(t)
		app.markedTab = NewMarkedTabModel()
		app.history = NewHistoryModel()
		return app
	}
	press := func(app *App, r rune) (tea.Cmd, bool) {
		return app.handleGlobalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Only ever run a command we expect to be tea.Quit: a notification
	// command is a tea.Tick and would hold the test for the banner's
	// full display window.
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}

	t.Run("clean state quits at once", func(t *testing.T) {
		app := newApp(t)
		cmd, _ := press(app, 'q')
		if !quits(cmd) {
			t.Error("q with nothing pending should quit immediately")
		}
	})

	t.Run("marks need a second q", func(t *testing.T) {
		app := newApp(t)
		app.filelist.marked["a.txt"] = true
		app.filelist.marked["b.txt"] = true

		if _, handled := press(app, 'q'); !handled {
			t.Fatal("q should be consumed")
		}
		if !app.quitPending {
			t.Error("quit should be pending after the warning")
		}
		if !strings.Contains(app.notification, "2 files marked") ||
			!strings.Contains(app.notification, "press q again") {
			t.Errorf("banner should name the work and the way out, got %q", app.notification)
		}

		cmd, _ := press(app, 'q')
		if !quits(cmd) {
			t.Error("the confirming q should quit")
		}
	})

	t.Run("an unsent commit message counts as work", func(t *testing.T) {
		app := newApp(t)
		app.markedTab.input.SetValue("fix the parser")
		press(app, 'q')
		if !app.quitPending {
			t.Fatal("q threw away a typed commit message")
		}
		if !strings.Contains(app.notification, "commit message") {
			t.Errorf("banner should name the message, got %q", app.notification)
		}
	})

	t.Run("any other key cancels the pending quit", func(t *testing.T) {
		app := newApp(t)
		app.filelist.marked["a.txt"] = true
		press(app, 'q')

		// Update has a value receiver: the cleared flag lives in the
		// model it returns, which is what the runtime keeps.
		next, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		if next.(App).quitPending {
			t.Error("the warning should expire as soon as the user works on")
		}
	})
}

// "see Console" is only an answer if the console then shows the
// failure instead of everything that happened after it.
func TestConsoleOpensOnErrorsAfterAFailure(t *testing.T) {
	app := newListingTestApp(t)
	app.markedTab = NewMarkedTabModel()
	app.history = NewHistoryModel()
	app.cmdLog.Add(cvs.CommandResult{Command: "cvs commit", ExitCode: 1})
	app.setResult("✗ Commit failed for docs/notes.txt — see Console", false)

	if !app.bannerPointsToConsole() {
		t.Fatal("a failed command plus a red banner should point at the console")
	}

	// A refusal that logged nothing must not send the user there.
	quiet := newListingTestApp(t)
	quiet.markedTab = NewMarkedTabModel()
	quiet.history = NewHistoryModel()
	quiet.setResult("2 file(s) marked — press q again to quit", false)
	if quiet.bannerPointsToConsole() {
		t.Error("a warning with nothing in the log should not point at the console")
	}
	if _, handled := app.handleGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlJ}); !handled {
		t.Fatal("C-j should focus the console")
	}
	if app.focus != PanelConsole {
		t.Errorf("focus = %v, want PanelConsole", app.focus)
	}
	if app.console.filter != ConsoleErrors {
		t.Errorf("console filter = %v, want ConsoleErrors", app.console.filter)
	}

	// Without a failure, C-j leaves the filter alone.
	app2 := newListingTestApp(t)
	app2.markedTab = NewMarkedTabModel()
	app2.history = NewHistoryModel()
	app2.handleGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if app2.console.filter != ConsoleCompact {
		t.Errorf("filter changed without a failure: %v", app2.console.filter)
	}
}

// Looking at the marked set must not change it. Entering the tab used
// to mark the file under the cursor whenever nothing was marked yet —
// a silent edit to the worklist, made by navigation, that the next
// bulk action then acted on.
func TestOpeningTheMarkedTabMarksNothing(t *testing.T) {
	app := newListingTestApp(t)
	app.markedTab = NewMarkedTabModel()
	app.history = NewHistoryModel()
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.updateFileListForDir(".")
	app.filelist.FocusOnFile("top.txt")

	app.handleGlobalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})

	if app.activeTab != TabMarked {
		t.Fatalf("3 did not open the marked tab: %v", app.activeTab)
	}
	if n := len(app.filelist.marked); n != 0 {
		t.Errorf("opening the tab marked %d file(s): %v", n, app.filelist.marked)
	}
	if n := len(app.markedTab.files); n != 0 {
		t.Errorf("marked list shows %d file(s) nobody marked", n)
	}
}

// The tab bar's labels and the click map are built from one list; when
// they were built twice, renaming a tab shifted every click target.
func TestTabClickTargetsFollowTheLabels(t *testing.T) {
	app := newListingTestApp(t)
	app.width = 100
	app.filelist.marked["a.txt"] = true // widens the third label

	names := app.tabNames()
	pos := 0
	for i, name := range names {
		label := tabLabel(i, name)
		mid := pos + lipgloss.Width(label)/2
		if got := app.tabAtX(mid); got != i {
			t.Errorf("click in the middle of %q hits tab %d, want %d", label, got, i)
		}
		pos += lipgloss.Width(label) + 1 // the │ separator
	}
}

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// In a text input a letter is text. The search box matched keys.Down /
// keys.Up, which also bind j and k — so those two letters moved the
// result cursor and a file name containing them could not be typed
// (issue 025, item 1). The arrows and C-n / C-p pick.
func TestSearchTypesJAndK(t *testing.T) {
	s := NewSearchModel()
	s.Open("/tmp")
	s.allPaths = []string{"jack.txt", "kilo.txt", "abc.txt"}
	for _, c := range []string{"j", "k"} {
		s, _ = s.Update(runeKey(c))
	}
	if got := s.input.Value(); got != "jk" {
		t.Fatalf("typed j, k: input = %q, want \"jk\"", got)
	}

	s = NewSearchModel()
	s.Open("/tmp")
	s.allPaths = []string{"a1.txt", "a2.txt", "a3.txt"}
	s, _ = s.Update(runeKey("a"))
	if len(s.results) != 3 {
		t.Fatalf("query a: %d results, want 3", len(s.results))
	}
	s, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s, _ = s.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if s.cursor != 2 {
		t.Errorf("↓ then C-n: cursor = %d, want 2", s.cursor)
	}
	s, _ = s.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	s, _ = s.Update(tea.KeyMsg{Type: tea.KeyUp})
	if s.cursor != 0 {
		t.Errorf("C-p then ↑: cursor = %d, want 0", s.cursor)
	}
}

// The keybar offers F (status filter) on the Files tab with either
// pane focused; the handler lived in the file list alone, so with the
// focus on the tree nothing happened (issue 025, item 3).
func TestFilterWorksFromEitherPane(t *testing.T) {
	for _, focus := range []Panel{PanelLeft, PanelRight} {
		app := newListingTestApp(t)
		app.markedTab = NewMarkedTabModel()
		app.activeTab = TabTree
		app.focus = focus
		if _, handled := app.handleGlobalKey(runeKey("F")); !handled {
			t.Fatalf("focus %v: F not handled", focus)
		}
		if app.filelist.filter != "*" {
			t.Errorf("focus %v: filter = %q after one F, want \"*\"", focus, app.filelist.filter)
		}
	}

	// The console keeps its own F (filter the log).
	app := newListingTestApp(t)
	app.markedTab = NewMarkedTabModel()
	app.activeTab = TabTree
	app.focus = PanelConsole
	if _, handled := app.handleGlobalKey(runeKey("F")); handled {
		t.Error("console focus: the file filter took F away from the console")
	}
	if app.filelist.filter != "" {
		t.Errorf("console focus: file filter = %q, want unchanged", app.filelist.filter)
	}
}

// < / > scroll the History content sideways and the keybar offers them
// with either focus; with the focus on the content pane they fell into
// the viewport and did nothing (issue 025, item 2).
func TestHistoryScrollWorksFromEitherPane(t *testing.T) {
	for _, focus := range []Panel{PanelLeft, PanelRight} {
		app := newListingTestApp(t)
		app.history = NewHistoryModel()
		app.markedTab = NewMarkedTabModel()
		app.activeTab = TabHistory
		app.focus = focus
		app.history.path = "top.txt"
		before := app.history.hOffset
		if _, handled := app.handleGlobalKey(runeKey(">")); !handled {
			app.delegateKey(runeKey(">"))
		}
		if app.history.hOffset <= before {
			t.Errorf("focus %v: > left hOffset at %d", focus, app.history.hOffset)
		}
	}
}

// u and U must address the same row. With the cursor on a file in the
// file pane, u updated the tree's directory while the progress line
// named the file (issue 025, item 11). A recording stand-in for cvs
// shows which path the command receives.
func TestUpdateTargetsTheCursorFile(t *testing.T) {
	app := newListingTestApp(t)
	root := app.exec.WorkDir
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(t.TempDir(), "cvs")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	app.exec.CVSBin = fake
	app.exec.Timeout = 10 * time.Second // the fixture's zero timeout expires before the process starts
	app.tree = NewTreeModel(root)
	app.tree, _ = app.tree.Update(treeLoadedMsg{nodes: scanDir(root, ".")})
	app.markedTab = NewMarkedTabModel()
	app.activeTab = TabTree
	app.updateFileListForDir(".")
	app.focus = PanelRight

	// The first rows may be directory headers — walk to a file row.
	var file *cvs.FileEntry
	for c := 0; c < 20 && file == nil; c++ {
		app.filelist.cursor = c
		file = app.filelist.SelectedFile()
	}
	if file == nil {
		t.Fatal("fixture: no file row in the file pane")
	}
	if node := app.tree.SelectedNode(); node != nil && node.Path == file.Path {
		t.Fatalf("fixture: tree cursor is on the same path %s — the test would not tell the two targets apart", file.Path)
	}
	cmd, handled := app.handleGlobalKey(runeKey("u"))
	if !handled || cmd == nil {
		t.Fatalf("u: handled=%v cmd=%v, want a command", handled, cmd != nil)
	}
	msg, ok := cmd().(updateDoneMsg)
	if !ok {
		t.Fatalf("u produced %T, want updateDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("stand-in cvs failed: %v", msg.err)
	}
	if len(msg.paths) != 1 || msg.paths[0] != file.Path {
		t.Errorf("update reported paths %v, want [%s]", msg.paths, file.Path)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("stand-in cvs was not run: %v", err)
	}
	args := strings.Fields(string(got))
	if len(args) == 0 || args[len(args)-1] != file.Path {
		t.Errorf("cvs received %v, want the last argument to be %s", args, file.Path)
	}
}
