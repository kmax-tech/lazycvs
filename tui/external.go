package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/config"
	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// moduleRoot returns the repository path of the working-copy root
// relative to CVSROOT, read from CVS/Repository. filepath.Clean
// normalizes the "." case (a root-level checkout) so joins like
// root+"/"+path never produce a leading "./" — that trips an internal
// CVS assertion in recurse.c and aborts `cvs co -p` with exit 1.
// Shared by the revision-extraction and recent-changes queries.
func moduleRoot(executor *cvs.CVSExecutor) (string, error) {
	data, err := os.ReadFile(filepath.Join(executor.WorkDir, "CVS", "Repository"))
	if err != nil {
		return "", fmt.Errorf("read CVS/Repository: %w", err)
	}
	return filepath.Clean(strings.TrimSpace(string(data))), nil
}

// externalDiffMsg reports the result of an external diff/merge launch. err is
// non-nil if extraction or spawning failed and is surfaced in the result bar.
// A plain diff succeeds silently (the tool appearing is the feedback); a merge
// sets note, because it hands the tool write access to the working copy and
// that must not happen without a word.
//
// refreshPath names a working-copy file the tool may have written to; the
// handler refreshes its status and invalidates its History cache so the
// tree and the vs-working diff don't keep showing the pre-merge state. For
// terminal tools the message arrives after the tool exits, so the refresh
// sees the result; for detached GUI tools it arrives at launch time, where
// it at least drops the stale cached diff (the tool writes later — the
// next status refresh picks the change up).
type externalDiffMsg struct {
	note        string
	refreshPath string
	err         error
}

// catRevisionStdout returns the on-disk content of a specific revision of a
// file from the CVS repository, bypassing the working copy state.
//
// Uses `cvs co -p <module-path>` instead of the more obvious `cvs update -p`
// because `update -p` returns empty stdout when the file is in an unresolved-
// conflict state (CVS quirk: it refuses to "checkout the working revision"
// while there are conflict markers in the working copy). `co -p` reads
// directly from the RCS file in the repository and is unaffected.
//
// rev=="" means HEAD.
func catRevisionStdout(executor *cvs.CVSExecutor, path, rev string) (string, error) {
	root, err := moduleRoot(executor)
	if err != nil {
		return "", err
	}
	modulePath := filepath.Clean(root + "/" + path)

	args := []string{"co", "-p"}
	if rev == "" {
		args = append(args, "-r", "HEAD")
	} else {
		args = append(args, "-r", rev)
	}
	args = append(args, modulePath)

	result, err := executor.RunReadOnly(args...)
	if err != nil && result == nil {
		return "", fmt.Errorf("cvs %s: %w", strings.Join(args, " "), err)
	}
	return stripCheckoutHeader(result.Stdout), nil
}

// stripCheckoutHeader removes the metadata block that `cvs co -p` emits
// before each file's content:
//
//	===================================================================
//	Checking out <module-path>
//	RCS:  <repo,v>
//	VERS: <revision>
//	***************
//	<actual file content starts here>
//
// The block is identified by the asterisk separator line that ends it;
// everything up to and including that line is metadata. If no separator
// is found (e.g. cvs format changes, or this is a continuation), the
// content is returned unchanged.
func stripCheckoutHeader(content string) string {
	lines := strings.SplitN(content, "\n", -1)
	for i, line := range lines {
		// Tolerate trailing whitespace and require at least three '*'
		// so we don't strip a content line that legitimately starts
		// with one or two asterisks (e.g. Markdown list items).
		if strings.HasPrefix(strings.TrimSpace(line), "***") &&
			strings.TrimRight(strings.TrimSpace(line), "*") == "" {
			return strings.Join(lines[i+1:], "\n")
		}
		// Header lines are bounded — bail out if we don't see the
		// asterisk separator within the first handful of lines.
		if i >= 6 {
			break
		}
	}
	return content
}

