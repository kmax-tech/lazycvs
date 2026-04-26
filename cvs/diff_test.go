package cvs

import "testing"

func TestParseDiff(t *testing.T) {
	input := `Index: report.tex
===================================================================
RCS file: /srv/cvsroot/code-in-progress/report.tex,v
retrieving revision 1.1
diff -u -r1.1 report.tex
--- report.tex	1 Jan 2024 00:00:00 -0000	1.1
+++ report.tex	2 Jan 2024 00:00:00 -0000
@@ -1,5 +1,6 @@
 \documentclass{article}
 \begin{document}
-\title{Old Title}
+\title{New Title}
+\date{2024}
 \author{Test}
 \end{document}
`
	result := ParseDiff(input)

	if len(result.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(result.Hunks))
	}

	hunk := result.Hunks[0]
	if hunk.OldStart != 1 || hunk.OldCount != 5 {
		t.Errorf("old range: got %d,%d, want 1,5", hunk.OldStart, hunk.OldCount)
	}
	if hunk.NewStart != 1 || hunk.NewCount != 6 {
		t.Errorf("new range: got %d,%d, want 1,6", hunk.NewStart, hunk.NewCount)
	}

	// Count line types
	var added, removed, context int
	for _, line := range hunk.Lines {
		switch line.Type {
		case DiffLineAdded:
			added++
		case DiffLineRemoved:
			removed++
		case DiffLineContext:
			context++
		}
	}

	if added != 2 {
		t.Errorf("added lines: got %d, want 2", added)
	}
	if removed != 1 {
		t.Errorf("removed lines: got %d, want 1", removed)
	}
	if context < 3 {
		t.Errorf("context lines: got %d, want at least 3", context)
	}
}

func TestBuildSideBySide(t *testing.T) {
	hunks := []DiffHunk{
		{
			Header:   "@@ -1,3 +1,3 @@",
			OldStart: 1, OldCount: 3,
			NewStart: 1, NewCount: 3,
			Lines: []DiffLine{
				{Type: DiffLineContext, Content: "line 1", OldNum: 1, NewNum: 1},
				{Type: DiffLineRemoved, Content: "old line 2", OldNum: 2},
				{Type: DiffLineAdded, Content: "new line 2", NewNum: 2},
				{Type: DiffLineContext, Content: "line 3", OldNum: 3, NewNum: 3},
			},
		},
	}

	pairs := BuildSideBySide(hunks)

	// First pair is hunk header
	if len(pairs) < 4 {
		t.Fatalf("expected at least 4 pairs, got %d", len(pairs))
	}

	// Second pair: context line 1
	if pairs[1].Left == nil || pairs[1].Right == nil {
		t.Fatal("context pair should have both sides")
	}

	// Third pair: removed/added paired
	if pairs[2].Left == nil || pairs[2].Right == nil {
		t.Fatal("removed/added pair should have both sides")
	}
	if pairs[2].Left.Type != DiffLineRemoved {
		t.Errorf("left should be removed, got %s", pairs[2].Left.Type)
	}
	if pairs[2].Right.Type != DiffLineAdded {
		t.Errorf("right should be added, got %s", pairs[2].Right.Type)
	}
}
