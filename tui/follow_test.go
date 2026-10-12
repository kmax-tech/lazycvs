package tui

import (
	"path/filepath"
	"testing"
)

func sendFollow(t *testing.T, app App, path string) App {
	t.Helper()
	next, _ := app.Update(FollowMsg{Path: path})
	got, ok := next.(App)
	if !ok {
		t.Fatalf("Update returned %T, want App", next)
	}
	return got
}

func selectedTreePath(app App) string {
	if n := app.tree.SelectedNode(); n != nil {
		return n.Path
	}
	return ""
}

// Following the current root itself is no re-root: the root is already
// where the file manager stands, so only the selection moves (to it).
func TestFollowCurrentRootOnlySelects(t *testing.T) {
	app, _, wc := newRerootTestApp(t)
	app.revealPath("sub")
	app = sendFollow(t, app, wc)
	if app.exec.WorkDir != wc {
		t.Fatalf("WorkDir = %q, want unchanged %q", app.exec.WorkDir, wc)
	}
	if got := selectedTreePath(app); got != "." {
		t.Errorf("tree cursor = %q, want the root selected", got)
	}
	if app.pendingFollow != "" || app.pendingReroot != "" {
		t.Errorf("pending = (%q, %q), want both consumed", app.pendingFollow, app.pendingReroot)
	}
}

// A followed file in the current root selects the file, no re-root.
func TestFollowFileInRootSelectsIt(t *testing.T) {
	app, _, wc := newRerootTestApp(t)
	app = sendFollow(t, app, filepath.Join(wc, "top.txt"))
	if app.exec.WorkDir != wc {
		t.Fatalf("WorkDir = %q, want unchanged %q", app.exec.WorkDir, wc)
	}
	if sel := app.filelist.SelectedFile(); sel == nil || sel.Path != "top.txt" {
		t.Errorf("selected file = %+v, want top.txt", sel)
	}
}

// Busy on another tab, or before the tree has loaded: the re-root waits.
func TestFollowWaitsOnOtherTabsAndBeforeTreeLoad(t *testing.T) {
	for name, mutate := range map[string]func(*App){
		"history tab":     func(a *App) { a.activeTab = TabHistory },
		"tree not loaded": func(a *App) { a.followReady = false },
	} {
		app, parent, wc := newRerootTestApp(t)
		mutate(&app)
		app = sendFollow(t, app, parent)
		if app.pendingReroot != parent || app.exec.WorkDir != wc {
			t.Errorf("%s: pendingReroot = %q, WorkDir = %q — want %q queued and the root unchanged",
				name, app.pendingReroot, app.exec.WorkDir, parent)
		}
	}
}