// extractRevToTemp checks out a specific revision and writes the result to a
// temp file whose basename leads with the revision label, so external diff
// tools (which usually only show the basename in tab titles) make it obvious
// which side is which: `rev-1.1-foo.md` vs `rev-1.2-foo.md`. The original
// extension is preserved so syntax-aware tools light up correctly. Each call
// gets its own MkdirTemp directory — that way no random suffix is needed in
// the filename itself, keeping the rev visually adjacent to the basename.
// The caller is responsible for cleanup if desired; most diff tools open the
// files lazily so we deliberately do NOT remove them ourselves.
func extractRevToTemp(executor *cvs.CVSExecutor, path, rev string) (string, error) {
	content, err := catRevisionStdout(executor, path, rev)
	if err != nil {
		return "", err
	}

	revLabel := "HEAD"
	if rev != "" {
		revLabel = rev
	}
	dir, err := os.MkdirTemp("", "lazycvs-diff-")
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("rev-%s-%s", revLabel, filepath.Base(path))
	fullPath := filepath.Join(dir, name)
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return fullPath, nil
}

// resolveDiffTool returns the command template configured by the user, or an
// error if no tool is set. If the configured value is a known preset name, the
// preset's template is returned; otherwise the value is used as-is (custom
// command).
func resolveDiffTool(cfg config.Config) (string, error) {
	tool := cfg.Editor.DiffTool
	if cfg.Editor.DiffCommand != "" {
		// Custom command override takes precedence.
		tool = cfg.Editor.DiffCommand
	}
	if tool == "" {
		return "", fmt.Errorf("no diff tool configured (set [editor] diff_tool in config, e.g. \"meld\")")
	}
	if tmpl, ok := config.DiffToolPresets[tool]; ok {
		return tmpl, nil
	}
	return tool, nil
}

// isTerminalTool reports whether the given argv[0] is a known
// terminal-based (curses) program. Such tools must take over the terminal
// via tea.ExecProcess — launching them detached makes them fight the TUI
// for the terminal and garbles both.
func isTerminalTool(bin string) bool {
	switch filepath.Base(bin) {
	case "vim", "vimdiff", "nvim":
		return true
	}
	return false
}

// useTerminal decides the launch mode for a tool: the user's explicit
// config flag wins; unset falls back to the binary-name inference.
func useTerminal(argv0 string, override *bool) bool {
	if override != nil {
		return *override
	}
	return isTerminalTool(argv0)
}

// expandTemplate splits a command template into argv and substitutes the
// placeholder tokens afterwards — in this order so absolute paths that
// contain spaces stay one argument.
func expandTemplate(tmpl string, subs map[string]string) []string {
	parts := strings.Fields(tmpl)
	for i, p := range parts {
		for placeholder, value := range subs {
			p = strings.ReplaceAll(p, placeholder, value)
		}
		parts[i] = p
	}
	return parts
}

// spawnTool runs an already-built argv either detached (GUI tools) or by
// suspending the TUI (terminal tools). terminalOverride is the user's
// diff_terminal/merge_terminal config flag; nil means infer from the
// binary name. Both paths resolve to an externalDiffMsg.
func spawnTool(argv []string, terminalOverride *bool, note, refreshPath string) tea.Msg {
	cmd := exec.Command(argv[0], argv[1:]...)
	if useTerminal(argv[0], terminalOverride) {
		// tea.ExecProcess wires the tool to the real terminal and restores
		// the TUI when it exits. Its Cmd just wraps a message the bubbletea
		// runtime intercepts, so invoking it inside this Cmd is fine.
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return externalDiffMsg{note: note, refreshPath: refreshPath, err: err}
		})()
	}
	if err := cmd.Start(); err != nil {
		return externalDiffMsg{err: fmt.Errorf("launch %s: %w", argv[0], err)}
	}
	return externalDiffMsg{note: note, refreshPath: refreshPath}
}

