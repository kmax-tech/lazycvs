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

// OpenRemove opens a confirmation dialog for `cvs remove` on path. status is
// the file's current status code (M, A, ?, C, R, "") — used to phrase the
// dialog wording and decide the actual command in doRemove.
func (m *DialogModel) OpenRemove(path, status string) {
	m.kind = DialogRemove
	m.path = path
	// Stash status in files[0] — we don't have a cleaner slot and adding a
	// dedicated field for this single dialog isn't worth it.
	m.files = []string{status}
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
}

func (m *DialogModel) OpenForceUpdate(paths []string) {
	m.kind = DialogForceUpdate
	m.files = paths
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

func (m *DialogModel) SetSize(width, height int) {
	m.width = width
	m.height = height
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
type removeMsg struct{ path, status string }

// removeDoneMsg reports the outcome of doRemove. The handler in App.Update
// turns this into a banner notification, drops the path from marked on
// success, and applies an optimistic status update so other tabs see the
// new state immediately. oldStatus is the file's status before the remove
// (used to decide the optimistic next status).
type removeDoneMsg struct {
	path      string
	oldStatus string
	err       error
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
type editorClosedMsg struct {
	path string
	err  error
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
		exec.Run("update", "-C", path)
		result, _ := exec.DryRunUpdate()
		return statusRefreshedMsg{result: result}
	}
}

// doRemove deletes a file from the working copy and (when applicable) tells
// CVS to schedule it for removal at the next commit. Behavior by status:
//   - "?"            : just delete from disk (CVS doesn't know about it)
//   - "A"            : `cvs remove -f` un-schedules the add
//   - "" / M / C / U : `cvs remove -f` deletes the file AND schedules removal,
//                      so the file ends up in R status until next commit
//   - "R"            : already scheduled — no-op
//
// Returns removeDoneMsg with the first error encountered (or nil on success).
// The handler in App.Update is responsible for the banner, the marked-set
// cleanup, and the follow-up status refresh.
func doRemove(executor *cvs.CVSExecutor, path, status string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch status {
		case "R":
			// already removed — nothing to do
		case "?":
			// untracked — just delete the file
			abs := filepath.Join(executor.WorkDir, path)
			err = os.Remove(abs)
		default:
			// `cvs remove -f` deletes the working file AND schedules removal
			// (or un-adds if the file was in `A` status).
			r, runErr := executor.Run("remove", "-f", path)
			if runErr != nil && (r == nil || !r.Success) {
				err = runErr
			}
		}
		return removeDoneMsg{path: path, oldStatus: status, err: err}
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
			}
			return m, nil
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
		if message == "" {
			return m, nil
		}
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
		path := m.path
		status := ""
		if len(m.files) > 0 {
			status = m.files[0]
		}
		m.Close()
		return m, func() tea.Msg {
			return removeMsg{path: path, status: status}
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
	case DialogPreview:
		content = m.viewPreview()
	case DialogHelp:
		content = m.viewHelp()
	}

	return boxStyle.Render(content)
}

func (m DialogModel) viewCommit() string {
	var b strings.Builder
	// Count what's actually committable (?, A, M, C, R — same as
	// staging.isCommittable) vs skipped (clean / U).
	var committable, skipped int
	for _, f := range m.files {
		if isCommittable(m.commitStatuses[f]) {
			committable++
		} else {
			skipped++
		}
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "%s\n\n", titleStyle.Render(fmt.Sprintf("Commit (%d of %d)", committable, len(m.files))))
	} else {
		b.WriteString(titleStyle.Render("Commit") + "\n\n")
	}
	b.WriteString("Files:\n")
	for _, f := range m.files {
		s := m.commitStatuses[f]
		label := "  "
		if s != "" {
			label = lipgloss.NewStyle().Width(2).Foreground(statusColor(s)).Render(s)
		}
		line := "  " + label + "  " + f
		if isCommittable(s) {
			if s == "?" {
				line += mutedStyle.Render("  (will be added first)")
			}
		} else {
			hint := "(unchanged)"
			if s == "U" {
				hint = "(needs update — skip)"
			}
			line = mutedStyle.Render(line + "  " + hint)
		}
		b.WriteString(line + "\n")
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
	status := ""
	if len(m.files) > 0 {
		status = m.files[0]
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Remove") + "\n\n")
	fmt.Fprintf(&b, "Remove %s?\n\n", m.path)
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

func (m DialogModel) viewHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Keybindings") + "\n\n")

	b.WriteString(titleStyle.Render("Global") + "\n")
	b.WriteString(keyHelp(keys.Tab1, keys.Tab2, keys.Tab3, keys.Tab4) + "\n")
	b.WriteString("  " + keyStyle.Render("<left>/<right>") + ":switch columns  " +
		keyHelp(keys.FocusC) + "\n")
	b.WriteString(keyHelp(keys.Update, keys.Search, keys.Help, keys.Quit) + "\n\n")

	b.WriteString(titleStyle.Render("File Actions") + "\n")
	b.WriteString(keyHelp(keys.Diff, keys.Commit, keys.Revert, keys.Remove) + "\n")
	b.WriteString(keyHelp(keys.Edit, keys.EditDiff, keys.Merge, keys.Open, keys.Add, keys.Ignore) + "\n")
	b.WriteString("  " + keyStyle.Render("E") + " uses [editor] diff_tool, " +
		keyStyle.Render("M") + " uses merge_tool (conflict files only)\n")
	b.WriteString(keyHelp(keys.ViewMode, keys.Space, keys.Filter) + "  " +
		keyStyle.Render("f") + ":flat  " +
		keyStyle.Render("s") + ":sub  " +
		keyStyle.Render("t") + ":tree\n\n")

	b.WriteString(titleStyle.Render("Navigation") + "\n")
	b.WriteString(keyHelp(keys.Up, keys.Down, keys.Left, keys.Right) + "\n")
	b.WriteString(keyHelp(keys.Top, keys.Bottom, keys.Enter) + "\n\n")

	b.WriteString(titleStyle.Render("Tabs") + "\n")
	b.WriteString("  " + keyStyle.Render("[1] Files/Detail") +
		" — " + keyStyle.Render("v") + " toggles view mode\n")
	b.WriteString("      Files: dirs left, files right\n")
	b.WriteString("      Detail: full tree left, diff preview right\n")
	b.WriteString("  " + keyStyle.Render("[2] Favorites") +
		" — pinned directories\n")
	b.WriteString("  " + keyStyle.Render("[3] Staged") +
		" — files marked for commit\n")
	b.WriteString("  " + keyStyle.Render("[4] History") +
		" — revisions + working copy of a file\n")
	b.WriteString("      Top row is the working copy when modified;\n")
	b.WriteString("      diff there = local changes vs repository.\n")
	b.WriteString("      " + keyStyle.Render("enter") + ":content  " +
		keyStyle.Render("d") + ":diff  " +
		keyStyle.Render("b") + ":blame\n")
	b.WriteString("      " + keyStyle.Render("space") + ":compare two rows  " +
		keyStyle.Render("w") + ":compare vs working\n")
	b.WriteString("      " + keyStyle.Render("s") + ":side-by-side  " +
		keyStyle.Render("esc") + ":cancel compare\n\n")

	b.WriteString(titleStyle.Render("Console") + "\n")
	b.WriteString("  Default view is compact (one line per command, errors red).\n")
	b.WriteString("  " + keyStyle.Render("F") + ":cycle filter (compact → verbose → errors → slow)  " +
		keyStyle.Render("x") + ":clear\n\n")

	b.WriteString(titleStyle.Render("Status Codes") + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorMod).Render("  M") + "  Locally Modified — you changed this file\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorUpdated).Render("  U") + "  Updated — newer revision on server, pull with " + keyStyle.Render("u") + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorConflict).Render("  C") + "  Conflict — both you and server changed it\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorUntracked).Render("  ?") + "  Untracked — not in CVS\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorUpdated).Render("  A") + "  Added — scheduled for commit\n\n")

	b.WriteString(helpStyle.Render("esc:close"))
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
