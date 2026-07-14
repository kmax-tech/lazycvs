package cvs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePath(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "sub", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Sibling dir with the workdir as name prefix — must NOT pass.
	sibling := work + "-evil"
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the working copy pointing outside of it.
	if err := os.Symlink(sibling, filepath.Join(work, "escape")); err != nil {
		t.Fatal(err)
	}

	e := &CVSExecutor{WorkDir: work}
	tests := []struct {
		path string
		ok   bool
	}{
		{"sub/f.txt", true},
		{".", true},
		{"sub", true},
		{"new-file.txt", true},       // may not exist yet (add)
		{"", false},                  // empty
		{"/etc/passwd", false},       // absolute
		{"../outside", false},        // traversal
		{"sub/../../outside", false}, // sneaky traversal
		{"escape/loot.txt", false},   // symlink escape
	}
	for _, tt := range tests {
		err := e.ValidatePath(tt.path)
		if tt.ok && err != nil {
			t.Errorf("ValidatePath(%q) = %v, want ok", tt.path, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidatePath(%q) = ok, want error", tt.path)
		}
	}
}
