package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// statusRefreshedMsg carries the merged result of the user-triggered
// refresh (`s` key). It bundles two complementary CVS calls so both
// land in the same handler tick — one View() render, atomic state:
//
//   result   — `cvs -n update`: server-side U-status, conflicts
//              that update would surface, and StaleDirs (only this
//              call sees dirs removed on the server).
//   statuses — per-partition `cvs status`: per-file WorkingRev
//              (sticky-tag aware), the only source for baseRevMap.
//
// Running them sequentially produced two frames; the layout shift
// between them caused terminal-diff glitches (doubled headers,
// stray ANSI escapes). Parallel-then-merge gives the same data in
// one frame.
type statusRefreshedMsg struct {
	result   *cvs.UpdateResult
	statuses []cvs.FileStatus
	epoch    uint64
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

	// staleDirs is the set of directory paths CVS reported as gone from
	// the server (parsed from `cvs update -n` output). Populated on
	// statusRefreshedMsg and rendered in the tree as a yellow "!" badge.
	// Only the dry-run-update knows the truth about staleness; per-dir
	// `cvs status` doesn't report it, so this map is only refreshed by
	// the explicit `s` action, not the targeted refreshes after commit
	// or the initial directory scan.
	staleDirs map[string]bool

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

	// histRequestID + histStreams support streaming cvs log loads.
	// Each openHistoryFor that misses the cache bumps histRequestID
	// and stashes the stream's channel in histStreams. The handler
	// drops batches whose requestID isn't current (e.g., user opened
	// a different file mid-stream), so a slow loader from the
	// previous file can't poison the new view.
	histRequestID uint64
	histStreams   map[uint64]chan tea.Msg
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
		staleDirs:     make(map[string]bool),
		baseRevMap:    make(map[string]string),
		statusEpoch:   1,
		dirEpoch:      make(map[string]uint64),
		histRevisions: make(map[string]*cvs.FileHistory),
		histDiffs:     make(map[string]map[string]*cvs.DiffResult),
		histContents:  make(map[string]map[string]string),
		histPending:   make(map[string]bool),
		histStreams:   make(map[uint64]chan tea.Msg),
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
	m.tree.RefreshStatus(m.statusMap, m.staleDirs)
	m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
	m.refreshHistoryWorkingState()
}

// refreshHistoryWorkingState pushes the latest dirty flag and base
// revision into HistoryModel for the currently-viewed file. Called
// after every statusMap mutation so a History tab opened before the
// background dir scan finished still picks up the (working) badge or
// pseudo-row once the data lands.
func (m *App) refreshHistoryWorkingState() {
	path := m.history.Path()
	if path == "" {
		return
	}
	m.history.RefreshWorkingState(m.workingCopyIsDirty(path), m.baseRevMap[path])
}

// rebuildStatusMap replaces the status / stale-dir maps with fresh
// entries derived from a dry-run update result. Called from the
// statusRefreshedMsg handler — it's the only path that knows about
// stale directories, since per-dir `cvs status -l` doesn't report
// them.
func (m *App) rebuildStatusMap(result *cvs.UpdateResult) {
	m.statusMap = make(map[string]string)
	m.staleDirs = make(map[string]bool)
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
	for _, dir := range result.StaleDirs {
		m.staleDirs[dir] = true
	}
}

func (m App) Init() tea.Cmd {
	initEpoch := m.statusEpoch
	return tea.Batch(
		m.tree.Init(),
		backgroundDirScan(m.exec, initEpoch, m.initialPath),
	)
}

