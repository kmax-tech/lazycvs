package main

import (
	"flag"
	"fmt"
	"github.com/kmax-tech/lazycvs/config"
	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/tui"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Set via -ldflags "-X main.version=..." at build time (see Makefile).
// Defaults to "dev" for unbranded `go build .` invocations.
var version = "dev"

func versionString() string {
	v := version
	if v == "dev" {
		// `go build` without ldflags leaves the placeholder — fall back to
		// the VCS info Go embeds in module builds so plain builds still
		// produce a useful identifier (commit hash + dirty flag).
		if info, ok := debug.ReadBuildInfo(); ok {
			var rev, modified string
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					rev = s.Value
				case "vcs.modified":
					if s.Value == "true" {
						modified = "-dirty"
					}
				}
			}
			if rev != "" {
				if len(rev) > 12 {
					rev = rev[:12]
				}
				v = rev + modified
			}
		}
	}
	return fmt.Sprintf("lazycvs %s (%s/%s, %s)", v, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func main() {
	// `init` subcommand bypasses flag parsing so a positional CVSROOT
	// argument doesn't get mistaken for a -flag value. Anything after
	// `init` is forwarded to runSetupMode untouched.
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runSetupMode(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "clean-backups" {
		runCleanBackups(os.Args[2:])
		return
	}

	cvsBin := flag.String("cvs", "", "path to CVS binary")
	configPath := flag.String("config", "", "config file path")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	cfgMgr := loadConfig(*configPath)
	cfg := cfgMgr.Get()

	// Path argument: first positional arg, or empty if none
	var targetPath string
	pathExplicit := false
	if args := flag.Args(); len(args) > 0 {
		targetPath = args[0]
		pathExplicit = true
	} else {
		targetPath = "."
	}

	absTarget, err := resolvePath(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve path %q: %v\n", targetPath, err)
		os.Exit(1)
	}

	workDir := absTarget
	initialPath := ""

	if !hasCVSMetadata(absTarget) {
		// Walk every ancestor looking for the first dir with a CVS/Root
		// marker. Bounded by the user's home directory and the filesystem
		// root so we don't accidentally land in someone else's working
		// copy or scan the whole disk.
		if root, rel, ok := findWorkingCopy(absTarget); ok {
			workDir = root
			initialPath = rel
		} else if pathExplicit {
			fmt.Fprintf(os.Stderr, "Error: %q is not inside a CVS working copy (no CVS/Root found on any ancestor).\n", absTarget)
			os.Exit(1)
		} else {
			if cfg.CVS.DefaultPath == "" {
				fmt.Fprintf(os.Stderr, "Error: %q has no CVS/Root and no [cvs] default_path is configured.\n", absTarget)
				fmt.Fprintln(os.Stderr, "Hint: `lazycvs init` walks you through cloning a module.")
				os.Exit(1)
			}
			fallback, err := resolvePath(cfg.CVS.DefaultPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot resolve default_path %q: %v\n", cfg.CVS.DefaultPath, err)
				os.Exit(1)
			}
			if !hasCVSMetadata(fallback) {
				fmt.Fprintf(os.Stderr, "Error: default_path %q has no CVS/Root.\n", fallback)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "No CVS/Root in cwd; using default_path %q.\n", fallback)
			workDir = fallback
		}
	}

	runApp(workDir, initialPath, cfgMgr, *cvsBin)
}

// loadConfig handles the "config exists / doesn't exist / corrupt" branches
// uniformly so both main and runSetupMode get the same ConfigManager.
func loadConfig(explicitPath string) *config.ConfigManager {
	cfgPath := explicitPath
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfgMgr, err := config.NewConfigManager(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot load config: %v (using defaults)\n", err)
		cfgMgr, _ = config.NewConfigManager("")
	}
	return cfgMgr
}

// resolveCVSBin returns the absolute path to the cvs binary, or exits
// with a clear error. Shared by main and runSetupMode.
func resolveCVSBin(cfgMgr *config.ConfigManager, override string) string {
	cfg := cfgMgr.Get()
	bin := cfg.CVS.Binary
	if override != "" {
		bin = override
	}
	cvsPath, err := exec.LookPath(bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: CVS binary %q not found. Install CVS or use -cvs flag.\n", bin)
		os.Exit(1)
	}
	return cvsPath
}

// runApp is the normal startup path: validate the working copy, build the
// executor + TUI, and run until the user quits. Factored out of main so
// runSetupMode can chain into it after a successful checkout.
func runApp(workDir, initialPath string, cfgMgr *config.ConfigManager, cvsBinOverride string) {
	cfg := cfgMgr.Get()
	cvsPath := resolveCVSBin(cfgMgr, cvsBinOverride)
	timeout := time.Duration(cfg.CVS.Timeout) * time.Second

	// Read CVSROOT (validates the working copy is reachable; the value isn't
	// otherwise needed by the TUI, but failing here gives a clearer error than
	// the first cvs command failing later).
	rootFile := filepath.Join(workDir, "CVS", "Root")
	if _, err := os.ReadFile(rootFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read CVS/Root in %q.\n", workDir)
		os.Exit(1)
	}

	// Sync global ignore patterns
	if cfg.Ignore.SyncCvsignore && len(cfg.Ignore.GlobalPatterns) > 0 {
		if err := config.SyncGlobalIgnore(cfg.Ignore.GlobalPatterns); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot sync .cvsignore: %v\n", err)
		}
	}

	// 200 entries keeps a whole session's worth of bulk actions visible
	// in the console; the append-only file below is the unbounded record.
	cmdLog := cvs.NewCommandLog(200)
	logPath := config.DefaultLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err == nil {
		if err := cmdLog.EnableFileLog(logPath, workDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: session log disabled: %v\n", err)
		}
	}
	executor := cvs.NewCVSExecutor(workDir, cvsPath, timeout, cmdLog)
	app := tui.NewApp(executor, cmdLog, cfgMgr, initialPath)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runSetupMode drives `lazycvs init`: prompt for CVSROOT (with sensible
// defaults), list modules, check one out, and chain into runApp against
// the new working copy. cwd is captured once here so no downstream code
// has to peek at os.Getwd.
func runSetupMode(args []string) {
	// Re-parse a minimal flag set so `lazycvs init -cvs /opt/bin/cvs ROOT`
	// still works. Anything not consumed here ends up as positional.
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	cvsBin := fs.String("cvs", "", "path to CVS binary")
	configPath := fs.String("config", "", "config file path")
	_ = fs.Parse(args)

	cfgMgr := loadConfig(*configPath)
	cfg := cfgMgr.Get()

	// CVSROOT preference order: positional → $CVSROOT → cfg.CVS.Root.
	root := ""
	if pos := fs.Args(); len(pos) > 0 {
		root = pos[0]
	}
	if root == "" {
		root = os.Getenv("CVSROOT")
	}
	if root == "" {
		root = cfg.CVS.Root
	}

	parentDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine cwd: %v\n", err)
		os.Exit(1)
	}

	cvsPath := resolveCVSBin(cfgMgr, *cvsBin)

	app := tui.NewSetupApp(cfgMgr, cvsPath, root, cfg.CVS.SSHKey, parentDir)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// On a successful checkout the App stashes the new working copy
	// path; main chains into the normal startup path against it.
	// Empty means the user aborted with Esc — exit cleanly.
	finalApp, ok := finalModel.(tui.App)
	if !ok {
		return
	}
	newWorkDir := finalApp.BootstrapResult()
	if newWorkDir == "" {
		return
	}
	runApp(newWorkDir, "", cfgMgr, *cvsBin)
}

// runCleanBackups walks a path looking for .lazycvs-backup sidecar files
// and removes them. Defaults to the current directory; a positional arg
// overrides. Dry-run mode prints what would be deleted without touching
// disk. Headless — no TUI, just stdout, so it's easy to run from
// scripts or one-off after a noisy revert session.
func runCleanBackups(args []string) {
	fs := flag.NewFlagSet("clean-backups", flag.ExitOnError)
	dryRun := fs.Bool("n", false, "list candidates but don't delete (dry-run)")
	_ = fs.Parse(args)

	root := "."
	if pos := fs.Args(); len(pos) > 0 {
		root = pos[0]
	}
	abs, err := resolvePath(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot resolve path %q: %v\n", root, err)
		os.Exit(1)
	}

	var count int
	walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // tolerate unreadable subtrees
		}
		// Don't descend into CVS metadata — never our backup files there
		// and the dir is otherwise off-limits for hygiene reasons.
		if info.IsDir() && info.Name() == "CVS" {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".lazycvs-backup") {
			return nil
		}
		if *dryRun {
			fmt.Println(path)
		} else {
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot remove %s: %v\n", path, err)
				return nil
			}
			fmt.Println("removed", path)
		}
		count++
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "Error: walk failed: %v\n", walkErr)
		os.Exit(1)
	}
	if *dryRun {
		fmt.Printf("\n%d candidate(s) under %s\n", count, abs)
	} else {
		fmt.Printf("\n%d backup(s) removed under %s\n", count, abs)
	}
}

