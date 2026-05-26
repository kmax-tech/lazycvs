package tui

import (
	"lazycvs/cvs"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DialogKind int

const (
	DialogNone DialogKind = iota
	DialogCommit
	DialogRevert
	DialogRemove
	DialogIgnore
	DialogConflict
	DialogHelp
	DialogForceUpdate
	DialogPreview
	DialogInTheWay
)

type DialogModel struct {
	kind     DialogKind
	path     string
	files    []string
	// commitStatuses maps path → status code (M/C/A/?/…) for the files in
	// the current commit dialog, so viewCommit can show real status instead
	// of hard-coding M for everything. Set by OpenCommit alongside files.
	commitStatuses map[string]string
	input    textinput.Model
	conflict *cvs.ConflictFile
	preview  viewport.Model
	width    int
	height   int
}

func NewDialogModel() DialogModel {
	ti := textinput.New()
	ti.Placeholder = "Commit message..."
	ti.CharLimit = 256
	return DialogModel{input: ti}
}

func (m *DialogModel) OpenCommit(files []string, statuses map[string]string) {
	m.kind = DialogCommit
	m.files = files
	m.commitStatuses = statuses
	m.input.Focus()
	m.input.SetValue("")
}

// OpenRemove opens a confirmation dialog for `cvs remove` on the given
// paths. statuses maps each path to its current CVS status code (M, A,
// ?, C, R, "") — used to phrase the dialog wording and decide the
// actual command in doRemove. Single-file and bulk-remove flows share
// this dialog: len(paths) == 1 renders the original per-file detail;
// len(paths) > 1 renders a checklist with status counts.
func (m *DialogModel) OpenRemove(paths []string, statuses map[string]string) {
	m.kind = DialogRemove
	m.files = paths
	m.commitStatuses = statuses
	if len(paths) > 0 {
		m.path = paths[0]
	}
}

func (m *DialogModel) OpenRevert(path string) {
	m.kind = DialogRevert
	m.path = path
}

func (m *DialogModel) OpenIgnore(path string) {
	m.kind = DialogIgnore
	m.path = path
}

func (m *DialogModel) OpenConflict(path string, conflict *cvs.ConflictFile) {
	m.kind = DialogConflict
	m.path = path
	m.conflict = conflict
}

func (m *DialogModel) OpenHelp() {
	m.kind = DialogHelp
	w := min(m.width-6, 78)
	h := min(m.height-8, 38)
	m.preview = viewport.New(w, h)
	m.preview.SetContent(helpContent())
}

func (m *DialogModel) OpenForceUpdate(paths []string) {
	m.kind = DialogForceUpdate
	m.files = paths
}

// OpenInTheWay opens the resolution dialog for paths CVS reported as
// "move away" — local files that blocked the server's version from
// being pulled. The user picks one action that applies to all paths
// (move-aside / delete / keep) or edits the first one in $EDITOR.
func (m *DialogModel) OpenInTheWay(paths []string) {
	m.kind = DialogInTheWay
	m.files = paths
	if len(paths) > 0 {
		m.path = paths[0]
	}
}

func (m *DialogModel) OpenPreview(path, content string) {
	m.kind = DialogPreview
	m.path = path
	w := min(m.width-6, 80)
	h := min(m.height-10, 30)
	m.preview = viewport.New(w, h)
	m.preview.SetContent(content)
}

func (m *DialogModel) Close() {
	m.kind = DialogNone
	m.input.Blur()
}

// SetSize takes the App's layout envelope. Dialogs are overlays sized to
// the full terminal, so the App passes the screen size via LeftW and
// Height; RightW is unused.
func (m *DialogModel) SetSize(d PanelDims) {
	m.width = d.LeftW
	m.height = d.Height
}

func (m DialogModel) Active() bool {
	return m.kind != DialogNone
}

// Messages produced by dialogs
type commitMsg struct {
	message string
	// untracked are paths that need a `cvs add` before the commit (i.e.,
	// files that were marked while still in `?` status). Empty for the
	// common case where everything is already tracked.
	untracked []string
	// files are all paths to include in the commit (= untracked + the
	// already-tracked A/M/C files). After cvs-add the untracked ones
	// transition to A and become commit-able in the same operation.
	files []string
}
type revertMsg struct{ path string }

// removeMsg carries the user's confirmation from the Remove dialog. paths
// is non-empty (single or bulk); statuses[path] is the CVS status code
// each file had at dialog-open time so doRemove can pick the right cvs
// command per file.
type removeMsg struct {
	paths    []string
	statuses map[string]string
}

// removeDoneMsg reports the outcome of doRemove. The handler in App.Update
// turns this into a banner notification, drops the paths from marked on
// success, and dispatches a follow-up status refresh. err is the first
// error encountered, or nil on success.
type removeDoneMsg struct {
	paths []string
	err   error
}
type ignoreMsg struct {
	path   string
	choice int // 1=local, 2=global pattern, 3=global exact
}
type conflictResolveMsg struct {
	path   string
	choice string // "local" or "server"
}
type forceUpdateMsg struct {
	paths []string
}

// inTheWayChoice is the action the user picked in the "move away"
// resolution dialog.
type inTheWayChoice int

const (
	inTheWayKeep      inTheWayChoice = iota // do nothing — keep local files, server versions stay out
	inTheWayMoveAside                       // rename each local file to "<name>.moved-by-lazycvs", then re-update
	inTheWayDelete                          // os.Remove each local file, then re-update
)

// inTheWayResolveMsg carries the user's chosen action for the
// blocking paths. The handler in app_core.go performs the disk
// operations and re-runs cvs update on the now-unblocked paths.
type inTheWayResolveMsg struct {
	paths  []string
	choice inTheWayChoice
}

type editorClosedMsg struct {
	path string
	err  error
}

// commitMessageEditedMsg is dispatched by openCommitEditor after $EDITOR
// closes. message is the user's text with comment lines stripped (empty
// means "abort the commit"); err is non-nil if the editor invocation
// itself failed.
type commitMessageEditedMsg struct {
	message string
	err     error
}

// ensureParentDirs adds any ancestor directories of relPath that are not yet
// known to CVS (i.e. missing a CVS/ subdirectory). Directories are added
// top-down so that `cvs add <file>` inside a new directory tree succeeds.
// Returns the first cvs failure encountered — non-nil means later file adds
// under that ancestor will likely fail too, and the caller can short-circuit
// or surface the error instead of letting the operation silently degrade.
func ensureParentDirs(exec *cvs.CVSExecutor, relPath string) error {
	dir := filepath.Dir(relPath)
	if dir == "." || dir == "" {
		return nil
	}
	// Collect ancestors that need adding (bottom-up), then add top-down.
	var missing []string
	for d := dir; d != "." && d != ""; d = filepath.Dir(d) {
		cvsDir := filepath.Join(exec.WorkDir, d, "CVS")
		if info, err := os.Stat(cvsDir); err == nil && info.IsDir() {
			break
		}
		missing = append(missing, d)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		r, err := exec.Run("add", missing[i])
		if err != nil {
			return err
		}
		if r != nil && !r.Success {
			return fmt.Errorf("cvs add %s exited %d", missing[i], r.ExitCode)
		}
	}
	return nil
}

// doCommit runs `cvs add` for any untracked paths first (one command per
// file so each shows in the console), then a single `cvs commit -m msg` for
// all files. After cvs-add the untracked files are in A status and the
// single commit picks them up alongside the existing A/M/C/R entries.
//
// Returns commitDoneMsg with the first error encountered (if any). The
// commitDoneMsg handler in App.Update is responsible for the banner, the
// marked-set cleanup, and the follow-up status refresh.
func doCommit(exec *cvs.CVSExecutor, message string, untracked, files []string) tea.Cmd {
	return func() tea.Msg {
		var firstErr error
		for _, p := range untracked {
			if err := ensureParentDirs(exec, p); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue // skip the add; cvs would fail with a confusing
				// "no such directory" error anyway, and the captured
				// firstErr will surface the actual root cause.
			}
			r, err := exec.Run("add", p)
			if firstErr == nil && err != nil && (r == nil || !r.Success) {
				firstErr = err
			}
		}
		args := []string{"commit", "-m", message}
		args = append(args, files...)
		r, err := exec.Run(args...)
		if firstErr == nil && err != nil && (r == nil || !r.Success) {
			firstErr = err
		}
		return commitDoneMsg{files: files, message: message, err: firstErr}
	}
}

