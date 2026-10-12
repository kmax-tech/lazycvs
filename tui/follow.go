package tui

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kmax-tech/lazycvs/cvs"
)

// Follow mode (`lazycvs -follow <fifo>`): another program — a file
// manager such as conspectus — writes absolute paths, one per line, into
// a FIFO, and lazycvs moves its selection there as the user navigates
// next door. main reads the FIFO and delivers each line as a FollowMsg.
//
// A path only applies while the user is not in the middle of something:
// no dialog, no fuzzy search, Tree tab active, initial tree loaded.
// Otherwise the latest path waits in pendingFollow and is applied on the
// first Update after the user is done — closing a commit dialog lands on
// where the file manager is by then, not on a path from minutes ago.
// Paths outside the working copy are dropped: lazycvs stays where it is.

// FollowMsg carries one line read from the -follow FIFO.
type FollowMsg struct{ Path string }

// followTarget maps an absolute path from the follow channel to a path
// relative to the working copy. ok is false for anything outside it
// (relative input, another checkout, a symlink escaping the checkout) —
// the normal case when the user browses elsewhere, not an error.
// Symlinks are resolved on both sides, as ValidatePath does, so
// /tmp ↔ /private/tmp style aliases still match.
func followTarget(exec *cvs.CVSExecutor, path string) (rel string, ok bool) {
	if !filepath.IsAbs(path) {
		return "", false
	}
	wd, err := filepath.EvalSymlinks(exec.WorkDir)
	if err != nil {
		wd = exec.WorkDir
	}
	abs, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false // vanished between write and read: nothing to show
	}
	rel, err = filepath.Rel(wd, abs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return ".", true
	}
	if exec.ValidatePath(rel) != nil {
		return "", false
	}
	return rel, true
}

// followIdle reports whether a followed path may move the selection now.
func (m *App) followIdle() bool {
	return m.followReady && !m.dialog.Active() && !m.search.active && m.activeTab == TabTree
}

// queueFollow records the latest followed path (relative), or drops it
// when it lies outside the working copy.
func (m *App) queueFollow(path string) {
	if rel, ok := followTarget(m.exec, path); ok {
		m.pendingFollow = rel
	}
}

// applyPendingFollow moves the selection to the queued path once the user
// is idle. Side effects: consumes pendingFollow, changes tree/filelist
// selection and focus via revealPath.
func (m *App) applyPendingFollow() tea.Cmd {
	if m.pendingFollow == "" || !m.followIdle() {
		return nil
	}
	target := m.pendingFollow
	m.pendingFollow = ""
	return m.revealPath(target)
}
