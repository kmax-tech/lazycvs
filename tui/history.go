package tui

import (
	"bufio"
	"fmt"
	"github.com/kmax-tech/lazycvs/cvs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// hScrollStep is how many visible columns one < / > press scrolls by.
const hScrollStep = 8

type HistoryMode int

const (
	HistoryContent HistoryMode = iota
	HistoryDiff
	HistoryBlame
)

// historyFileUI is the per-file UI state (cursor, mode, scroll, compare
// state). Preserved across file switches so navigating away and back
// keeps the user's place — including any active comparison.
type historyFileUI struct {
	cursor        int
	offset        int
	mode          HistoryMode
	diffSBS       bool
	compareAnchor int
	vsWorking     bool
}

// HistoryModel renders the History tab. It owns only the *current* view
// (which file is open, where the cursor is, what's in the viewport). All
// CVS data — revision lists, diffs, file contents — lives in App-owned
// per-path caches (see App.historyData) and is pushed in via Apply* methods
// from the App handlers. This keeps async results from one file from ever
// poisoning another file's view: the cache is keyed by the path the result
// came from, and the model only reflects data for its current path.
type HistoryModel struct {
	path       string
	revisions  []cvs.Revision
	cursor     int
	offset     int
	mode       HistoryMode
	leftWidth  int
	rightWidth int
	height     int

	// Current right-pane projection. diffData is non-nil in HistoryDiff
	// mode when a diff is loaded; content is non-empty otherwise. Both are
	// derived from App-owned caches.
	diffData *cvs.DiffResult
	content  string
	diffSBS  bool

	// Right-pane labels for the panel border title. Set eagerly when the
	// cursor moves so the title reflects the cursor immediately, not after
	// the async result lands.
	diffFromRev string
	diffToRev   string

	// compareAnchor is the index of a revision the user pinned as the
	// "from" side of a diff via the space key. -1 = no anchor; in that
	// case the parent revision is used as "from". When set, the right
	// pane shows revisions[anchor] ↔ cursor instead of cursor.parent ↔
	// cursor.
	compareAnchor int

	// vsWorking, when true, replaces the "to" side of the diff with the
	// on-disk working copy. Toggled with the w key.
	vsWorking bool

	// Working-copy state vs the revision history. Set by ApplyRevisions
	// from the file's CVS status; mutually exclusive.
	//
	// hasWorkingRow      → working tree diverges from base (M/C/A/R); a
	//                      pseudo "working" row is exposed at virtual
	//                      cursor index 0.
	// workingMatchesBase → working tree matches the base revision; the
	//                      base row gets a "(working)" annotation.
	hasWorkingRow      bool
	workingMatchesBase bool

	// baseRev is the revision number the on-disk file is checked out to
	// — populated from `cvs status` "Working revision:". Sticky-tag and
	// branch aware: not necessarily the most recent revision in the log.
	// Empty when unknown (no status loaded yet).
	baseRev string

	viewport viewport.Model
	hOffset  int
	rawView  string

	// Per-file UI memory. Saved on path switch, restored on return.
	uiState map[string]historyFileUI
}

func NewHistoryModel() HistoryModel {
	return HistoryModel{
		compareAnchor: -1,
		uiState:       make(map[string]historyFileUI),
	}
}

// --- async messages ---------------------------------------------------------

// historyLoadedMsg carries the final, complete `cvs log` output for a
// path — sent once the streaming loader has finished consuming the
// subprocess pipe. Intermediate progress is delivered via
// historyBatchMsg.
type historyLoadedMsg struct {
	path      string
	requestID uint64
	history   *cvs.FileHistory
}

// historyBatchMsg carries a partial slice of revisions that the
// streaming loader parsed since the last batch. The handler appends
// the slice to the model so the user sees revisions appear
// progressively rather than in one block at the end.
//
// requestID is the per-load identifier the loader was started with; a
// later openHistoryFor for a different file (or same file after an
// invalidation) bumps it, so any in-flight batches from the previous
// request get dropped at the handler.
type historyBatchMsg struct {
	path      string
	requestID uint64
	revisions []cvs.Revision
}

// historyStreamErrMsg reports a streaming load failure. Treated as
// "loader finished with empty result" by the handler — the cache
// slot stays empty so the next openHistoryFor retries.
type historyStreamErrMsg struct {
	path      string
	requestID uint64
	err       error
}

// historyContentMsg carries the on-disk content of one revision (or a
// blame/working-copy result). path+rev key the App's content cache.
type historyContentMsg struct {
	path    string
	rev     string
	content string
}

// historyDiffMsg carries a parsed unified diff between two revisions.
// path + (fromRev:toRev) key the App's diff cache.
type historyDiffMsg struct {
	path    string
	fromRev string
	toRev   string
	diff    *cvs.DiffResult
}

// historyStreamBatchSize is how many revisions accumulate before the
// streaming loader flushes a batch to the UI. Small enough that the
// first paint happens within a few hundred ms on slow links; large
// enough that the channel doesn't get spammed for tiny commits.
const historyStreamBatchSize = 3

// historyStreamFlushInterval bounds how long a partial batch can sit
// before being sent. Together with batchSize it gives the UI either
// "3 revisions" or "everything I have so far after 100ms" — whichever
// comes first.
const historyStreamFlushInterval = 100 * time.Millisecond

// startHistoryStream spawns a goroutine that runs `cvs log -N <path>`
// with a piped stdout, parses revision blocks as they arrive, and
// pushes batches of up to historyStreamBatchSize revisions (or after
// historyStreamFlushInterval) to the returned channel as
// historyBatchMsg. When the cvs process finishes the goroutine sends
// a final historyLoadedMsg with the full revisions slice (so the App
// cache slot can be filled atomically) and closes the channel.
//
// The first tea.Cmd returned reads the first message from the
// channel; the App handler returns historyStreamNext to wait for the
// next, until it receives the historyLoadedMsg sentinel.
//
// requestID is the per-load identifier; every emitted msg carries it
// so the handler can drop batches from a now-stale request after a
// file switch.
func startHistoryStream(executor *cvs.CVSExecutor, path string, requestID uint64) (chan tea.Msg, tea.Cmd) {
	out := make(chan tea.Msg, 8)

	go runHistoryStream(executor, path, requestID, out)

	return out, historyStreamNext(out)
}

// historyStreamNext is the tea.Cmd helper the handler returns after
// each batch to keep the channel-pump going. It blocks the cmd
// goroutine on a single channel receive — bubbletea's cmd loop will
// just spawn it again when the previous batch is delivered.
func historyStreamNext(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			// Channel closed without a sentinel — treat as empty done.
			return nil
		}
		return msg
	}
}

