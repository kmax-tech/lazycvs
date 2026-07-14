package cvs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CVSExecutor runs CVS commands with mutex serialization and timeout.
// Write commands use an exclusive lock; read-only commands (status, diff,
// log) can run concurrently via RunReadOnly.
type CVSExecutor struct {
	WorkDir string
	CVSBin  string
	Timeout time.Duration
	Log     *CommandLog
	mu      sync.RWMutex

}

// NewCVSExecutor creates a new executor.
func NewCVSExecutor(workDir, cvsBin string, timeout time.Duration, log *CommandLog) *CVSExecutor {
	return &CVSExecutor{
		WorkDir: workDir,
		CVSBin:  cvsBin,
		Timeout: timeout,
		Log:     log,
	}
}

// Run executes a CVS command with an exclusive lock.
func (e *CVSExecutor) Run(args ...string) (*CommandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run(args...)
}

// RunReadOnly executes a read-only CVS command (status, diff, log) with a
// shared lock, allowing multiple reads to run concurrently.
func (e *CVSExecutor) RunReadOnly(args ...string) (*CommandResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.run(args...)
}

func (e *CVSExecutor) run(args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, e.CVSBin, args...)
	cmd.Dir = e.WorkDir

	// cvs interleaves progress messages on stderr with data on stdout
	// (most visibly `cvs status`, where "Examining <dir>" headers are
	// on stderr but the File: blocks are on stdout). Parsers that need
	// to associate each file with the dir it lives in must see them in
	// the order cvs wrote them.
	//
	// Setting cmd.Stdout and cmd.Stderr to the SAME *bytes.Buffer makes
	// exec.Cmd reuse a single OS pipe for both streams; the kernel then
	// serializes writes from cvs at the syscall boundary so the
	// captured byte order matches cvs's actual write order.
	//
	// Side effect: result.Stdout and result.Stderr end up identical
	// (both = combined output). Callers that used stderr for warning
	// regexes (move-away, stale dirs) keep working because those
	// regexes are specific enough that running them over the combined
	// stream doesn't false-match the data lines on stdout.
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			quoted[i] = fmt.Sprintf("%q", a)
		} else {
			quoted[i] = a
		}
	}

	result := &CommandResult{
		Command:   e.CVSBin + " " + strings.Join(quoted, " "),
		Args:      args,
		Stdout:    combined.String(),
		Stderr:    combined.String(),
		Combined:  combined.String(),
		Duration:  duration,
		Timestamp: start,
		WorkDir:   e.WorkDir,
		Success:   err == nil,
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			if result.ExitCode == 1 && isInformationalExit(args) {
				result.Success = true
			}
		} else if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = -1
			// Append to both — Stdout and Stderr share the same combined
			// stream now (see executor.go::run), so a single-stream
			// append would break the invariant and hide the timeout from
			// the Console panel (which renders result.Stdout).
			msg := "\nTimeout: command exceeded " + e.Timeout.String()
			result.Stdout += msg
			result.Stderr += msg
		}
	}

	e.Log.Add(*result)

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", e.Timeout)
	}

	return result, nil
}

// FirstFailure returns err iff the command genuinely failed — non-nil
// err combined with either no result at all or result.Success == false.
// The Success-false check matters because the executor promotes some
// non-zero exits to Success=true (see isInformationalExit), and those
// shouldn't bubble up as caller-visible failures. Callers that loop
// running cvs commands use this to capture only the first real failure
// without duplicating the condition at every call site.
func FirstFailure(r *CommandResult, err error) error {
	if err != nil && (r == nil || !r.Success) {
		return err
	}
	return nil
}

// DryRunUpdate runs cvs update in dry-run mode and parses the result.
func (e *CVSExecutor) DryRunUpdate() (*UpdateResult, error) {
	result, err := e.RunReadOnly("-n", "-q", "update", "-d", "-P")
	if err != nil && result == nil {
		return nil, err
	}

	update := ParseUpdate(result.Stdout, result.Stderr, e.WorkDir)
	update.Duration = result.Duration
	return update, nil
}

// ValidatePath checks that a path is safe (no traversal, no symlink
// escape). Enforced centrally by the TUI's action dispatchers before
// any cvs or filesystem operation touches the path.
func (e *CVSExecutor) ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths are not allowed: %s", path)
	}

	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path outside working copy: %s", path)
	}

	full := filepath.Join(e.WorkDir, cleaned)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		// File may not exist yet (e.g. for add), just check the directory
		resolved, err = filepath.EvalSymlinks(filepath.Dir(full))
		if err != nil {
			return fmt.Errorf("cannot resolve path: %s", path)
		}
		resolved = filepath.Join(resolved, filepath.Base(full))
	}

	// Resolve the working dir too: on macOS /tmp and /var are symlinks
	// into /private, so an unresolved WorkDir would flag every valid
	// path as escaping. The comparison is path-boundary aware — a
	// sibling like <workdir>-evil must not pass the prefix test.
	wd, err := filepath.EvalSymlinks(e.WorkDir)
	if err != nil {
		wd = e.WorkDir
	}
	if resolved != wd && !strings.HasPrefix(resolved, wd+string(os.PathSeparator)) {
		return fmt.Errorf("path escapes working copy: %s", path)
	}

	return nil
}

// isInformationalExit reports whether an exit-1 from CVS for the given args
// is "informational" (the command succeeded in delivering data, exit code 1
// just signals a noteworthy condition like differences/conflicts) rather than
// a real failure. Used to keep the unread-error counter focused on real
// problems.
func isInformationalExit(args []string) bool {
	dryRun := false
	cmd := ""
	for _, a := range args {
		if a == "-n" {
			dryRun = true
			continue
		}
		// First non-flag arg is the cvs subcommand.
		if cmd == "" && !strings.HasPrefix(a, "-") {
			cmd = a
		}
	}
	switch cmd {
	case "diff", "status", "log":
		return true
	case "update":
		return dryRun || hasFlag(args, "-C")
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