func doRevert(exec *cvs.CVSExecutor, path string) tea.Cmd {
	return func() tea.Msg {
		// Create backup
		data, _ := os.ReadFile(path)
		if data != nil {
			os.WriteFile(path+".lazycvs-backup", data, 0644)
		}
		r, err := exec.Run("update", "-C", path)
		if err == nil && r != nil && !r.Success {
			err = fmt.Errorf("cvs update -C %s exited %d", path, r.ExitCode)
		}
		return actionDoneMsg{paths: []string{path}, err: err}
	}
}

// doRemove deletes each path from the working copy and (when applicable)
// tells CVS to schedule it for removal at the next commit. Behavior per
// path is decided from the status passed in `statuses`:
//   - "?"            : just delete from disk (CVS doesn't know about it)
//   - "A"            : `cvs remove -f` un-schedules the add
//   - "" / M / C / U : `cvs remove -f` deletes the file AND schedules removal,
//                      so the file ends up in R status until next commit
//   - "R"            : already scheduled — no-op
//
// Returns removeDoneMsg with the first error encountered (or nil on success).
// The handler in App.Update is responsible for the banner, the marked-set
// cleanup, and the follow-up status refresh. Single-file removal is just
// the len(paths)==1 case.
func doRemove(executor *cvs.CVSExecutor, paths []string, statuses map[string]string) tea.Cmd {
	return func() tea.Msg {
		var firstErr error
		for _, path := range paths {
			var err error
			switch statuses[path] {
			case "R":
				// already removed — nothing to do
			case "?":
				// untracked — just delete the file
				abs := filepath.Join(executor.WorkDir, path)
				err = os.Remove(abs)
			default:
				// `cvs remove -f` deletes the working file AND schedules
				// removal (or un-adds if the file was in `A` status).
				r, runErr := executor.Run("remove", "-f", path)
				if runErr != nil && (r == nil || !r.Success) {
					err = runErr
				}
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return removeDoneMsg{paths: paths, err: firstErr}
	}
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorClosedMsg{path: path, err: err}
	})
}

