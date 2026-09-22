package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/kmax-tech/lazycvs/config"
	"github.com/kmax-tech/lazycvs/cvs"
)

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

func TestAltOpenArgv(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tmpl    string
		path    string
		want    []string
		wantErr bool
	}{
		{
			name: "plain command appends the path",
			tmpl: "emacsclient -n",
			path: "/repo/foo.tex",
			want: []string{"emacsclient", "-n", "/repo/foo.tex"},
		},
		{
			name: "$FILE placeholder is substituted in place",
			tmpl: "code --goto $FILE",
			path: "/repo/foo.tex",
			want: []string{"code", "--goto", "/repo/foo.tex"},
		},
		{
			name:    "empty template errors (fail loud, not a silent no-op)",
			tmpl:    "",
			path:    "/repo/foo.tex",
			wantErr: true,
		},
		{
			name:    "whitespace-only template errors",
			tmpl:    "   ",
			path:    "/repo/foo.tex",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := altOpenArgv(tc.tmpl, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("altOpenArgv(%q) expected error, got %v", tc.tmpl, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("altOpenArgv(%q): %v", tc.tmpl, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("altOpenArgv(%q) = %v, want %v", tc.tmpl, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("altOpenArgv(%q)[%d] = %q, want %q", tc.tmpl, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestIsTerminalTool(t *testing.T) {
	for _, tc := range []struct {
		bin  string
		want bool
	}{
		{"vimdiff", true},
		{"nvim", true},
		{"vim", true},
		{"/usr/local/bin/nvim", true},
		{"meld", false},
		{"code", false},
		{"opendiff", false},
	} {
		if got := isTerminalTool(tc.bin); got != tc.want {
			t.Errorf("isTerminalTool(%q) = %v, want %v", tc.bin, got, tc.want)
		}
	}
}

func TestUseTerminal(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		argv0    string
		override *bool
		want     bool
	}{
		{"unset falls back to binary inference (terminal)", "vimdiff", nil, true},
		{"unset falls back to binary inference (gui)", "meld", nil, false},
		{"explicit true wins over gui inference", "hx-wrapper", &yes, true},
		{"explicit false wins over terminal inference", "nvim", &no, false},
	} {
		if got := useTerminal(tc.argv0, tc.override); got != tc.want {
			t.Errorf("%s: useTerminal(%q) = %v, want %v", tc.name, tc.argv0, got, tc.want)
		}
	}
}

func TestExpandTemplate(t *testing.T) {
	got := expandTemplate("meld $LEFT $RIGHT", map[string]string{
		"$LEFT":  "/tmp/rev-1.1-my notes.bib",
		"$RIGHT": "/repo/my notes.bib",
	})
	want := []string{"meld", "/tmp/rev-1.1-my notes.bib", "/repo/my notes.bib"}
	if len(got) != len(want) {
		t.Fatalf("expandTemplate = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("expandTemplate[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Placeholders embedded in a larger token still substitute (emacs preset).
	got = expandTemplate(`emacs --eval (ediff-files "$LEFT" "$RIGHT")`, map[string]string{
		"$LEFT": "/a", "$RIGHT": "/b",
	})
	want = []string{"emacs", "--eval", "(ediff-files", `"/a"`, `"/b")`}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("embedded: expandTemplate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSideForRev(t *testing.T) {
	if got := sideForRev(workingRev); !got.working {
		t.Errorf("sideForRev(%q) = %+v, want working side", workingRev, got)
	}
	got := sideForRev("1.85")
	if got.working || got.rev != "1.85" {
		t.Errorf(`sideForRev("1.85") = %+v, want {rev:"1.85"}`, got)
	}
}

// newHistoryTestApp builds an App whose History pane holds revisions
// 1.90 → 1.85 (newest first) for a clean file (no working-copy row).
func newHistoryTestApp(mode HistoryMode) *App {
	h := NewHistoryModel()
	h.path = "notes.txt"
	h.mode = mode
	h.revisions = []cvs.Revision{
		{Number: "1.90", PrevNumber: "1.89"},
		{Number: "1.89", PrevNumber: "1.88"},
		{Number: "1.88", PrevNumber: "1.87"},
		{Number: "1.87", PrevNumber: "1.86"},
		{Number: "1.86", PrevNumber: "1.85"},
		{Number: "1.85", PrevNumber: ""},
	}
	return &App{activeTab: TabHistory, history: h}
}

// The compare anchor (space) and the vs-working toggle (w) must steer the
// pair regardless of the right pane's mode — the external diff tool (E)
// reads the same resolution while the pane may sit in Content or Blame.
// Regression test: E used to hardcode cursor.PrevNumber ↔ cursor.Number
// and silently ignored the anchor.
func TestComparePairHonorsAnchorInEveryMode(t *testing.T) {
	for _, mode := range []HistoryMode{HistoryDiff, HistoryContent, HistoryBlame} {
		app := newHistoryTestApp(mode)
		app.history.cursor = 0        // 1.90
		app.history.compareAnchor = 5 // 1.85

		from, to := app.comparePair()
		if from != "1.85" || to != "1.90" {
			t.Errorf("mode %v: comparePair() = (%q, %q), want (1.85, 1.90)", mode, from, to)
		}
	}
}

func TestComparePairWithoutAnchorUsesParent(t *testing.T) {
	app := newHistoryTestApp(HistoryContent)
	app.history.cursor = 0 // 1.90

	from, to := app.comparePair()
	if from != "1.89" || to != "1.90" {
		t.Errorf("comparePair() = (%q, %q), want (1.89, 1.90)", from, to)
	}
}

// vsWorking moves the "to" side onto the on-disk file; with an anchor the
// "from" side stays the anchor rather than the cursor.
func TestComparePairVsWorkingWithAnchor(t *testing.T) {
	app := newHistoryTestApp(HistoryContent)
	app.history.cursor = 0
	app.history.compareAnchor = 5 // 1.85
	app.history.vsWorking = true

	from, to := app.comparePair()
	if from != "1.85" || to != workingRev {
		t.Errorf("comparePair() = (%q, %q), want (1.85, %s)", from, to, workingRev)
	}
}

// Initial revision with no anchor has nothing to compare against — E must
// stay quiet rather than launching the tool on a bogus pair.
func TestComparePairInitialRevisionIsEmpty(t *testing.T) {
	app := newHistoryTestApp(HistoryContent)
	app.history.cursor = 5 // 1.85, PrevNumber ""
	app.history.compareAnchor = -1

	if from, to := app.comparePair(); from != "" || to != "" {
		t.Errorf("comparePair() = (%q, %q), want empty pair", from, to)
	}
}

// currentDiffKey keeps its Diff-mode gate: the right pane's cache path
// must not ask for a diff while showing content or blame.
func TestCurrentDiffKeyGatedOnDiffMode(t *testing.T) {
	app := newHistoryTestApp(HistoryContent)
	app.history.cursor = 0

	if from, to := app.currentDiffKey(); from != "" || to != "" {
		t.Errorf("currentDiffKey() in Content mode = (%q, %q), want empty pair", from, to)
	}
	app.history.mode = HistoryDiff
	if from, to := app.currentDiffKey(); from != "1.89" || to != "1.90" {
		t.Errorf("currentDiffKey() in Diff mode = (%q, %q), want (1.89, 1.90)", from, to)
	}
}

// Each revision occupies two rows in the list. The anchor arrow marks the
// revision, not each of its rows — it used to be written into both, which
// showed up as two triangles stacked directly on top of each other.
func TestViewLeftAnchorArrowMarksHeaderRowOnly(t *testing.T) {
	app := newHistoryTestApp(HistoryDiff)
	app.history.height = 12
	app.history.leftWidth = 40
	app.history.cursor = 0        // 1.90
	app.history.compareAnchor = 1 // 1.89

	out := ansi.Strip(app.history.ViewLeft())
	if got := strings.Count(out, "▸"); got != 1 {
		t.Errorf("anchor arrows in list = %d, want 1\n%s", got, out)
	}

	// The arrow must sit on the revision header row, not the message row.
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "▸") {
			continue
		}
		if !strings.Contains(line, "1.89") {
			t.Errorf("anchor arrow on wrong row: %q", line)
		}
	}
}

// The cursor bar deliberately spans both rows of its entry so it reads as
// one continuous block — that is not the stacking bug.
func TestViewLeftCursorBarSpansBothRows(t *testing.T) {
	app := newHistoryTestApp(HistoryDiff)
	app.history.height = 12
	app.history.leftWidth = 40
	app.history.cursor = 1 // 1.89
	app.history.compareAnchor = -1

	out := ansi.Strip(app.history.ViewLeft())
	if got := strings.Count(out, "▌"); got != 2 {
		t.Errorf("cursor bars in list = %d, want 2\n%s", got, out)
	}
}

func TestCopyWorkingToTemp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub/notes.txt"), []byte("local edits\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tmp, err := copyWorkingToTemp(root, "sub/notes.txt")
	if err != nil {
		t.Fatalf("copyWorkingToTemp: %v", err)
	}
	// The snapshot must be a separate file — the merge tool writes its
	// result into the working copy and would otherwise clobber its own
	// LOCAL side mid-merge.
	if tmp == filepath.Join(root, "sub/notes.txt") {
		t.Fatal("snapshot path equals the working file")
	}
	if base := filepath.Base(tmp); base != "working-notes.txt" {
		t.Errorf("snapshot basename = %q, want working-notes.txt", base)
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local edits\n" {
		t.Errorf("snapshot content = %q, want %q", data, "local edits\n")
	}
}

// mergeTestApp wires the minimum an App needs for the History merge
// dispatch: a configured merge tool and a loaded revision list.
func mergeTestApp(t *testing.T) *App {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[editor]\n  merge_tool = \"meld\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfgMgr, err := config.NewConfigManager(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	app := newHistoryTestApp(HistoryDiff)
	app.cfgMgr = cfgMgr
	return app
}

// runCmd executes a tea.Cmd and returns its message. Nil-safe so the
// "key is a no-op" case reads the same as the "key errored" case.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// Without a pair there is nothing to merge — M must say so rather than
// launching the tool on a half-filled argv.
func TestHistoryMergeWithoutPairErrors(t *testing.T) {
	app := mergeTestApp(t)
	app.history.cursor = 5 // 1.85, initial revision, no parent
	app.history.compareAnchor = -1

	msg, ok := runCmd(app.launchHistoryMergeForCurrent()).(externalDiffMsg)
	if !ok || msg.err == nil {
		t.Fatalf("want an error message, got %#v", msg)
	}
	if !strings.Contains(msg.err.Error(), "anchor") {
		t.Errorf("error should point at the anchor key, got %q", msg.err)
	}
}

// The working copy is already the LOCAL side of the merge, so it cannot
// also be one of the two revisions being merged in.
func TestHistoryMergeRejectsWorkingCopyInPair(t *testing.T) {
	app := mergeTestApp(t)
	app.history.cursor = 0
	app.history.compareAnchor = 5 // 1.85
	app.history.vsWorking = true  // pushes workingRev onto the "to" side

	msg, ok := runCmd(app.launchHistoryMergeForCurrent()).(externalDiffMsg)
	if !ok || msg.err == nil {
		t.Fatalf("want an error message, got %#v", msg)
	}
	if !strings.Contains(msg.err.Error(), "two revisions") {
		t.Errorf("error should explain the two-revision requirement, got %q", msg.err)
	}
}

// A 3-way text merge on a binary file produces garbage; M must say so
// instead of launching the tool (mirrors the E dispatch's IsBinary guard).
func TestHistoryMergeRejectsBinaryFile(t *testing.T) {
	app := mergeTestApp(t)
	app.exec = &cvs.CVSExecutor{WorkDir: t.TempDir()}
	app.history.path = "logo.png" // binary by extension, no on-disk file needed
	app.history.cursor = 0
	app.history.compareAnchor = 5 // valid pair 1.85 ↔ 1.90

	msg, ok := runCmd(app.launchHistoryMergeForCurrent()).(externalDiffMsg)
	if !ok || msg.err == nil {
		t.Fatalf("want an error message, got %#v", msg)
	}
	if !strings.Contains(msg.err.Error(), "binary") {
		t.Errorf("error should name the binary problem, got %q", msg.err)
	}
}

func TestHistoryMergeWithoutPathIsNoOp(t *testing.T) {
	app := mergeTestApp(t)
	app.history.path = ""

	if cmd := app.launchHistoryMergeForCurrent(); cmd != nil {
		t.Error("merge without a loaded file should be a no-op")
	}
}

// A merge template without $MERGED gives the tool nowhere to write the
// result — the user would get the success note and an untouched working
// copy. Regression test: the old `diffuse` preset was exactly that.
func TestLaunchExternalMergeRejectsTemplateWithoutMerged(t *testing.T) {
	cfg := config.Config{}
	cfg.Editor.MergeCommand = "diffuse $LOCAL $BASE $REMOTE"

	msg, ok := launchExternalMerge(cfg, "b", "l", "r", "m", "note", "p")().(externalDiffMsg)
	if !ok || msg.err == nil {
		t.Fatalf("want an error message, got %#v", msg)
	}
	if !strings.Contains(msg.err.Error(), "$MERGED") {
		t.Errorf("error should name the missing $MERGED, got %q", msg.err)
	}
}

// Every built-in merge preset must satisfy the $MERGED guard the
// launcher enforces — a preset that cannot launch is a config trap.
func TestMergePresetsAllCarryMerged(t *testing.T) {
	for name, tmpl := range config.MergeToolPresets {
		if !strings.Contains(tmpl, "$MERGED") {
			t.Errorf("preset %q lacks $MERGED: %q", name, tmpl)
		}
	}
}

// When the pseudo working row appears or disappears while the revision
// list is unchanged, cursor and compare anchor must keep pointing at
// the same revision instead of sliding one row. Regression test: a
// History pair merge dirtied the file, the working row appeared, and
// the anchored revision silently became its neighbour.
func TestRefreshWorkingStateShiftsSelection(t *testing.T) {
	app := newHistoryTestApp(HistoryDiff)
	app.history.cursor = 0        // 1.90
	app.history.compareAnchor = 5 // 1.85

	app.history.RefreshWorkingState(true, "1.90") // working row appears
	if app.history.cursor != 1 || app.history.compareAnchor != 6 {
		t.Errorf("after dirty: cursor=%d anchor=%d, want 1 and 6",
			app.history.cursor, app.history.compareAnchor)
	}
	if from, to := app.comparePair(); from != "1.85" || to != "1.90" {
		t.Errorf("pair after dirty = (%q, %q), want (1.85, 1.90)", from, to)
	}

	app.history.RefreshWorkingState(false, "1.90") // working row disappears
	if app.history.cursor != 0 || app.history.compareAnchor != 5 {
		t.Errorf("after clean: cursor=%d anchor=%d, want 0 and 5",
			app.history.cursor, app.history.compareAnchor)
	}
}

// An anchor sitting on the working row loses its target when the row
// disappears; it must be cleared, not land on the newest revision.
func TestRefreshWorkingStateClearsAnchorOnVanishedWorkingRow(t *testing.T) {
	app := newHistoryTestApp(HistoryDiff)
	app.history.RefreshWorkingState(true, "1.90") // working row appears
	app.history.cursor = 2                        // 1.89
	app.history.compareAnchor = 0                 // the working row

	app.history.RefreshWorkingState(false, "1.90")
	if app.history.compareAnchor != -1 {
		t.Errorf("anchor = %d, want -1 (working row vanished)", app.history.compareAnchor)
	}
	if app.history.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (still on 1.89)", app.history.cursor)
	}
}

// A merge's externalDiffMsg names the file the tool wrote to; the
// handler must refresh its status and drop its History cache so the
// tree and the vs-working diff don't keep showing the pre-merge state.
func TestExternalDiffMsgRefreshesMergedPath(t *testing.T) {
	app := newListingTestApp(t)
	app.histRevisions = make(map[string]*cvs.FileHistory)
	app.histDiffs = map[string]map[string]*cvs.DiffResult{
		"top.txt": {"1.1:@working": nil},
	}
	app.histContents = make(map[string]map[string]string)
	app.histPending = make(map[string]bool)
	app.histStreams = make(map[uint64]chan tea.Msg)
	app.history = NewHistoryModel()

	model, cmd := app.Update(externalDiffMsg{note: "merged", refreshPath: "top.txt"})
	if cmd == nil {
		t.Fatal("handler produced no command — no refresh dispatched")
	}
	updated := model.(App)
	if updated.statusEpoch == 0 {
		t.Error("statusEpoch unchanged — refreshStatusForPaths did not run")
	}
	if _, ok := updated.histDiffs["top.txt"]; ok {
		t.Error("cached diff for the merged file survived the invalidation")
	}
}
