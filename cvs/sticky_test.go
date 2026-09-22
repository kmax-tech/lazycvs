package cvs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSticky(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" means: write no CVS/Tag at all
		want    Sticky
		label   string
	}{
		{"no tag file is the trunk", "", Sticky{}, "MAIN"},
		{"branch tag", "Trelease-2\n", Sticky{Name: "release-2"}, "release-2"},
		{"non-branch tag", "Nv1_0\n", Sticky{Name: "v1_0", NonBranch: true}, "v1_0"},
		{"sticky date", "D2026-01-31 12:00:00\n",
			Sticky{Name: "2026-01-31 12:00:00", Date: true}, "@2026-01-31 12:00:00"},
		{"empty file is the trunk", "\n", Sticky{}, "MAIN"},
		{"an unknown kind keeps the whole line", "xweird", Sticky{Name: "xweird"}, "xweird"},
		{"trailing whitespace is trimmed", "T  spaced  \n", Sticky{Name: "spaced"}, "spaced"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "CVS"), 0755); err != nil {
				t.Fatal(err)
			}
			if c.content != "" {
				if err := os.WriteFile(filepath.Join(dir, "CVS", "Tag"), []byte(c.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got := ReadSticky(dir)
			if got != c.want {
				t.Errorf("ReadSticky = %+v, want %+v", got, c.want)
			}
			if got.Label() != c.label {
				t.Errorf("Label = %q, want %q", got.Label(), c.label)
			}
			if wantTrunk := c.want.Name == ""; got.OnTrunk() != wantTrunk {
				t.Errorf("OnTrunk = %v, want %v", got.OnTrunk(), wantTrunk)
			}
		})
	}
}

// A directory without CVS metadata at all must read as the trunk, not
// as an error: CVS draws the same conclusion, and the UI has nothing
// useful to say about it.
func TestReadStickyOnUnmanagedDir(t *testing.T) {
	if got := ReadSticky(t.TempDir()); got != (Sticky{}) {
		t.Errorf("ReadSticky on a plain directory = %+v, want the zero value", got)
	}
}