// launchExternalDiff spawns the configured diff tool with two file paths,
// returning a Cmd that produces an externalDiffMsg when done. GUI tools run
// detached in the background; terminal tools (vimdiff, nvim) suspend the
// TUI until they exit.
func launchExternalDiff(cfg config.Config, leftFile, rightFile string) tea.Cmd {
	return func() tea.Msg {
		tmpl, err := resolveDiffTool(cfg)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		// Accept both $LEFT/$RIGHT and the legacy $LOCAL/$SERVER placeholders.
		parts := expandTemplate(tmpl, map[string]string{
			"$LEFT": leftFile, "$RIGHT": rightFile,
			"$LOCAL": leftFile, "$SERVER": rightFile,
		})
		if len(parts) == 0 {
			return externalDiffMsg{err: fmt.Errorf("empty diff tool command")}
		}
		return spawnTool(parts, cfg.Editor.DiffTerminal, "", "")
	}
}

// resolveMergeTool returns the configured 3-way merge tool template.
func resolveMergeTool(cfg config.Config) (string, error) {
	tool := cfg.Editor.MergeTool
	if cfg.Editor.MergeCommand != "" {
		tool = cfg.Editor.MergeCommand
	}
	if tool == "" {
		return "", fmt.Errorf("no merge tool configured (set [editor] merge_tool, e.g. \"meld\")")
	}
	if tmpl, ok := config.MergeToolPresets[tool]; ok {
		return tmpl, nil
	}
	return tool, nil
}

// findCVSBackup looks for a CVS pre-merge backup file `.#<name>.<rev>` in the
// directory of `path` and returns its absolute path plus the rev parsed from
// the filename. Returns ("", "", nil) if no backup is found.
//
// CVS creates this backup whenever an update produces a conflict — it's a
// snapshot of your working copy with your local edits, BEFORE the merge
// attempt that introduced the conflict markers. That makes it a clean LOCAL
// side for a 3-way merge.
func findCVSBackup(workDir, path string) (backupAbs, rev string, err error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	absDir := filepath.Join(workDir, dir)
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return "", "", err
	}
	prefix := ".#" + base + "."
	var bestName, bestRev string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		r := strings.TrimPrefix(name, prefix)
		// Pick the rev with the highest numeric suffix if multiple exist.
		if cmpRev(r, bestRev) > 0 {
			bestRev = r
			bestName = name
		}
	}
	if bestName == "" {
		return "", "", nil
	}
	return filepath.Join(absDir, bestName), bestRev, nil
}

// cmpRev compares two CVS revision strings (e.g. "1.5" vs "1.10") numerically
// per dotted segment. Empty strings sort as smallest. Returns >0, 0, <0.
func cmpRev(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			fmt.Sscanf(as[i], "%d", &ai)
		}
		if i < len(bs) {
			fmt.Sscanf(bs[i], "%d", &bi)
		}
		if ai != bi {
			return ai - bi
		}
	}
	return 0
}

// extractConflictSides materializes the four sides of a 3-way merge for a
// conflict file. Returns absolute paths in (base, local, remote, merged)
// order. The MERGED path is the on-disk working file (with conflict markers);
// most tools save back to it. If no `.#` backup exists, LOCAL falls back to
// MERGED — the tool will see a 2-way comparison effectively.
func extractConflictSides(executor *cvs.CVSExecutor, path string) (base, local, remote, merged string, err error) {
	merged = filepath.Join(executor.WorkDir, path)

	backupAbs, baseRev, _ := findCVSBackup(executor.WorkDir, path)
	if backupAbs != "" {
		local = backupAbs
	} else {
		// No pre-merge backup — the working file IS the local side.
		local = merged
	}

	if baseRev != "" {
		base, err = extractRevToTemp(executor, path, baseRev)
		if err != nil {
			return "", "", "", "", fmt.Errorf("extract base %s: %w", baseRev, err)
		}
	}

	remote, err = extractRevToTemp(executor, path, "")
	if err != nil {
		return "", "", "", "", fmt.Errorf("extract HEAD: %w", err)
	}

	if base == "" {
		// Fall back: use REMOTE as BASE (so the tool still works even when
		// we can't find the pre-merge backup; user gets a degenerate 3-way).
		base = remote
	}

	return base, local, remote, merged, nil
}

