package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindWorkingCopy(t *testing.T) {
	tmp := t.TempDir()
	// Layout:
	//   tmp/                        (no CVS)
	//     repo/                     (has CVS)
	//       CVS/Root
	//       sub/                    (no CVS — new dir)
	//         deeper/               (no CVS)
	//     elsewhere/                (no CVS)
	mustMk := func(rel string) string {
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	repo := mustMk("repo")
	mustMk("repo/CVS")
	if err := os.WriteFile(filepath.Join(repo, "CVS", "Root"), []byte(":local:/srv/cvsroot"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := mustMk("repo/sub")
	deeper := mustMk("repo/sub/deeper")
	elsewhere := mustMk("elsewhere")

	for _, tc := range []struct {
		name     string
		start    string
		wantRoot string
		wantRel  string
		wantOK   bool
	}{
		{"sub dir under repo finds repo", sub, repo, "sub", true},
		{"two levels deep also finds repo", deeper, repo, "sub/deeper", true},
		{"repo itself returns root with empty rel", repo, repo, "", true},
		{"dir outside any working copy returns false", elsewhere, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, gotRel, gotOK := findWorkingCopy(tc.start)
			if gotOK != tc.wantOK || gotRoot != tc.wantRoot || gotRel != tc.wantRel {
				t.Errorf("findWorkingCopy(%q)\n  got  (%q, %q, %v)\n  want (%q, %q, %v)",
					tc.start, gotRoot, gotRel, gotOK, tc.wantRoot, tc.wantRel, tc.wantOK)
			}
		})
	}
}