// refreshStatusUser fans out the two refresh sources in parallel
// (dry-run update + per-partition `cvs status`) and returns a single
// statusRefreshedMsg. Total wait equals the slower of the two; both
// share the same statusEpoch so a stale targeted scan can't override
// the merged result.
func (m *App) refreshStatusUser() tea.Cmd {
	m.statusEpoch++
	epoch := m.statusEpoch
	exec := m.exec
	return func() tea.Msg {
		var (
			wg       sync.WaitGroup
			result   *cvs.UpdateResult
			statuses []cvs.FileStatus
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			result, _ = exec.DryRunUpdate()
		}()
		go func() {
			defer wg.Done()
			statuses = runPartitionScan(exec, "")
		}()
		wg.Wait()
		return statusRefreshedMsg{result: result, statuses: statuses, epoch: epoch}
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

	case historyBatchMsg:
		// Stale batch from a previous request? Drop it. The
		// goroutine will keep pushing until it finishes or its
		// channel goes unread; the historyLoadedMsg sentinel for
		// that request is what eventually closes the loop.
		if msg.requestID != m.histRequestID {
			return m, historyStreamNext(m.histStreams[msg.requestID])
		}
		// Append batch to the model if the user is on this file. The
		// first batch also kicks off the diff load now that we know
		// the cursor's revision (and thus PrevNumber).
		needsDiff := false
		if m.history.Path() == msg.path {
			needsDiff = !m.history.HasRevisions()
			m.history.AppendRevisions(msg.revisions, m.workingCopyIsDirty(msg.path), m.baseRevMap[msg.path])
		}
		next := historyStreamNext(m.histStreams[msg.requestID])
		if needsDiff && m.history.NumRevisions() > 0 {
			return m, tea.Batch(next, m.loadHistoryContent())
		}
		return m, next

	case historyStreamErrMsg:
		// Treat as "loader finished, no data". The pending guard
		// clears so the user can retry by reopening the file.
		if msg.requestID == m.histRequestID {
			delete(m.histPending, pendingLog(msg.path))
		}
		delete(m.histStreams, msg.requestID)
		return m, nil

	case historyLoadedMsg:
		// Final sentinel from the streaming loader: full revisions
		// list arrives so we can fill the cache atomically. If a
		// newer request started while we were streaming, the cache
		// is still authoritative for this path — store it — but
		// don't touch the model (newer request owns the display).
		m.histRevisions[msg.path] = msg.history
		delete(m.histStreams, msg.requestID)
		if msg.requestID != m.histRequestID {
			return m, nil
		}
		delete(m.histPending, pendingLog(msg.path))
		// Re-apply with the canonical revisions list to re-derive
		// PrevNumber on the most-recent rev (the streaming parser
		// can't fill PrevNumber for the last revision it emits in a
		// batch — only the next batch can). The batched revisions
		// already in the model and the canonical list match
		// element-for-element, so we just rebuild from the cache.
		if m.history.Path() == msg.path && msg.history != nil {
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
			// Dry-run gives the canonical statusMap (incl. server-side
			// U-status the partition scan can't see) and StaleDirs.
			m.rebuildStatusMap(msg.result)
			// Overlay partition-scan results: per-file WorkingRev
			// (the only source for baseRevMap, needed for the History
			// (working) badge), and any sticky-tag-aware status the
			// scan reports more accurately than the dry-run summary.
			// Skip the reset on empty statuses — a failed/timed-out
			// scan must not wipe the prior baseRevMap.
			if len(msg.statuses) > 0 {
				m.baseRevMap = make(map[string]string, len(msg.statuses))
				for _, fs := range msg.statuses {
					if fs.WorkingRev != "" {
						m.baseRevMap[fs.Path] = fs.WorkingRev
					}
					if code := cvsStatusCode(fs.Status); code != "" {
						if _, ok := m.statusMap[fs.Path]; !ok {
							m.statusMap[fs.Path] = code
						}
					}
				}
			}
			for dir := range m.dirEpoch {
				m.dirEpoch[dir] = msg.epoch
			}
			m.tree.RefreshStatus(m.statusMap, m.staleDirs)
			m.favorites.UpdateCounts(m.statusMap)
			m.updateFileList()
			m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
			m.refreshHistoryWorkingState()
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

	case commitMessageEditedMsg:
		// User came back from $EDITOR. Empty message → abort; otherwise
		// dispatch the universal commitMsg so the conflict-marker check
		// and doCommit run via the same path as the inline-input flow.
		if msg.err != nil {
			m.notification = "✗ Editor invocation failed — see Console"
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, notifyAfter(notifyDuration)
		}
		message := strings.TrimSpace(msg.message)
		if message == "" {
			m.notification = "Commit aborted (empty message)"
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, notifyAfter(notifyDuration)
		}
		untracked := m.staged.PathsByStatus("?")
		commitFiles := m.staged.PathsByStatus("?", "A", "M", "C", "R")
		if len(commitFiles) == 0 {
			return m, nil
		}
		// Reset the inline input + mode so the post-commit state matches
		// what an Enter-based commit would leave behind.
		m.staged.input.SetValue("")
		m.staged.input.Blur()
		m.staged.mode = StagedActions
		m.focus = PanelLeft
		return m, func() tea.Msg {
			return commitMsg{message: message, untracked: untracked, files: commitFiles}
		}

	case searchSelectedMsg:
		// Navigate to the path the user picked from the fuzzy finder.
		// Behavior depends on which left-panel variant is active:
		//
		//   TreeViewFiles (default — left=dirs, right=files):
		//     Expand the tree down to the file's parent directory and
		//     populate the right-pane file list. For a file pick the
		//     filelist cursor lands on the file and focus shifts to
		//     the right panel so e/E/c/D/d/... work immediately. For
		//     a dir pick the tree cursor lands on the dir itself; the
		//     right pane shows that dir's contents.
		//
		//   TreeViewDetails (variant B — left=full tree+files, right=preview):
		//     Tree shows everything, so the cursor goes directly on
		//     the picked path (file or directory). Focus stays on the
		//     left panel; the diff preview refreshes for the new
		//     selection on the next event tick.
		m.activeTab = TabTree
		target := msg.path
		isDir := false
		if info, err := os.Stat(filepath.Join(m.exec.WorkDir, target)); err == nil {
			isDir = info.IsDir()
		}
		if m.treeMode == TreeViewDetails {
			m.tree.ExpandToPath(target)
			m.focus = PanelLeft
			return m, m.autoLoadPreview()
		}
		dir := target
		if !isDir {
			dir = filepath.Dir(target)
			if dir == "" {
				dir = "."
			}
		}
		m.tree.ExpandToPath(dir)
		m.updateFileListForDir(dir)
		if !isDir && m.filelist.FocusOnFile(target) {
			m.focus = PanelRight
		} else {
			m.focus = PanelLeft
		}
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
