package tui

import (
	"lazycvs/cvs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// File listing, ignore-pattern handling, and per-tab path resolution.
// CVS is encoding-agnostic; the patterns and skip rules here mirror what
// `cvs` itself ignores, plus a couple of editor backup conventions
// (`.#*`, `*.~`) that aren't in the cvs(5) defaults but should be hidden
// in any TUI listing.

// cvsDefaultIgnore is the built-in CVS ignore list (see cvs(5) "Ignoring files
// via cvsignore"). These patterns are always active unless cleared with "!".
var cvsDefaultIgnore = strings.Fields(`
	RCS SCCS CVS CVS.adm
	RCSLOG cvslog.*
	tags TAGS
	.make.state .nse_depinfo
	*~ #* .#* ,* _$* *$
	*.old *.bak *.BAK *.orig *.rej .del-*
	*.a *.olb *.o *.obj *.so *.exe
	*.Z *.elc *.ln
	*.lazycvs-backup
	core
`)

// globalIgnorePatterns returns the ignore list built from:
//  1. CVS built-in defaults
//  2. ~/.cvsignore
//  3. $CVSIGNORE environment variable
//
// A lone "!" entry resets the list.
//
// The result is computed once per process (sync.Once): ~/.cvsignore
// and $CVSIGNORE don't change while lazycvs runs, and loadIgnorePatterns
// is on the file-listing hot path — it fires for the listed dir plus
// every visible subdir on each refresh, so re-reading the home file
// every time added dozens of redundant file reads per render cycle.
func globalIgnorePatterns() []string {
	globalIgnoreOnce.Do(func() {
		patterns := append([]string{}, cvsDefaultIgnore...)
		if home, err := os.UserHomeDir(); err == nil {
			patterns = appendIgnoreFile(patterns, filepath.Join(home, ".cvsignore"))
		}
		if env := os.Getenv("CVSIGNORE"); env != "" {
			patterns = appendPatterns(patterns, strings.Fields(env))
		}
		globalIgnoreCache = patterns
	})
	return globalIgnoreCache
}

var (
	globalIgnoreOnce  sync.Once
	globalIgnoreCache []string
)

// loadIgnorePatterns collects all CVS ignore patterns that apply to a directory:
// built-in defaults + ~/.cvsignore + $CVSIGNORE + per-directory .cvsignore.
// The returned slice always starts with a fresh copy of the global set, so
// appending the per-dir patterns can't corrupt the shared cache.
func loadIgnorePatterns(absDir string) []string {
	global := globalIgnorePatterns()
	patterns := make([]string, len(global), len(global)+8)
	copy(patterns, global)
	patterns = appendIgnoreFile(patterns, filepath.Join(absDir, ".cvsignore"))
	return patterns
}

// appendIgnoreFile reads a cvsignore file and appends its patterns. CVS ignore
// files contain space-separated patterns (no comment syntax). "!" resets the
// list.
func appendIgnoreFile(patterns []string, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return patterns
	}
	return appendPatterns(patterns, strings.Fields(string(data)))
}

// appendPatterns adds entries to the pattern list, handling "!" as a reset.
func appendPatterns(patterns, entries []string) []string {
	for _, e := range entries {
		if e == "!" {
			patterns = patterns[:0]
		} else {
			patterns = append(patterns, e)
		}
	}
	return patterns
}

func matchesIgnore(name string, patterns []string) bool {
	for _, p := range patterns {
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
	}
	return false
}

func skipInListing(name string) bool {
	// .cvsignore stays in the listing: it's tracked by CVS, users
	// edit it directly, and hiding the local copy made it appear as
	// a phantom "(server)" entry whenever cvs status reported it.
	return name == "CVS" || name == ".DS_Store" ||
		strings.HasPrefix(name, ".#") || strings.HasSuffix(name, ".~")
}

// readCVSEntries returns the set of names listed in absDir/CVS/Entries
// (both file and directory entries). Nil when the file is missing or
// unreadable — the caller treats that as "no Entries info available"
// and falls back to the ancestor walk. Used to distinguish a clean,
// tracked file from an untracked one in a dir that has a CVS/ marker
// but where Entries is empty or doesn't list the file (typical right
// after `cvs add <dir>` before the files themselves are added).
func readCVSEntries(absDir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(absDir, "CVS", "Entries"))
	if err != nil {
		return nil
	}
	tracked := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line == "D" {
			continue
		}
		// Format: /name/version/timestamp/options/tag  (files)
		//        D/name////                            (directories)
		parts := strings.SplitN(line, "/", 3)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		tracked[parts[1]] = true
	}
	return tracked
}

// resolveListingStatus derives the badge code and ignore flag for one
// entry in the file-list build. Shared between the top-level dir scan
// and each visible subdir scan so both apply the same precedence:
// (1) promote a clean status to "?" when the file isn't in the dir's
// Entries (CVS/ exists but Entries doesn't list it — typical right
// after `cvs add <dir>` before the files themselves are added);
// (2) apply `.cvsignore` patterns to clean *and* "?" entries, matching
// cvs(1)'s precedence (ignore patterns consulted before "?" reporting).
func resolveListingStatus(name, status string, tracked map[string]bool, ignorePatterns []string) (string, bool) {
	if status == "" && tracked != nil && !tracked[name] {
		status = "?"
	}
	ignored := (status == "" || status == "?") && matchesIgnore(name, ignorePatterns)
	return status, ignored
}

