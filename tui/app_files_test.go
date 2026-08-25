package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kmax-tech/lazycvs/cvs"
)

// newListingTestApp builds a minimal App over a real temp working copy.
// Layout (CVS/ admin dirs everywhere so resolveFileStatus doesn't
// report "?" for clean files):
//
//	top.txt
//	sub/direct.txt
//	sub/deep/modified.txt   ← statusMap: M (two levels below root)
//
// statusMap also carries gone.txt (U, not on disk → server-only).
func newListingTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"CVS", "sub/CVS", "sub/deep/CVS"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"top.txt", "sub/direct.txt", "sub/deep/modified.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cmdLog := cvs.NewCommandLog(10)
	return &App{
		exec:     &cvs.CVSExecutor{WorkDir: root, Log: cmdLog},
		cmdLog:   cmdLog,
		console:  NewConsoleModel(cmdLog),
		filelist: NewFileListModel(),
		statusMap: map[string]string{
			"sub/deep/modified.txt": "M",
			"gone.txt":              "U",
		},
	}
}

// A changed file two levels below the listed dir must get a row —
// attached to its top-level subdir group — even though the listing
// walk only descends one level. Regression test for the "dir badge
// says 1M but no M row exists anywhere" bug.
func TestUpdateFileListSurfacesDeepChanges(t *testing.T) {
	app := newListingTestApp(t)
	app.updateFileListForDir(".")

	var sub *SubDirGroup
	for i := range app.filelist.subDirs {
		if app.filelist.subDirs[i].Name == "sub" {
			sub = &app.filelist.subDirs[i]
		}
	}
	if sub == nil {
		t.Fatalf("no subdir group for sub/, got %+v", app.filelist.subDirs)
	}
	var deep *cvs.FileEntry
	for i := range sub.Files {
		if sub.Files[i].Path == "sub/deep/modified.txt" {
			deep = &sub.Files[i]
		}
	}
	if deep == nil {
		t.Fatalf("deep modified file missing from sub/ group, got %+v", sub.Files)
	}
	if deep.Status != "M" {
		t.Errorf("deep file status = %q, want M", deep.Status)
	}
	if deep.ServerOnly {
		t.Error("deep file is on disk but flagged ServerOnly")
	}

	// Server-only direct child still surfaces in the top-level files.
	found := false
	for _, f := range app.filelist.files {
		if f.Path == "gone.txt" && f.ServerOnly && f.Status == "U" {
			found = true
		}
	}
	if !found {
		t.Errorf("server-only gone.txt missing from files, got %+v", app.filelist.files)
	}
}

// An untracked directory reported by `cvs -n update` ("? sub/newdir")
// exists on disk — it must surface as a dir row, NOT as a "(server)"
// pseudo-file. Regression test for the mislabeled (server) rows.
func TestSurfacedUntrackedDirIsNotServerOnly(t *testing.T) {
	app := newListingTestApp(t)
	if err := os.MkdirAll(filepath.Join(app.exec.WorkDir, "sub/newdir"), 0755); err != nil {
		t.Fatal(err)
	}
	app.statusMap["sub/newdir"] = "?"
	app.updateFileListForDir(".")

	for i := range app.filelist.subDirs {
		for _, f := range app.filelist.subDirs[i].Files {
			if f.Path != "sub/newdir" {
				continue
			}
			if f.ServerOnly {
				t.Error("on-disk untracked dir flagged ServerOnly")
			}
			if !f.IsDir {
				t.Error("untracked dir not flagged IsDir")
			}
			return
		}
	}
	t.Fatal("untracked dir sub/newdir missing from listing")
}

