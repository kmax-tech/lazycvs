package tui

import (
	"lazycvs/cvs"
	"lazycvs/fs"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// History tab logic: opening files, resolving the right-pane cache key
// from the current cursor + compare state, dispatching async loads,
// serving from cache, and prefetching neighbors. The History panel's
// per-file caches live on App (histRevisions / histDiffs / histContents)
// and are populated by handlers in app_core.go; everything in this file
// reads or writes those caches.

// --- cache & pending-set key builders -------------------------------------
//
// histPending is keyed by strings; concentrating the format in helpers
// prevents the construct-here / delete-there pair from drifting if a
// prefix or separator ever changes. histDiffs entries are keyed by the
// (fromRev, toRev) pair without a prefix — that's what diffCacheKey
// builds.

func pendingLog(path string) string {
	return "log:" + path
}

func pendingContent(path, rev string) string {
	return "content:" + path + ":" + rev
}

func pendingDiff(path, fromRev, toRev string) string {
	return "diff:" + path + ":" + fromRev + ":" + toRev
}

func diffCacheKey(fromRev, toRev string) string {
	return fromRev + ":" + toRev
}

// blameRev is the sentinel "revision" used as the rev field for blame
// results in the content cache. Not a real CVS revision — it's just a
// stable cache key so blame and revision content can share histContents.
const blameRev = "@blame"

// autoLoadHistory is called when the user opens the History tab. If the
// currently-selected file in tree/filelist differs from what History is
// showing, switch the History view to that file.
func (m *App) autoLoadHistory() tea.Cmd {
	var path string
	if f := m.filelist.SelectedFile(); f != nil {
		path = f.Path
	} else if node := m.tree.SelectedNode(); node != nil && !node.IsDir {
		path = node.Path
	}
	if path == "" || path == m.history.Path() {
		return nil
	}
	return m.openHistoryFor(path)
}

// openHistoryFor switches the History view to path. Revisions are served
// from the per-file cache when available (no re-fetch on revisit); only
// the first visit hits the cvs binary.
func (m *App) openHistoryFor(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	if path == m.history.Path() {
		return nil
	}
	m.history.SwitchTo(path)

	// Cache hit: push revisions in synchronously and load content.
	if hist, ok := m.histRevisions[path]; ok {
		m.history.ApplyRevisions(hist)
		if m.history.NumRevisions() > 0 {
			return m.loadHistoryContent()
		}
		return nil
	}

	// Cache miss: dispatch async. Guard against duplicate dispatch.
	pendKey := pendingLog(path)
	if m.histPending[pendKey] {
		return nil
	}
	m.histPending[pendKey] = true
	return loadHistory(m.exec, path)
}

// currentContentKey returns the cache key for the right-pane content the
// cursor is currently asking for. Empty when the model isn't asking for
// content (e.g. it's asking for a diff). Used by msg handlers to decide
// whether an arriving result should update the display.
func (m *App) currentContentKey() string {
	rev := m.history.SelectedRevision()
	if rev == nil {
		return ""
	}
	switch m.history.mode {
	case HistoryContent:
		return rev.Number
	case HistoryBlame:
		return blameRev
	case HistoryDiff:
		// Diff mode falls back to content for the initial revision (no parent).
		if rev.PrevNumber == "" {
			return rev.Number
		}
	}
	return ""
}

// currentDiffKey returns the (from, to) pair the cursor is asking for, or
// ("", "") when not in a diff scenario. The "from" side defaults to the
// cursor revision's parent but is overridden by an active compare anchor.
// The "to" side defaults to the cursor revision but is replaced by the
// workingRev sentinel when vs-working is toggled.
func (m *App) currentDiffKey() (string, string) {
	if m.history.mode != HistoryDiff {
		return "", ""
	}
	rev := m.history.SelectedRevision()
	if rev == nil {
		return "", ""
	}

	fromRev := rev.PrevNumber
	if a := m.history.compareAnchor; a >= 0 && a < len(m.history.revisions) {
		fromRev = m.history.revisions[a].Number
	}
	toRev := rev.Number
	if m.history.vsWorking {
		toRev = workingRev
	}

	// Initial revision with no parent and no anchor — nothing to diff.
	if fromRev == "" {
		return "", ""
	}
	// Anchor on the cursor row with no working-copy override is a no-op.
	if fromRev == toRev {
		return "", ""
	}
	return fromRev, toRev
}

// loadHistoryContent figures out what the right pane should be showing
// (diff, content, blame), serves it from cache if possible, and dispatches
// an async load otherwise. Pending dispatches are tracked so fast scrolling
// doesn't flood the cvs binary with duplicate requests for the same key.
func (m *App) loadHistoryContent() tea.Cmd {
	path := m.history.Path()
	if path == "" {
		return nil
	}

	if fs.IsBinary(filepath.Join(m.exec.WorkDir, path)) {
		m.history.ApplyContent("", "Binary file — cannot display content")
		return nil
	}

	rev := m.history.SelectedRevision()
	if rev == nil {
		return nil
	}

	switch m.history.mode {
	case HistoryContent:
		return m.serveContent(path, rev.Number)
	case HistoryBlame:
		return m.serveBlame(path)
	case HistoryDiff:
		fromRev, toRev := m.currentDiffKey()
		if fromRev == "" {
			// No diff to compute — initial revision with no parent or
			// the anchor coincides with the cursor row. Fall back to
			// the revision's content.
			return m.serveContent(path, rev.Number)
		}
		return m.serveDiff(path, fromRev, toRev)
	}
	return nil
}

func (m *App) serveContent(path, rev string) tea.Cmd {
	m.history.SetPendingLabels("", rev)
	if cached, ok := m.histContents[path][rev]; ok {
		m.history.ApplyContent(rev, cached)
		return nil
	}
	pendKey := pendingContent(path, rev)
	if m.histPending[pendKey] {
		return nil
	}
	m.histPending[pendKey] = true
	return loadRevisionContent(m.exec, path, rev)
}

func (m *App) serveDiff(path, fromRev, toRev string) tea.Cmd {
	m.history.SetPendingLabels(fromRev, toRev)
	cacheKey := diffCacheKey(fromRev, toRev)
	if cached, ok := m.histDiffs[path][cacheKey]; ok {
		m.history.ApplyDiff(fromRev, toRev, cached)
		// Prefetch only makes sense for the default parent-diff case;
		// in compare/working modes the adjacency relationship doesn't
		// hold and prefetching neighbors would just be wasted work.
		if m.parentDiff(fromRev, toRev) {
			return m.prefetchAdjacentDiffs(path)
		}
		return nil
	}
	pendKey := pendingDiff(path, fromRev, toRev)
	if m.histPending[pendKey] {
		return nil
	}
	m.histPending[pendKey] = true

	var loader tea.Cmd
	if toRev == workingRev {
		loader = loadWorkingDiff(m.exec, path, fromRev)
	} else {
		loader = loadRevisionDiff(m.exec, path, fromRev, toRev)
	}

	if !m.parentDiff(fromRev, toRev) {
		return loader
	}
	prefetch := m.prefetchAdjacentDiffs(path)
	if prefetch == nil {
		return loader
	}
	return tea.Batch(loader, prefetch)
}

// parentDiff reports whether (fromRev, toRev) is the cursor's natural
// parent-vs-cursor diff. Anchored compares and working-copy diffs are
// excluded — they don't follow the linear adjacency that prefetching
// exploits.
func (m *App) parentDiff(fromRev, toRev string) bool {
	if m.history.compareAnchor >= 0 || m.history.vsWorking {
		return false
	}
	rev := m.history.SelectedRevision()
	if rev == nil {
		return false
	}
	return fromRev == rev.PrevNumber && toRev == rev.Number
}

func (m *App) serveBlame(path string) tea.Cmd {
	m.history.SetPendingLabels("", blameRev)
	if cached, ok := m.histContents[path][blameRev]; ok {
		m.history.ApplyContent(blameRev, cached)
		return nil
	}
	pendKey := pendingContent(path, blameRev)
	if m.histPending[pendKey] {
		return nil
	}
	m.histPending[pendKey] = true
	return loadBlame(m.exec, path)
}

// prefetchAdjacentDiffs warms the cache for revisions near the cursor so
// neighbor navigation is instant. Skips entries already cached or pending.
func (m *App) prefetchAdjacentDiffs(path string) tea.Cmd {
	revs := m.histRevisions[path]
	if revs == nil {
		return nil
	}
	cursor := m.history.cursor
	var cmds []tea.Cmd
	for _, delta := range []int{-1, 1} {
		idx := cursor + delta
		if idx < 0 || idx >= len(revs.Revisions) {
			continue
		}
		adj := revs.Revisions[idx]
		if adj.PrevNumber == "" {
			continue
		}
		key := diffCacheKey(adj.PrevNumber, adj.Number)
		if _, ok := m.histDiffs[path][key]; ok {
			continue
		}
		pendKey := pendingDiff(path, adj.PrevNumber, adj.Number)
		if m.histPending[pendKey] {
			continue
		}
		m.histPending[pendKey] = true
		cmds = append(cmds, loadRevisionDiff(m.exec, path, adj.PrevNumber, adj.Number))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// --- Tree variant B: inline preview (lives here because it parallels
// the diff-loading flow used by the History tab proper) ----------------

func loadPreviewDiff(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", path)
		if result == nil {
			return previewMsg{path: path}
		}
		parsed := cvs.ParseDiff(cvs.EnsureUTF8(result.Stdout))
		return previewMsg{path: path, diff: parsed}
	}
}

func (m *App) autoLoadPreview() tea.Cmd {
	node := m.tree.SelectedNode()
	if node == nil || node.IsDir {
		m.previewReady = false
		return nil
	}
	if node.Path == m.previewPath && m.previewReady {
		return nil // already loaded
	}
	m.previewPath = node.Path
	m.previewReady = false
	if fs.IsBinary(filepath.Join(m.exec.WorkDir, node.Path)) {
		m.previewDiff = nil
		m.previewReady = true
		return nil
	}
	if node.Status == "M" || node.Status == "C" {
		return loadPreviewDiff(m.exec, node.Path)
	}
	// No diff needed — mark ready with nil diff
	m.previewDiff = nil
	m.previewReady = true
	return nil
}

func (m *App) applyTreeMode() tea.Cmd {
	m.tree.showFiles = (m.treeMode == TreeViewDetails)
	m.tree.rebuildFlat()
	if m.treeMode == TreeViewDetails {
		return m.autoLoadPreview()
	}
	m.updateFileList()
	return nil
}
