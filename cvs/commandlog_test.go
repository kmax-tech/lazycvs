package cvs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LogFileOp entries land in the ring like commands (errors counted),
// and EnableFileLog mirrors everything into the session file: marker
// line, one line per success, error details for failures.
func TestLogFileOpAndSessionFile(t *testing.T) {
	log := NewCommandLog(10)
	path := filepath.Join(t.TempDir(), "session.log")
	if err := log.EnableFileLog(path, "/work/copy"); err != nil {
		t.Fatal(err)
	}

	log.LogFileOp("rm foo.jpg  # revert conflict", nil)
	log.LogFileOp("mv a b  # restore backup", errors.New("permission denied"))

	if got := len(log.All()); got != 2 {
		t.Fatalf("ring has %d entries, want 2", got)
	}
	if got := log.UnreadErrors(); got != 1 {
		t.Errorf("unread errors = %d, want 1", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"=== lazycvs session",
		"/work/copy",
		"$ rm foo.jpg  # revert conflict ok",
		"FAILED exit=1",
		"permission denied",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("session log missing %q:\n%s", want, s)
		}
	}
}

// A nil CommandLog must be safe to log through — helpers run against
// executors that tests construct without a log.
func TestLogFileOpNilReceiver(t *testing.T) {
	var log *CommandLog
	log.LogFileOp("rm x", nil) // must not panic
}

// An oversized session log is rotated to .old at startup; the fresh
// file starts with the new session marker.
func TestSessionLogRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.log")
	if err := os.WriteFile(path, []byte("old content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxSessionLogBytes+1); err != nil {
		t.Fatal(err)
	}

	log := NewCommandLog(10)
	if err := log.EnableFileLog(path, "/wc"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".old"); err != nil {
		t.Errorf("rotated .old file missing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) > maxSessionLogBytes {
		t.Errorf("fresh log still oversized (%d bytes) — rotation didn't happen", len(data))
	}
	if !strings.Contains(string(data), "=== lazycvs session") {
		t.Error("fresh log missing session marker")
	}
}
