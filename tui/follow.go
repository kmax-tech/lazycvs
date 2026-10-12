package tui

import (
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// Follow mode (`lazycvs -follow <fifo>`): another program — a file
// manager such as conspectus — writes absolute paths, one per line, into
// a FIFO, and lazycvs follows the user as they navigate next door. main
// reads the FIFO and delivers each line as a FollowMsg.
//
// The rule is simple: the working-copy root IS the followed directory.
// Every directory of a CVS checkout carries CVS/Root, so any of them can
// be a root, and a tree rooted where the file manager stands is the one
// that reads unambiguously next to it — a root somewhere above, with the
// current directory buried as a subdirectory, does not. A followed
// directory other than the current root therefore re-roots lazycvs there
// (switchWorkDir, in-process; the scans of the old root are cancelled).
// The current root itself, or a file in it, only moves the selection.
//
// A path only applies while the user is not in the middle of something:
// no dialog, no fuzzy search, Tree tab active, tree loaded — and, for a
// re-root, no action running, because its result belongs to the root it
// ran in. Until then the latest path waits (pendingFollow /
// pendingReroot) and lands on the first Update after the user is done:
// closing a commit dialog lands where the file manager is by then, not on
// a path from minutes ago. Paths outside any checkout are dropped:
// lazycvs stays where it is.

// FollowMsg carries one line read from the -follow FIFO.
type FollowMsg struct{ Path string }

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

// queueFollow records the latest followed path: in pendingFollow (what
// to select, "." or a file name) when it lies in the current root
// directory itself, in pendingReroot (absolute) when it names another
// checkout directory. The latest path wins over both kinds — the file
// manager is in one place. Paths outside any checkout are dropped.
func (m *App) queueFollow(path string) {
	root, rel, ok := rerootTarget(path)
	if !ok {
		return
	}
	if root == canonicalDir(m.exec.WorkDir) {
		m.pendingFollow, m.pendingReroot = rel, ""
		return
	}
	m.pendingFollow, m.pendingReroot = "", path
}

// canonicalDir resolves symlinks so roots compare by identity
// (/tmp ↔ /private/tmp on macOS); an unresolvable path compares as given.
func canonicalDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// applyPendingFollow moves the selection to the queued path once the
// user is idle — or re-roots the app when the queued path is another
// checkout directory. Side effects: consumes the pending path; a follow changes
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