// openCommitEditor creates a temp file pre-filled with currentMsg plus
// a comment header listing the files to commit, suspends to $EDITOR
// so the user can write a multi-line message, then reads the file
// back and emits commitMessageEditedMsg with the comment lines stripped.
//
// The dispatch happens in tea.ExecProcess's callback so the TUI is
// suspended during the edit and restored cleanly afterward — same
// pattern the e key uses for editing files.
func openCommitEditor(currentMsg string, files []string, statuses map[string]string) tea.Cmd {
	tmp, err := os.CreateTemp("", "lazycvs-commit-*.txt")
	if err != nil {
		return func() tea.Msg { return commitMessageEditedMsg{err: err} }
	}
	if currentMsg != "" {
		fmt.Fprintln(tmp, currentMsg)
	} else {
		fmt.Fprintln(tmp, "")
	}
	fmt.Fprintln(tmp, "")
	fmt.Fprintln(tmp, "# Commit message for lazycvs")
	fmt.Fprintln(tmp, "# Lines starting with # are ignored.")
	fmt.Fprintln(tmp, "# Save and quit to commit; leave empty to abort.")
	fmt.Fprintln(tmp, "#")
	fmt.Fprintln(tmp, "# Files:")
	for _, f := range files {
		st := statuses[f]
		if st == "" {
			st = " "
		}
		fmt.Fprintf(tmp, "#   %s  %s\n", st, f)
	}
	path := tmp.Name()
	tmp.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return commitMessageEditedMsg{err: err}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return commitMessageEditedMsg{err: readErr}
		}
		return commitMessageEditedMsg{message: stripCommentLines(string(data))}
	})
}

