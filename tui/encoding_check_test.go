package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kmax-tech/lazycvs/cvs"
)

func newEncTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	cmdLog := cvs.NewCommandLog(10)
	return &App{
		exec:     &cvs.CVSExecutor{WorkDir: root, Log: cmdLog},
		cmdLog:   cmdLog,
		encCache: make(map[string]encVerdict),
	}
}

func TestEncodingIssueFor(t *testing.T) {
	app := newEncTestApp(t)
	root := app.exec.WorkDir

	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("ascii.txt", []byte("plain ascii"))
	write("latin1.txt", []byte("f\xfcr 20 Themen"))
	write("utf8.txt", []byte("Größe für"))
	write("moji.txt", []byte("MÃ¼ller"))
	write("cp1252.txt", []byte("\x93quote\x94 f\xfcr"))
	// .dat is not on the extension allowlists, so IsBinary content-sniffs
	// the NUL byte; the CP1252 bytes after it must not produce a warning.
	write("blob.dat", []byte("\x00\x93f\xfcr"))

	// Default expectation: Latin-1 (classic CVS repos).
	tests := []struct {
		path string
		want cvs.EncodingIssue
	}{
		{"ascii.txt", cvs.EncodingOK},
		{"latin1.txt", cvs.EncodingOK},
		{"utf8.txt", cvs.EncodingUnexpectedUTF8},
		{"moji.txt", cvs.EncodingMojibake},
		{"cp1252.txt", cvs.EncodingNonISO},
		{"blob.dat", cvs.EncodingOK},
		{"missing.txt", cvs.EncodingOK},
	}
	for _, tt := range tests {
		if got := app.encodingIssueFor(tt.path); got != tt.want {
			t.Errorf("encodingIssueFor(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}

	// With a UTF-8 repo the verdicts flip. Fresh cache — the verdict is
	// part of the cached value and expectedEnc is fixed per session.
	app.expectedEnc = cvs.ExpectUTF8
	app.encCache = make(map[string]encVerdict)
	if got := app.encodingIssueFor("latin1.txt"); got != cvs.EncodingNotUTF8 {
		t.Errorf("utf8 repo: encodingIssueFor(latin1.txt) = %v, want EncodingNotUTF8", got)
	}
	if got := app.encodingIssueFor("utf8.txt"); got != cvs.EncodingOK {
		t.Errorf("utf8 repo: encodingIssueFor(utf8.txt) = %v, want EncodingOK", got)
	}
}

func TestEncodingIssueForCacheInvalidation(t *testing.T) {
	app := newEncTestApp(t)
	abs := filepath.Join(app.exec.WorkDir, "file.txt")

	if err := os.WriteFile(abs, []byte("f\xc3\xbcr"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := app.encodingIssueFor("file.txt"); got != cvs.EncodingUnexpectedUTF8 {
		t.Fatalf("initial verdict = %v, want EncodingUnexpectedUTF8", got)
	}

	// Rewrite as clean Latin-1; bump mtime explicitly so the check does
	// not depend on filesystem timestamp granularity (same-size rewrite
	// within the same tick would otherwise hit the stale cache entry).
	if err := os.WriteFile(abs, []byte("f\xfcrs"), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, future, future); err != nil {
		t.Fatal(err)
	}
	if got := app.encodingIssueFor("file.txt"); got != cvs.EncodingOK {
		t.Errorf("verdict after rewrite = %v, want EncodingOK", got)
	}
}
