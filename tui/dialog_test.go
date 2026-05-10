package tui

import "testing"

func TestStripCommentLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "single line, no comments",
			in:   "fix bug",
			want: "fix bug",
		},
		{
			name: "comment header stripped",
			in: "fix bug\n\n# Commit message\n# Files:\n#   M foo.go\n",
			want: "fix bug",
		},
		{
			name: "multi-line message with trailing comments",
			in: "subject line\n\nlonger paragraph\nspanning lines\n\n# ignored\n",
			want: "subject line\n\nlonger paragraph\nspanning lines",
		},
		{
			name: "all comments → empty",
			in:   "# only comments\n# nothing else\n",
			want: "",
		},
		{
			name: "leading + trailing whitespace trimmed",
			in:   "\n\n   actual message   \n\n# tail\n",
			want: "actual message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripCommentLines(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