// launchExternalMerge spawns the configured 3-way merge tool with the four
// conflict file paths. GUI tools run detached; terminal tools suspend the TUI.
// refreshPath is the working-copy file the tool writes into ($MERGED),
// relative to the work dir — see externalDiffMsg.
func launchExternalMerge(cfg config.Config, base, local, remote, merged, note, refreshPath string) tea.Cmd {
	return func() tea.Msg {
		tmpl, err := resolveMergeTool(cfg)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		// Without $MERGED the tool has nowhere to write the result — the
		// user would get the success note and an untouched working copy.
		if !strings.Contains(tmpl, "$MERGED") {
			return externalDiffMsg{err: fmt.Errorf(
				"merge command lacks $MERGED — the result would have nowhere to land: %q", tmpl)}
		}
		parts := expandTemplate(tmpl, map[string]string{
			"$BASE": base, "$LOCAL": local,
			"$REMOTE": remote, "$MERGED": merged,
		})
		if len(parts) == 0 {
			return externalDiffMsg{err: fmt.Errorf("empty merge tool command")}
		}
		return spawnTool(parts, cfg.Editor.MergeTerminal, note, refreshPath)
	}
}

// --- App-level dispatch helpers ----------------------------------------

// diffSide describes one side of an external-diff invocation.
// If working is true, the file is the on-disk working copy (no extraction).
// Otherwise rev is checked out via `cvs update -p [-r rev]` to a temp file
// (rev="" means HEAD).
type diffSide struct {
	rev     string
	working bool
}

// launchExternalDiffFor extracts both sides to files (or uses the working
// file directly) and spawns the configured external diff tool. If no tool is
// configured, opens a preview dialog explaining how to set one.
func (m *App) launchExternalDiffFor(path string, left, right diffSide) tea.Cmd {
	cfg := m.cfgMgr.Get()
	if cfg.Editor.DiffTool == "" && cfg.Editor.DiffCommand == "" {
		m.dialog.OpenPreview("external diff", "No diff tool configured.\n\n"+
			"Set [editor] diff_tool in your config, e.g.:\n\n"+
			"  [editor]\n"+
			"  diff_tool = \"meld\"\n\n"+
			"Built-in presets: vscode, emacs, vimdiff, nvimdiff,\n"+
			"meld, opendiff, kdiff3, diffuse, bcompare.\n"+
			"(vimdiff/nvimdiff suspend the TUI while open.)\n\n"+
			"Or use a custom command template:\n"+
			"  diff_command = \"my-tool $LEFT $RIGHT\"")
		return nil
	}
	executor := m.exec
	return func() tea.Msg {
		leftFile, err := materializeSide(executor, path, left)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		rightFile, err := materializeSide(executor, path, right)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		return launchExternalDiff(cfg, leftFile, rightFile)()
	}
}

// materializeSide returns the absolute file path for a diff side, extracting
// the revision to a temp file if necessary.
func materializeSide(executor *cvs.CVSExecutor, path string, side diffSide) (string, error) {
	if side.working {
		return filepath.Join(executor.WorkDir, path), nil
	}
	return extractRevToTemp(executor, path, side.rev)
}

// launchExternalMergeFor extracts the four sides of a CVS conflict file and
// spawns the configured 3-way merge tool. Returns nil with a help dialog
// open if no merge tool is configured.
func (m *App) launchExternalMergeFor(path string) tea.Cmd {
	if !m.requireMergeTool() {
		return nil
	}
	cfg := m.cfgMgr.Get()
	executor := m.exec
	return func() tea.Msg {
		base, local, remote, merged, err := extractConflictSides(executor, path)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		return launchExternalMerge(cfg, base, local, remote, merged,
			"Merging conflict in "+filepath.Base(path)+" — save in the tool, then commit", path)()
	}
}

