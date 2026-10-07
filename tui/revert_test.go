package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kmax-tech/lazycvs/cvs"
)

func TestEntriesMarkRemoved(t *testing.T) {
	entries := "/kept.txt/1.2/Wed Oct  7 15:52:58 2026//\n" +
		"/gone.xlsx/-1.3/Wed Oct  7 15:52:58 2026/-kb/\n" +
		"D/sub////\n" +
		"D\n"
	cases := []struct {
		name string
		want bool
	}{
		{"kept.txt", false},
		{"gone.xlsx", true},
		{"sub", false},    // directory line, never "removed"
		{"absent", false}, // not in Entries at all
		{"gone", false},   // prefix of a removed name must not match
	}
	for _, c := range cases {
		if got := entriesMarkRemoved(entries, c.name); got != c.want {
			t.Errorf("entriesMarkRemoved(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRevertOneUndoesRemove drives revertOne against a real local CVS
// repository for the two shapes of a scheduled removal: R (server
// unchanged — `update -C` used to exit 0 and leave it removed) and C
// (server moved on — `update` used to fail with "removed `f' was
// modified by second party"). Both must end with the server's current
// content back on disk and nothing pending.
func TestRevertOneUndoesRemove(t *testing.T) {
	cvsBin, err := exec.LookPath("cvs")
	if err != nil {
		t.Skip("cvs binary not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(cvsBin, append([]string{"-Q", "-d", repo}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cvs %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	run(root, "init")
	imp := filepath.Join(root, "imp")
	if err := os.Mkdir(imp, 0755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(imp, "r.txt"), "r1\n")
	write(filepath.Join(imp, "c.txt"), "c1\n")
	run(imp, "import", "-m", "init", "mod", "vendor", "start")
	run(root, "checkout", "-d", "wc", "mod")
	run(root, "checkout", "-d", "other", "mod")

	// Second party moves c.txt on; r.txt stays at the imported revision.
	write(filepath.Join(root, "other", "c.txt"), "c1\nc2\n")
	run(filepath.Join(root, "other"), "commit", "-m", "second party", "c.txt")

	wc := filepath.Join(root, "wc")
	for _, f := range []string{"r.txt", "c.txt"} {
		if err := os.Remove(filepath.Join(wc, f)); err != nil {
			t.Fatal(err)
		}
		run(wc, "remove", f)
	}

	ex := cvs.NewCVSExecutor(wc, cvsBin, 30*time.Second, cvs.NewCommandLog(50))
	for _, c := range []struct{ path, status, want string }{
		{"r.txt", "R", "r1\n"},
		{"c.txt", "C", "c1\nc2\n"},
	} {
		if err := revertOne(ex, wc, c.path, c.status); err != nil {
			t.Fatalf("revertOne(%s, %s): %v", c.path, c.status, err)
		}
		got, err := os.ReadFile(filepath.Join(wc, c.path))
		if err != nil {
			t.Fatalf("%s not back on disk: %v", c.path, err)
		}
		if string(got) != c.want {
			t.Errorf("%s content = %q, want %q", c.path, got, c.want)
		}
		removed, err := entryRemoved(wc, c.path)
		if err != nil {
			t.Fatal(err)
		}
		if removed {
			t.Errorf("%s still scheduled for removal after revert", c.path)
		}
	}

	cmd := exec.Command(cvsBin, "-d", repo, "-n", "-q", "update")
	cmd.Dir = wc
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cvs -n update: %v\n%s", err, out)
	}
	if len(out) != 0 {
		t.Errorf("working copy not clean after revert:\n%s", out)
	}
}
