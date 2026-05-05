package tui

import (
	"lazycvs/cvs"
	ioFS "io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CVS status loading. The general flow is:
//   user action / startup → backgroundDirScan or refreshStagedFiles
//   → loadDirStatus / refreshStagedFiles dispatched per directory or path
//   → dirStatusMsg / stagedStatusMsg handlers in app_core.go merge into
//     m.statusMap via m.applyStatuses

// resolveFileStatus returns the CVS status code for path. If statusMap
// has no entry, walks up the file's ancestor directories (at most three
// levels) looking for a missing CVS/ marker. A *single* missing marker
// in the chain means the file is in a user-created subdirectory that
// hasn't been `cvs add`-ed yet — its files aren't tracked, so we
// report "?". The file is only treated as tracked-but-clean ("") when
// every ancestor we checked has its own CVS/ marker.
//
// Three levels is a syscall-budget cap: most "user created a fresh
// subdir inside a managed tree" cases are caught within one or two
// hops; deeper broken chains are accepted as the cost of not stat'ing
// every ancestor up to the filesystem root for every file.
func (m *App) resolveFileStatus(path string) string {
	if s := m.statusMap[path]; s != "" {
		return s
	}
	dir := filepath.Dir(path)
	for i := 0; i < 3; i++ {
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

// backgroundDirScan walks the working directory, collects all CVS-managed
// directories, and returns a tea.Batch that runs loadDirStatus for each one.
// Because loadDirStatus uses RunReadOnly (shared lock), all directory scans
// run concurrently. Results arrive as individual dirStatusMsg messages and
// render progressively.
func backgroundDirScan(executor *cvs.CVSExecutor, epoch uint64, scope string) tea.Cmd {
	root := executor.WorkDir
	if scope != "" {
		root = filepath.Join(executor.WorkDir, scope)
	}
	var dirs []string
	filepath.WalkDir(root, func(path string, d ioFS.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "CVS" || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		cvsDir := filepath.Join(path, "CVS")
		if _, err := os.Stat(cvsDir); err != nil {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(executor.WorkDir, path)
		if rel == "" {
			rel = "."
		}
		dirs = append(dirs, rel)
		return nil
	})
	cmds := make([]tea.Cmd, len(dirs))
	for i, dir := range dirs {
		cmds[i] = loadDirStatus(executor, dir, epoch)
	}
	return tea.Batch(cmds...)
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
