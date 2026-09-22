package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	app.staged = NewStagedModel()
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
		app.staged = NewStagedModel()
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
		if !strings.Contains(app.notification, "2 file(s) marked") ||
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
		app.staged.input.SetValue("fix the parser")
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
	app.staged = NewStagedModel()
	app.history = NewHistoryModel()
	app.setResult("✗ Commit failed for docs/notes.txt — see Console", false)

	if !app.bannerIsFailure() {
		t.Fatal("a failed result should read as a failure banner")
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
	app2.staged = NewStagedModel()
	app2.history = NewHistoryModel()
	app2.handleGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if app2.console.filter != ConsoleCompact {
		t.Errorf("filter changed without a failure: %v", app2.console.filter)
	}
}
