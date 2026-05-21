package main

import (
	"flag"
	"fmt"
	"lazycvs/config"
	"lazycvs/cvs"
	"lazycvs/tui"
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
	cvsBin := flag.String("cvs", "", "path to CVS binary")
	configPath := flag.String("config", "", "config file path")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	// Path argument: first positional arg, or empty if none
	var targetPath string
	pathExplicit := false
	if args := flag.Args(); len(args) > 0 {
		targetPath = args[0]
		pathExplicit = true
	}

	// Load config (needed early for the default-path fallback)
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfgMgr, err := config.NewConfigManager(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot load config: %v (using defaults)\n", err)
		cfgMgr, _ = config.NewConfigManager("")
	}
	cfg := cfgMgr.Get()

	// If no explicit path: prefer cwd; if cwd has no CVS metadata, fall back to
	// configured default_path (if any). With an explicit path, no fallback.
	if !pathExplicit {
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
		// Check parent directory before giving up
		parent := filepath.Dir(absTarget)
		if parent != absTarget && hasCVSMetadata(parent) {
			workDir = parent
			rel, _ := filepath.Rel(parent, absTarget)
			initialPath = rel
		} else if pathExplicit {
			fmt.Fprintf(os.Stderr, "Error: %q has no CVS/Root (not a CVS working copy directory).\n", absTarget)
			os.Exit(1)
		} else {
			if cfg.CVS.DefaultPath == "" {
				fmt.Fprintf(os.Stderr, "Error: %q has no CVS/Root and no [cvs] default_path is configured.\n", absTarget)
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

	actualCVS := cfg.CVS.Binary
	if *cvsBin != "" {
		actualCVS = *cvsBin
	}
	timeout := time.Duration(cfg.CVS.Timeout) * time.Second

	// Validate CVS binary
	cvsPath, err := exec.LookPath(actualCVS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: CVS binary %q not found. Install CVS or use -cvs flag.\n", actualCVS)
		os.Exit(1)
	}

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

	// Initialize components
	cmdLog := cvs.NewCommandLog(50)
	executor := cvs.NewCVSExecutor(workDir, cvsPath, timeout, cmdLog)

	// Launch the TUI
	app := tui.NewApp(executor, cmdLog, cfgMgr, initialPath)
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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

