package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFollowTargetMapsPathsIntoTheWorkingCopy(t *testing.T) {
	app := newListingTestApp(t)
	root := app.exec.WorkDir
	outside := t.TempDir()

	cases := []struct {
		name, path, want string
		ok               bool
	}{
		{"subdirectory", filepath.Join(root, "sub/deep"), "sub/deep", true},
		{"file", filepath.Join(root, "sub/direct.txt"), "sub/direct.txt", true},
		{"working copy root", root, ".", true},
		{"other directory", outside, "", false},
		{"relative path", "sub", "", false},
		{"missing path", filepath.Join(root, "nope"), "", false},
	}
	for _, c := range cases {
		got, ok := followTarget(app.exec, c.path)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: followTarget(%q) = (%q, %v), want (%q, %v)", c.name, c.path, got, ok, c.want, c.ok)
		}
	}
}

// t.TempDir lives under /var on macOS, which is a symlink to /private/var:
// the file manager may report either spelling and both must match.
func TestFollowTargetResolvesSymlinkedSpelling(t *testing.T) {
	app := newListingTestApp(t)
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(app.exec.WorkDir, link); err != nil {
		t.Fatal(err)
	}
	got, ok := followTarget(app.exec, filepath.Join(link, "sub"))
	if !ok || got != "sub" {
		t.Errorf("followTarget via symlink = (%q, %v), want (sub, true)", got, ok)
	}
}

func followApp(t *testing.T) App {
	t.Helper()
	app := newRevealTestApp(t)
	app.activeTab = TabTree
	app.followReady = true
	return *app
}

func sendFollow(t *testing.T, app App, path string) App {
	t.Helper()
	next, _ := app.Update(FollowMsg{Path: path})
	got, ok := next.(App)
	if !ok {
		t.Fatalf("Update returned %T, want App", next)
	}
	return got
}

func selectedTreePath(app App) string {
	if n := app.tree.SelectedNode(); n != nil {
		return n.Path
	}
	return ""
}

func TestFollowMovesSelectionWhenIdle(t *testing.T) {
	app := followApp(t)
	app = sendFollow(t, app, filepath.Join(app.exec.WorkDir, "sub"))
	if got := selectedTreePath(app); got != "sub" {
		t.Errorf("tree cursor = %q, want sub", got)
	}
	if app.pendingFollow != "" {
		t.Errorf("pendingFollow = %q, want consumed", app.pendingFollow)
	}
}

// A path arriving while the fuzzy search is open must not yank the
// selection away; it lands on the first message after the search closes.
func TestFollowWaitsUntilSearchCloses(t *testing.T) {
	app := followApp(t)
	app.search.active = true
	app = sendFollow(t, app, filepath.Join(app.exec.WorkDir, "sub"))
	if app.pendingFollow != "sub" {
		t.Fatalf("pendingFollow = %q, want sub queued while busy", app.pendingFollow)
	}
	if got := selectedTreePath(app); got == "sub" {
		t.Error("selection moved while the search was open")
	}

	app.search.active = false
	next, _ := app.Update(clockTickMsg{})
	app = next.(App)
	if got := selectedTreePath(app); got != "sub" {
		t.Errorf("after the search closed: tree cursor = %q, want sub", got)
	}
}

func TestFollowWaitsOnOtherTabsAndBeforeTreeLoad(t *testing.T) {
	for name, mutate := range map[string]func(*App){
		"history tab":     func(a *App) { a.activeTab = TabHistory },
		"tree not loaded": func(a *App) { a.followReady = false },
	} {
		app := followApp(t)
		mutate(&app)
		app = sendFollow(t, app, filepath.Join(app.exec.WorkDir, "sub"))
		if app.pendingFollow != "sub" {
			t.Errorf("%s: pendingFollow = %q, want sub", name, app.pendingFollow)
		}
	}
}

func TestFollowOutsideWorkingCopyIsDropped(t *testing.T) {
	app := sendFollow(t, followApp(t), t.TempDir())
	if app.pendingFollow != "" {
		t.Errorf("pendingFollow = %q, want nothing queued for a foreign path", app.pendingFollow)
	}
}
