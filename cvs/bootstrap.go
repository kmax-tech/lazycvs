package cvs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Bootstrap mode: running `cvs -d ROOT …` *outside* an existing working
// copy. The session-bound CVSExecutor doesn't fit because it carries a
// fixed WorkDir, the per-call RWMutex, and the CommandLog wiring — none
// of which apply before there's a working copy to act on. The functions
// in this file are deliberately free-standing so they can run from
// main.go's `init` subcommand without constructing a half-populated App.

const (
	// listModulesTimeout caps `cvs co -c`. The command is metadata-only
	// and returns within milliseconds on a healthy server; 30s is
	// generous for slow networks / lazy DNS while still failing fast on
	// dead servers.
	listModulesTimeout = 30 * time.Second
	// checkoutTimeout caps `cvs co MODULE`. Large modules can take
	// minutes over a slow link, so the session executor's per-command
	// timeout (default 60s) is too short here. Named so it's easy to
	// promote to config later.
	checkoutTimeout = 5 * time.Minute
)

// ListModules runs `cvs -d <root> co -c` and parses the curated
// CVSROOT/modules entries. The command is metadata-only and writes
// nothing to disk, so cwd doesn't matter — we use os.TempDir() as a
// safe, always-existing directory. sshKey, when non-empty, sets
// CVS_RSH=ssh -i <sshKey> for :ext: connections (whitespace in the
// path will break shell-style interpolation; documented in
// USER_GUIDE.md).
func ListModules(cvsBin, root, sshKey string) ([]Module, error) {
	stdout, err := runBootstrap(cvsBin, []string{"-d", root, "co", "-c"}, sshKey, os.TempDir(), listModulesTimeout)
	if err != nil {
		return nil, err
	}
	return ParseModules(stdout), nil
}

// CheckoutModule runs `cvs -d <root> co <name>` in parentDir. Returns
// the absolute path of the newly created working-copy directory
// (parentDir + mod.CheckoutDir). The expected path is stat'd after a
// successful cvs invocation — if cvs returns ok but the directory
// isn't there, surface a clear error rather than fall back to
// guessing (a sibling-watching heuristic risks picking up an unrelated
// dir created by a parallel process).
func CheckoutModule(cvsBin, root, sshKey string, mod Module, parentDir string) (string, error) {
	if _, err := runBootstrap(cvsBin, []string{"-d", root, "co", mod.Name}, sshKey, parentDir, checkoutTimeout); err != nil {
		return "", err
	}
	expected := filepath.Join(parentDir, mod.CheckoutDir)
	info, err := os.Stat(expected)
	if err != nil {
		return "", fmt.Errorf("cvs co %s reported success but %s is missing: %w", mod.Name, expected, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cvs co %s created %s but it isn't a directory", mod.Name, expected)
	}
	return expected, nil
}

// runBootstrap is the private workhorse: prepares the cmd with the
// given args, cwd, CVS_RSH env, and timeout; returns combined output
// on success and an error that wraps stderr on failure.
func runBootstrap(cvsBin string, args []string, sshKey, cwd string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cvsBin, args...)
	cmd.Dir = cwd

	// Inherit env, then layer CVS_RSH if requested. Inheriting matters
	// for pserver's ~/.cvspass lookup (HOME) and ssh-agent forwarding
	// (SSH_AUTH_SOCK) — both must reach the child.
	env := os.Environ()
	if sshKey != "" {
		env = append(env, "CVS_RSH=ssh -i "+sshKey)
	}
	cmd.Env = env

	// Single combined buffer for the same reason executor.run uses one:
	// cvs interleaves progress messages on stderr with data on stdout,
	// and we want them in the order cvs actually emitted them when
	// formatting an error.
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()
	out := combined.String()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("cvs %v timed out after %s", args, timeout)
		}
		return out, fmt.Errorf("cvs %v failed: %w\n%s", args, err, out)
	}
	return out, nil
}
