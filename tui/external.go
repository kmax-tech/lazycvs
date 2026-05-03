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
	modulePath := strings.TrimSpace(string(rootRepo)) + "/" + path

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
	return result.Stdout, nil
}

// extractRevToTemp checks out a specific revision and writes the result to a
// temp file with the original file's extension preserved (so syntax-aware
// diff tools light up correctly). Returns the temp-file path. The caller is
// responsible for cleanup if desired; most diff tools open the files lazily
// so we deliberately do NOT remove them ourselves.
func extractRevToTemp(executor *cvs.CVSExecutor, path, rev string) (string, error) {
	content, err := catRevisionStdout(executor, path, rev)
	if err != nil {
		return "", err
	}

	revLabel := "HEAD"
	if rev != "" {
		revLabel = strings.ReplaceAll(rev, ".", "_")
	}
	base := filepath.Base(path)
	pattern := fmt.Sprintf("lazycvs-%s-%s-*%s", strings.TrimSuffix(base, filepath.Ext(base)), revLabel, filepath.Ext(base))
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
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