// resolvePath turns a user-supplied path into an absolute path. It expands a
// leading "~" to the home directory and, if the path points at a file, returns
// its parent directory.
func resolvePath(p string) (string, error) {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	return abs, nil
}

// hasCVSMetadata reports whether the given directory has a CVS/Root file
// (indicating it's a CVS working copy directory).
func hasCVSMetadata(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "CVS", "Root"))
	return err == nil
}

// findWorkingCopy walks from `start` toward the filesystem root looking
// for the first ancestor with CVS/Root. Returns (root, rel, true) where
// `root` is that ancestor and `rel` is start's path relative to it, so
// the TUI can land on the originally-requested subdirectory. Returns
// ("", "", false) if no working copy is found before hitting the user's
// home directory or the filesystem root — both stop conditions keep us
// from silently descending into an unexpected repo or scanning the disk.
func findWorkingCopy(start string) (string, string, bool) {
	home, _ := os.UserHomeDir()
	dir := start
	for {
		if hasCVSMetadata(dir) {
			rel, _ := filepath.Rel(dir, start)
			if rel == "." {
				rel = ""
			}
			return dir, rel, true
		}
		// Stop conditions: we've left the user's home tree, or we've
		// reached the filesystem root.
		if home != "" && dir == home {
			return "", "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