// updateFileList rebuilds the right-pane file list from the active tab's
// selected directory. No-op for tabs that don't have a file list (Staged,
// History).
func (m *App) updateFileList() {
	var dir string
	switch m.activeTab {
	case TabTree:
		node := m.tree.SelectedNode()
		if node == nil {
			// No node selected (e.g. root has only files, no dirs) —
			// fall back to showing root directory.
			m.updateFileListForDir(".")
			return
		}
		if node.IsDir {
			dir = node.Path
		} else {
			dir = filepath.Dir(node.Path)
			if dir == "." {
				dir = "."
			}
		}
	case TabFavorites:
		dir = m.favorites.SelectedPath()
	default:
		return
	}

	if dir == "" {
		return
	}

	m.updateFileListForDir(dir)
}

func (m *App) updateFileListForDir(dir string) {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}

	absDir := filepath.Join(m.exec.WorkDir, dir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return
	}

	var files []cvs.FileEntry
	var subDirs []SubDirGroup
	ignorePatterns := loadIgnorePatterns(absDir)
	tracked := readCVSEntries(absDir)

	for _, e := range entries {
		name := e.Name()
		if skipInListing(name) {
			continue
		}

		path := prefix + name
		if dir == "." {
			path = name
		}

		if e.IsDir() {
			// A directory is "ignored" when the parent's .cvsignore (or
			// the global pattern set) matches its name AND it isn't
			// CVS-tracked. Tracked dirs are never hidden — we always
			// surface them so a user-edited file inside a tracked dir
			// stays reachable even if the dirname happens to match a
			// generic pattern.
			subHasCVS := isCVSDir(filepath.Join(absDir, name))
			sg := SubDirGroup{
				Name:    name,
				Path:    path,
				Ignored: !subHasCVS && matchesIgnore(name, ignorePatterns),
			}
			if node := m.tree.findNode(path); node != nil {
				sg.Counts = node.Counts
			}
			subAbsDir := filepath.Join(absDir, name)
			subEntries, err := os.ReadDir(subAbsDir)
			subIgnore := loadIgnorePatterns(subAbsDir)
			subTracked := readCVSEntries(subAbsDir)
			if err == nil {
				for _, se := range subEntries {
					sn := se.Name()
					if skipInListing(sn) || se.IsDir() {
						continue
					}
					sp := path + "/" + sn
					var sz int64
					if info, err := se.Info(); err == nil {
						sz = info.Size()
					}
					st, ignored := resolveListingStatus(sn, m.resolveFileStatus(sp), subTracked, subIgnore)
					sg.Files = append(sg.Files, cvs.FileEntry{Path: sp, Status: st, Size: sz, Ignored: ignored})
				}
			}
			subDirs = append(subDirs, sg)
			continue
		}

		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		status, ignored := resolveListingStatus(name, m.resolveFileStatus(path), tracked, ignorePatterns)
		files = append(files, cvs.FileEntry{Path: path, Status: status, Size: size, Ignored: ignored})
	}

	// Surface server-only files. CVS status "U" / "P" can refer to files
	// that exist on the server but haven't been pulled to disk yet; they
	// show up in statusMap (so the directory's aggregate count is right)
	// but `os.ReadDir` doesn't see them. Without this pass the user sees
	// "1U" on a directory and no U row to act on.
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.Path] = true
	}
	for i := range subDirs {
		for _, sf := range subDirs[i].Files {
			seen[sf.Path] = true
		}
	}
	for path, status := range m.statusMap {
		if status == "" || seen[path] {
			continue
		}
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(path, prefix)
		parts := strings.Split(rel, "/")
		switch len(parts) {
		case 1:
			// Direct child of dir, missing on disk.
			files = append(files, cvs.FileEntry{Path: path, Status: status, ServerOnly: true})
		case 2:
			// File inside an immediate subdir — attach to the matching
			// SubDirGroup if we already have one. Skip otherwise (the
			// subdir itself doesn't exist on disk, which is rare).
			subdirName := parts[0]
			for j := range subDirs {
				if subDirs[j].Name == subdirName {
					subDirs[j].Files = append(subDirs[j].Files, cvs.FileEntry{Path: path, Status: status, ServerOnly: true})
					break
				}
			}
		}
	}

	m.filelist.SetFiles(dir, files, subDirs)
}

// selectedFilePath returns the path of the file under cursor in the active
// tab, or "" if no file is selected (cursor on a dir, no selection, etc.).
// Mirrors the resolution logic in delegateKey so keybar hints stay in sync.
func (m App) selectedFilePath() string {
	switch m.activeTab {
	case TabStaged:
		return m.staged.SelectedPath()
	case TabHistory:
		return m.history.Path()
	case TabTree:
		if m.focus == PanelRight && m.treeMode != TreeViewDetails {
			if f := m.filelist.SelectedFile(); f != nil {
				return f.Path
			}
			return ""
		}
		if node := m.tree.SelectedNode(); node != nil && !node.IsDir {
			return node.Path
		}
		return ""
	case TabFavorites:
		if f := m.filelist.SelectedFile(); f != nil {
			return f.Path
		}
	}
	return ""
}

// selectedTarget returns the path of the currently focused file or directory.
func (m *App) selectedTarget() string {
	if m.focus == PanelRight {
		if f := m.filelist.SelectedFile(); f != nil {
			return f.Path
		}
	}
	if node := m.tree.SelectedNode(); node != nil {
		return node.Path
	}
	return ""
}
