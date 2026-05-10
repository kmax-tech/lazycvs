package tui

import (
	"lazycvs/cvs"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// CVS status loading. The general flow is:
//   user action / startup → backgroundDirScan or refreshStagedFiles
//   → loadDirStatus / refreshStagedFiles dispatched per directory or path
//   → dirStatusMsg / stagedStatusMsg handlers in app_core.go merge into
//     m.statusMap via m.applyStatuses

// resolveFileStatus returns the CVS status code for path. If statusMap
// has no entry, walks every ancestor directory from the file's
// immediate parent up to the working-copy root, looking for a missing
// CVS/ marker. A *single* missing marker anywhere in the chain means
// the file is in a user-created subdirectory that hasn't been
// `cvs add`-ed yet — its files aren't tracked, so we report "?". The
// file is only treated as tracked-but-clean ("") when every ancestor
// has its own CVS/ marker.
//
// Stat'ing each ancestor is cheap (the filesystem caches inode lookups
// and CVS depths are usually shallow), so there's no artificial cap on
// the walk: an unbroken chain of CVS/ markers all the way to the root
// is the only condition that returns "".
func (m *App) resolveFileStatus(path string) string {
	if s := m.statusMap[path]; s != "" {
		return s
	}
	dir := filepath.Dir(path)
	for {
		cvsDir := filepath.Join(m.exec.WorkDir, dir, "CVS")
		if _, err := os.Stat(cvsDir); os.IsNotExist(err) {
			return "?"
		}
		if dir == "." || dir == "/" || dir == "" {
			break
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// statusesFor returns a path → status code map for the given paths,
// using resolveFileStatus per entry (so untracked files in
// CVS-less subdirs get "?" rather than ""). Used by the commit /
// remove dialogs and any other multi-path action that needs to
// display or branch on status per file.
func (m *App) statusesFor(paths []string) map[string]string {
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		out[p] = m.resolveFileStatus(p)
	}
	return out
}

// refreshStagedFiles runs `cvs status` for the given file paths. CVS
// reports basenames only when status is invoked on individual files,
// so this remaps basenames back to the caller's full paths before
// returning the message.
func refreshStagedFiles(exec *cvs.CVSExecutor, paths []string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"status"}, paths...)
		result, _ := exec.RunReadOnly(args...)
		if result == nil {
			return stagedStatusMsg{paths: paths}
		}
		statuses := cvs.ParseStatus(result.Stdout)
		// `cvs status <path>` reports the basename only ("File: notes.txt")
		// — no "Examining <dir>" prefix to give ParseStatus a directory
		// context. Remap basenames back to the original input paths so the
		// stagedStatusMsg handler keys statusMap correctly.
		byBase := make(map[string]string, len(paths))
		for _, p := range paths {
			byBase[filepath.Base(p)] = p
		}
		for i, s := range statuses {
			if full, ok := byBase[s.Path]; ok {
				statuses[i].Path = full
			}
		}
		return stagedStatusMsg{paths: paths, statuses: statuses}
	}
}

func loadDirStatus(executor *cvs.CVSExecutor, dir string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		args := []string{"status", "-l"}
		if dir != "." {
			args = append(args, dir)
		}
		result, _ := executor.RunReadOnly(args...)
		if result == nil {
			return dirStatusMsg{dir: dir, epoch: epoch}
		}
		return dirStatusMsg{dir: dir, statuses: cvs.ParseStatus(result.Stdout), epoch: epoch}
	}
}

// backgroundDirScan runs a single recursive `cvs status` over the
// working copy (or the given scope) and reports the result as one
// dirStatusMsg with the recursive flag set. The handler clears every
// statusMap entry under the scope before merging the new statuses, so
// the result is canonical for that subtree.
//
// Earlier this function did a filepath.WalkDir of every CVS-managed
// directory and dispatched one `cvs status -l <dir>` per dir. That
// worked for small repos but spawned thousands of subprocesses on
// large ones and triggered a tree refresh per dir (O(tree²)). The
// recursive single-call avoids both and lets cvs do its own walk
// once.
//
// Targeted refreshes after individual actions still use loadDirStatus
// directly (via refreshStatusForPaths), which only touches the affected
// directories — unaffected by repo size.
func backgroundDirScan(executor *cvs.CVSExecutor, epoch uint64, scope string) tea.Cmd {
	dir := scope
	if dir == "" {
		dir = "."
	}
	return func() tea.Msg {
		args := []string{"status"}
		if dir != "." {
			args = append(args, dir)
		}
		result, _ := executor.RunReadOnly(args...)
		if result == nil {
			return dirStatusMsg{dir: dir, recursive: true, epoch: epoch}
		}
		return dirStatusMsg{
			dir:       dir,
			recursive: true,
			statuses:  cvs.ParseStatus(result.Stdout),
			epoch:     epoch,
		}
	}
}


// cvsStatusCode maps the long-form status strings emitted by `cvs status`
// to the single-letter codes used throughout the UI. Returns "" for
// statuses that don't map to anything actionable (e.g. "Up-to-date").
func cvsStatusCode(status string) string {
	switch status {
	case "Locally Modified":
		return "M"
	case "Locally Added":
		return "A"
	case "Locally Removed":
		return "R"
	case "Needs Update", "Needs Patch":
		return "U"
	case "Needs Merge":
		return "M"
	case "File had conflicts on merge", "Unresolved Conflict":
		return "C"
	case "Unknown":
		// `cvs status` calls untracked files "Unknown" while `cvs update -n`
		// uses "?". Map both to the same code so the TUI shows ? consistently.
		return "?"
	default:
		return ""
	}
}
