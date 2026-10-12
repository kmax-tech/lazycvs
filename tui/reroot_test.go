package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kmax-tech/lazycvs/cvs"
)

// markCheckout gives dir the two files that make it a working-copy
// root for lazycvs: CVS/Root and CVS/Repository (the module shown in
// the header).
func markCheckout(t *testing.T, dir, module string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "CVS"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"Root": ":local:/repo\n", "Repository": module + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, "CVS", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// newRerootTestApp is the follow fixture rooted one level down: the
// listing fixture lives in parent/wc, and parent is itself a checkout
// of the same module — the shape of a file manager walking up out of
// a sub-checkout. Both are canonical paths (t.TempDir may sit behind a
// symlink on macOS).
func newRerootTestApp(t *testing.T) (app App, parent, wc string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	markCheckout(t, parent, "webis")
	if err := os.WriteFile(filepath.Join(parent, "notes.txt"), []byte("n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, "plain"), 0755); err != nil {
		t.Fatal(err)
	}
	wc = filepath.Join(parent, "wc")
	if err := os.Mkdir(wc, 0755); err != nil {
		t.Fatal(err)
	}
	base := newListingTestAppAt(t, wc)
	markCheckout(t, wc, "webis/wc")
	app = *base
	app.tree = NewTreeModel(wc)
	app.tree, _ = app.tree.Update(treeLoadedMsg{nodes: scanDir(wc, ".")})
	app.activeTab = TabTree
	app.followReady = true
	app.module = moduleName(app.exec)
	return app, parent, wc
}

// loadTree runs the tree's own Init command synchronously and feeds
// its result back, the way the Bubble Tea runtime would.
func loadTree(t *testing.T, app App) App {
	t.Helper()
	next, _ := app.Update(app.tree.Init()())
	got, ok := next.(App)
	if !ok {
		t.Fatalf("Update returned %T, want App", next)
	}
	return got
}

func consoleLines(app App) []string {
	var lines []string
	for _, e := range app.cmdLog.All() {
		lines = append(lines, e.Command)
	}
	return lines
}

func hasConsoleLine(app App, substr string) bool {
	for _, l := range consoleLines(app) {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func TestRerootTargetClassifiesPaths(t *testing.T) {
	_, parent, wc := newRerootTestApp(t)
	cases := []struct {
		name, path, root, rel string
		ok                    bool
	}{
		{"checkout dir", parent, parent, ".", true},
		{"file in a checkout", filepath.Join(parent, "notes.txt"), parent, "notes.txt", true},
		{"nested checkout", wc, wc, ".", true},
		{"dir without CVS/Root", filepath.Join(parent, "plain"), "", "", false},
		{"dir with CVS/ but no Root", filepath.Join(wc, "sub"), "", "", false},
		{"relative", "wc", "", "", false},
		{"missing", filepath.Join(parent, "nope"), "", "", false},
	}
	for _, c := range cases {
		root, rel, ok := rerootTarget(c.path)
		if root != c.root || rel != c.rel || ok != c.ok {
			t.Errorf("%s: rerootTarget(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.name, c.path, root, rel, ok, c.root, c.rel, c.ok)
		}
	}
}

// The file manager walks up from wc to its parent, itself a checkout:
// lazycvs re-roots there in place — new executor, new tree with the
// parent as its root row, the caches of the old root gone, the header's
// module read from the new CVS/Repository, a console line saying so.
func TestFollowParentRerootsWhenIdle(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	app = sendFollow(t, app, parent)

	if app.exec.WorkDir != parent {
		t.Fatalf("exec.WorkDir = %q, want %q", app.exec.WorkDir, parent)
	}
	if app.pendingReroot != "" || app.pendingFollow != "" {
		t.Errorf("pending = (%q, %q), want both consumed", app.pendingFollow, app.pendingReroot)
	}
	if len(app.statusMap) != 0 || len(app.baseRevMap) != 0 || len(app.dirEpoch) != 0 {
		t.Errorf("status caches survived the switch: %v %v %v", app.statusMap, app.baseRevMap, app.dirEpoch)
	}
	if app.module != "webis" {
		t.Errorf("header module = %q, want webis (the new root's CVS/Repository)", app.module)
	}
	if app.followReady {
		t.Error("followReady stayed true before the new tree loaded")
	}
	if !hasConsoleLine(app, "re-rooted to "+parent) {
		t.Errorf("console has no re-root line: %q", consoleLines(app))
	}
	if hasConsoleLine(app, "dropped") {
		t.Errorf("moving up dropped marks: %q", consoleLines(app))
	}

	app = loadTree(t, app)
	root := app.tree.root
	if len(root) != 1 || root[0].Name != filepath.Base(parent) {
		t.Fatalf("tree root = %+v, want %s", root, filepath.Base(parent))
	}
	if got := selectedTreePath(app); got != "." {
		t.Errorf("tree cursor = %q, want the new root selected", got)
	}
	if !app.followReady {
		t.Error("followReady not set by the new tree's load")
	}
	var names []string
	for _, n := range root[0].Children {
		names = append(names, n.Name)
	}
	if want := []string{"plain", "wc", "notes.txt"}; strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("new root lists %v, want %v", names, want)
	}
	_ = wc
}

// A followed file in the other checkout lands on the file, as a file
// argument on the command line does.
func TestFollowFileInParentCheckoutLandsOnIt(t *testing.T) {
	app, parent, _ := newRerootTestApp(t)
	app = sendFollow(t, app, filepath.Join(parent, "notes.txt"))
	if app.exec.WorkDir != parent {
		t.Fatalf("exec.WorkDir = %q, want %q", app.exec.WorkDir, parent)
	}
	app = loadTree(t, app)
	sel := app.filelist.SelectedFile()
	if sel == nil || sel.Path != "notes.txt" {
		t.Errorf("selected file = %+v, want notes.txt", sel)
	}
}

// Busy means busy for a re-root too: with the search open the switch
// waits, and happens on the first message after the search closes.
func TestFollowRerootWaitsUntilSearchCloses(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	app.search.active = true
	app = sendFollow(t, app, parent)
	if app.pendingReroot != parent {
		t.Fatalf("pendingReroot = %q, want %q queued while busy", app.pendingReroot, parent)
	}
	if app.exec.WorkDir != wc {
		t.Fatalf("re-rooted while the search was open: WorkDir = %q", app.exec.WorkDir)
	}

	app.search.active = false
	next, _ := app.Update(clockTickMsg{})
	app = next.(App)
	if app.exec.WorkDir != parent {
		t.Errorf("after the search closed: WorkDir = %q, want %q", app.exec.WorkDir, parent)
	}
	if app.pendingReroot != "" {
		t.Errorf("pendingReroot = %q, want consumed", app.pendingReroot)
	}
}

// A running action (progress banner) belongs to the root it was
// dispatched in; the re-root waits for its result.
func TestFollowRerootWaitsForARunningAction(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	app.setProgress("⟳ Committing 1 file…")
	app = sendFollow(t, app, parent)
	if app.exec.WorkDir != wc {
		t.Fatalf("re-rooted under a running action: WorkDir = %q", app.exec.WorkDir)
	}
	app.clearProgress()
	next, _ := app.Update(clockTickMsg{})
	app = next.(App)
	if app.exec.WorkDir != parent {
		t.Errorf("after the action finished: WorkDir = %q, want %q", app.exec.WorkDir, parent)
	}
}

// The latest path wins over both pending kinds.
func TestFollowLatestPathWinsOverPendingReroot(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	app.search.active = true
	app = sendFollow(t, app, parent)
	app = sendFollow(t, app, filepath.Join(wc, "sub"))
	if app.pendingReroot != "" || app.pendingFollow != "sub" {
		t.Errorf("pending = (%q, %q), want (sub, \"\")", app.pendingFollow, app.pendingReroot)
	}
	app = sendFollow(t, app, parent)
	if app.pendingReroot != parent || app.pendingFollow != "" {
		t.Errorf("pending = (%q, %q), want (\"\", %s)", app.pendingFollow, app.pendingReroot, parent)
	}
}

func TestRerootRebasesOrDropsMarks(t *testing.T) {
	marks := func(app App) []string {
		var out []string
		for p := range app.filelist.marked {
			out = append(out, p)
		}
		return out
	}
	t.Run("moving up keeps every mark, prefixed", func(t *testing.T) {
		app, parent, _ := newRerootTestApp(t)
		app.filelist.marked = map[string]bool{"sub/direct.txt": true, "top.txt": true}
		app = sendFollow(t, app, parent)
		want := map[string]bool{"wc/sub/direct.txt": true, "wc/top.txt": true}
		if len(app.filelist.marked) != len(want) {
			t.Fatalf("marks = %v, want %v", marks(app), want)
		}
		for p := range want {
			if !app.filelist.marked[p] {
				t.Errorf("mark %q missing after moving up: %v", p, marks(app))
			}
		}
		if len(app.markedTab.files) != 2 {
			t.Errorf("Marked tab lists %d files, want 2", len(app.markedTab.files))
		}
		if hasConsoleLine(app, "dropped") {
			t.Errorf("nothing was dropped, console says otherwise: %q", consoleLines(app))
		}
	})
	t.Run("an unrelated checkout drops them and says so", func(t *testing.T) {
		app, _, _ := newRerootTestApp(t)
		app.filelist.marked = map[string]bool{"sub/direct.txt": true, "top.txt": true}
		other, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		markCheckout(t, other, "elsewhere")
		app = sendFollow(t, app, other)
		if app.exec.WorkDir != other {
			t.Fatalf("WorkDir = %q, want %q", app.exec.WorkDir, other)
		}
		if len(app.filelist.marked) != 0 {
			t.Errorf("marks survived a switch to an unrelated root: %v", marks(app))
		}
		if !hasConsoleLine(app, "dropped 2 marks") {
			t.Errorf("console does not report the dropped marks: %q", consoleLines(app))
		}
		if !strings.Contains(app.notification, "2 marks") {
			t.Errorf("banner = %q, want the dropped count", app.notification)
		}
	})
}

func TestRebaseMarks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, oldRoot, newRoot string
		marks                  []string
		want                   []string
		dropped                int
	}{
		{"up one level", "a/b", "a", []string{"x.txt", "d/y.txt"}, []string{"b/x.txt", "b/d/y.txt"}, 0},
		{"up two levels", "a/b/c", "a", []string{"x.txt"}, []string{"b/c/x.txt"}, 0},
		{"down keeps what lies inside", "a", "a/b", []string{"b/x.txt", "x.txt"}, []string{"x.txt"}, 1},
		{"sibling drops all", "a/b", "a/c", []string{"x.txt"}, nil, 1},
		{"same root is identity", "a", "a", []string{"x.txt"}, []string{"x.txt"}, 0},
	}
	for _, c := range cases {
		in := make(map[string]bool)
		for _, p := range c.marks {
			in[p] = true
		}
		kept, dropped := rebaseMarks(in, filepath.Join(base, c.oldRoot), filepath.Join(base, c.newRoot))
		if dropped != c.dropped || len(kept) != len(c.want) {
			t.Errorf("%s: kept %v dropped %d, want %v / %d", c.name, kept, dropped, c.want, c.dropped)
			continue
		}
		for _, p := range c.want {
			if !kept[p] {
				t.Errorf("%s: %q missing from %v", c.name, p, kept)
			}
		}
	}
}

// Results of commands started against the old root carry its
// generation and must not land in the new one — whatever guard they
// had before (status epochs reset per root, history request IDs are
// bumped) they fail the generation check.
func TestStaleResultsAfterRerootAreIgnored(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	app = sendFollow(t, app, parent)
	app = loadTree(t, app)
	oldGen := app.rootGen - 1
	oldNodes := scanDir(wc, ".")
	status := []cvs.FileStatus{{Path: "top.txt", Status: "Locally Modified", WorkingRev: "1.2"}}

	cases := []struct {
		name  string
		msg   interface{}
		check func(App) string
	}{
		{"dir scan", dirStatusMsg{dir: ".", recursive: true, statuses: status, epoch: app.statusEpoch, gen: oldGen},
			func(a App) string {
				if len(a.statusMap) != 0 || !a.statusAt.IsZero() {
					return "status map or statusAt taken from the old root's scan"
				}
				return ""
			}},
		{"user refresh", statusRefreshedMsg{result: &cvs.UpdateResult{}, statuses: status, epoch: app.statusEpoch, gen: oldGen},
			func(a App) string {
				if len(a.baseRevMap) != 0 || !a.statusAt.IsZero() || a.notification != "" {
					return "refresh of the old root applied"
				}
				return ""
			}},
		{"marked status", markedStatusMsg{paths: []string{"top.txt"}, statuses: status, gen: oldGen},
			func(a App) string {
				if len(a.statusMap) != 0 {
					return "marked status of the old root applied"
				}
				return ""
			}},
		{"tree listing", treeLoadedMsg{nodes: oldNodes, gen: oldGen},
			func(a App) string {
				if a.tree.root[0].Name != filepath.Base(parent) || len(a.tree.root[0].Children) != 3 {
					return "old root's listing grafted onto the new tree"
				}
				return ""
			}},
		{"tree dir listing", treeDirLoadedMsg{path: "wc", nodes: oldNodes, gen: oldGen},
			func(a App) string {
				if n := a.tree.findNode("wc"); n != nil && n.Children != nil {
					return "old root's dir listing grafted onto the new tree"
				}
				return ""
			}},
		{"diff pane", diffLoadedMsg{path: "top.txt", diff: &cvs.DiffResult{}, gen: oldGen},
			func(a App) string {
				if a.diffReady || a.diffPath != "" {
					return "diff of the old root shown"
				}
				return ""
			}},
		{"history stream end", historyLoadedMsg{path: "top.txt", requestID: app.histRequestID, gen: oldGen, history: &cvs.FileHistory{}},
			func(a App) string {
				if len(a.histRevisions) != 0 {
					return "history of the old root cached"
				}
				return ""
			}},
		{"history content", historyContentMsg{path: "top.txt", rev: "1.1", content: "x", gen: oldGen},
			func(a App) string {
				if len(a.histContents) != 0 {
					return "revision content of the old root cached"
				}
				return ""
			}},
		{"history diff", historyDiffMsg{path: "top.txt", fromRev: "1.1", toRev: "1.2", gen: oldGen},
			func(a App) string {
				if len(a.histDiffs) != 0 {
					return "revision diff of the old root cached"
				}
				return ""
			}},
	}
	for _, c := range cases {
		next, _ := app.Update(c.msg)
		got := next.(App)
		if problem := c.check(got); problem != "" {
			t.Errorf("%s: %s", c.name, problem)
		}
	}

	// The same results with the current generation do apply — the
	// guard is the generation, not the message.
	next, _ := app.Update(dirStatusMsg{dir: ".", recursive: true, statuses: status, epoch: app.statusEpoch, gen: app.rootGen})
	if got := next.(App); got.statusMap["top.txt"] != "M" {
		t.Errorf("a current-generation scan was ignored: %v", got.statusMap)
	}
}

func TestFollowOutsideAnyCheckoutIsStillDropped(t *testing.T) {
	app, parent, wc := newRerootTestApp(t)
	for name, path := range map[string]string{
		"plain directory": filepath.Join(parent, "plain"),
		"foreign tempdir": t.TempDir(),
	} {
		got := sendFollow(t, app, path)
		if got.pendingReroot != "" || got.pendingFollow != "" || got.exec.WorkDir != wc {
			t.Errorf("%s: pending = (%q, %q), WorkDir = %q — want nothing queued and the root unchanged",
				name, got.pendingFollow, got.pendingReroot, got.exec.WorkDir)
		}
	}
}

// Walking down is a follow, never a re-root — even though every
// subdirectory of a checkout carries CVS metadata.
func TestFollowIntoSubdirectoryIsAPlainFollow(t *testing.T) {
	app, _, wc := newRerootTestApp(t)
	markCheckout(t, filepath.Join(wc, "sub"), "webis/wc/sub")
	app = sendFollow(t, app, filepath.Join(wc, "sub"))
	if app.exec.WorkDir != wc {
		t.Fatalf("WorkDir = %q, want unchanged %q", app.exec.WorkDir, wc)
	}
	if got := selectedTreePath(app); got != "sub" {
		t.Errorf("tree cursor = %q, want sub", got)
	}
}
