package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmax-tech/lazycvs/cvs"
)

// -update rewrites the golden file instead of comparing against it.
// Run it only when a keybar change is intended, and read the resulting
// diff — that diff is the review.
var updateKeybarGolden = flag.Bool("update", false, "rewrite testdata golden files")

// keybarApp extends the listing fixture with the models renderKeybar
// reaches into (tree, staged, history). newListingTestApp leaves them
// zero, and a zero HistoryModel would claim compareAnchor 0 = anchored.
func newKeybarApp(t *testing.T) *App {
	t.Helper()
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.staged = NewStagedModel()
	app.history = NewHistoryModel()
	app.width = 200
	return app
}

// keybarCase is one rendered state. The name doubles as the section
// header in the golden file, so a diff points at the situation, not at
// a line number.
type keybarCase struct {
	name  string
	setup func(*App)
}

func keybarCases() []keybarCase {
	return []keybarCase{
		{"tree/clean", func(a *App) { a.activeTab = TabTree }},
		{"tree/cursor-on-conflict", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			a.statusMap["conflict.txt"] = "C"
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "conflict.txt", Status: "C"}}, nil)
			a.filelist.cursor = 1 // row 0 is the directory header
		}},
		{"favorites", func(a *App) { a.activeTab = TabFavorites }},
		{"staged/actions", func(a *App) { a.activeTab = TabStaged }},
		{"staged/commit-input", func(a *App) {
			a.activeTab = TabStaged
			a.focus = PanelRight
			a.staged.mode = StagedCommit
			a.staged.input.Focus()
		}},
		{"history/revisions", func(a *App) {
			a.activeTab = TabHistory
			a.history.path = "top.txt"
			a.history.revisions = []cvs.Revision{{Number: "1.2"}, {Number: "1.1"}}
		}},
		{"history/diff", func(a *App) {
			a.activeTab = TabHistory
			a.history.path = "top.txt"
			a.history.revisions = []cvs.Revision{{Number: "1.2"}, {Number: "1.1"}}
			a.history.mode = HistoryDiff
		}},
		{"history/compare-pair", func(a *App) {
			a.activeTab = TabHistory
			a.history.path = "top.txt"
			a.history.revisions = []cvs.Revision{{Number: "1.2"}, {Number: "1.1"}}
			a.history.mode = HistoryDiff
			a.history.compareAnchor = 1
			a.history.cursor = 0
		}},
		{"tree/narrow-80", func(a *App) {
			a.activeTab = TabTree
			a.width = 80
		}},
	}
}

func renderKeybarCases(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, tc := range keybarCases() {
		app := newKeybarApp(t)
		tc.setup(app)
		b.WriteString("## " + tc.name + "\n")
		b.WriteString(app.renderKeybar() + "\n\n")
	}
	return b.String()
}

// The keybar's wording is the user-visible contract. The registry
// rewrite (S1) changes how these rows are assembled and must not
// change what they say — this test is what makes "no visible change"
// checkable instead of claimed.
func TestKeybarGolden(t *testing.T) {
	got := renderKeybarCases(t)
	golden := filepath.Join("testdata", "keybar.golden")

	if *updateKeybarGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run: go test ./tui/ -run TestKeybarGolden -update)", err)
	}
	if string(want) != got {
		t.Errorf("keybar output changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// The help modal is the second surface that describes the keymap, and
// the one that drifted in issue 021. Same contract as the keybar: S1
// changes where the rows come from, not what they say.
func TestHelpGolden(t *testing.T) {
	got := buildHelpContent()
	golden := filepath.Join("testdata", "help.golden")

	if *updateKeybarGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run: go test ./tui/ -run Golden -update)", err)
	}
	if string(want) != got {
		t.Errorf("help output changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
