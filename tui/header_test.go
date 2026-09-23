package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kmax-tech/lazycvs/cvs"
)

func TestRefreshAge(t *testing.T) {
	cases := []struct {
		since time.Duration
		want  string
	}{
		{0, "now"},
		{59 * time.Second, "now"},
		{time.Minute, "1m"},
		{90 * time.Second, "1m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{23 * time.Hour, "23h"},
		{25 * time.Hour, "1d"},
		{72 * time.Hour, "3d"},
	}
	for _, c := range cases {
		if got := refreshAge(c.since); got != c.want {
			t.Errorf("refreshAge(%s) = %q, want %q", c.since, got, c.want)
		}
	}
}

// The header gives up its parts in order of what their absence costs:
// the age first, the module second, the branch never — being on the
// wrong branch is the one that costs you an afternoon.
func TestHeaderContextDropsTheCheapestPartFirst(t *testing.T) {
	sticky := cvs.Sticky{Name: "release-2"}

	full := ansi.Strip(headerContext("myproject", sticky, "3m", 60))
	for _, want := range []string{"myproject", "release-2", "3m"} {
		if !strings.Contains(full, want) {
			t.Errorf("wide header lost %q: %q", want, full)
		}
	}

	noAge := ansi.Strip(headerContext("myproject", sticky, "3m", 24))
	if strings.Contains(noAge, "3m") {
		t.Errorf("the age should go first: %q", noAge)
	}
	if !strings.Contains(noAge, "myproject") || !strings.Contains(noAge, "release-2") {
		t.Errorf("dropped too much at width 24: %q", noAge)
	}

	branchOnly := ansi.Strip(headerContext("myproject", sticky, "3m", 12))
	if !strings.Contains(branchOnly, "release-2") {
		t.Errorf("the branch must survive as long as anything does: %q", branchOnly)
	}
	if strings.Contains(branchOnly, "myproject") {
		t.Errorf("the module should be gone at width 12: %q", branchOnly)
	}

	if got := headerContext("myproject", sticky, "3m", 4); got != "" {
		t.Errorf("with no room for the branch, show nothing: %q", ansi.Strip(got))
	}
}

func TestHeaderContextFitsItsWidth(t *testing.T) {
	for _, width := range []int{8, 12, 20, 30, 45, 60, 100} {
		got := headerContext("some/module/path", cvs.Sticky{Name: "release-2"}, "15m", width)
		if w := lipgloss.Width(got); w > width {
			t.Errorf("width %d: header is %d cells: %q", width, w, ansi.Strip(got))
		}
	}
}

// On the trunk the header stays quiet; on a sticky tag it does not,
// and a tag you cannot commit to says so before you write the code.
func TestHeaderContextMarksTheUncommittable(t *testing.T) {
	trunk := ansi.Strip(headerContext("m", cvs.Sticky{}, "", 40))
	if !strings.Contains(trunk, "MAIN") {
		t.Errorf("trunk header should name the trunk: %q", trunk)
	}
	if strings.Contains(trunk, "no-commit") {
		t.Errorf("the trunk is committable: %q", trunk)
	}

	tagged := ansi.Strip(headerContext("m", cvs.Sticky{Name: "v1_0", NonBranch: true}, "", 40))
	if !strings.Contains(tagged, "v1_0") || !strings.Contains(tagged, "no-commit") {
		t.Errorf("a non-branch tag should warn: %q", tagged)
	}
}

// CVS pins stickiness per directory, so the header has to follow the
// directory on screen — a subdirectory on a branch inside a trunk
// checkout is exactly the case nothing else on screen reveals.
func TestStickyFollowsTheShownDirectory(t *testing.T) {
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)

	tagFile := filepath.Join(app.exec.WorkDir, "sub", "CVS", "Tag")
	if err := os.WriteFile(tagFile, []byte("Trelease-2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	app.updateFileListForDir(".")
	if !app.sticky.OnTrunk() {
		t.Errorf("root should read as the trunk, got %+v", app.sticky)
	}

	app.updateFileListForDir("sub")
	if app.sticky.Label() != "release-2" {
		t.Errorf("subdirectory on a branch reads as %q, want release-2", app.sticky.Label())
	}

	app.updateFileListForDir(".")
	if !app.sticky.OnTrunk() {
		t.Errorf("walking back to the root should read as the trunk, got %+v", app.sticky)
	}
}

// Before the first whole-tree check the age slot stays empty: "0m"
// would claim a check that never happened.
func TestRefreshAgeIsEmptyUntilTheFirstCheck(t *testing.T) {
	app := newListingTestApp(t)
	if got := app.refreshAge(); got != "" {
		t.Errorf("age before any refresh = %q, want empty", got)
	}
	app.statusAt = time.Now().Add(-5 * time.Minute)
	if got := app.refreshAge(); got != "5m" {
		t.Errorf("age = %q, want 5m", got)
	}
}

// "Nothing yet" and "nothing, and that is the answer" used to render
// identically: a file CVS does not track sat on "Loading revisions..."
// forever, because zero rows was read as pending.
func TestHistoryDistinguishesPendingFromEmpty(t *testing.T) {
	h := NewHistoryModel()
	h.SetSize(PanelDims{LeftW: 40, RightW: 60, Height: 20})
	h.SwitchTo("scratch.log")

	pending := ansi.Strip(h.ViewLeft())
	if !strings.Contains(pending, "Loading revisions") {
		t.Errorf("before the log arrives the pane should say so: %q", pending)
	}

	h.MarkLoaded("Not in CVS — no revisions yet. Press a in the Files tab to add it.")
	done := ansi.Strip(h.ViewLeft())
	if strings.Contains(done, "Loading") {
		t.Errorf("an empty answer must not read as pending: %q", done)
	}
	if !strings.Contains(done, "Not in CVS") {
		t.Errorf("the empty pane should explain itself: %q", done)
	}
	for _, line := range strings.Split(done, "\n") {
		if lipgloss.Width(line) > 40 {
			t.Errorf("explanation runs past the pane: %q", line)
		}
	}

	// The right pane has nothing to add in that state.
	if got := ansi.Strip(h.ViewRight()); strings.Contains(got, "Loading") {
		t.Errorf("right pane still claims to be loading: %q", got)
	}

	// Switching files starts the cycle over.
	h.SwitchTo("other.txt")
	if again := ansi.Strip(h.ViewLeft()); !strings.Contains(again, "Loading revisions") {
		t.Errorf("a new file starts pending again: %q", again)
	}
}

// The three empty cases have three different next steps, so they get
// three different sentences.
func TestHistoryEmptyNoteFollowsTheStatus(t *testing.T) {
	app := newListingTestApp(t)
	// The statuses the scan would have put there.
	app.statusMap["untracked.txt"] = "?"
	app.statusMap["added.txt"] = "A"
	app.statusMap["tracked.txt"] = "M"

	if got := app.historyEmptyNote("untracked.txt"); !strings.Contains(got, "Not in CVS") {
		t.Errorf("untracked note = %q", got)
	}
	if got := app.historyEmptyNote("added.txt"); !strings.Contains(got, "not committed") {
		t.Errorf("added note = %q", got)
	}
	if got := app.historyEmptyNote("tracked.txt"); !strings.Contains(got, "Console") {
		t.Errorf("unexplained note should point at the console: %q", got)
	}
}

// The right pane had the same confusion as the revision list: an empty
// answer read as a pending one. An empty file's content is "", and it
// sat on "Loading diff..." forever.
func TestHistoryRightPaneShowsEmptyResults(t *testing.T) {
	h := NewHistoryModel()
	h.SetSize(PanelDims{LeftW: 40, RightW: 60, Height: 20})
	h.SwitchTo("empty.txt")
	h.ApplyRevisions(&cvs.FileHistory{
		Path:      "empty.txt",
		Revisions: []cvs.Revision{{Number: "1.1"}},
	}, false, "1.1")
	h.MarkLoaded("")

	if got := ansi.Strip(h.ViewRight()); !strings.Contains(got, "Loading") {
		t.Fatalf("before the content arrives it is pending: %q", got)
	}

	h.ApplyContent("1.1", "") // an empty file's content
	got := ansi.Strip(h.ViewRight())
	if strings.Contains(got, "Loading") {
		t.Errorf("an empty result must not read as pending: %q", got)
	}
	if !strings.Contains(got, "(empty)") {
		t.Errorf("the pane should say the result was empty: %q", got)
	}
}

// "see Console" is only an answer when the console has something to
// say. A refusal that never reached cvs — a path escaping the working
// copy — left it empty, and the banner sent the user there anyway.
func TestActionFailureCarriesTheReason(t *testing.T) {
	cases := []struct {
		name  string
		what  string
		label string
		err   error
		want  string
	}{
		{"no error falls back to the console", "Remove", "a.txt", nil,
			"✗ Remove failed for a.txt — see Console"},
		{"the reason replaces the pointer", "Action", "a.txt", errors.New("disk full"),
			"✗ Action failed for a.txt: disk full"},
		{"a reason naming the file does not stutter", "Remove", "escape-link.txt",
			errors.New("path escapes working copy: escape-link.txt"),
			"✗ Remove failed: path escapes working copy: escape-link.txt"},
		{"only the first line", "Action", "a.txt", errors.New("first line\nsecond line"),
			"✗ Action failed for a.txt: first line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := actionFailure(c.what, c.label, c.err); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}

	long := errors.New(strings.Repeat("x", 200))
	if got := actionFailure("Action", "a.txt", long); ansi.StringWidth(got) > 110 {
		t.Errorf("a long cvs error should be cut, got %d cells", ansi.StringWidth(got))
	}

	// The cut lands on a character boundary, not inside an umlaut:
	// 77 bytes into this reason is the middle of the "ö".
	umlaut := errors.New("path escapes working copy: docs/" + strings.Repeat("x", 40) + "Größe.txt")
	if got := actionFailure("Remove", "Größe.txt", umlaut); !utf8.ValidString(got) {
		t.Errorf("the cut split a character: %q", got)
	}

	if got := pathsLabel([]string{"docs/a.txt"}); got != "a.txt" {
		t.Errorf("one path is named, got %q", got)
	}
	if got := pathsLabel([]string{"a", "b"}); got != "2 files" {
		t.Errorf("several paths are counted, got %q", got)
	}
}
