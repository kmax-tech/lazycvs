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

func TestParseStatusInDirAppliesFallbackContext(t *testing.T) {
	// `cvs status -l <subdir>` on a dir containing only Locally Removed
	// files sometimes omits the "Examining <dir>" line. Without a
	// fallback the parsed paths would be bare basenames and the UI
	// would surface them in the parent dir's listing instead of the
	// scoped subdir's. ParseStatusInDir is the contract that loadDirStatus
	// relies on to keep R rows attached to the right place.
	output := `===================================================================
File: no file figure-neutral-persona.tex	Status: Locally Removed

   Working revision:	-1.2	2026-03-01 09:42:09 +0000
   Repository revision:	1.2	/cvsroot/proj/ijcai26-fallacy-detection-appendix/figure-neutral-persona.tex,v
`
	got := ParseStatusInDir(output, "ijcai26-fallacy-detection-appendix")
	if len(got) != 1 {
		t.Fatalf("expected 1 status, got %d", len(got))
	}
	want := "ijcai26-fallacy-detection-appendix/figure-neutral-persona.tex"
	if got[0].Path != want {
		t.Errorf("expected Path=%q, got %q", want, got[0].Path)
	}
}

func TestParseStatusInDirRespectsExaminingOverride(t *testing.T) {
	// When the Examining line IS present, it overrides the fallback.
	// Guards against accidentally double-prefixing or ignoring sub-walks.
	output := `cvs status: Examining nested
===================================================================
File: foo.tex	Status: Locally Modified

   Working revision:	1.3	2026-03-01 09:42:09 +0000
   Repository revision:	1.3	/cvsroot/proj/scope/nested/foo.tex,v
`
	got := ParseStatusInDir(output, "scope")
	if len(got) != 1 {
		t.Fatalf("expected 1 status, got %d", len(got))
	}
	if got[0].Path != "nested/foo.tex" {
		t.Errorf("expected Path=nested/foo.tex, got %q", got[0].Path)
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
