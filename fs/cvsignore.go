package fs

import (
	"os"
	"strings"
)

// AppendIgnoreEntries appends one entry per line to the cvsignore file at
// path, creating it when missing. A file whose last byte is not a newline
// (hand-written ignore files often end that way) gets one first; without
// it the first new entry glues onto the last existing one, so "*.iml"
// plus ".DS_Store" became the single pattern "*.iml.DS_Store" and neither
// file was ignored any more.
func AppendIgnoreEntries(path string, entries ...string) (err error) {
	if len(entries) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	var b strings.Builder
	if size := info.Size(); size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			b.WriteByte('\n')
		}
	}
	for _, e := range entries {
		b.WriteString(e + "\n")
	}
	_, err = f.WriteString(b.String())
	return err
}
