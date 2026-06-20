package cvs

import (
	"reflect"
	"testing"
)

func TestParseModules(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []Module
	}{
		{
			name: "simple modules without options",
			in: `mymodule mymodule
other other/path
`,
			want: []Module{
				{Name: "mymodule", CheckoutDir: "mymodule", Definition: "mymodule"},
				{Name: "other", CheckoutDir: "other", Definition: "other/path"},
			},
		},
		{
			name: "-d alias overrides checkout directory",
			in:   `foo -d bar real/path`,
			want: []Module{
				{Name: "foo", CheckoutDir: "bar", Definition: "-d bar real/path"},
			},
		},
		{
			name: "comment lines and blanks are skipped",
			in: `# This file lists module aliases.

mymodule mymodule
   # leading-whitespace comment
other other
`,
			want: []Module{
				{Name: "mymodule", CheckoutDir: "mymodule", Definition: "mymodule"},
				{Name: "other", CheckoutDir: "other", Definition: "other"},
			},
		},
		{
			name: "mixed options preserve -d alias precedence",
			in:   `CVSROOT -i mkmodules CVSROOT`,
			want: []Module{
				// no -d: CheckoutDir falls back to Name
				{Name: "CVSROOT", CheckoutDir: "CVSROOT", Definition: "-i mkmodules CVSROOT"},
			},
		},
		{
			name: "name only (defect line) does not panic",
			in:   `solitary`,
			want: []Module{
				{Name: "solitary", CheckoutDir: "solitary", Definition: ""},
			},
		},
		{
			name: "trailing -d with no alias keeps Name as CheckoutDir",
			in:   `incomplete -d`,
			want: []Module{
				// "-d" is the last token, lookahead would be out of bounds —
				// the loop's `i < len-1` guard skips it, CheckoutDir stays Name.
				{Name: "incomplete", CheckoutDir: "incomplete", Definition: "-d"},
			},
		},
		{
			name: "verbatim cvs co -c capture",
			in: `CVSROOT      -i mkmodules CVSROOT
research-in-progress  research-in-progress
experiments       experiments
archive -d old-archive archive/v1
`,
			want: []Module{
				{Name: "CVSROOT", CheckoutDir: "CVSROOT", Definition: "-i mkmodules CVSROOT"},
				{Name: "research-in-progress", CheckoutDir: "research-in-progress", Definition: "research-in-progress"},
				{Name: "experiments", CheckoutDir: "experiments", Definition: "experiments"},
				{Name: "archive", CheckoutDir: "old-archive", Definition: "-d old-archive archive/v1"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseModules(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseModules\n  input: %q\n  got:  %#v\n  want: %#v", tc.in, got, tc.want)
			}
		})
	}
}
