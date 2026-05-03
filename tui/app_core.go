package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"fmt"
	"path/filepath"
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

type dirStatusMsg struct {
	dir      string
	statuses []cvs.FileStatus
	epoch    uint64
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
type updateDoneMsg struct {
	paths []string
	err   error
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
		statusEpoch:   1,
		dirEpoch:      make(map[string]uint64),
	}
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
		var cmd tea.Cmd
		m.history, cmd = m.history.Update(msg)
		// Auto-load first revision's content
		if len(m.history.revisions) > 0 {
			return m, tea.Batch(cmd, m.loadHistoryContent())
		}
		return m, cmd

	case dirStatusMsg:
		m.console.refreshContent()
		if msg.epoch < m.dirEpoch[msg.dir] {
			return m, nil
		}
		m.dirEpoch[msg.dir] = msg.epoch
		for path := range m.statusMap {
			if filepath.Dir(path) == msg.dir {
				delete(m.statusMap, path)
			}
		}
		for _, fs := range msg.statuses {
			code := cvsStatusCode(fs.Status)
			if code != "" {
				m.statusMap[fs.Path] = code
			}
		}
		m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
		m.tree.rebuildFlat()
		m.updateFileList()
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		return m, nil

	case statusRefreshedMsg:
		m.console.refreshContent()
		if msg.result != nil {
			m.rebuildStatusMap(msg.result)
			for dir := range m.dirEpoch {
				m.dirEpoch[dir] = msg.epoch
			}
			m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
			m.tree.rebuildFlat()
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
		return m, m.refreshStatusForPaths(msg.paths)

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
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.files))

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
		return m, doRemove(m.exec, msg.path, msg.status)

	case updateDoneMsg:
		if msg.err != nil {
			m.notification = "✗ Update failed — see Console"
			m.notificationOK = false
		} else if len(msg.paths) == 1 {
			m.notification = fmt.Sprintf("✓ Updated %s", filepath.Base(msg.paths[0]))
			m.notificationOK = true
		} else {
			m.notification = fmt.Sprintf("✓ Updated %d file(s)", len(msg.paths))
			m.notificationOK = true
		}
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes()
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(msg.paths))

	case removeDoneMsg:
		paths := []string{msg.path}
		if msg.err != nil {
			m.notification = fmt.Sprintf("✗ Remove failed for %s — see Console", filepath.Base(msg.path))
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(paths))
		}
		delete(m.filelist.marked, msg.path)
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
		m.notification = fmt.Sprintf("✓ Removed %s", filepath.Base(msg.path))
		m.notificationOK = true
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes()
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatusForPaths(paths))

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
		// Clear old status for checked files, apply new
		for _, p := range msg.paths {
			delete(m.statusMap, p)
		}
		for _, fs := range msg.statuses {
			code := cvsStatusCode(fs.Status)
			if code != "" {
				m.statusMap[fs.Path] = code
			}
		}
		m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
		m.tree.rebuildFlat()
		m.staged.Refresh(m.filelist.marked, m.resolveFileStatus)
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

	m.history, cmd = m.history.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	m.search, cmd = m.search.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}
