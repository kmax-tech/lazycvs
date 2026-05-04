package tui

import (
	"lazycvs/cvs"
	"os"
	"path/filepath"
	"strings"
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
	core
`)

// globalIgnorePatterns returns the ignore list built from:
//  1. CVS built-in defaults
//  2. ~/.cvsignore
//  3. $CVSIGNORE environment variable
//
// A lone "!" entry resets the list.
func globalIgnorePatterns() []string {
	patterns := append([]string{}, cvsDefaultIgnore...)
	if home, err := os.UserHomeDir(); err == nil {
		patterns = appendIgnoreFile(patterns, filepath.Join(home, ".cvsignore"))
	}
	if env := os.Getenv("CVSIGNORE"); env != "" {
		patterns = appendPatterns(patterns, strings.Fields(env))
	}
	return patterns
}

// loadIgnorePatterns collects all CVS ignore patterns that apply to a directory:
// built-in defaults + ~/.cvsignore + $CVSIGNORE + per-directory .cvsignore.
func loadIgnorePatterns(absDir string) []string {
	patterns := globalIgnorePatterns()
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
	return name == "CVS" || name == ".cvsignore" || name == ".DS_Store" ||
		strings.HasPrefix(name, ".#") || strings.HasSuffix(name, ".~")
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
			sg := SubDirGroup{
				Name: name,
				Path: path,
			}
			if node := m.tree.findNode(path); node != nil {
				sg.Counts = node.Counts
			}
			subAbsDir := filepath.Join(absDir, name)
			subEntries, err := os.ReadDir(subAbsDir)
			subIgnore := loadIgnorePatterns(subAbsDir)
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
					st := m.resolveFileStatus(sp)
					ignored := st == "" && matchesIgnore(sn, subIgnore)
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
		status := m.resolveFileStatus(path)
		ignored := status == "" && matchesIgnore(name, ignorePatterns)
		files = append(files, cvs.FileEntry{Path: path, Status: status, Size: size, Ignored: ignored})
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