// runHistoryStream is the goroutine body: starts cvs log -N, scans
// stdout, accumulates revisions into batches, flushes on size or
// timer, and emits the final historyLoadedMsg with the full list.
//
// Implementation notes:
//   - PrevNumber is set on each revision when the *next* one is
//     parsed (cvs log lists newest-first, so revision N's predecessor
//     is whatever arrives next). The most recent revision in any
//     batch therefore has its PrevNumber filled in only when the
//     subsequent revision is parsed — we hold one revision back as
//     "pending" and emit it once we've seen its successor.
//   - The flush timer is implemented via a select on a time.Timer
//     channel so we don't block waiting for the next line if the
//     subprocess goes quiet.
func runHistoryStream(executor *cvs.CVSExecutor, path string, requestID uint64, out chan<- tea.Msg) {
	defer close(out)

	cmd := exec.Command(executor.CVSBin, "-Q", "log", "-N", path)
	cmd.Dir = executor.WorkDir
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		out <- historyStreamErrMsg{path: path, requestID: requestID, err: err}
		return
	}
	if err := cmd.Start(); err != nil {
		out <- historyStreamErrMsg{path: path, requestID: requestID, err: err}
		return
	}

	// Read lines into a channel so we can select on them alongside
	// the flush timer.
	lines := make(chan string, 32)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(stdoutPipe)
		s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for s.Scan() {
			lines <- s.Text()
		}
		// Scanner errors (I/O failure mid-stream, oversized token) would
		// otherwise be discarded — the consumer would see a truncated
		// revision list with no explanation. Surface them as a stream
		// error message so the handler can show "history load failed".
		if err := s.Err(); err != nil {
			out <- historyStreamErrMsg{path: path, requestID: requestID, err: err}
		}
	}()

	parser := newRevisionParser()
	var pending []cvs.Revision  // accumulated since last flush
	var emitted []cvs.Revision  // everything we've sent for the final cache fill

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := append([]cvs.Revision(nil), pending...)
		emitted = append(emitted, batch...)
		out <- historyBatchMsg{path: path, requestID: requestID, revisions: batch}
		pending = pending[:0]
	}

	timer := time.NewTimer(historyStreamFlushInterval)
	defer timer.Stop()

