package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type Panel int

const (
	PanelLeft Panel = iota
	PanelRight
	PanelConsole
)

const (
	TabTree = iota
	TabFavorites
	TabStaged
	TabHistory
)

type TreeViewMode int

const (
	TreeViewFiles   TreeViewMode = iota // Variant A: dirs left, files right
	TreeViewDetails                     // Variant B: full tree left, diff preview right
)

type statusRefreshedMsg struct {
	result *cvs.UpdateResult
	epoch  uint64
}

// dirStatusMsg carries the result of a `cvs status` invocation.
//
//   recursive == false (default) — `cvs status -l <dir>`, scanning only
//     the immediate directory. The handler clears statusMap entries
//     whose immediate parent equals dir, then merges the new statuses.
//
//   recursive == true — recursive `cvs status [scope]`. The handler
//     clears every entry under dir's subtree, then merges. Used for
//     the initial scan and for whole-tree refreshes; the targeted
//     refresh path (refreshStatusForPaths) keeps the per-dir form
//     because it only touches a small set of directories.
type dirStatusMsg struct {
	dir       string
	recursive bool
	statuses  []cvs.FileStatus
	epoch     uint64
}

type previewMsg struct {
	path string
	diff *cvs.DiffResult
}

type stagedStatusMsg struct {
	paths    []string         // files that were checked
	statuses []cvs.FileStatus // results from cvs status
}

// commitDoneMsg is sent by doCommit when its async cvs add+commit sequence
// finishes. The handler turns this into a banner notification, drops the
// committed paths from the marked set (so the Staged view reflects success),
// and triggers a status refresh. err is non-nil if any cvs invocation failed.
type commitDoneMsg struct {
	files   []string
	message string
	err     error
}

// notificationExpiredMsg is fired by a tea.Tick to clear the banner after
// the notification's display window elapses.
type notificationExpiredMsg struct{}

// updateDoneMsg reports the outcome of an actual `cvs update -d` operation
// (NOT the silent dry-run that just refreshes the status display). The
// handler in App.Update shows a banner and triggers a follow-up status
// refresh — same shape as commitDoneMsg / removeDoneMsg.
//
// inTheWay carries any paths CVS reported as "move away" — local files
// that blocked the server's version from being pulled down. The
// handler opens DialogInTheWay so the user can resolve them.
type updateDoneMsg struct {
	paths    []string
	err      error
	inTheWay []string
}

// actionDoneMsg is sent by async actions (add, revert) that previously ran a
// full DryRunUpdate. It carries the affected paths so the handler can dispatch
// a fast, targeted directory refresh instead of a full repo scan.
type actionDoneMsg struct {
	paths []string
}

// notifyAfter schedules a notificationExpiredMsg after d.
func notifyAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return notificationExpiredMsg{} })
}

