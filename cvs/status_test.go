package cvs

import (
	"testing"
)

func TestParseStatusLocallyRemoved(t *testing.T) {
	// `cvs status` after `cvs remove -f` emits "File: no file <name>"
	// — the actual filename is the second token, not the first. If the
	// parser captures "no" the removed file disappears from the UI
	// (statusMap key never matches the real path).
	output := `===================================================================
File: no file figure-bayes-assessment.tex	Status: Locally Removed

   Working revision:	-1.5	2026-03-01 09:42:09 +0000
   Repository revision:	1.5	/cvsroot/proj/figure-bayes-assessment.tex,v
   Sticky Tag:		(none)
   Sticky Date:		(none)
   Sticky Options:	(none)
`

	got := ParseStatus(output)
	if len(got) != 1 {
		t.Fatalf("expected 1 status, got %d", len(got))
	}
	if got[0].Path != "figure-bayes-assessment.tex" {
		t.Errorf("expected Path=figure-bayes-assessment.tex, got %q", got[0].Path)
	}
	if got[0].Status != "Locally Removed" {
		t.Errorf("expected Status=Locally Removed, got %q", got[0].Status)
	}
}

func TestParseStatusNormalFile(t *testing.T) {
	// Regression guard: without the "no file" prefix the parser must
	// still capture the filename as the first token.
	output := `===================================================================
File: foo.txt	Status: Locally Modified

   Working revision:	1.3	2026-03-01 09:42:09 +0000
   Repository revision:	1.3	/cvsroot/proj/foo.txt,v
`
	got := ParseStatus(output)
	if len(got) != 1 {
		t.Fatalf("expected 1 status, got %d", len(got))
	}
	if got[0].Path != "foo.txt" {
		t.Errorf("expected Path=foo.txt, got %q", got[0].Path)
	}
	if got[0].Status != "Locally Modified" {
		t.Errorf("expected Status=Locally Modified, got %q", got[0].Status)
	}
}
