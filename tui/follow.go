package tui

import (
	"os"
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
// A path outside the working copy but inside another checkout — its
// directory carries CVS/Root — re-roots lazycvs there (switchWorkDir),
// in-process: every directory of a CVS checkout has a CVS/Root, so the
// start directory is only ever one possible root, and a file manager
// walking up out of it expects the tree to follow. Such a path waits
// in pendingReroot under the same idle rule, plus one more: no action
// may be running, because its result belongs to the root it ran in.
// Paths outside any checkout are dropped: lazycvs stays where it is.

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

// rerootTarget maps an absolute path from the follow channel to the
// checkout it belongs to: root is the path itself for a directory, its
// parent for a file, and must carry CVS/Root; rel is what to select
// under it ("." for the root itself, the file's name otherwise). ok is
// false for anything else — a relative path, a path that vanished, a
// directory outside any checkout. Symlinks are resolved as in
// followTarget, so the root the app switches to is canonical.
func rerootTarget(path string) (root, rel string, ok bool) {
	if !filepath.IsAbs(path) {
		return "", "", false
	}
	abs, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", false
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", false
	}
	root, rel = abs, "."
	if !info.IsDir() {
		root, rel = filepath.Dir(abs), filepath.Base(abs)
	}
	if fi, err := os.Stat(filepath.Join(root, "CVS", "Root")); err != nil || fi.IsDir() {
		return "", "", false
	}
	return root, rel, true
}

// followIdle reports whether a followed path may move the selection now.
func (m *App) followIdle() bool {
	return m.followReady && !m.dialog.Active() && !m.search.active && m.activeTab == TabTree
}

// queueFollow records the latest followed path: relative, in
// pendingFollow, when it lies inside the working copy; absolute, in
// pendingReroot, when it lies in another checkout. The latest path wins
// over both kinds — the file manager is in one place. Anything else is
// dropped.
func (m *App) queueFollow(path string) {
	if rel, ok := followTarget(m.exec, path); ok {
		m.pendingFollow, m.pendingReroot = rel, ""
		return
	}
	if _, _, ok := rerootTarget(path); ok {
		m.pendingFollow, m.pendingReroot = "", path
	}
}

// applyPendingFollow moves the selection to the queued path once the
// user is idle — or re-roots the app when the queued path is in another
// checkout. Side effects: consumes the pending path; a follow changes
// tree/filelist selection and focus via revealPath; a re-root replaces
// the working copy (switchWorkDir) and returns the loads for it.
//
// A re-root additionally waits for a running action (the progress
// banner): a commit or revert in flight was dispatched against this
// root, and its *DoneMsg — consuming marks, refreshing the directories
// it names — has to land here, not in a root where those paths mean
// something else.
func (m *App) applyPendingFollow() tea.Cmd {
	if !m.followIdle() {
		return nil
	}
	if m.pendingReroot != "" {
		if m.bannerInProgress() {
			return nil
		}
		path := m.pendingReroot
		m.pendingReroot = ""
		root, rel, ok := rerootTarget(path)
		if !ok {
			return nil // vanished between queueing and now
		}
		cmd := m.switchWorkDir(root)
		if rel != "." {
			// A followed file: land on it once the new tree has loaded,
			// the way a file argument on the command line does.
			m.initialPath = rel
		}
		return cmd
	}
	if m.pendingFollow == "" {
		return nil
	}
	target := m.pendingFollow
	m.pendingFollow = ""
	return m.revealPath(target)
}
