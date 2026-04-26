package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"fmt"
	ioFS "io/fs"
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
}

type dirStatusMsg struct {
	dir      string
	statuses []cvs.FileStatus
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

// notifyAfter schedules a notificationExpiredMsg after d.
func notifyAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return notificationExpiredMsg{} })
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

const maxFilesForFullScan = 1000

func (m App) Init() tea.Cmd {
	cmds := []tea.Cmd{m.tree.Init()}
	if m.initialPath != "" {
		scopeDir := filepath.Join(m.exec.WorkDir, m.initialPath)
		if countFilesQuick(scopeDir, maxFilesForFullScan) < maxFilesForFullScan {
			cmds = append(cmds, m.refreshStatusScoped(m.initialPath))
		}
	} else if countFilesQuick(m.exec.WorkDir, maxFilesForFullScan) < maxFilesForFullScan {
		// Small repo — full recursive scan
		cmds = append(cmds, m.refreshStatus())
	}
	// Large repo without initialPath: skip full scan, rely on per-directory status
	return tea.Batch(cmds...)
}

// countFilesQuick counts files up to a limit, skipping CVS/hidden dirs.
func countFilesQuick(dir string, limit int) int {
	count := 0
	filepath.WalkDir(dir, func(path string, d ioFS.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			name := d.Name()
			if name == "CVS" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return count
}

// refreshStatus runs a full recursive dry-run update.
func (m App) refreshStatus() tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		result, _ := exec.DryRunUpdate()
		return statusRefreshedMsg{result: result}
	}
}

// refreshStatusScoped runs a dry-run update scoped to a specific directory.
func (m App) refreshStatusScoped(dir string) tea.Cmd {
	exec := m.exec
	return func() tea.Msg {
		result, err := exec.Run("-n", "-q", "update", "-d", "-P", dir)
		if err != nil && result == nil {
			return statusRefreshedMsg{}
		}
		update := cvs.ParseUpdate(result.Stdout, result.Stderr, exec.WorkDir)
		update.Duration = result.Duration
		return statusRefreshedMsg{result: update}
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
		cmds := []tea.Cmd{cmd, loadDirStatus(m.exec, ".")}
		if m.initialPath != "" {
			expandedDirs := m.tree.ExpandToPath(m.initialPath)
			m.updateFileList()
			for _, dir := range expandedDirs {
				cmds = append(cmds, loadDirStatus(m.exec, dir))
			}
			m.initialPath = ""
		}
		return m, tea.Batch(cmds...)

	case treeDirLoadedMsg:
		var cmd tea.Cmd
		m.tree, cmd = m.tree.Update(msg)
		m.updateFileList()
		statusCmd := loadDirStatus(m.exec, msg.path)
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
		for _, fs := range msg.statuses {
			code := cvsStatusCode(fs.Status)
			if code != "" {
				m.statusMap[fs.Path] = code
			}
		}
		m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
		m.tree.rebuildFlat()
		m.updateFileList()
		return m, nil

	case statusRefreshedMsg:
		m.console.refreshContent()
		if msg.result != nil {
			m.rebuildStatusMap(msg.result)
			m.tree.applyStatusToNodes(m.tree.root, m.statusMap)
			m.tree.rebuildFlat()
			m.favorites.UpdateCounts(m.statusMap)
			m.updateFileList()
			// Also load per-directory status for expanded dirs
			var cmds []tea.Cmd
			for _, dir := range m.tree.ExpandedDirs() {
				cmds = append(cmds, loadDirStatus(m.exec, dir))
			}
			return m, tea.Batch(cmds...)
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

	case commitDoneMsg:
		if msg.err == nil {
			m.notification = fmt.Sprintf("✓ Committed %d file(s): %q", len(msg.files), msg.message)
			m.notificationOK = true
			// Drop committed paths from the staged set — the commit
			// succeeded so they're no longer pending. (For a failed
			// commit we leave them so the user can retry.)
			for _, p := range msg.files {
				delete(m.filelist.marked, p)
			}
			// Optimistic: committed files are now clean. Update statusMap
			// + tree + file list immediately so switching to Tab 1 shows
			// the new state without waiting for the async refresh.
			m.applyOptimisticStatus(msg.files, "")
			m.staged.Refresh(m.filelist.marked, m.statusMap)
		} else {
			m.notification = "✗ Commit failed — see Console for details"
			m.notificationOK = false
		}
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes() // banner reduces contentHeight by 1 — relayout sub-panels
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatus())

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
		// Update can produce arbitrary status changes (M, C, "" — we
		// can't predict per-file). Always run the full refresh to pick
		// up the actual state. No optimistic update.
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatus())

	case removeDoneMsg:
		if msg.err != nil {
			m.notification = fmt.Sprintf("✗ Remove failed for %s — see Console", filepath.Base(msg.path))
			m.notificationOK = false
			m.notificationExpiry = time.Now().Add(notifyDuration)
			m.updateSizes()
			return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatus())
		}
		// Drop the path from the staged set — it's gone or scheduled for
		// removal, no point leaving it marked for commit.
		delete(m.filelist.marked, msg.path)
		// Optimistic status update: choose the next status based on the
		// pre-remove status, so other tabs reflect the new state without
		// waiting for the async refreshStatus to complete.
		var newStatus string
		switch msg.oldStatus {
		case "?", "A":
			// Untracked file → gone. Added-but-not-committed → un-added.
			// In both cases the file is no longer relevant to CVS.
			newStatus = ""
		case "R":
			// Already R — no transition.
			newStatus = "R"
		default:
			// Tracked file (clean / M / C / U / "") → scheduled for
			// removal. cvs remove -f sets the entry to R until commit.
			newStatus = "R"
		}
		m.applyOptimisticStatus([]string{msg.path}, newStatus)
		m.staged.Refresh(m.filelist.marked, m.statusMap)
		m.notification = fmt.Sprintf("✓ Removed %s", filepath.Base(msg.path))
		m.notificationOK = true
		m.notificationExpiry = time.Now().Add(notifyDuration)
		m.updateSizes()
		return m, tea.Batch(notifyAfter(notifyDuration), m.refreshStatus())

	case editorClosedMsg:
		return m, m.refreshStatus()

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
		m.staged.Refresh(m.filelist.marked, m.statusMap)
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
		m.staged.Refresh(m.filelist.marked, m.statusMap)
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
