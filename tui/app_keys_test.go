package tui

import (
	"os"
	"path/filepath"
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
