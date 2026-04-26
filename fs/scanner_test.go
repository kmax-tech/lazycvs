package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBinaryByExtension(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"png is binary", "image.png", true},
		{"PNG uppercase is binary", "IMAGE.PNG", true},
		{"zip is binary", "archive.zip", true},
		{"go is text", "main.go", false},
		{"md is text", "README.md", false},
		{"json is text", "config.json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use a non-existent path under a tempdir — extension fast paths
			// must not touch disk, so this still returns the extension verdict.
			abs := filepath.Join(t.TempDir(), tc.path)
			if got := IsBinary(abs); got != tc.want {
				t.Errorf("IsBinary(%q) = %v, want %v", abs, got, tc.want)
			}
		})
	}
}

func TestIsBinaryByFilename(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		filename string
		want     bool
	}{
		{"Makefile", "Makefile", false},
		{"makefile lowercase", "makefile", false},
		{"Dockerfile", "Dockerfile", false},
		{"LICENSE", "LICENSE", false},
		{".cvsignore", ".cvsignore", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Write a small text file so sniffing also passes if the filename
			// table misses — both paths agree the file is text.
			abs := filepath.Join(dir, tc.filename)
			if err := os.WriteFile(abs, []byte("hello\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if got := IsBinary(abs); got != tc.want {
				t.Errorf("IsBinary(%q) = %v, want %v", abs, got, tc.want)
			}
		})
	}
}

func TestIsBinarySniff(t *testing.T) {
	dir := t.TempDir()

	textFile := filepath.Join(dir, "data")
	if err := os.WriteFile(textFile, []byte("plain text without nuls\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsBinary(textFile) {
		t.Errorf("IsBinary(%q) = true, want false (no NUL bytes)", textFile)
	}

	binaryFile := filepath.Join(dir, "blob")
	if err := os.WriteFile(binaryFile, []byte{'a', 'b', 0x00, 'c', 'd'}, 0644); err != nil {
		t.Fatal(err)
	}
	if !IsBinary(binaryFile) {
		t.Errorf("IsBinary(%q) = false, want true (NUL byte present)", binaryFile)
	}

	emptyFile := filepath.Join(dir, "empty")
	if err := os.WriteFile(emptyFile, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if IsBinary(emptyFile) {
		t.Errorf("IsBinary(%q) = true, want false (empty file)", emptyFile)
	}
}

func TestIsBinaryMissingFile(t *testing.T) {
	// Unknown extension + missing file: caller should get false so they can
	// surface the real error themselves.
	abs := filepath.Join(t.TempDir(), "does-not-exist")
	if IsBinary(abs) {
		t.Errorf("IsBinary on missing file returned true, want false")
	}
}
