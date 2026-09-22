package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/kmax-tech/lazycvs/cvs"
)

// -update rewrites the golden file instead of comparing against it.
// Run it only when a keybar change is intended, and read the resulting
// diff — that diff is the review.
var updateKeybarGolden = flag.Bool("update", false, "rewrite testdata golden files")

// keybarApp extends the listing fixture with the models renderKeybar
// reaches into (tree, markedTab, history). newListingTestApp leaves them
// zero, and a zero HistoryModel would claim compareAnchor 0 = anchored.
func newKeybarApp(t *testing.T) *App {
	t.Helper()
	app := newListingTestApp(t)
	app.tree = NewTreeModel(app.exec.WorkDir)
	app.markedTab = NewMarkedTabModel()
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
		{"tree/no-selection", func(a *App) { a.activeTab = TabTree }},
		{"tree/cursor-on-clean-file", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "steady.txt"}}, nil)
			a.filelist.cursor = 1
		}},
		{"tree/cursor-on-conflict", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			a.statusMap["conflict.txt"] = "C"
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "conflict.txt", Status: "C"}}, nil)
			a.filelist.cursor = 1 // row 0 is the directory header
		}},
		{"tree/cursor-on-untracked", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "scratch.log", Status: "?"}}, nil)
			a.filelist.cursor = 1
		}},
		{"tree/cursor-on-directory", func(a *App) {
			// Focus on the tree pane with no file under the cursor:
			// file actions have no target, directory ones do.
			a.activeTab = TabTree
			a.focus = PanelLeft
		}},
		{"tree/three-marked", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			for _, p := range []string{"a.txt", "b.txt", "c.txt"} {
				a.filelist.marked[p] = true
				a.statusMap[p] = "M"
			}
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "a.txt", Status: "M"}}, nil)
			a.filelist.cursor = 1
		}},
		{"favorites", func(a *App) { a.activeTab = TabFavorites }},
		{"marked/empty", func(a *App) { a.activeTab = TabMarked }},
		{"marked/actions", func(a *App) {
			a.activeTab = TabMarked
			a.markedTab.files = []markedFile{
				{path: "a.txt", status: "M"},
				{path: "new.txt", status: "?"},
			}
			a.filelist.marked = map[string]bool{"a.txt": true, "new.txt": true}
			a.statusMap["a.txt"] = "M"
			a.statusMap["new.txt"] = "?"
		}},
		{"marked/commit-input", func(a *App) {
			a.activeTab = TabMarked
			a.focus = PanelRight
			a.markedTab.mode = MarkedCommit
			a.markedTab.input.Focus()
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
			a.focus = PanelRight
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "changed.txt", Status: "M"}}, nil)
			a.filelist.cursor = 1
			a.width = 80
		}},
		{"tree/narrow-60", func(a *App) {
			a.activeTab = TabTree
			a.focus = PanelRight
			a.filelist.SetFiles(".", []cvs.FileEntry{{Path: "changed.txt", Status: "M"}}, nil)
			a.filelist.cursor = 1
			a.width = 60
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

// The width promise: a terminal too narrow for every key loses keys,
// never words. "…" hid the whole [View] group on an 80-column terminal
// with nothing to say that it had; "+7 (?)" names the count and where
// to look.
func TestActionRowDropsKeysInsteadOfCutting(t *testing.T) {
	for _, width := range []int{60, 80, 100, 120, 200} {
		app := newKeybarApp(t)
		app.activeTab = TabTree
		app.focus = PanelRight
		app.filelist.SetFiles(".", []cvs.FileEntry{{Path: "changed.txt", Status: "M"}}, nil)
		app.filelist.cursor = 1
		app.width = width

		ctx := app.keyContext()
		row := app.actionRow(ctx)
		plain := ansi.Strip(row)

		if w := lipgloss.Width(row); w > width-1 {
			t.Errorf("width %d: row is %d cells wide", width, w)
		}
		if strings.HasSuffix(plain, "…") {
			t.Errorf("width %d: row was cut mid-word: %q", width, plain)
		}
		total := len(liveActions(ctx))
		shown := strings.Count(plain, ":")
		if m := regexp.MustCompile(`\+(\d+) \(\?\)`).FindStringSubmatch(plain); m != nil {
			dropped, _ := strconv.Atoi(m[1])
			if shown+dropped != total {
				t.Errorf("width %d: %d shown + %d dropped != %d live actions (%q)",
					width, shown, dropped, total, plain)
			}
		} else if shown != total {
			t.Errorf("width %d: %d keys shown but %d apply and no overflow marker (%q)",
				width, shown, total, plain)
		}
	}
}

// With a worklist, every action addresses it — the row says so before
// naming a key, which is rule 1 where the user actually looks.
func TestActionRowAnnouncesTheMarkedSet(t *testing.T) {
	app := newKeybarApp(t)
	app.activeTab = TabTree
	for _, p := range []string{"a.txt", "b.txt"} {
		app.filelist.marked[p] = true
		app.statusMap[p] = "M"
	}
	plain := ansi.Strip(app.actionRow(app.keyContext()))
	if !strings.HasPrefix(plain, "2 marked ▸ ") {
		t.Errorf("marked row should lead with the count, got %q", plain)
	}

	app.filelist.marked = map[string]bool{}
	plain = ansi.Strip(app.actionRow(app.keyContext()))
	if strings.Contains(plain, "marked ▸") {
		t.Errorf("unmarked row should not mention a marked set, got %q", plain)
	}
}
