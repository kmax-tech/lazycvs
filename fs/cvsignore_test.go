package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendIgnoreEntries(t *testing.T) {
	tests := []struct {
		name     string
		existing *string // nil = file does not exist
		entries  []string
		want     string
	}{
		{"missing file is created", nil, []string{"*.aux"}, "*.aux\n"},
		{"empty file", strp(""), []string{"*.aux", "*.log"}, "*.aux\n*.log\n"},
		{"trailing newline kept as is", strp("*.iml\n"), []string{".DS_Store"}, "*.iml\n.DS_Store\n"},
		{"missing trailing newline is added", strp("*.iml"), []string{".DS_Store"}, "*.iml\n.DS_Store\n"},
		{"no entries leaves file untouched", strp("*.iml"), nil, "*.iml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".cvsignore")
			if tt.existing != nil {
				if err := os.WriteFile(path, []byte(*tt.existing), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := AppendIgnoreEntries(path, tt.entries...); err != nil {
				t.Fatalf("AppendIgnoreEntries: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("file = %q, want %q", got, tt.want)
			}
		})
	}
}

func strp(s string) *string { return &s }