LOOP:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if rev := parser.finish(); rev != nil {
					pending = append(pending, *rev)
				}
				flush()
				break LOOP
			}
			if rev := parser.feed(line); rev != nil {
				pending = append(pending, *rev)
				if len(pending) >= historyStreamBatchSize {
					flush()
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(historyStreamFlushInterval)
				}
			}
		case <-timer.C:
			flush()
			timer.Reset(historyStreamFlushInterval)
		}
	}

	_ = cmd.Wait()

	// Final message: the full list, atomic and cacheable.
	full := &cvs.FileHistory{Path: path, Revisions: emitted}
	out <- historyLoadedMsg{path: path, requestID: requestID, history: full}
}

// revisionParser is a streaming parser for `cvs log -N` output. Each
// call to feed(line) consumes one line; when the parser detects that
// it has finished a revision block, it returns the parsed Revision.
// The PrevNumber field is filled in via the "hold one back" mechanism
// described in runHistoryStream's doc comment.
type revisionParser struct {
	current *cvs.Revision // currently-being-parsed revision
	pending *cvs.Revision // last completed, awaiting successor for PrevNumber
	inMsg   bool          // we're inside the multi-line commit-message section
}

func newRevisionParser() *revisionParser { return &revisionParser{} }

// feed consumes one line. Returns a Revision when one was just
// completed (and its PrevNumber filled in by the next-arriving
// block); returns nil when more lines are needed before emitting.
func (p *revisionParser) feed(line string) *cvs.Revision {
	const sep = "----------------------------"
	const end = "============================================================================="

	if line == sep || line == end {
		// Block boundary: finalize current, possibly emit pending.
		var emit *cvs.Revision
		if p.current != nil {
			p.current.Message = strings.TrimSpace(p.current.Message)
			if p.pending != nil {
				p.pending.PrevNumber = p.current.Number
				cp := *p.pending
				emit = &cp
			}
			p.pending = p.current
			p.current = nil
		}
		p.inMsg = false
		return emit
	}

	if m := revisionRe.FindStringSubmatch(line); m != nil {
		p.current = &cvs.Revision{Number: m[1]}
		p.inMsg = false
		return nil
	}

	if p.current != nil && !p.inMsg {
		if m := dateLineRe.FindStringSubmatch(line); m != nil {
			p.current.Date = parseStreamCVSDate(strings.TrimSpace(m[1]))
			p.current.Author = m[2]
			if m[4] != "" {
				p.current.LinesAdded, _ = strconv.Atoi(m[4])
			}
			if m[5] != "" {
				p.current.LinesRemoved, _ = strconv.Atoi(m[5])
			}
			p.inMsg = true
			return nil
		}
	}

	if p.inMsg && p.current != nil {
		if p.current.Message != "" {
			p.current.Message += "\n"
		}
		p.current.Message += line
	}
	return nil
}

// finish flushes any remaining pending revision when the input ends.
// PrevNumber stays empty for the very last revision (it has no
// predecessor in the log — it's the oldest commit).
func (p *revisionParser) finish() *cvs.Revision {
	if p.pending == nil {
		return nil
	}
	cp := *p.pending
	p.pending = nil
	return &cp
}