// stripCommentLines removes lines starting with '#' from s and trims
// surrounding whitespace, matching the convention used by git commit
// messages.
func stripCommentLines(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "#") {
			keep = append(keep, line)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

func (m DialogModel) Update(msg tea.Msg) (DialogModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.kind {
		case DialogCommit:
			return m.updateCommit(msg)
		case DialogRevert:
			return m.updateRevert(msg)
		case DialogRemove:
			return m.updateRemove(msg)
		case DialogIgnore:
			return m.updateIgnore(msg)
		case DialogConflict:
			return m.updateConflict(msg)
		case DialogForceUpdate:
			return m.updateForceUpdate(msg)
		case DialogInTheWay:
			return m.updateInTheWay(msg)
		case DialogPreview:
			if key.Matches(msg, keys.Escape) || msg.String() == "q" || msg.String() == "p" {
				m.Close()
				return m, nil
			}
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(msg)
			return m, cmd
		case DialogHelp:
			if key.Matches(msg, keys.Escape) || msg.String() == "?" || msg.String() == "q" {
				m.Close()
				return m, nil
			}
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(msg)
			return m, cmd
		}
	}

	// Update text input if in commit mode
	if m.kind == DialogCommit {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m DialogModel) updateCommit(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Escape):
		m.Close()
		return m, nil
	case key.Matches(msg, keys.Enter):
		message := m.input.Value()
		// Universal commit: include untracked (?), added (A), modified (M),
		// conflict (C), removed (R). The handler runs `cvs add` for ?
		// files first, then a single `cvs commit` for everything.
		// Mirrors isCommittable in staging.go.
		var untracked, files []string
		for _, f := range m.files {
			s := m.commitStatuses[f]
			switch s {
			case "?":
				untracked = append(untracked, f)
				files = append(files, f)
			case "A", "M", "C", "R":
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			return m, nil // nothing committable; keep dialog open
		}
		m.Close()
		return m, func() tea.Msg {
			return commitMsg{message: message, untracked: untracked, files: files}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m DialogModel) updateRevert(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Yes):
		path := m.path
		m.Close()
		return m, func() tea.Msg {
			return revertMsg{path: path}
		}
	case key.Matches(msg, keys.No), key.Matches(msg, keys.Escape):
		m.Close()
		return m, nil
	}
	return m, nil
}

func (m DialogModel) updateRemove(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Yes):
		paths := m.files
		statuses := m.commitStatuses
		m.Close()
		return m, func() tea.Msg {
			return removeMsg{paths: paths, statuses: statuses}
		}
	case key.Matches(msg, keys.No), key.Matches(msg, keys.Escape):
		m.Close()
		return m, nil
	}
	return m, nil
}

func (m DialogModel) updateIgnore(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch msg.String() {
	case "1", "2", "3":
		choice := int(msg.String()[0] - '0')
		path := m.path
		m.Close()
		return m, func() tea.Msg {
			return ignoreMsg{path: path, choice: choice}
		}
	case "esc":
		m.Close()
		return m, nil
	}
	return m, nil
}

func (m DialogModel) updateForceUpdate(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Yes):
		paths := m.files
		m.Close()
		return m, func() tea.Msg {
			return forceUpdateMsg{paths: paths}
		}
	case key.Matches(msg, keys.No), key.Matches(msg, keys.Escape):
		m.Close()
		return m, nil
	}
	return m, nil
}

// updateInTheWay handles the "move away" resolution dialog. The chosen
// action applies to every blocking path; per-file editing isn't
// supported (rare enough that the user can drop to the shell).
func (m DialogModel) updateInTheWay(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch msg.String() {
	case "m":
		paths := m.files
		m.Close()
		return m, func() tea.Msg {
			return inTheWayResolveMsg{paths: paths, choice: inTheWayMoveAside}
		}
	case "d":
		paths := m.files
		m.Close()
		return m, func() tea.Msg {
			return inTheWayResolveMsg{paths: paths, choice: inTheWayDelete}
		}
	case "k":
		paths := m.files
		m.Close()
		return m, func() tea.Msg {
			return inTheWayResolveMsg{paths: paths, choice: inTheWayKeep}
		}
	}
	if key.Matches(msg, keys.Escape) {
		paths := m.files
		m.Close()
		return m, func() tea.Msg {
			return inTheWayResolveMsg{paths: paths, choice: inTheWayKeep}
		}
	}
	return m, nil
}

