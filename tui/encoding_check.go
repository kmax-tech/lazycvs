package tui

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kmax-tech/lazycvs/cvs"
	"github.com/kmax-tech/lazycvs/fs"
)

// encVerdict is a cached encoding check result for one file. A cache
// entry is valid as long as the file's mtime and size are unchanged.
type encVerdict struct {
	modTime time.Time
	size    int64
	issue   cvs.EncodingIssue
}

// encCheckMaxBytes caps how much of a file the encoding check reads.
// Damage past the cap goes undetected — acceptable for a warning flag.
const encCheckMaxBytes = 128 * 1024

// encodingIssueFor returns the cached or freshly computed encoding
// verdict for relPath (relative to the working copy root). Cheap on
// cache hit: one os.Stat plus a map lookup. Returns EncodingOK for
// binaries, unreadable files, and empty files. Display-only — callers
// must never gate commit/add/diff on it.
func (m App) encodingIssueFor(relPath string) cvs.EncodingIssue {
	abs := filepath.Join(m.exec.WorkDir, relPath)
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return cvs.EncodingOK
	}
	if v, ok := m.encCache[relPath]; ok && v.modTime.Equal(info.ModTime()) && v.size == info.Size() {
		return v.issue
	}
	issue := cvs.EncodingOK
	if !fs.IsBinary(abs) {
		if data, err := readPrefix(abs, encCheckMaxBytes); err == nil {
			issue = cvs.DetectEncodingIssue(data, m.expectedEnc)
		}
	}
	if m.encCache != nil {
		m.encCache[relPath] = encVerdict{modTime: info.ModTime(), size: info.Size(), issue: issue}
	}
	return issue
}

// readPrefix reads at most maxBytes from the start of the file.
func readPrefix(abs string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, err
	}
	return data, nil
}
