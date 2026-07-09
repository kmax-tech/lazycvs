package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// ConfigManager provides thread-safe config access with read-modify-write.
type ConfigManager struct {
	mu     sync.Mutex
	path   string
	config *Config
}

// NewConfigManager loads or creates a config file and returns a manager.
func NewConfigManager(path string) (*ConfigManager, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &ConfigManager{path: path, config: cfg}, nil
}

// Get returns a copy of the current config.
func (m *ConfigManager) Get() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.config
}

// Update applies a modification function atomically. It re-reads from disk
// before applying to respect manual edits.
func (m *ConfigManager) Update(fn func(*Config)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-read from disk to pick up manual edits
	cfg, err := Load(m.path)
	if err != nil {
		cfg = m.config
	}

	fn(cfg)

	if err := Save(m.path, cfg); err != nil {
		return err
	}
	m.config = cfg
	return nil
}

// DefaultConfigPath returns the platform-appropriate config file path.
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "lazycvs", "config.toml")
}

// DefaultLogPath returns the session-log path, next to the config file.
func DefaultLogPath() string {
	return filepath.Join(filepath.Dir(DefaultConfigPath()), "lazycvs.log")
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		CVS: CVSConfig{
			Binary:       "cvs",
			DefaultFlags: []string{"-q"},
			Timeout:      60,
		},
		Ignore: IgnoreConfig{
			SyncCvsignore:  true,
			GlobalPatterns: []string{".DS_Store"},
		},
	}
}

// Load reads a config file, creating it with defaults if it doesn't exist.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create with defaults
			if err := Save(path, cfg); err != nil {
				return cfg, nil // return defaults even if save fails
			}
			return cfg, nil
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes config to a TOML file, creating parent directories as needed.
func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(cfg)
}

// SyncGlobalIgnore syncs config patterns to ~/.cvsignore.
func SyncGlobalIgnore(patterns []string) error {
	if len(patterns) == 0 {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cvsignorePath := filepath.Join(home, ".cvsignore")
	existing := make(map[string]bool)

	data, err := os.ReadFile(cvsignorePath)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				existing[line] = true
			}
		}
	}

	// Add missing patterns
	var added []string
	for _, p := range patterns {
		if !existing[p] {
			added = append(added, p)
		}
	}

	if len(added) == 0 {
		return nil
	}

	// Append to file
	f, err := os.OpenFile(cvsignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, p := range added {
		f.WriteString(p + "\n")
	}

	return nil
}

// DiffToolPresets maps preset names to command templates. The placeholders
// $LOCAL and $SERVER (or $LEFT and $RIGHT — both work) are replaced with
// absolute file paths at launch time.
var DiffToolPresets = map[string]string{
	"vscode":   "code --diff $LEFT $RIGHT",
	"emacs":    "emacs --eval (ediff-files \"$LEFT\" \"$RIGHT\")",
	"vimdiff":  "vimdiff $LEFT $RIGHT",
	"meld":     "meld $LEFT $RIGHT",
	"opendiff": "opendiff $LEFT $RIGHT",
	"kdiff3":   "kdiff3 $LEFT $RIGHT",
	"diffuse":  "diffuse $LEFT $RIGHT",
	"bcompare": "bcompare $LEFT $RIGHT",
}

// MergeToolPresets maps preset names to 3-way merge command templates.
// Placeholders: $BASE, $LOCAL, $REMOTE, $MERGED. The MERGED file is the
// working copy on disk — most tools save the resolved result back to it.
var MergeToolPresets = map[string]string{
	"meld":     "meld --auto-merge $LOCAL $BASE $REMOTE --output $MERGED",
	"kdiff3":   "kdiff3 $BASE $LOCAL $REMOTE -o $MERGED",
	"vimdiff":  "vimdiff $LOCAL $BASE $REMOTE $MERGED",
	"vscode":   "code --merge $LOCAL $REMOTE $BASE $MERGED",
	"opendiff": "opendiff $LOCAL $REMOTE -ancestor $BASE -merge $MERGED",
	"diffuse":  "diffuse $LOCAL $BASE $REMOTE",
	"bcompare": "bcompare $LOCAL $REMOTE $BASE $MERGED",
}

// IgnorePresets maps preset names to lists of ignore patterns.
var IgnorePresets = map[string][]string{
	"latex": {
		"*.aux", "*.bbl", "*.blg", "*.fdb_latexmk", "*.fls",
		"*.log", "*.out", "*.synctex.gz", "*.toc", "*.lof",
		"*.lot", "*.nav", "*.snm", "*.vrb", "*.run.xml",
		"*.bcf",
	},
	"macos": {
		".DS_Store", "._*", ".Spotlight-V100", ".Trashes",
	},
	"editor": {
		"*.swp", "*.swo", "*~", ".*.swp", "#*#", ".#*",
	},
	"python": {
		"__pycache__", "*.pyc", "*.pyo", ".venv", "*.egg-info",
	},
	"build": {
		"*.o", "*.a", "*.so", "*.dylib", "*.exe",
	},
}