func (m DialogModel) updateConflict(msg tea.KeyMsg) (DialogModel, tea.Cmd) {
	switch msg.String() {
	case "l":
		path := m.path
		m.Close()
		return m, func() tea.Msg {
			return conflictResolveMsg{path: path, choice: "local"}
		}
	case "s":
		path := m.path
		m.Close()
		return m, func() tea.Msg {
			return conflictResolveMsg{path: path, choice: "server"}
		}
	case "e":
		path := m.path
		m.Close()
		return m, openEditor(path)
	case "esc":
		m.Close()
		return m, nil
	}
	return m, nil
}

func (m DialogModel) View() string {
	if m.kind == DialogNone {
		return ""
	}

	boxWidth := min(m.width-4, 55)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorActive).
		Padding(1, 2).
		Width(boxWidth)

	var content string
	switch m.kind {
	case DialogCommit:
		content = m.viewCommit()
	case DialogRevert:
		content = m.viewRevert()
	case DialogRemove:
		content = m.viewRemove()
	case DialogIgnore:
		content = m.viewIgnore()
	case DialogConflict:
		content = m.viewConflict()
	case DialogForceUpdate:
		content = m.viewForceUpdate()
	case DialogInTheWay:
		content = m.viewInTheWay()
	case DialogPreview:
		content = m.viewPreview()
	case DialogHelp:
		content = m.viewHelp()
	}

	return boxStyle.Render(content)
}

func (m DialogModel) viewCommit() string {
	var b strings.Builder
	// Split marked files by committability so the dialog body can
	// focus on what will actually be sent to `cvs commit` and report
	// the skipped count separately. Same predicate as staging.isCommittable.
	var committable, skipped []string
	for _, f := range m.files {
		if isCommittable(m.commitStatuses[f]) {
			committable = append(committable, f)
		} else {
			skipped = append(skipped, f)
		}
	}

	fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(fmt.Sprintf("Commit (%d of %d)", len(committable), len(m.files))))

	if len(committable) == 0 {
		fmt.Fprintf(&b, "No committable files among %d marked.\n", len(m.files))
		b.WriteString(mutedStyle.Render("All marked files are clean or up-to-date —\nthey may already be committed.\n\n"))
		// `s` is the global status-refresh key but the dialog's text
		// input swallows it; only esc actually works from this state.
		b.WriteString(helpStyle.Render("esc: close   (then press s to refresh status)"))
		return b.String()
	}

	// header (3) + skipped line (2) + message label (2) + input (1) +
	// blank (1) + help (1) + box border/padding (4). Anything left
	// over is the file-list budget; cap below that to keep a hint
	// of the message input visible even on very small terminals.
	const commitDialogChromeLines = 14
	maxFiles := m.height - commitDialogChromeLines
	if maxFiles < 4 {
		maxFiles = 4
	}

	b.WriteString("Files:\n")
	visible := committable
	truncated := 0
	if len(committable) > maxFiles {
		visible = committable[:maxFiles-1]
		truncated = len(committable) - len(visible)
	}
	for _, f := range visible {
		s := m.commitStatuses[f]
		label := lipgloss.NewStyle().Width(2).Foreground(statusColor(s)).Render(s)
		line := "  " + label + "  " + f
		if s == "?" {
			line += mutedStyle.Render("  (will be added first)")
		}
		b.WriteString(line + "\n")
	}
	if truncated > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  … %d more committable file(s)\n", truncated)))
	}
	if len(skipped) > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("\nSkipped: %d file(s) — clean or up-to-date\n", len(skipped))))
	}
	b.WriteString("\nMessage:\n")
	b.WriteString(m.input.View() + "\n\n")
	b.WriteString(helpStyle.Render("enter:commit  esc:cancel"))
	return b.String()
}