// Surfaced statusMap entries respect the ignore patterns — a deep
// *.lazycvs-backup reported as "?" must carry Ignored so hide-ignored
// keeps it out of the listing.
func TestSurfacedEntriesRespectIgnore(t *testing.T) {
	app := newListingTestApp(t)
	backup := "sub/deep/old.txt.lazycvs-backup"
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, backup), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	app.statusMap[backup] = "?"
	app.updateFileListForDir(".")

	for i := range app.filelist.subDirs {
		for _, f := range app.filelist.subDirs[i].Files {
			if f.Path == backup && !f.Ignored {
				t.Error("surfaced *.lazycvs-backup entry not flagged Ignored")
			}
		}
	}
}

// Flat mode must offer a selectable row for every changed file below
// the dir (as dir-relative path), while clean subdir files stay hidden.
func TestFlatViewShowsDeepChanges(t *testing.T) {
	app := newListingTestApp(t)
	app.updateFileListForDir(".")
	app.filelist.viewMode = FileViewFlat

	paths := map[string]bool{}
	for _, r := range app.filelist.rows() {
		if r.file != nil {
			paths[r.file.Path] = true
		}
	}
	if !paths["sub/deep/modified.txt"] {
		t.Errorf("flat view misses deep modified file, rows: %v", paths)
	}
	if paths["sub/direct.txt"] {
		t.Error("flat view shows clean subdir file — should stay hidden")
	}
	if !paths["top.txt"] {
		t.Error("flat view misses direct child top.txt")
	}
}

// Several modified files nested at different depths under different
// subdirs must ALL appear in flat view — the reported failure was
// "mehrere genestete modifizierte files fehlen im flat view".
func TestFlatViewShowsManyNestedChanges(t *testing.T) {
	app := newListingTestApp(t)
	nested := []string{
		"sub/deep/modified.txt", // already M via fixture
		"sub/deep/second.txt",   // sibling at depth 2
		"other/a/b/c/third.txt", // depth 4 under a different subdir
		"other/fourth.txt",      // depth 1 inside subdir
	}
	for _, f := range nested[1:] {
		abs := filepath.Join(app.exec.WorkDir, f)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		app.statusMap[f] = "M"
	}
	app.updateFileListForDir(".")
	app.filelist.viewMode = FileViewFlat

	paths := map[string]bool{}
	for _, r := range app.filelist.rows() {
		if r.file != nil && r.file.Status == "M" {
			paths[r.file.Path] = true
		}
	}
	for _, f := range nested {
		if !paths[f] {
			t.Errorf("flat view misses nested modified %s (got %v)", f, paths)
		}
	}
}

// A file inside a dir that has CVS/ metadata but does NOT list the
// file in CVS/Entries must resolve to "?" — the commit gate and the
// listing use different resolvers, and this is the case where they
// used to disagree (list showed ?, commit gate saw "" and c silently
// refused). Ignored-by-.cvsignore files hit exactly this path: the
// dry-run never reports them, so statusMap is empty for them.
func TestResolveFileStatusEntriesPromotion(t *testing.T) {
	app := newListingTestApp(t)
	entries := "/direct.txt/1.1/Mon Jan  1 00:00:00 2026//\n"
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, "sub/CVS/Entries"), []byte(entries), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.exec.WorkDir, "sub/new.pdf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := app.resolveFileStatus("sub/new.pdf"); got != "?" {
		t.Errorf("unregistered file in tracked dir: status %q, want ?", got)
	}
	if got := app.resolveFileStatus("sub/direct.txt"); got != "" {
		t.Errorf("registered clean file: status %q, want \"\"", got)
	}
}

// Binary files must be cvs-added with -kb or CVS corrupts them on
// future checkouts (keyword expansion + line-ending conversion).
func TestAddArgsBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4\x00\x01\x02binary"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("plain text\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := addArgs(dir, "doc.pdf"); len(got) != 3 || got[1] != "-kb" {
		t.Errorf("binary file: addArgs = %v, want [add -kb doc.pdf]", got)
	}
	if got := addArgs(dir, "notes.txt"); len(got) != 2 || got[1] != "notes.txt" {
		t.Errorf("text file: addArgs = %v, want [add notes.txt]", got)
	}
}
