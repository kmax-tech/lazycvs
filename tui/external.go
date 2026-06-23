package tui

import (
	"lazycvs/config"
	"lazycvs/cvs"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// externalDiffMsg reports the result of an external-diff launch attempt. err
// is non-nil if extraction or spawning failed; otherwise the tool was started
// in the background. The fields aren't used by the model today — the message
// exists so callers don't have to ignore the Cmd return.
type externalDiffMsg struct {
	err error
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
	rootRepo, err := os.ReadFile(filepath.Join(executor.WorkDir, "CVS", "Repository"))
	if err != nil {
		return "", fmt.Errorf("read CVS/Repository: %w", err)
	}
	// filepath.Clean normalizes the join when CVS/Repository is "." (a
	// root-level checkout): ".  /  foo/bar" → "foo/bar". Without this
	// the leading "./" trips an internal CVS assertion in recurse.c
	// ("/./" must not appear in the repository path) and `cvs co -p`
	// aborts with exit 1.
	modulePath := filepath.Clean(strings.TrimSpace(string(rootRepo)) + "/" + path)

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

// launchExternalDiff spawns the configured diff tool with two file paths,
// returning a Cmd that produces an externalDiffMsg when done. The tool runs in
// the background — we don't wait for it.
func launchExternalDiff(cfg config.Config, leftFile, rightFile string) tea.Cmd {
	return func() tea.Msg {
		tmpl, err := resolveDiffTool(cfg)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		// Accept both $LEFT/$RIGHT and the legacy $LOCAL/$SERVER placeholders.
		cmdStr := strings.ReplaceAll(tmpl, "$LEFT", leftFile)
		cmdStr = strings.ReplaceAll(cmdStr, "$RIGHT", rightFile)
		cmdStr = strings.ReplaceAll(cmdStr, "$LOCAL", leftFile)
		cmdStr = strings.ReplaceAll(cmdStr, "$SERVER", rightFile)

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			return externalDiffMsg{err: fmt.Errorf("empty diff tool command")}
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		if err := cmd.Start(); err != nil {
			return externalDiffMsg{err: fmt.Errorf("launch %s: %w", parts[0], err)}
		}
		return externalDiffMsg{}
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
// conflict file paths. Runs in the background.
func launchExternalMerge(cfg config.Config, base, local, remote, merged string) tea.Cmd {
	return func() tea.Msg {
		tmpl, err := resolveMergeTool(cfg)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		cmdStr := strings.ReplaceAll(tmpl, "$BASE", base)
		cmdStr = strings.ReplaceAll(cmdStr, "$LOCAL", local)
		cmdStr = strings.ReplaceAll(cmdStr, "$REMOTE", remote)
		cmdStr = strings.ReplaceAll(cmdStr, "$MERGED", merged)

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			return externalDiffMsg{err: fmt.Errorf("empty merge tool command")}
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		if err := cmd.Start(); err != nil {
			return externalDiffMsg{err: fmt.Errorf("launch %s: %w", parts[0], err)}
		}
		return externalDiffMsg{}
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
			"Built-in presets: vscode, emacs, vimdiff, meld,\n"+
			"opendiff, kdiff3, diffuse, bcompare.\n\n"+
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
	cfg := m.cfgMgr.Get()
	if cfg.Editor.MergeTool == "" && cfg.Editor.MergeCommand == "" {
		m.dialog.OpenPreview("merge tool", "No merge tool configured.\n\n"+
			"Set [editor] merge_tool in your config, e.g.:\n\n"+
			"  [editor]\n"+
			"  merge_tool = \"meld\"\n\n"+
			"Built-in 3-way presets: meld, kdiff3, vimdiff,\n"+
			"vscode, opendiff, diffuse, bcompare.\n\n"+
			"Or use a custom command:\n"+
			"  merge_command = \"my-tool $BASE $LOCAL $REMOTE -o $MERGED\"")
		return nil
	}
	executor := m.exec
	return func() tea.Msg {
		base, local, remote, merged, err := extractConflictSides(executor, path)
		if err != nil {
			return externalDiffMsg{err: err}
		}
		return launchExternalMerge(cfg, base, local, remote, merged)()
	}
}

// launchExternalDiffForCurrent picks left/right based on the active tab and,
// when on TabHistory, the history mode + cursor. The selectedPath argument is
// the path under cursor in Tree/Fav/Staged; for TabHistory we use m.history.path.
func (m *App) launchExternalDiffForCurrent(selectedPath string) tea.Cmd {
	if m.activeTab != TabHistory {
		// Working copy vs HEAD — what most users want from outside History.
		return m.launchExternalDiffFor(selectedPath,
			diffSide{rev: ""},          // HEAD
			diffSide{working: true})    // local file
	}

	path := m.history.Path()
	if path == "" {
		return nil
	}

	rev := m.history.SelectedRevision()
	if rev == nil {
		return nil
	}
	if rev.PrevNumber == "" {
		// First revision has no parent — nothing to diff against.
		return nil
	}
	return m.launchExternalDiffFor(path,
		diffSide{rev: rev.PrevNumber},
		diffSide{rev: rev.Number})
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
	os.WriteFile(fullPath, []byte(resolved), 0644)
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
