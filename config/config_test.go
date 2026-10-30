package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDefaultIgnorePatternsCoverLatexAndMacos(t *testing.T) {
	got := DefaultConfig().Ignore.GlobalPatterns
	for _, want := range []string{".DS_Store", "*.aux", "*.blg", "*.fdb_latexmk", "*.fls", "*.log", "*.out"} {
		if !slices.Contains(got, want) {
			t.Errorf("default global_patterns lack %q: %v", want, got)
		}
	}
}

func TestLoadWithoutGlobalPatternsKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeFile(path, "[ignore]\n  sync_cvsignore = true\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Equal(cfg.Ignore.GlobalPatterns, DefaultIgnorePatterns()) {
		t.Errorf("global_patterns = %v, want defaults %v", cfg.Ignore.GlobalPatterns, DefaultIgnorePatterns())
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
