package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazycvs/config"
	"lazycvs/cvs"

	tea "github.com/charmbracelet/bubbletea"
)

// Worklist lifecycle: a successful bulk action consumes the marks for
// its paths, a failed one keeps them (fix + retry), and add keeps them
// even on success (preparation for the follow-up commit).
func TestActionDoneConsumesMarks(t *testing.T) {
	newMarked := func() *App {
		app := newListingTestApp(t)
		app.filelist.marked = map[string]bool{"a.txt": true, "b.txt": true}
		return app
	}

	t.Run("success consumes", func(t *testing.T) {
		app := newMarked()
		updated, _ := app.Update(actionDoneMsg{paths: []string{"a.txt", "b.txt"}})
		if got := len(updated.(App).filelist.marked); got != 0 {
			t.Errorf("marks after success = %d, want 0", got)
		}
	})

	t.Run("failure keeps", func(t *testing.T) {
		app := newMarked()
		updated, _ := app.Update(actionDoneMsg{paths: []string{"a.txt", "b.txt"}, err: errors.New("boom")})
		if got := len(updated.(App).filelist.marked); got != 2 {
			t.Errorf("marks after failure = %d, want 2 (kept for retry)", got)
		}
	})

	t.Run("add keeps on success", func(t *testing.T) {
		app := newMarked()
		updated, _ := app.Update(actionDoneMsg{paths: []string{"a.txt", "b.txt"}, keepMarks: true})
		if got := len(updated.(App).filelist.marked); got != 2 {
			t.Errorf("marks after add = %d, want 2 (staged for commit)", got)
		}
	})

	t.Run("success consumes only acted paths", func(t *testing.T) {
		app := newMarked()
		updated, _ := app.Update(actionDoneMsg{paths: []string{"a.txt"}})
		marked := updated.(App).filelist.marked
		if marked["a.txt"] || !marked["b.txt"] {
			t.Errorf("want only a.txt consumed, got %v", marked)
		}
	})
}

// updateDoneMsg consumes on clean success but keeps the worklist when
// in-the-way paths block part of the update (they re-run after the
// resolve dialog and consume then).
func TestUpdateDoneMarkLifecycle(t *testing.T) {
	app := newListingTestApp(t)
	app.filelist.marked = map[string]bool{"a.txt": true}
	updated, _ := app.Update(updateDoneMsg{paths: []string{"a.txt"}, inTheWay: []string{"a.txt"}})
	if got := len(updated.(App).filelist.marked); got != 1 {
		t.Errorf("marks with in-the-way = %d, want 1 (kept)", got)
	}

	updated, _ = updated.(App).Update(updateDoneMsg{paths: []string{"a.txt"}})
	if got := len(updated.(App).filelist.marked); got != 0 {
		t.Errorf("marks after clean update = %d, want 0", got)
	}
}

// Enter on a recent-changes row must emit recentOpenMsg with the
// working-copy-relative path so the App can open the History tab.
func TestRecentDialogEnterOpensHistory(t *testing.T) {
	d := NewDialogModel()
	d.OpenRecent("Changes", 7, []recentEntry{
		{label: "a.txt", wcPath: "sub/a.txt"},
		{label: "b.txt", wcPath: "sub/b.txt"},
	})

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter produced no command")
	}
	msg, ok := cmd().(recentOpenMsg)
	if !ok {
		t.Fatalf("Enter produced %T, want recentOpenMsg", cmd())
	}
	if msg.path != "sub/b.txt" {
		t.Errorf("path = %q, want sub/b.txt (cursor row)", msg.path)
	}
	if d.Active() {
		t.Error("dialog still open after Enter")
	}
}

// r with a marked set in the Files tab must open ONE bulk revert
// dialog over every revertable marked path — not the single-file
// dialog for the cursor row (which silently ignored the rest).
func TestRevertWithMarksOpensBulkDialog(t *testing.T) {
	app := newListingTestApp(t)
	app.activeTab = TabTree
	app.focus = PanelLeft
	app.statusMap["top.txt"] = "M"
	app.statusMap["sub/direct.txt"] = "C"
	app.filelist.marked = map[string]bool{"top.txt": true, "sub/direct.txt": true}

	app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !app.dialog.Active() {
		t.Fatal("r with marks opened no dialog")
	}
	if got := len(app.dialog.files); got != 2 {
		t.Fatalf("bulk revert dialog has %d file(s), want 2: %v", got, app.dialog.files)
	}
	if app.dialog.commitStatuses["sub/direct.txt"] != "C" {
		t.Errorf("statuses not carried: %v", app.dialog.commitStatuses)
	}
}

// A fresh repo-global cache serves H instantly for ANY directory —
// no cvs round trip, dialog opens in the same frame with the
// client-side dir filter applied.
func TestRecentChangesServedFromCache(t *testing.T) {
	app := newListingTestApp(t)
	cfgMgr, err := config.NewConfigManager(filepath.Join(t.TempDir(), "cfg.toml"))
	if err != nil {
		t.Fatal(err)
	}
	app.cfgMgr = cfgMgr
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, "CVS", "Repository"), []byte("mod\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app.recentEvents = []cvs.HistoryEvent{
		{Code: "M", User: "anna", Rev: "1.2", File: "f.txt", RepoDir: "mod/sub", Time: time.Now()},
		{Code: "A", User: "timo", Rev: "1.1", File: "other.txt", RepoDir: "othermod", Time: time.Now()},
	}
	app.recentCoverage = time.Now().AddDate(0, 0, -7)
	app.recentFetched = time.Now()
	app.activeTab = TabTree
	app.focus = PanelLeft

	app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if !app.dialog.Active() {
		t.Fatal("H with fresh cache opened no dialog")
	}
	if got := len(app.dialog.recentEntries); got != 1 {
		t.Fatalf("cache-served dialog has %d entries, want 1 (dir filter): %+v", got, app.dialog.recentEntries)
	}
	if app.dialog.recentEntries[0].wcPath != "sub/f.txt" {
		t.Errorf("wcPath = %q, want sub/f.txt", app.dialog.recentEntries[0].wcPath)
	}
}

// After the TTL the local history is STALE but still true — H must
// show it immediately (stale-while-revalidate) instead of blocking on
// the server round trip.
func TestRecentChangesStaleCacheOpensImmediately(t *testing.T) {
	app := newListingTestApp(t)
	cfgMgr, err := config.NewConfigManager(filepath.Join(t.TempDir(), "cfg.toml"))
	if err != nil {
		t.Fatal(err)
	}
	app.cfgMgr = cfgMgr
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, "CVS", "Repository"), []byte("mod\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app.recentEvents = []cvs.HistoryEvent{
		{Code: "M", User: "anna", Rev: "1.2", File: "f.txt", RepoDir: "mod/sub", Time: time.Now()},
	}
	app.recentCoverage = time.Now().AddDate(0, 0, -7)
	app.recentFetched = time.Now().Add(-30 * time.Minute) // well past the TTL
	app.activeTab = TabTree
	app.focus = PanelLeft

	cmd := app.delegateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if !app.dialog.Active() {
		t.Fatal("H with stale-but-covered cache did not open the dialog immediately")
	}
	if cmd == nil {
		t.Fatal("no background revalidation command dispatched")
	}
}