func (m DialogModel) viewRevert() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Revert") + "\n\n")
	fmt.Fprintf(&b, "Revert %s?\n\n", m.path)
	b.WriteString("Local changes will be overwritten.\n")
	fmt.Fprintf(&b, "Backup: %s.lazycvs-backup\n\n", m.path)
	b.WriteString(helpStyle.Render("y:revert  n:cancel"))
	return b.String()
}

func (m DialogModel) viewRemove() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Remove") + "\n\n")

	// Single-file detail view: keep the existing wording so the most
	// common case stays unchanged.
	if len(m.files) <= 1 {
		path := m.path
		status := ""
		if len(m.files) == 1 {
			status = m.commitStatuses[path]
		}
		fmt.Fprintf(&b, "Remove %s?\n\n", path)
		switch status {
		case "?":
			b.WriteString("Untracked file — will be deleted from disk only.\n\n")
		case "A":
			b.WriteString("Added but not committed — un-schedules the add\n")
			b.WriteString("and deletes the file from disk.\n\n")
		case "R":
			b.WriteString("Already scheduled for removal — nothing to do.\n\n")
		default:
			b.WriteString("File will be deleted from disk and scheduled\n")
			b.WriteString("for removal in CVS (status → R until next commit).\n\n")
		}
		b.WriteString(helpStyle.Render("y:remove  n:cancel"))
		return b.String()
	}

	// Bulk view: list each file with its status code so the user can
	// review exactly what will happen before confirming.
	fmt.Fprintf(&b, "Remove %d files?\n\n", len(m.files))
	for _, p := range m.files {
		st := m.commitStatuses[p]
		marker := " "
		if st != "" {
			marker = st
		}
		fmt.Fprintf(&b, "  %s  %s\n", marker, p)
	}
	b.WriteString("\nUntracked (?) → deleted from disk only.\n")
	b.WriteString("Added (A)     → un-scheduled, deleted from disk.\n")
	b.WriteString("Tracked / M / C / U → deleted, scheduled for removal\n")
	b.WriteString("                      (status → R until next commit).\n\n")
	b.WriteString(helpStyle.Render("y:remove  n:cancel"))
	return b.String()
}

func (m DialogModel) viewIgnore() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Ignore") + "\n\n")
	fmt.Fprintf(&b, "Ignore %s:\n\n", m.path)
	b.WriteString(" [1] This file only (.cvsignore local)\n")
	b.WriteString(" [2] Pattern *" + ext(m.path) + " (global ~/.cvsignore)\n")
	b.WriteString(" [3] \"" + base(m.path) + "\" everywhere (global)\n\n")
	b.WriteString(helpStyle.Render("1-3:choose  esc:cancel"))
	return b.String()
}

func (m DialogModel) viewConflict() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Conflict — "+m.path) + "\n\n")

	if m.conflict != nil && len(m.conflict.Regions) > 0 {
		r := m.conflict.Regions[0]
		b.WriteString(fmt.Sprintf("Region %d of %d:\n\n", 1, len(m.conflict.Regions)))
		b.WriteString(titleStyle.Render("LOCAL (yours):") + "\n")
		for _, line := range r.LocalLines {
			b.WriteString("  " + diffAdd.Render(line) + "\n")
		}
		b.WriteString("\n" + titleStyle.Render("SERVER (theirs):") + "\n")
		for _, line := range r.ServerLines {
			b.WriteString("  " + diffDel.Render(line) + "\n")
		}
	}

	b.WriteString("\n" + helpStyle.Render("l:keep local  s:keep server  e:edit  esc:cancel"))
	return b.String()
}

func (m DialogModel) viewPreview() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Preview — "+m.path) + "\n\n")
	b.WriteString(m.preview.View())
	b.WriteString("\n\n" + helpStyle.Render("j/k:scroll  esc:close"))
	return b.String()
}