// requireMergeTool reports whether a merge tool is configured, opening the
// how-to-configure dialog and returning false when it isn't. Shared by the
// conflict flow (M on a C file) and the revision flow (M in History).
func (m *App) requireMergeTool() bool {
	cfg := m.cfgMgr.Get()
	if cfg.Editor.MergeTool != "" || cfg.Editor.MergeCommand != "" {
		return true
	}
	m.dialog.OpenPreview("merge tool", "No merge tool configured.\n\n"+
		"Set [editor] merge_tool in your config, e.g.:\n\n"+
		"  [editor]\n"+
		"  merge_tool = \"meld\"\n\n"+
		"Built-in 3-way presets: meld, kdiff3, vimdiff,\n"+
		"nvimdiff, vscode, opendiff, bcompare.\n\n"+
		"Or use a custom command:\n"+
		"  merge_command = \"my-tool $BASE $LOCAL $REMOTE -o $MERGED\"")
	return false
}

// copyWorkingToTemp snapshots the on-disk working file into a temp file, so
// a merge tool gets a stable LOCAL side that is NOT the same file it writes
// its result to. Mirrors what the conflict flow gets for free from CVS's
// `.#file.rev` pre-merge backup.
func copyWorkingToTemp(workDir, path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(workDir, path))
	if err != nil {
		return "", fmt.Errorf("read working copy: %w", err)
	}
	dir, err := os.MkdirTemp("", "lazycvs-merge-")
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, "working-"+filepath.Base(path))
	if err := os.WriteFile(full, data, 0644); err != nil {
		return "", err
	}
	return full, nil
}

// launchHistoryMergeForCurrent merges the change between the two revisions
// selected in History (anchor → cursor, see comparePair) into the working
// copy — the interactive equivalent of `cvs update -j <from> -j <to>`, but
// resolved in the merge tool instead of leaving conflict markers behind.
//
//	BASE   = the "from" revision (anchor, or the cursor's parent)
//	REMOTE = the "to" revision (cursor)
//	LOCAL  = snapshot of the working copy as it is right now
//	MERGED = the working file itself — the tool writes the result there,
//	         and it shows up as a normal local modification to commit.
//
// A .lazycvs-backup is written first, because handing an external tool
// write access to the working copy is a destructive action by the same
// standard as revert and restore-rev.
func (m *App) launchHistoryMergeForCurrent() tea.Cmd {
	path := m.history.Path()
	if path == "" {
		return nil
	}
	if !m.requireMergeTool() {
		return nil
	}

	fromRev, toRev := m.comparePair()
	if fromRev == "" || toRev == "" {
		return func() tea.Msg {
			return externalDiffMsg{err: fmt.Errorf(
				"no revision pair selected — anchor a revision with space, then put the cursor on the other one")}
		}
	}
	// The working copy is already the LOCAL side; it cannot also be one of
	// the two revisions being merged in.
	if fromRev == workingRev || toRev == workingRev {
		return func() tea.Msg {
			return externalDiffMsg{err: fmt.Errorf(
				"merge needs two revisions — press w to take the working copy off the comparison")}
		}
	}
	// Same guard E carries in the key dispatch: a 3-way text merge on a
	// binary file produces garbage, and staying silent would make M read
	// as a dead key.
	if fs.IsBinary(filepath.Join(m.exec.WorkDir, path)) {
		return func() tea.Msg {
			return externalDiffMsg{err: fmt.Errorf(
				"%s is binary — a text merge tool cannot merge it", filepath.Base(path))}
		}
	}

	cfg := m.cfgMgr.Get()
	executor := m.exec
	cmdLog := m.cmdLog
	return func() tea.Msg {
		if err := validatePaths(executor, path); err != nil {
			return externalDiffMsg{err: err}
		}
		base, err := extractRevToTemp(executor, path, fromRev)
		if err != nil {
			return externalDiffMsg{err: fmt.Errorf("extract %s: %w", fromRev, err)}
		}
		remote, err := extractRevToTemp(executor, path, toRev)
		if err != nil {
			return externalDiffMsg{err: fmt.Errorf("extract %s: %w", toRev, err)}
		}
		local, err := copyWorkingToTemp(executor.WorkDir, path)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		// The working file exists (copyWorkingToTemp just read it), so a
		// false here is a write failure — read-only dir, full disk. The
		// docs promise the safety copy; proceeding without one would hand
		// the tool an unprotected working copy.
		if !writeBackupFile(executor.WorkDir, path) {
			return externalDiffMsg{err: fmt.Errorf(
				"could not write %s%s — merge aborted to keep an undo copy", path, backupSuffix)}
		}
		cmdLog.LogFileOp("cp "+path+" "+path+backupSuffix+"  # pre-merge safety copy", nil)
		merged := filepath.Join(executor.WorkDir, path)
		return launchExternalMerge(cfg, base, local, remote, merged,
			fmt.Sprintf("Merging %s → %s into %s — save in the tool, then commit",
				fromRev, toRev, filepath.Base(path)), path)()
	}
}

