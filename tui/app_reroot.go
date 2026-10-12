package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kmax-tech/lazycvs/cvs"
)

// switchWorkDir re-roots the running app on abs, a directory carrying
// CVS/Root, without restarting the process: follow mode calls it when
// the file manager next door walks out of the working copy into
// another checkout (usually the parent of this one). Afterwards abs is
// the tree root; the tree and the status scan for it are returned as
// commands, the same two Init starts, and the root row is selected
// once the tree has loaded (the caller may set initialPath to land
// deeper, as the command line does).
//
// What happens to each App field, so the next person does not have to
// re-derive it from NewApp:
//
//	keep     — console, cmdLog (the ring is history: entries of the old
//	           root stay), cfgMgr, ui, consoleOverride, width/height,
//	           filesLayout, activeTab (Tree, by the idle rule), dialog
//	           (inactive, by the idle rule), markedTab's typed commit
//	           message, the notification banner, expectedEnc,
//	           favorites (config-owned root-relative paths: they are not
//	           rewritten; UpdateCounts re-derives their badges from the
//	           empty status map), the filelist's view preferences
//	           (hideIgnored, viewMode, expandAll, filter), the bootstrap
//	           fields (unused outside setup mode).
//	rebuild  — exec (a new CVSExecutor on abs with the same binary,
//	           timeout and log: nothing else in it derives from WorkDir,
//	           and a new value rather than a mutated one so a command
//	           already running — the partition scan reads WorkDir per
//	           partition — finishes against the root it started in),
//	           tree (NewTreeModel on abs, stamped with the new rootGen),
//	           filelist (fresh listing, preferences and rebased marks
//	           carried over), history (NewHistoryModel: it was showing a
//	           path of the old root), module (CVS/Repository of abs),
//	           sticky (re-read by the first updateFileListForDir),
//	           statusAt (zero until the first whole-tree scan lands),
//	           search's path list (Open notices the new WorkDir).
//	clear    — statusMap, staleDirs, baseRevMap, encCache, dirEpoch,
//	           the diff pane (diffPath/diffData/diffReady), histRevisions,
//	           histDiffs, histContents, histPending, recentEvents with
//	           its fetch/coverage times, pendingFollow, initialPath,
//	           quitPending; followReady until the new tree has loaded.
//	bump     — rootGen (every async result of the old root is dropped
//	           on arrival, see App.rootGen), statusEpoch, histRequestID.
//	           histStreams is kept: a stream still running pushes into
//	           its channel until done, and the batch handler keeps
//	           draining stale streams by request ID — deleting the entry
//	           would block that pump on a nil channel.
//	rebase   — marks (filelist.marked), see rebaseMarks: re-expressed
//	           relative to abs; marks outside it are dropped, counted
//	           and reported.
func (m *App) switchWorkDir(abs string) tea.Cmd {
	old := m.exec
	m.rootGen++
	m.exec = cvs.NewCVSExecutor(abs, old.CVSBin, old.Timeout, old.Log)

	marks, dropped := rebaseMarks(m.filelist.marked, old.WorkDir, abs)
	m.cmdLog.LogFileOp("re-rooted to "+abs+"  # was "+old.WorkDir, nil)
	if dropped > 0 {
		m.cmdLog.LogFileOp(fmt.Sprintf("re-rooted to %s  # dropped %s outside the new working copy",
			abs, countLabel(dropped, "mark")), nil)
	}
	m.console.refreshContent()

	tree := NewTreeModel(abs)
	tree.gen = m.rootGen
	tree.showFiles = m.filesLayout == LayoutDiff
	tree.SetHideIgnored(m.filelist.hideIgnored)
	m.tree = tree

	list := NewFileListModel()
	list.marked = marks
	list.hideIgnored = m.filelist.hideIgnored
	list.viewMode = m.filelist.viewMode
	list.expandAll = m.filelist.expandAll
	list.filter = m.filelist.filter
	m.filelist = list

	m.module = moduleName(m.exec)
	m.sticky = cvs.Sticky{}
	m.statusAt = time.Time{}

	m.statusMap = make(map[string]string)
	m.staleDirs = make(map[string]bool)
	m.baseRevMap = make(map[string]string)
	m.encCache = make(map[string]encVerdict)
	m.statusEpoch++
	m.dirEpoch = make(map[string]uint64)

	m.diffPath = ""
	m.diffData = nil
	m.diffReady = false

	m.history = NewHistoryModel()
	m.histRevisions = make(map[string]*cvs.FileHistory)
	m.histDiffs = make(map[string]map[string]*cvs.DiffResult)
	m.histContents = make(map[string]map[string]string)
	m.histPending = make(map[string]bool)
	m.histRequestID++
	m.recentEvents = nil
	m.recentFetched = time.Time{}
	m.recentCoverage = time.Time{}

	m.pendingFollow = ""
	m.initialPath = ""
	m.followReady = false
	m.quitPending = false
	m.focus = PanelLeft

	m.favorites.UpdateCounts(m.statusMap)
	m.markedTab.Refresh(m.filelist.marked, m.resolveFileStatus)
	m.updateSizes()

	var notify tea.Cmd
	if dropped > 0 {
		// The console line alone sits in a collapsed panel; a dropped
		// worklist deserves the banner.
		notify = m.setResult(fmt.Sprintf("Re-rooted to %s — %s outside it dropped (see Console)",
			filepath.Base(abs), countLabel(dropped, "mark")), false)
	}
	return tea.Batch(
		m.tree.Init(),
		backgroundDirScan(m.exec, m.statusEpoch, m.rootGen, ""),
		notify,
	)
}

// rebaseMarks re-expresses root-relative marks of oldRoot relative to
// newRoot. Moving up (newRoot an ancestor — the usual re-root) keeps
// every mark, prefixed with the old root's path under the new one;
// otherwise only the marks that lie inside newRoot survive and the rest
// are counted in dropped. Both roots are resolved through symlinks
// first, as followTarget does: the executor's WorkDir may carry the
// spelling the user typed (/tmp/…) while the followed path arrives
// resolved (/private/tmp/… on macOS), and the two must still line up.
func rebaseMarks(marked map[string]bool, oldRoot, newRoot string) (kept map[string]bool, dropped int) {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	oldRoot, newRoot = resolve(oldRoot), resolve(newRoot)
	kept = make(map[string]bool, len(marked))
	for p := range marked {
		rel, err := filepath.Rel(newRoot, filepath.Join(oldRoot, p))
		if err != nil {
			dropped++
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			dropped++
			continue
		}
		kept[rel] = true
	}
	return kept, dropped
}