// CVS log uses several date formats; reuse the same fallback chain
// the batched parser does.
func parseStreamCVSDate(s string) time.Time {
	for _, f := range []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05 -0700",
		"2006/01/02 15:04:05 -0700",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	if len(s) > 19 {
		for _, f := range []string{
			"2006/01/02 15:04:05",
			"2006-01-02 15:04:05 -0700",
		} {
			if t, err := time.Parse(f, s[:19]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// Reuse the regexes from cvs/log.go via package-level vars in cvs.
// (revisionRe and dateLineRe are unexported there, so we duplicate
// them here. They're stable enough that drift is unlikely.)
var (
	revisionRe = regexp.MustCompile(`^revision\s+(\S+)`)
	dateLineRe = regexp.MustCompile(`^date:\s+(.+?);\s+author:\s+(.+?);\s+state:\s+(.+?);(?:\s+lines:\s+\+(\d+)\s+-(\d+))?`)
)

func loadRevisionContent(exec *cvs.CVSExecutor, path, rev string) tea.Cmd {
	return func() tea.Msg {
		content, err := catRevisionStdout(exec, path, rev)
		if err != nil {
			return historyContentMsg{path: path, rev: rev, content: "Error loading revision: " + err.Error()}
		}
		return historyContentMsg{path: path, rev: rev, content: cvs.EnsureUTF8(content)}
	}
}

func loadRevisionDiff(exec *cvs.CVSExecutor, path, fromRev, toRev string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", "-r", fromRev, "-r", toRev, path)
		var diff *cvs.DiffResult
		if result != nil {
			diff = cvs.ParseDiff(cvs.EnsureUTF8(result.Stdout))
		}
		return historyDiffMsg{path: path, fromRev: fromRev, toRev: toRev, diff: diff}
	}
}

// loadWorkingDiff runs `cvs diff -u -r <fromRev> <file>` (one -r flag), which
// produces a diff from fromRev to the on-disk working copy. The result is
// stored in the diff cache under the sentinel toRev "@working" so the
// working-copy comparison gets cached just like a rev-to-rev diff.
func loadWorkingDiff(exec *cvs.CVSExecutor, path, fromRev string) tea.Cmd {
	return func() tea.Msg {
		result, _ := exec.RunReadOnly("diff", "-u", "-r", fromRev, path)
		var diff *cvs.DiffResult
		if result != nil {
			diff = cvs.ParseDiff(cvs.EnsureUTF8(result.Stdout))
		}
		return historyDiffMsg{path: path, fromRev: fromRev, toRev: workingRev, diff: diff}
	}
}

// workingRev is the sentinel revision string used as the "to" side of a
// working-copy diff. It's not a real CVS revision, just a stable cache
// key and label. prettyRev maps it to a human-readable form for titles.
const workingRev = "@working"

// prettyRev converts internal sentinel revision identifiers into the
// labels we want to show in panel titles.
func prettyRev(rev string) string {
	if rev == workingRev {
		return "working"
	}
	return rev
}

func loadBlame(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		result, err := exec.RunReadOnly("annotate", path)
		if err != nil && result == nil {
			return historyContentMsg{path: path, rev: blameRev, content: "Error loading blame"}
		}
		return historyContentMsg{path: path, rev: blameRev, content: cvs.EnsureUTF8(result.Stdout)}
	}
}

// --- per-file UI state ------------------------------------------------------

// SwitchTo loads UI state for path, saving the current path's state first.
// Caller is responsible for then pushing the file's revisions/diff/content
// from the App's caches via the Apply* methods.
func (m *HistoryModel) SwitchTo(path string) {
	if m.path != "" {
		m.uiState[m.path] = historyFileUI{
			cursor:        m.cursor,
			offset:        m.offset,
			mode:          m.mode,
			diffSBS:       m.diffSBS,
			compareAnchor: m.compareAnchor,
			vsWorking:     m.vsWorking,
		}
	}
	m.path = path
	m.revisions = nil
	m.diffData = nil
	m.content = ""
	m.diffFromRev = ""
	m.diffToRev = ""
	m.hOffset = 0
	m.rawView = ""
	if s, ok := m.uiState[path]; ok {
		m.cursor = s.cursor
		m.offset = s.offset
		m.mode = s.mode
		m.diffSBS = s.diffSBS
		m.compareAnchor = s.compareAnchor
		m.vsWorking = s.vsWorking
	} else {
		m.cursor, m.offset = 0, 0
		m.mode = HistoryDiff
		m.diffSBS = false
		m.compareAnchor = -1
		m.vsWorking = false
	}
}

// ApplyRevisions pushes a freshly loaded revisions list and the file's
// working-copy state.
//
//   dirty   — the working tree diverges from the base revision
//             (M / C / A / R). When true, a pseudo "working" row is
//             exposed at virtual cursor index 0 and the working copy
//             can be picked for compare like any other row.
//   baseRev — the revision the on-disk file is checked out to (sticky-
//             tag aware). When dirty is false and baseRev matches a
//             revision in the list, that row gets a "(working)" badge.
//             Note: this is NOT necessarily the repo HEAD — a working
//             copy can sit on a sticky tag or a branch.
func (m *HistoryModel) ApplyRevisions(history *cvs.FileHistory, dirty bool, baseRev string) {
	if history != nil {
		m.revisions = history.Revisions
	} else {
		m.revisions = nil
	}
	m.applyWorkingState(dirty, baseRev)
}

// RefreshWorkingState updates the dirty / baseRev derived fields
// without reloading the revisions list. Called by App when statusMap
// or baseRevMap entries change for the currently-viewed file (e.g. a
// background dir scan finished after the History tab was opened, or a
// post-action status refresh updated this path).
func (m *HistoryModel) RefreshWorkingState(dirty bool, baseRev string) {
	m.applyWorkingState(dirty, baseRev)
}

// AppendRevisions appends a batch of revisions to the existing list
// and re-derives the working-state flags. Used by the streaming
// loader to grow the displayed list incrementally; the final
// historyLoadedMsg replaces the model's list with the canonical
// version once the loader finishes.
func (m *HistoryModel) AppendRevisions(revs []cvs.Revision, dirty bool, baseRev string) {
	m.revisions = append(m.revisions, revs...)
	m.applyWorkingState(dirty, baseRev)
}

// HasRevisions reports whether the model already has at least one
// revision in its list. The streaming loader uses this to decide
// whether the just-arrived batch is the *first* batch — in which
// case it kicks off the diff load now that PrevNumber on the
// cursor row is known.
func (m HistoryModel) HasRevisions() bool { return len(m.revisions) > 0 }

// applyWorkingState centralizes the bookkeeping that ApplyRevisions and
// RefreshWorkingState both need: derived flags, cursor/offset clamp,
// stale-anchor drop. Cheap and idempotent.
func (m *HistoryModel) applyWorkingState(dirty bool, baseRev string) {
	hasRevisions := len(m.revisions) > 0
	m.hasWorkingRow = dirty && hasRevisions
	m.workingMatchesBase = !dirty && hasRevisions
	m.baseRev = baseRev
	rows := m.numRows()
	if m.cursor >= rows {
		m.cursor = 0
		m.offset = 0
	}
	// A previously-saved compare anchor may be stale if the file's
	// dirty state changed (working pseudo-row appeared or disappeared).
	// Drop it rather than referencing a non-existent row.
	if m.compareAnchor >= rows {
		m.compareAnchor = -1
	}
}

// numRows returns the count of selectable rows in the left pane,
// including the pseudo working-copy row when present.
func (m HistoryModel) numRows() int {
	if m.hasWorkingRow {
		return len(m.revisions) + 1
	}
	return len(m.revisions)
}

// revisionIndex maps a left-pane row index to the m.revisions slice
// index. Returns -1 for the pseudo working-copy row (when present).
func (m HistoryModel) revisionIndex(row int) int {
	if m.hasWorkingRow {
		if row == 0 {
			return -1
		}
		return row - 1
	}
	return row
}

// IsWorkingCopyRow reports whether the cursor is on the pseudo
// working-copy row.
func (m HistoryModel) IsWorkingCopyRow() bool {
	return m.hasWorkingRow && m.cursor == 0
}

// CompareAnchorIsWorking reports whether the user pinned the working
// copy as the comparison "from" via space on the pseudo-row.
func (m HistoryModel) CompareAnchorIsWorking() bool {
	return m.hasWorkingRow && m.compareAnchor == 0
}

// HasCompare reports whether any compare anchor is currently set
// (either on a real revision or on the working pseudo-row).
func (m HistoryModel) HasCompare() bool {
	return m.compareAnchor >= 0
}

// CompareAnchorRev returns the revision pinned as compare anchor, or
// nil if no anchor is set or the anchor is the working pseudo-row.
func (m HistoryModel) CompareAnchorRev() *cvs.Revision {
	if m.compareAnchor < 0 {
		return nil
	}
	idx := m.revisionIndex(m.compareAnchor)
	if idx < 0 || idx >= len(m.revisions) {
		return nil
	}
	return &m.revisions[idx]
}

// BaseRevision returns the revision the on-disk file is checked out to
// (the "Working revision" from cvs status). Falls back to the most
// recent revision in the list if the working revision is unknown or
// not present in the log — the common case for a clean file on trunk
// where these coincide.
//
// This is the revision the working copy is BASED on, not necessarily
// repo HEAD: a sticky-tag or branch checkout can have a base far below
// the latest revision.
func (m HistoryModel) BaseRevision() *cvs.Revision {
	if len(m.revisions) == 0 {
		return nil
	}
	if m.baseRev != "" {
		for i := range m.revisions {
			if m.revisions[i].Number == m.baseRev {
				return &m.revisions[i]
			}
		}
	}
	return &m.revisions[0]
}

// ApplyDiff pushes a parsed diff into the right pane. The labels are kept
// in sync with diffFromRev/diffToRev so the panel title matches the body.
func (m *HistoryModel) ApplyDiff(fromRev, toRev string, diff *cvs.DiffResult) {
	m.diffFromRev = fromRev
	m.diffToRev = toRev
	m.diffData = diff
	m.content = ""
	m.hOffset = 0
	m.resetViewport()
	m.setView(m.renderDiffView())
}

// ApplyContent pushes file content into the right pane (revision content,
// blame, or working copy).
func (m *HistoryModel) ApplyContent(rev, content string) {
	m.diffFromRev = ""
	m.diffToRev = rev
	m.diffData = nil
	m.content = content
	m.hOffset = 0
	m.resetViewport()
	m.setView(content)
}

// SetPendingLabels updates the right-panel header labels eagerly (before
// the async result lands), so the title always matches the cursor.
func (m *HistoryModel) SetPendingLabels(fromRev, toRev string) {
	m.diffFromRev = fromRev
	m.diffToRev = toRev
}

// ClearProjection drops every right-pane and revisions-list field on
// the model, leaving only the path and per-file UI state. Called by
// invalidateHistoryCache when the file's CVS state changed under us
// and the cached projection is stale; the caller is expected to
// re-dispatch loadHistory so the model gets repopulated.
func (m *HistoryModel) ClearProjection() {
	m.revisions = nil
	m.diffData = nil
	m.content = ""
	m.diffFromRev = ""
	m.diffToRev = ""
	m.rawView = ""
	m.hasWorkingRow = false
	m.workingMatchesBase = false
	m.baseRev = ""
}

// --- update / view ---------------------------------------------------------

func (m HistoryModel) Update(msg tea.Msg) (HistoryModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	rows := m.numRows()
	switch {
	case key.Matches(keyMsg, keys.Down):
		if m.cursor < rows-1 {
			m.cursor++
			m.ensureVisible()
		}
	case key.Matches(keyMsg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.ensureVisible()
		}
	case key.Matches(keyMsg, keys.Top):
		m.cursor = 0
		m.offset = 0
	case key.Matches(keyMsg, keys.Bottom):
		m.cursor = max(0, rows-1)
		m.ensureVisible()
	case key.Matches(keyMsg, keys.PageDown):
		// Each revision row spans 2 visual lines; a page is height/2 rows.
		page := m.height / 2
		if page < 1 {
			page = 1
		}
		m.cursor = clamp(m.cursor+page, 0, max(0, rows-1))
		m.ensureVisible()
	case key.Matches(keyMsg, keys.PageUp):
		page := m.height / 2
		if page < 1 {
			page = 1
		}
		m.cursor = clamp(m.cursor-page, 0, max(0, rows-1))
		m.ensureVisible()
	case key.Matches(keyMsg, keys.Diff):
		m.mode = HistoryDiff
	case key.Matches(keyMsg, keys.Blame):
		m.mode = HistoryBlame
	case key.Matches(keyMsg, keys.Enter):
		m.mode = HistoryContent
	case key.Matches(keyMsg, keys.SideBySide):
		if m.mode == HistoryDiff && m.diffData != nil {
			m.diffSBS = !m.diffSBS
			m.setView(m.renderDiffView())
		}
	case key.Matches(keyMsg, keys.Space):
		// Toggle the "from" anchor. Pressing space on the already-anchored
		// row clears it; otherwise pin the cursor row as the comparison
		// origin. Force Diff mode so the result is visible.
		if m.compareAnchor == m.cursor {
			m.compareAnchor = -1
		} else {
			m.compareAnchor = m.cursor
		}
		m.mode = HistoryDiff
	case key.Matches(keyMsg, keys.CompareWorking):
		// Toggle the "to" side between the cursor's revision and the
		// on-disk working copy.
		m.vsWorking = !m.vsWorking
		m.mode = HistoryDiff
	case key.Matches(keyMsg, keys.ScrollLeft):
		if m.hOffset > 0 {
			m.hOffset -= hScrollStep
			if m.hOffset < 0 {
				m.hOffset = 0
			}
			m.reapplyHOffset()
		}
	case key.Matches(keyMsg, keys.ScrollRight):
		m.hOffset += hScrollStep
		m.reapplyHOffset()
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *HistoryModel) ensureVisible() {
	// Each revision takes two rows (header + description), so the
	// visible-row count is half the panel height.
	m.offset = ensureCursorVisible(m.cursor, m.offset, m.height/2)
}

// SetSize takes the App's layout envelope. History fills both panels —
// revisions list on the left, diff/content viewport on the right — so it
// reads LeftW, RightW, and Height.
func (m *HistoryModel) SetSize(d PanelDims) {
	widthChanged := d.RightW != m.rightWidth
	m.leftWidth = d.LeftW
	m.rightWidth = d.RightW
	m.height = d.Height
	m.viewport.Width = d.RightW
	m.viewport.Height = d.Height
	// On a width change we have to re-render the right-pane content at
	// the new width: the cached rawView was computed for the old width
	// and stretching/squeezing it by changing the viewport bounds alone
	// produces wrapped or truncated lines (UTF-8 sequences cut mid-byte
	// surface as � replacement chars).
	if widthChanged {
		switch {
		case m.diffData != nil:
			m.setView(m.renderDiffView())
		case m.content != "":
			m.setView(m.content)
		}
	}
}

func (m *HistoryModel) resetViewport() {
	w := m.rightWidth
	if w <= 0 {
		w = 80
	}
	m.viewport = viewport.New(w, m.height)
}

func (m *HistoryModel) setView(s string) {
	m.rawView = s
	m.viewport.SetContent(applyHOffset(s, m.hOffset))
}

func (m *HistoryModel) reapplyHOffset() {
	m.viewport.SetContent(applyHOffset(m.rawView, m.hOffset))
}

func applyHOffset(s string, offset int) string {
	if offset <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = ansi.TruncateLeft(line, offset, "")
	}
	return strings.Join(lines, "\n")
}

func (m HistoryModel) renderDiffView() string {
	if m.diffData == nil || len(m.diffData.Hunks) == 0 {
		return mutedStyle.Render("  No differences")
	}
	if m.diffSBS {
		return renderSideBySideDiff(m.diffData, m.rightWidth)
	}
	return renderUnifiedDiff(m.diffData.Hunks)
}

// SelectedRevision returns the revision under the cursor, or nil if the
// cursor is on the pseudo working-copy row.
func (m HistoryModel) SelectedRevision() *cvs.Revision {
	idx := m.revisionIndex(m.cursor)
	if idx < 0 || idx >= len(m.revisions) {
		return nil
	}
	return &m.revisions[idx]
}

// Path returns the currently displayed file path (may be "").
func (m HistoryModel) Path() string { return m.path }

// NumRevisions returns the count of selectable rows in the left pane,
// including the pseudo working-copy row when the file is dirty. Used
// by callers to decide whether there's anything to auto-load.
func (m HistoryModel) NumRevisions() int { return m.numRows() }

// ViewLeft renders the revision list. The header (file name) is rendered
// by the panel frame in app_layout.go — this returns rows only.
func (m HistoryModel) ViewLeft() string {
	if m.path == "" {
		return mutedStyle.Render("  No file selected")
	}
	rows := m.numRows()
	if rows == 0 {
		return mutedStyle.Render("  Loading revisions...")
	}

	visibleCount := m.height / 2
	if visibleCount < 1 {
		visibleCount = 1
	}
	end := min(m.offset+visibleCount, rows)

	// Use a guaranteed-distinct prefix character per row: "▌" for the cursor
	// row, " " otherwise. This forces the differential renderer to repaint
	// the prefix cell when the cursor moves, which in turn flushes any
	// stale reverse-video styling on the row left over from a previous
	// frame.
	var lines []string
	for i := m.offset; i < end; i++ {
		isCursor := i == m.cursor
		isAnchor := i == m.compareAnchor
		// Marker glyph at column 0: cursor wins over anchor on the same
		// row (reverse-video makes the anchor's identity clear via the
		// "Compare —" label in the right pane title).
		marker := " "
		switch {
		case isCursor:
			marker = "▌"
		case isAnchor:
			marker = "▸"
		}
		var renderedMarker string
		if isAnchor && !isCursor {
			renderedMarker = lipgloss.NewStyle().Foreground(colorActive).Render(marker)
		} else {
			renderedMarker = marker
		}

		var line1, line2 string
		if m.hasWorkingRow && i == 0 {
			// Pseudo "working" row. Label is consistent with how the
			// title and the badge spell it elsewhere.
			line1 = fmt.Sprintf("%s%-6s %-8s %s",
				renderedMarker,
				lipgloss.NewStyle().Foreground(colorActive).Render("working"),
				"you",
				"now")
			line2 = renderedMarker + "  " + mutedStyle.Render("local changes")
		} else {
			rev := m.revisions[m.revisionIndex(i)]
			date := rev.Date.Format("Jan 02 06")

			line1 = fmt.Sprintf("%s%-6s %-8s %s", renderedMarker, rev.Number, truncate(rev.Author, 8), date)
			if len(rev.Tags) > 0 {
				line1 += " " + lipgloss.NewStyle().Foreground(colorStale).Render(truncate(rev.Tags[0], 12))
			}
			// Base-revision badge: when the file is clean, badge the
			// row whose Number matches the working revision. This is
			// sticky-tag / branch aware — for a checkout pinned to a
			// non-HEAD revision, the badge lands on the actual base,
			// not on the most recent log entry.
			if m.workingMatchesBase && m.baseRev != "" && rev.Number == m.baseRev {
				line1 += " " + lipgloss.NewStyle().Foreground(colorActive).Render("(working)")
			}

			msg := strings.SplitN(rev.Message, "\n", 2)[0]
			msg = truncate(msg, m.leftWidth-4)
			delta := ""
			if rev.LinesAdded > 0 || rev.LinesRemoved > 0 {
				delta = fmt.Sprintf(" %s%s",
					lipgloss.NewStyle().Foreground(colorUpdated).Render(fmt.Sprintf("+%d", rev.LinesAdded)),
					lipgloss.NewStyle().Foreground(colorConflict).Render(fmt.Sprintf("-%d", rev.LinesRemoved)))
			}
			line2 = renderedMarker + "  " + mutedStyle.Render(msg) + delta
		}

		if isCursor {
			sel := lipgloss.NewStyle().Reverse(true)
			plain1 := ansi.Strip(line1)
			plain2 := ansi.Strip(line2)
			if w := lipgloss.Width(plain1); w < m.leftWidth {
				plain1 += strings.Repeat(" ", m.leftWidth-w)
			}
			if w := lipgloss.Width(plain2); w < m.leftWidth {
				plain2 += strings.Repeat(" ", m.leftWidth-w)
			}
			line1 = sel.Render(plain1)
			line2 = sel.Render(plain2)
		}
		lines = append(lines, line1, line2)
	}

	// Always pad to exactly m.height lines so renderPanel never has to
	// extend or truncate. Inconsistent line counts between frames are
	// what allow stale rows to persist outside the panel frame.
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// ViewRight renders whatever is currently in the viewport (diff, content,
// blame), or a context-appropriate placeholder while data is in flight.
// "ready" is now a derived condition: data is present iff diffData or
// content is non-empty.
func (m HistoryModel) ViewRight() string {
	if m.path == "" {
		return ""
	}
	if m.diffData == nil && m.content == "" {
		switch m.mode {
		case HistoryDiff:
			return mutedStyle.Render("  Loading diff...")
		case HistoryBlame:
			return mutedStyle.Render("  Loading blame...")
		default:
			return mutedStyle.Render("  Loading...")
		}
	}
	return m.viewport.View()
}