// sideForRev turns a revision produced by comparePair into a diffSide.
// The working-copy sentinel becomes the on-disk file; everything else is
// checked out to a temp file.
func sideForRev(rev string) diffSide {
	if rev == workingRev {
		return diffSide{working: true}
	}
	return diffSide{rev: rev}
}

// launchExternalDiffForCurrent picks left/right based on the active tab and,
// when on TabHistory, the same selection the Diff view uses. The selectedPath
// argument is the path under cursor in Tree/Fav/Staged; for TabHistory we use
// m.history.path.
//
// Inside History the pair comes from comparePair, so the compare anchor
// (space) and the vs-working toggle (w) steer the external tool exactly as
// they steer the built-in diff — anchor 1.85, cursor 1.90, E opens those two.
func (m *App) launchExternalDiffForCurrent(selectedPath string) tea.Cmd {
	if m.activeTab != TabHistory {
		// Working copy vs HEAD — what most users want from outside History.
		return m.launchExternalDiffFor(selectedPath,
			diffSide{rev: ""},       // HEAD
			diffSide{working: true}) // local file
	}

	path := m.history.Path()
	if path == "" {
		return nil
	}

	fromRev, toRev := m.comparePair()
	if fromRev == "" || toRev == "" {
		// Nothing to compare: initial revision with no anchor, both sides
		// pinned to the working copy, or from == to.
		return nil
	}
	return m.launchExternalDiffFor(path, sideForRev(fromRev), sideForRev(toRev))
}

// handleConflictResolve applies a resolution choice to a file with merge
// markers and refreshes its status. Lives next to the merge launching code
// because both walk the conflict-resolution flow.
func (m *App) handleConflictResolve(msg conflictResolveMsg) tea.Cmd {
	fullPath := filepath.Join(m.exec.WorkDir, msg.path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil
	}
	resolved := cvs.ResolveConflict(string(data), msg.choice, 0) // 0 = all regions
	err = os.WriteFile(fullPath, []byte(resolved), 0644)
	m.cmdLog.LogFileOp("resolve "+msg.path+" --keep="+msg.choice+"  # strip conflict markers", err)
	return m.refreshStatusForPaths([]string{msg.path})
}

// openInOS opens the given path with the OS default viewer (macOS open,
// Linux xdg-open). The launch is fire-and-forget — we don't wait for the
// viewer to exit and don't surface launch failures.
func openInOS(path string) tea.Cmd {
	return func() tea.Msg {
		bin := "open"
		if _, err := os.Stat("/usr/bin/xdg-open"); err == nil {
			bin = "xdg-open"
		}
		cmd := exec.Command(bin, path)
		cmd.Start()
		return nil
	}
}

// fileActionDoneMsg reports the outcome of a lightweight file utility
// (alt-open, reveal, copy path). note is shown green on success; a non-nil
// err is shown red. Unlike externalDiffMsg this IS consumed by the model —
// these actions have no other visible effect, so silence would read as
// "the key is dead".
type fileActionDoneMsg struct {
	note string
	err  error
}

