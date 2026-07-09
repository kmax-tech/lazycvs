package tui

import (
	"errors"
	"testing"

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
