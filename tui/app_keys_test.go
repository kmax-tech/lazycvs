package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