func (m DialogModel) viewForceUpdate() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Force Update") + "\n\n")
	b.WriteString("Overwrite local changes with server version?\n\n")
	for _, f := range m.files {
		b.WriteString("  " + f + "\n")
	}
	b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorConflict).Render("Local changes will be lost!") + "\n\n")
	b.WriteString(helpStyle.Render("y:confirm  n:cancel"))
	return b.String()
}

func (m DialogModel) viewInTheWay() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Files in the way") + "\n\n")
	if len(m.files) == 1 {
		fmt.Fprintf(&b, "%s exists locally and blocks the server version\n", m.files[0])
		b.WriteString("from being pulled.\n\n")
	} else {
		fmt.Fprintf(&b, "%d local files block the server versions:\n\n", len(m.files))
		for _, f := range m.files {
			b.WriteString("  " + f + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Pick an action — applies to all listed files:\n\n")
	b.WriteString("  [m] Move aside  → rename to <name>.moved-by-lazycvs,\n")
	b.WriteString("                    then re-update so the server version comes down\n")
	b.WriteString("  [d] Delete      → remove the local file, then re-update\n")
	b.WriteString("  [k] Keep        → leave local files alone, server version stays out\n\n")
	b.WriteString(helpStyle.Render("m/d/k:choose  esc:keep (cancel)"))
	return b.String()
}

func (m DialogModel) viewHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Keybindings") + "\n\n")
	b.WriteString(m.preview.View())

	pct := m.preview.ScrollPercent()
	scroll := helpStyle.Render(fmt.Sprintf("  ↑/↓:scroll  %.0f%%", pct*100))
	b.WriteString("\n" + scroll + "  " + helpStyle.Render("esc:close"))
	return b.String()
}

// helpRow formats one keybinding row as "<keys>  <description>" with a
// fixed-width key column so descriptions line up across rows. Keeps the
// help screen scannable instead of zig-zagging.
func helpRow(k, desc string) string {
	return "  " + keyStyle.Render(fmt.Sprintf("%-10s", k)) + "  " + desc
}

func helpSection(title string) string {
	return "\n" + titleStyle.Render(title) + "\n" +
		mutedStyle.Render(strings.Repeat("─", len(title))) + "\n"
}

func helpContent() string {
	var b strings.Builder

	// ── Convention banner ─────────────────────────────────────────
	b.WriteString(mutedStyle.Render(
		"Convention: lowercase acts on the file under the cursor;\n" +
			"UPPERCASE extends to the dir / every marked file.\n"))

	// ── Navigation ────────────────────────────────────────────────
	b.WriteString(helpSection("Navigation"))
	b.WriteString(helpRow("j / k", "down / up") + "\n")
	b.WriteString(helpRow("h / l", "collapse / expand (tree)") + "\n")
	b.WriteString(helpRow("g / G", "top / bottom") + "\n")
	b.WriteString(helpRow("PgUp/PgDn", "scroll page (also C-u / C-d)") + "\n")
	b.WriteString(helpRow("tab", "cycle focused panel") + "\n")
	b.WriteString(helpRow("[ / ]", "left / right panel") + "\n")
	b.WriteString(helpRow("C-j", "console panel") + "\n")
	b.WriteString(helpRow("/", "fuzzy search files") + "\n")

	// ── Tabs ──────────────────────────────────────────────────────
	b.WriteString(helpSection("Tabs"))
	b.WriteString(helpRow("1", "Files — tree+filelist OR tree+diff preview") + "\n")
	b.WriteString(helpRow("2", "Favorites — pinned directories") + "\n")
	b.WriteString(helpRow("3", "Staged — files marked for commit") + "\n")
	b.WriteString(helpRow("4", "History — revisions of the selected file") + "\n")

	// ── File actions ──────────────────────────────────────────────
	b.WriteString(helpSection("File actions"))
	b.WriteString(helpRow("d", "diff (working vs base revision)") + "\n")
	b.WriteString(helpRow("c", "commit cursor file (or marked files)") + "\n")
	b.WriteString(helpRow("a", "add cursor file (?-status) to CVS") + "\n")
	b.WriteString(helpRow("r", "revert cursor file") + "\n")
	b.WriteString(helpRow("D", "remove (single file or marked set)") + "\n")
	b.WriteString(helpRow("i", "ignore — add pattern to .cvsignore") + "\n")
	b.WriteString(helpRow("e", "edit in $EDITOR") + "\n")
	b.WriteString(helpRow("E", "external diff tool (config.diff_tool)") + "\n")
	b.WriteString(helpRow("M", "external merge tool (C-status only)") + "\n")
	b.WriteString(helpRow("o", "open in OS default app") + "\n")
	b.WriteString(helpRow("space", "mark/unmark") + "\n")
	b.WriteString(helpRow("A", "mark/unmark every changed file in dir") + "\n")
	b.WriteString(helpRow("+ / -", "add/remove from favorites") + "\n")

	// ── View modes & filters ──────────────────────────────────────
	b.WriteString(helpSection("View & filters"))
	b.WriteString(helpRow("v", "layout: Files (tree+list) ↔ Detail (tree+preview)") + "\n")
	b.WriteString(helpRow("f", "list mode: flat → sub → tree") + "\n")
	b.WriteString(helpRow("F", "status filter: all → * → M → C → ? → all") + "\n")
	b.WriteString(helpRow("I", "toggle hide-ignored on both panes") + "\n")
	b.WriteString(helpRow("t", "jump list to tree mode") + "\n")

	// ── CVS ───────────────────────────────────────────────────────
	b.WriteString(helpSection("CVS"))
	b.WriteString(helpRow("s", "status refresh (dry-run + scan, parallel)") + "\n")
	b.WriteString(helpRow("u", "update (cvs update -d -P)") + "\n")
	b.WriteString(helpRow("U", "force update (cvs update -C — overwrite!)") + "\n")

	// ── History ───────────────────────────────────────────────────
	b.WriteString(helpSection("History tab"))
	b.WriteString(helpRow("enter", "show revision content") + "\n")
	b.WriteString(helpRow("d", "show diff vs previous revision") + "\n")
	b.WriteString(helpRow("p", "toggle side-by-side diff") + "\n")
	b.WriteString(helpRow("b", "blame view") + "\n")
	b.WriteString(helpRow("w", "compare vs working copy") + "\n")
	b.WriteString(helpRow("space", "anchor revision for two-rev comparison") + "\n")
	b.WriteString(helpRow("< / >", "horizontal scroll in diff content") + "\n")
	b.WriteString(helpRow("esc", "cancel comparison mode") + "\n")
	b.WriteString("\n  " + mutedStyle.Render(
		"Top row is the working copy when modified — diff there shows\n"+
			"local changes vs repository.") + "\n")

	// ── Console ───────────────────────────────────────────────────
	b.WriteString(helpSection("Console"))
	b.WriteString(helpRow("F", "cycle filter: compact → verbose → errors → slow") + "\n")
	b.WriteString(helpRow("y", "copy selected entry") + "\n")
	b.WriteString(helpRow("x", "clear log") + "\n")

	// ── Status codes ──────────────────────────────────────────────
	b.WriteString(helpSection("Status codes"))
	color := func(c lipgloss.Color, code, desc string) string {
		return "  " + lipgloss.NewStyle().Foreground(c).Render(code) + "   " + desc + "\n"
	}
	b.WriteString(color(colorMod, "M", "Locally Modified — you changed this file"))
	b.WriteString(color(colorUpdated, "U", "Update available — newer revision on server"))
	b.WriteString(color(colorConflict, "C", "Conflict — both you and server changed it"))
	b.WriteString(color(colorUntracked, "?", "Untracked — not in CVS"))
	b.WriteString(color(colorIgnored, "I", "Ignored — matched by .cvsignore"))
	b.WriteString(color(colorUpdated, "A", "Added — scheduled for commit"))
	b.WriteString(color(colorConflict, "R", "Removed — scheduled for deletion"))

	return b.String()
}

func ext(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' {
			break
		}
	}
	return ""
}

func base(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
