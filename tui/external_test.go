package tui

import "testing"

func TestStripCheckoutHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "typical cvs co -p header",
			in: "===================================================================\n" +
				"Checking out research-in-progress/foo/bar.tex\n" +
				"RCS:  /srv/cvsroot/research-in-progress/foo/bar.tex,v\n" +
				"VERS: 1.1\n" +
				"***************\n" +
				"\\section{Introduction}\n" +
				"Body content here.\n",
			want: "\\section{Introduction}\nBody content here.\n",
		},
		{
			name: "no header at all (already-stripped content)",
			in:   "\\section{Introduction}\nNo metadata before this.\n",
			want: "\\section{Introduction}\nNo metadata before this.\n",
		},
		{
			name: "content line starting with '*' must not be stripped",
			in:   "* item one\n* item two\n",
			want: "* item one\n* item two\n",
		},
		{
			name: "content line starting with '**' (bold) must not be stripped",
			in:   "**bold** Markdown line\nnormal line\n",
			want: "**bold** Markdown line\nnormal line\n",
		},
		{
			name: "separator with trailing whitespace",
			in:   "Checking out foo\nRCS: bar\nVERS: 1.2\n***************   \nactual content\n",
			want: "actual content\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := stripCheckoutHeader(tc.in)
			if got != tc.want {
				t.Errorf("stripCheckoutHeader\n  input: %q\n  want:  %q\n  got:   %q", tc.in, tc.want, got)
			}
		})
	}
}