// refreshStatusForPaths dispatches loadDirStatus for each unique parent
// directory of the given paths. Much faster than a full DryRunUpdate when
// only a few directories are affected.
func (m *App) refreshStatusForPaths(paths []string) tea.Cmd {
	m.statusEpoch++
	epoch := m.statusEpoch
	seen := make(map[string]bool)
	var cmds []tea.Cmd
	for _, p := range paths {
		dir := filepath.Dir(p)
		if dir == "" {
			dir = "."
		}
		if !seen[dir] {
			seen[dir] = true
			cmds = append(cmds, loadDirStatus(m.exec, dir, epoch))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// notifyDuration controls how long success/error banners stay visible.
const notifyDuration = 3 * time.Second

type App struct {
	activeTab int
	focus     Panel
	treeMode  TreeViewMode

	tree      TreeModel
	filelist  FileListModel
	console   ConsoleModel
	favorites FavoritesModel
	staged    StagedModel
	history   HistoryModel
	search    SearchModel
	dialog    DialogModel

	exec   *cvs.CVSExecutor
	cmdLog *cvs.CommandLog
	cfgMgr *config.ConfigManager

	width  int
	height int

	consoleHeight int
	initialPath   string // path to navigate to on startup

	// statusMap is the single source of truth for per-file CVS status codes
	// (M/A/R/U/C/?). Owned by App and only mutated inside Update(), which
	// Bubble Tea serializes on the main goroutine. Sub-models receive it as
	// a read-only parameter when they need to render or compute over it.
	statusMap map[string]string

	// baseRevMap holds the working revision (sticky-tag aware) for each
	// known path, parsed from `cvs status` output. This is the revision
	// the on-disk file is actually checked out to — not necessarily the
	// repo HEAD. The History tab uses it to badge the "matches working"
	// row correctly when the working copy is on a sticky tag or branch.
	baseRevMap map[string]string

	// Epoch-based staleness tracking: each status command dispatch
	// increments statusEpoch. dirEpoch records the epoch of the most
	// recently applied update per directory. A result is only applied if
	// its epoch >= dirEpoch for that directory, so late-arriving full
	// scans don't overwrite fresher dir scans.
	statusEpoch uint64
	dirEpoch    map[string]uint64

	// Preview state for Variant B (TreeViewDetails)
	previewPath  string
	previewDiff  *cvs.DiffResult
	previewVP    viewport.Model
	previewReady bool

	// Transient banner shown above the tab bar (e.g. "✓ Committed 2 file(s)").
	// Cleared automatically via notificationExpiredMsg after a few seconds.
	notification     string
	notificationOK   bool // true=success (green), false=error (red)
	notificationExpiry time.Time

	// History tab caches — per-file, never wiped on file switch. Async
	// results carry the path they were loaded for; handlers write into
	// the matching slot regardless of which file the user is currently
	// viewing. So switching A → B → A reuses A's revisions and diffs
	// without re-running cvs.
	histRevisions map[string]*cvs.FileHistory
	histDiffs     map[string]map[string]*cvs.DiffResult // path → "from:to" → diff
	histContents  map[string]map[string]string          // path → rev → content
	// histPending tracks (path, key) load requests already dispatched, so
	// fast cursor movement doesn't queue duplicate cvs commands for the
	// same revision pair.
	histPending map[string]bool // keys built via pendingLog/pendingContent/pendingDiff
}

func NewApp(exec *cvs.CVSExecutor, cmdLog *cvs.CommandLog, cfgMgr *config.ConfigManager, initialPath string) App {
	cfg := cfgMgr.Get()
	return App{
		activeTab:     TabTree,
		focus:         PanelLeft,
		tree:          NewTreeModel(exec.WorkDir),
		filelist:      NewFileListModel(),
		console:       NewConsoleModel(cmdLog),
		favorites:     NewFavoritesModel(&cfg),
		staged:        NewStagedModel(),
		history:       NewHistoryModel(),
		search:        NewSearchModel(),
		dialog:        NewDialogModel(),
		exec:          exec,
		cmdLog:        cmdLog,
		cfgMgr:        cfgMgr,
		consoleHeight: 6,
		initialPath:   initialPath,
		statusMap:     make(map[string]string),
		baseRevMap:    make(map[string]string),
		statusEpoch:   1,
		dirEpoch:      make(map[string]uint64),
		histRevisions: make(map[string]*cvs.FileHistory),
		histDiffs:     make(map[string]map[string]*cvs.DiffResult),
		histContents:  make(map[string]map[string]string),
		histPending:   make(map[string]bool),
	}
}

// applyStatuses removes statusMap entries listed in toClear, then merges
// the codes parsed from statuses on top, and refreshes the tree and
// staged panel. Used by both dirStatusMsg (clears a whole directory)
// and stagedStatusMsg (clears specific paths) so the
// clear-then-merge-then-refresh sequence stays in one place.
//
// Also threads each file's working revision (the rev the on-disk
// content is actually checked out to — sticky-tag aware) into
// baseRevMap so the History tab can badge the right row.
func (m *App) applyStatuses(toClear []string, statuses []cvs.FileStatus) {
	for _, p := range toClear {
		delete(m.statusMap, p)
		delete(m.baseRevMap, p)
	}
	for _, fs := range statuses {
		if code := cvsStatusCode(fs.Status); code != "" {
			m.statusMap[fs.Path] = code
		}
		if fs.WorkingRev != "" {
			m.baseRevMap[fs.Path] = fs.WorkingRev
		}
	}
	m.tree.RefreshStatus(m.statusMap)
	m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
}

// rebuildStatusMap replaces the map with fresh entries derived from a dry-run
// update result. Called from the statusRefreshedMsg handler.
func (m *App) rebuildStatusMap(result *cvs.UpdateResult) {
	m.statusMap = make(map[string]string)
	if result == nil {
		return
	}
	for _, f := range result.Modified {
		m.statusMap[f.Path] = "M"
	}
	for _, f := range result.Conflicts {
		m.statusMap[f.Path] = "C"
	}
	for _, f := range result.Untracked {
		m.statusMap[f.Path] = "?"
	}
	for _, f := range result.Updated {
		m.statusMap[f.Path] = "U"
	}
}

func (m App) Init() tea.Cmd {
	initEpoch := m.statusEpoch
	return tea.Batch(
		m.tree.Init(),
		backgroundDirScan(m.exec, initEpoch, m.initialPath),
	)
}

func (m *App) refreshStatusUser() tea.Cmd {
	m.statusEpoch++
	epoch := m.statusEpoch
	exec := m.exec
	return func() tea.Msg {
		result, _ := exec.DryRunUpdate()
		return statusRefreshedMsg{result: result, epoch: epoch}
	}
}


func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// While the user has the console panel focused, treat any logged errors
	// as "seen" — clear the unread counter so the ⚠ indicator in the tab
	// bar disappears. New errors that arrive while still on console will
	// flash briefly before the next Update tick clears them again.
	if m.focus == PanelConsole {
		m.cmdLog.MarkRead()
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateSizes()
		return m, nil

	case tea.MouseMsg:
		if !m.dialog.Active() && !m.search.active {
			return m, m.handleMouse(msg)
		}
		return m, nil

	case tea.KeyMsg:
		// Priority chain: staged commit input → overlays (dialog/search) →
		// global keys → focused-panel delegate.
		if cmd, handled := m.handleStagedInput(msg); handled {
			return m, cmd
		}
		if cmd, handled := m.handleKeyPriority(msg); handled {
			return m, cmd
		}
		if cmd, handled := m.handleGlobalKey(msg); handled {
			return m, cmd
		}
		return m, m.delegateKey(msg)

	case treeLoadedMsg:
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg)
		if m.initialPath != "" {
			m.tree.ExpandToPath(m.initialPath)
			m.updateFileList()
			m.initialPath = ""
		} else {
			m.updateFileListForDir(".")
		}
		return m, cmd

	case treeDirLoadedMsg:
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg)
		m.updateFileList()
		m.statusEpoch++
		statusCmd := backgroundDirScan(m.exec, m.statusEpoch, msg.path)
		return m, tea.Batch(cmd, statusCmd)

	case historyLoadedMsg:
		// Cache the result by its source path, regardless of what the user
		// is currently viewing. If they switched files mid-load, we still
		// want the data ready for next time.
		m.histRevisions[msg.path] = msg.history
		delete(m.histPending, pendingLog(msg.path))
		// Only push into the model + auto-load content if the user is
		// actually viewing this file right now.
		if m.history.Path() == msg.path {
			m.history.ApplyRevisions(msg.history, m.workingCopyIsDirty(msg.path), m.baseRevMap[msg.path])
			if m.history.NumRevisions() > 0 {
				return m, m.loadHistoryContent()
			}
		}
		return m, nil

	case historyContentMsg:
		// Cache by (path, rev). Update display only if we're still on
		// that file and the cursor is on that revision.
		if m.histContents[msg.path] == nil {
			m.histContents[msg.path] = make(map[string]string)
		}
		m.histContents[msg.path][msg.rev] = msg.content
		delete(m.histPending, pendingContent(msg.path, msg.rev))
		if m.history.Path() == msg.path && m.currentContentKey() == msg.rev {
			m.history.ApplyContent(msg.rev, msg.content)
		}
		return m, nil

	case historyDiffMsg:
		if m.histDiffs[msg.path] == nil {
			m.histDiffs[msg.path] = make(map[string]*cvs.DiffResult)
		}
		m.histDiffs[msg.path][diffCacheKey(msg.fromRev, msg.toRev)] = msg.diff
		delete(m.histPending, pendingDiff(msg.path, msg.fromRev, msg.toRev))
		if m.history.Path() == msg.path {
			fromRev, toRev := m.currentDiffKey()
			if fromRev == msg.fromRev && toRev == msg.toRev {
				m.history.ApplyDiff(msg.fromRev, msg.toRev, msg.diff)
			}
		}
		return m, nil

	case dirStatusMsg:
		m.console.refreshContent()
		if msg.epoch < m.dirEpoch[msg.dir] {
			return m, nil
		}
		m.dirEpoch[msg.dir] = msg.epoch
		var toClear []string
		if msg.recursive {
			// Recursive scan covers msg.dir and every descendant; drop
			// every existing statusMap entry under that subtree before
			// merging so removed/cleaned files disappear.
			prefix := msg.dir + "/"
			rootScope := msg.dir == "."
			for path := range m.statusMap {
				if rootScope || path == msg.dir || strings.HasPrefix(path, prefix) {
					toClear = append(toClear, path)
				}
			}
			// Bump dirEpoch for every dir under this scope so a later
			// targeted scan (with a fresher epoch) is the only one
			// that can override us.
			for d := range m.dirEpoch {
				if rootScope || d == msg.dir || strings.HasPrefix(d, prefix) {
					m.dirEpoch[d] = msg.epoch
				}
			}
		} else {
			for path := range m.statusMap {
				if filepath.Dir(path) == msg.dir {
					toClear = append(toClear, path)
				}
			}
		}
		m.applyStatuses(toClear, msg.statuses)
		m.updateFileList()
		return m, nil

	case statusRefreshedMsg:
		m.console.refreshContent()
		if msg.result != nil {
			m.rebuildStatusMap(msg.result)
			for dir := range m.dirEpoch {
				m.dirEpoch[dir] = msg.epoch
			}
			m.tree.RefreshStatus(m.statusMap)
			m.favorites.UpdateCounts(m.statusMap)
			m.updateFileList()
			m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
			changes := len(m.statusMap)
			m.notification = fmt.Sprintf("Status: %d file(s) with changes (dry-run, nothing pulled)", changes)
			m.notificationOK = true
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, notifyAfter(notifyDuration)
		}

	case commitMsg:
		// Block files that are still in C status with on-disk conflict
		// markers — cvs commit would refuse those anyway, and a blanket
		// "Commit failed" notification doesn't tell the user what to do
		// next. Surface the issue with a clear "press M to merge first"
		// hint and don't dispatch.
		blocked := m.blockedByConflict(msg.files, func(p string) string { return m.statusMap[p] })
		if len(blocked) > 0 {
			noun := "file"
			if len(blocked) > 1 {
				noun = "files"
			}
			m.notification = fmt.Sprintf("✗ %d %s with unresolved conflict markers — press M to merge first", len(blocked), noun)
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, notifyAfter(notifyDuration)
		}
		return m, doCommit(m.exec, msg.message, msg.untracked, msg.files)

	case actionDoneMsg:
		m.console.refreshContent()
		// actionDoneMsg covers add / revert. Add doesn't touch history
		// (no new revision until commit), but revert can change the
		// working-copy diff cache for the path; safest to drop the
		// per-path History cache so the next view re-fetches.
		invalidateCmd := m.invalidateHistoryCache(msg.paths)
		return m, tea.Batch(m.refreshStatusForPaths(msg.paths), invalidateCmd)

	case commitDoneMsg:
		if msg.err == nil {
			m.notification = fmt.Sprintf("✓ Committed %d file(s): %q", len(msg.files), msg.message)
			m.notificationOK = true
			for _, p := range msg.files {
				delete(m.filelist.marked, p)
			}
			m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		} else {
			m.notification = "✗ Commit failed — see Console for details"
			m.notificationOK = false
		}
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes() // banner reduces contentHeight by 1 — relayout sub-panels
		// A successful commit creates a new revision: drop the cached
		// revisions / diffs / contents for every committed path so the
		// History tab reflects the new state immediately.
		var invalidateCmd tea.Cmd
		if msg.err == nil {
			invalidateCmd = m.invalidateHistoryCache(msg.files)
		}
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.files), invalidateCmd)

	case notificationExpiredMsg:
		// Only clear if we're actually past the displayed expiry. Multiple
		// ticks can stack if commits happen back-to-back — earlier ticks
		// shouldn't blank a freshly-set notification.
		if !m.notificationExpiry.IsZero() && !time.Now().Before(m.notificationExpiry) {
			m.notification = ""
			m.notificationExpiry = time.Time{}
			m.updateSizes()
		}
		return m, nil

	case revertMsg:
		return m, doRevert(m.exec, msg.path)

	case removeMsg:
		// Deliberately do NOT touch m.filelist.marked here — let the
		// removeDoneMsg handler clean up after success, mirroring the
		// commit path. A failed remove leaves the staging selection
		// intact so the user can retry.
		return m, doRemove(m.exec, msg.paths, msg.statuses)

	case updateDoneMsg:
		if msg.err != nil {
			m.notification = "✗ Update failed — see Console"
			m.notificationOK = false
		} else if len(msg.inTheWay) > 0 {
			// CVS held back N files because local files were in the
			// way; surface that in the banner and open the dialog.
			m.notification = fmt.Sprintf("⚠ %d file(s) blocked by local copies — choose how to resolve", len(msg.inTheWay))
			m.notificationOK = false
			m.dialog.OpenInTheWay(msg.inTheWay)
		} else if len(msg.paths) == 1 {
			m.notification = fmt.Sprintf("✓ Updated %s", filepath.Base(msg.paths[0]))
			m.notificationOK = true
		} else {
			m.notification = fmt.Sprintf("✓ Updated %d file(s)", len(msg.paths))
			m.notificationOK = true
		}
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes()
		// `cvs update` may pull new server revisions for these paths;
		// drop the History cache so the next view sees them.
		var invalidateCmd tea.Cmd
		if msg.err == nil {
			invalidateCmd = m.invalidateHistoryCache(msg.paths)
		}
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.paths), invalidateCmd)

	case inTheWayResolveMsg:
		return m, m.handleInTheWayResolve(msg)

	case removeDoneMsg:
		if msg.err != nil {
			label := fmt.Sprintf("%d files", len(msg.paths))
			if len(msg.paths) == 1 {
				label = filepath.Base(msg.paths[0])
			}
			m.notification = fmt.Sprintf("✗ Remove failed for %s — see Console", label)
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.paths))
		}
		for _, p := range msg.paths {
			delete(m.filelist.marked, p)
		}
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		if len(msg.paths) == 1 {
			m.notification = fmt.Sprintf("✓ Removed %s", filepath.Base(msg.paths[0]))
		} else {
			m.notification = fmt.Sprintf("✓ Removed %d file(s)", len(msg.paths))
		}
		m.notificationOK = true
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes()
		// Removed files: their on-disk state changed (R-status / gone)
		// so any cached working-copy diff is now stale. Tracked files
		// will get a new revision once the removal is committed; drop
		// the cache now so that future commit auto-refreshes the
		// History view.
		invalidateCmd := m.invalidateHistoryCache(msg.paths)
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.paths), invalidateCmd)

	case editorClosedMsg:
		return m, m.refreshStatusForPaths([]string{msg.path})

	case searchSelectedMsg:
		// Navigate to the selected path
		m.activeTab = TabTree
		return m, nil

	case ignoreMsg:
		return m, m.handleIgnore(msg)

	case conflictResolveMsg:
		return m, m.handleConflictResolve(msg)

	case forceUpdateMsg:
		for _, p := range msg.paths {
			delete(m.filelist.marked, p)
		}
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		return m, m.stagedBulkAction("revert", msg.paths)

	case stagedStatusMsg:
		m.applyStatuses(msg.paths, msg.statuses)
		return m, nil

	case previewMsg:
		m.previewPath = msg.path
		m.previewDiff = msg.diff
		_, rightW, contentH, _ := m.layout()
		m.previewVP = viewport.New(rightW-2, contentH)
		if msg.diff != nil && len(msg.diff.Hunks) > 0 {
			m.previewVP.SetContent(renderUnifiedDiff(msg.diff.Hunks))
		} else {
			m.previewVP.SetContent(mutedStyle.Render("  No local changes"))
		}
		m.previewReady = true
		return m, nil
	}

	// Delegate all other messages to sub-models
	var cmds []tea.Cmd

	var cmd tea.Cmd
	m.tree, cmd = m.tree.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	m.console, cmd = m.console.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	m.search, cmd = m.search.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}
