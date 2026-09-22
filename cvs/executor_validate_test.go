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

// A file whose name starts with a dash is a path, not a flag — but cvs
// reads it as one ("add: illegal option -- d") and does nothing. The
// executor prefixes it so cvs sees the path.
func TestDashPathArgs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"-dashfile.txt", "--double.txt", "normal.txt", "-u"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"a dash-named file that exists",
			[]string{"add", "-dashfile.txt"}, []string{"add", "./-dashfile.txt"}},
		{"a double-dash name",
			[]string{"add", "--double.txt"}, []string{"add", "./--double.txt"}},
		{"real flags are left alone",
			[]string{"diff", "-u", "-r", "1.1", "normal.txt"},
			[]string{"diff", "-u", "-r", "1.1", "normal.txt"}},
		{"a long flag that is not a file stays a flag",
			[]string{"update", "-dP"}, []string{"update", "-dP"}},
		{"the subcommand is never touched",
			[]string{"-dashfile.txt"}, []string{"-dashfile.txt"}},
		{"a short name matching a flag is left to cvs",
			[]string{"add", "-u"}, []string{"add", "-u"}},
		{"paths that do not exist are left alone",
			[]string{"add", "-gone.txt"}, []string{"add", "-gone.txt"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dashPathArgs(dir, c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("arg %d = %q, want %q (full: %v)", i, got[i], c.want[i], got)
				}
			}
		})
	}
}
