package cvs

import (
	"errors"
	"testing"
	"time"
)

// A retired executor kills a running read-only command and logs nothing
// for it; later read-only commands do not start at all. "/bin/sleep"
// stands in for a slow `cvs status`.
func TestRetireStopsReadOnlyCommands(t *testing.T) {
	log := NewCommandLog(10)
	e := NewCVSExecutor(t.TempDir(), "/bin/sleep", 10*time.Second, log)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := e.RunReadOnly("5")
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	e.Retire()

	select {
	case err := <-done:
		if !errors.Is(err, ErrRetired) {
			t.Errorf("running command returned %v, want ErrRetired", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("running read-only command was not killed by Retire")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("kill took %s", elapsed)
	}
	if res, err := e.RunReadOnly("5"); res != nil || !errors.Is(err, ErrRetired) {
		t.Errorf("RunReadOnly after Retire = (%v, %v), want (nil, ErrRetired)", res, err)
	}
	if n := len(log.All()); n != 0 {
		t.Errorf("console got %d entries for retired commands, want 0", n)
	}
	e.Retire() // idempotent
}

// Write commands ignore retirement: a commit must never be cut off.
func TestRetireLeavesWriteCommandsAlone(t *testing.T) {
	e := NewCVSExecutor(t.TempDir(), "/bin/sleep", 10*time.Second, NewCommandLog(10))
	e.Retire()
	res, err := e.Run("0")
	if err != nil || res == nil || !res.Success {
		t.Errorf("Run on a retired executor = (%+v, %v), want a successful run", res, err)
	}
}

// An executor built as a literal (tests do this) has no retire channel
// and must behave as never retired.
func TestLiteralExecutorNeverRetires(t *testing.T) {
	e := &CVSExecutor{WorkDir: t.TempDir(), CVSBin: "/bin/sleep", Timeout: time.Second, Log: NewCommandLog(1)}
	e.Retire()
	if _, err := e.RunReadOnly("0"); err != nil {
		t.Errorf("RunReadOnly on a literal executor = %v, want nil", err)
	}
}
