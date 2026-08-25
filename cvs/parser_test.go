package cvs

import "testing"

func TestParseUpdate(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		stderr    string
		modified  int
		conflicts int
		untracked int
		updated   int
		stale     int
		inTheWay  int
	}{
		{
			name:      "empty output",
			stdout:    "",
			stderr:    "",
			modified:  0,
			conflicts: 0,
		},
		{
			name:      "modified and untracked",
			stdout:    "M text-files/report.tex\n? ignore-test/.DS_Store\n? ignore-test/temp.aux\n",
			stderr:    "",
			modified:  1,
			untracked: 2,
		},
		{
			name:      "conflict",
			stdout:    "C conflict-test/shared-doc.tex\nM text-files/report.tex\n",
			stderr:    "",
			modified:  1,
			conflicts: 1,
		},
		{
			name:    "updated and patched",
			stdout:  "U text-files/report.tex\nP text-files/analysis.py\n",
			stderr:  "",
			updated: 2,
		},
		{
			name:      "move away",
			stdout:    "C conflict-test/new-server-file.txt\n",
			stderr:    "cvs update: move away `conflict-test/new-server-file.txt'; it is in the way\n",
			conflicts: 1,
			inTheWay:  1,
		},
		{
			name:   "stale directory",
			stdout: "",
			stderr: "cvs update: cannot open directory /srv/cvsroot/stale-test/temporary-dir: No such file or directory\ncvs update: skipping directory stale-test/temporary-dir\n",
			stale:  2,
		},
		{
			name:      "mixed output",
			stdout:    "M report.tex\nC shared.tex\n? .DS_Store\nU new-file.txt\n",
			stderr:    "cvs update: move away `blocker.txt'; it is in the way\n",
			modified:  1,
			conflicts: 1,
			untracked: 1,
			updated:   1,
			inTheWay:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseUpdate(tt.stdout, tt.stderr, "")

			if got := len(result.Modified); got != tt.modified {
				t.Errorf("Modified: got %d, want %d", got, tt.modified)
			}
			if got := len(result.Conflicts); got != tt.conflicts {
				t.Errorf("Conflicts: got %d, want %d", got, tt.conflicts)
			}
			if got := len(result.Untracked); got != tt.untracked {
				t.Errorf("Untracked: got %d, want %d", got, tt.untracked)
			}
			if got := len(result.Updated); got != tt.updated {
				t.Errorf("Updated: got %d, want %d", got, tt.updated)
			}
			if got := len(result.StaleDirs); got != tt.stale {
				t.Errorf("StaleDirs: got %d, want %d", got, tt.stale)
			}
			if got := len(result.InTheWay); got != tt.inTheWay {
				t.Errorf("InTheWay: got %d, want %d", got, tt.inTheWay)
			}
		})
	}
}

func TestParseInTheWay(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single move away",
			in:   "cvs update: move away `tests/foo.txt'; it is in the way\n",
			want: []string{"tests/foo.txt"},
		},
		{
			name: "multiple paths",
			in: "cvs update: move away `a.txt'; it is in the way\n" +
				"cvs update: move away `dir/b.txt'; it is in the way\n",
			want: []string{"a.txt", "dir/b.txt"},
		},
		{
			name: "no move away → nil",
			in:   "cvs update: Updating .\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseInTheWay(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