// altOpenArgv turns the [editor] alt_open template into an argv for the
// given absolute file path. $FILE is substituted; a template without $FILE
// gets the path appended. Split on whitespace like the diff/merge templates
// (no shell quoting). Errors on an empty/unset template.
func altOpenArgv(tmpl, path string) ([]string, error) {
	if strings.TrimSpace(tmpl) == "" {
		return nil, fmt.Errorf("no alternate opener configured (set [editor] alt_open, e.g. \"emacsclient -n\")")
	}
	if strings.Contains(tmpl, "$FILE") {
		parts := strings.Fields(strings.ReplaceAll(tmpl, "$FILE", path))
		if len(parts) == 0 {
			return nil, fmt.Errorf("empty alt_open command")
		}
		return parts, nil
	}
	return append(strings.Fields(tmpl), path), nil
}

// launchAltOpenFor spawns the configured alternate opener (Ctrl-O) on the
// file under the cursor, detached. If none is configured, opens a help
// dialog explaining how to set one — same pattern as diff/merge tools.
func (m *App) launchAltOpenFor(path string) tea.Cmd {
	cfg := m.cfgMgr.Get()
	if strings.TrimSpace(cfg.Editor.AltOpen) == "" {
		m.dialog.OpenPreview("alt open", "No alternate opener configured.\n\n"+
			"Ctrl-O opens the file under the cursor with a second tool of\n"+
			"your choice (a GUI editor, an IDE, …), without leaving lazycvs.\n\n"+
			"Set [editor] alt_open in your config, e.g.:\n\n"+
			"  [editor]\n"+
			"  alt_open = \"emacsclient -n\"\n\n"+
			"$FILE marks where the path goes; without it the path is\n"+
			"appended:\n"+
			"  alt_open = \"code --goto $FILE\"")
		return nil
	}
	fullPath := filepath.Join(m.exec.WorkDir, path)
	return func() tea.Msg {
		argv, err := altOpenArgv(cfg.Editor.AltOpen, fullPath)
		if err != nil {
			return fileActionDoneMsg{err: err}
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		if err := cmd.Start(); err != nil {
			return fileActionDoneMsg{err: fmt.Errorf("launch %s: %w", argv[0], err)}
		}
		return fileActionDoneMsg{note: "Opened " + filepath.Base(path) + " with " + filepath.Base(argv[0])}
	}
}

// revealInOS shows the file in the OS file manager (Ctrl-R): macOS reveals
// and selects it in Finder (`open -R`); elsewhere we open the containing
// directory — the closest portable equivalent.
func revealInOS(path string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", "-R", path)
		case "windows":
			cmd = exec.Command("explorer", "/select,", path)
		default:
			cmd = exec.Command("xdg-open", filepath.Dir(path))
		}
		if err := cmd.Start(); err != nil {
			return fileActionDoneMsg{err: fmt.Errorf("reveal %s: %w", filepath.Base(path), err)}
		}
		return fileActionDoneMsg{note: "Revealed " + filepath.Base(path)}
	}
}

// copyPathCmd puts the absolute path on the system clipboard (Ctrl-Y) via
// the platform's clipboard tool. Fails loud when no tool is available.
func copyPathCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := copyToClipboard(path); err != nil {
			return fileActionDoneMsg{err: err}
		}
		return fileActionDoneMsg{note: "Copied path: " + path}
	}
}

// copyToClipboard pipes text into the first available clipboard tool.
func copyToClipboard(text string) error {
	type tool struct {
		bin  string
		args []string
	}
	var tools []tool
	switch runtime.GOOS {
	case "darwin":
		tools = []tool{{"pbcopy", nil}}
	case "windows":
		tools = []tool{{"clip", nil}}
	default:
		tools = []tool{
			{"wl-copy", nil},
			{"xclip", []string{"-selection", "clipboard"}},
			{"xsel", []string{"--clipboard", "--input"}},
		}
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t.bin); err != nil {
			continue
		}
		cmd := exec.Command(t.bin, t.args...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return fmt.Errorf("no clipboard tool found (pbcopy / wl-copy / xclip / xsel)")
}
