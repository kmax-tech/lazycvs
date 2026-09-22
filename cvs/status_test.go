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

func TestParseStatusInDirRealWorldOutput(t *testing.T) {
	// Verbatim output from `cvs status ijcai26-fallacy-detection-appendix`
	// in a real working copy after `cvs remove -f` on every file. Every
	// row should come back prefixed with the scope dir, not the bare
	// basename — that's the contract loadDirStatus relies on.
	output := `
cvs status: Examining ijcai26-fallacy-detection-appendix
===================================================================
File: no file figure-benevolent-person.tex		Status: Locally Removed

   Working revision:	-1.1
   Repository revision:	1.1	/srv/cvsroot/research-in-progress/argumentation/IJCAI-26/ijcai26-fallacy-detection-paper-submitted/ijcai26-fallacy-detection-appendix/figure-benevolent-person.tex,v
   Commit Identifier:	100696F843EBFD62125
   Sticky Tag:		(none)

===================================================================
File: no file figure-critical-persona.tex		Status: Locally Removed

   Working revision:	-1.1
   Repository revision:	1.1	/srv/cvsroot/.../figure-critical-persona.tex,v
   Sticky Tag:		(none)

===================================================================
File: no file table-example-arguments.tex		Status: Locally Removed

   Working revision:	-1.1
   Repository revision:	1.1	/srv/cvsroot/.../table-example-arguments.tex,v
`
	got := ParseStatusInDir(output, "ijcai26-fallacy-detection-appendix")
	if len(got) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(got))
	}
	wantPaths := []string{
		"ijcai26-fallacy-detection-appendix/figure-benevolent-person.tex",
		"ijcai26-fallacy-detection-appendix/figure-critical-persona.tex",
		"ijcai26-fallacy-detection-appendix/table-example-arguments.tex",
	}
	for i, want := range wantPaths {
		if got[i].Path != want {
			t.Errorf("entry %d: expected Path=%q, got %q", i, want, got[i].Path)
		}
		if got[i].Status != "Locally Removed" {
			t.Errorf("entry %d: expected Status=Locally Removed, got %q", i, got[i].Status)
		}
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

// CVS separates the file name from "Status:" with a tab, and file
// names contain spaces. Parsing the name as \S+ stopped at the first
// space, so a file called "Zusammenfassung Größe.txt" never got a
// status recorded under its real path: the listing showed it clean
// while cvs considered it modified, and the diff pane said "No local
// changes" about a file with local changes.
func TestParseStatusNamesWithSpaces(t *testing.T) {
	// A whole status block, as cvs prints it — the parser only emits
	// an entry once the repository-revision line has arrived.
	block := func(fileLine string) string {
		return "===================================================================\n" +
			fileLine + "\n\n" +
			"   Working revision:\t1.3\t2026-03-01 09:42:09 +0000\n" +
			"   Repository revision:\t1.3\t/cvsroot/proj/x,v\n"
	}

	cases := []struct {
		name     string
		fileLine string
		wantPath string
		wantStat string
	}{
		{"plain name", "File: main.go\tStatus: Up-to-date", "main.go", "Up-to-date"},
		{"name with a space", "File: Zusammenfassung Größe.txt\tStatus: Locally Modified",
			"Zusammenfassung Größe.txt", "Locally Modified"},
		{"several spaces", "File: a b c.txt\tStatus: Locally Modified", "a b c.txt", "Locally Modified"},
		{"missing file with a space", "File: no file a b.txt\tStatus: Locally Removed",
			"a b.txt", "Locally Removed"},
		{"missing file, plain", "File: no file util.go\t\tStatus: Needs Checkout",
			"util.go", "Needs Checkout"},
		{"padded with spaces", "File: main.go       \tStatus: Up-to-date", "main.go", "Up-to-date"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseStatus(block(c.fileLine))
			if len(got) != 1 {
				t.Fatalf("parsed %d entries, want 1: %+v", len(got), got)
			}
			if got[0].Path != c.wantPath {
				t.Errorf("path = %q, want %q", got[0].Path, c.wantPath)
			}
			if got[0].Status != c.wantStat {
				t.Errorf("status = %q, want %q", got[0].Status, c.wantStat)
			}
		})
	}
}

// A directory whose name contains spaces prefixes its files correctly.
func TestParseStatusDirWithSpaces(t *testing.T) {
	out := "cvs status: Examining my docs\n" +
		"===================================================================\n" +
		"File: some file.txt\tStatus: Locally Modified\n\n" +
		"   Working revision:\t1.1\t2026-03-01 09:42:09 +0000\n" +
		"   Repository revision:\t1.1\t/cvsroot/proj/x,v\n"
	got := ParseStatus(out)
	if len(got) != 1 || got[0].Path != "my docs/some file.txt" {
		t.Fatalf("got %+v, want my docs/some file.txt", got)
	}
}
