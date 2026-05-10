package cvs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	moveAwayRe = regexp.MustCompile(`move away ['` + "`" + `](.+?)['` + "`" + `]`)
	staleRe    = regexp.MustCompile(`(?:cannot open directory|skipping directory)\s+(.+?)(?:\s*:|$)`)
)

// ParseUpdate parses combined stdout/stderr from cvs update into an UpdateResult.
func ParseUpdate(stdout, stderr, workDir string) *UpdateResult {
	result := &UpdateResult{}

	// Parse stdout: status lines like "M path", "C path", "? path", etc.
	for _, line := range splitLines(stdout) {
		line = strings.TrimSpace(line)
		if len(line) < 3 || line[1] != ' ' {
			continue
		}

		status := string(line[0])
		path := line[2:]
		entry := makeFileEntry(path, status, workDir)

		switch status {
		case "U", "P":
			entry.Status = "U"
			result.Updated = append(result.Updated, entry)
		case "M":
			result.Modified = append(result.Modified, entry)
		case "C":
			result.Conflicts = append(result.Conflicts, entry)
		case "?":
			result.Untracked = append(result.Untracked, entry)
		}
	}

	// Parse stderr: warnings and errors
	for _, line := range splitLines(stderr) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// "move away" conflicts
		if m := moveAwayRe.FindStringSubmatch(line); m != nil {
			result.InTheWay = append(result.InTheWay, InTheWayEntry{
				Path:      m[1],
				LocalPath: m[1],
			})
			continue
		}

		// Stale directories
		if m := staleRe.FindStringSubmatch(line); m != nil {
			dir := strings.TrimSpace(m[1])
			if !containsString(result.StaleDirs, dir) {
				result.StaleDirs = append(result.StaleDirs, dir)
			}
			continue
		}

		// Other errors
		if !isIgnorableLine(line) {
			result.Errors = append(result.Errors, line)
		}
	}

	return result
}

// ParseInTheWay extracts the paths CVS reported as "move away" / "in the
// way" from cvs update stderr. These are local files that block the
// server's version from being pulled down — typically untracked files
// whose name collides with one a colleague added on the server. The
// caller (TUI) uses this to surface a resolution dialog instead of
// letting the warning silently disappear into the console log.
func ParseInTheWay(stderr string) []string {
	var out []string
	for _, line := range splitLines(stderr) {
		if m := moveAwayRe.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func makeFileEntry(path, status, workDir string) FileEntry {
	entry := FileEntry{
		Path:   path,
		Status: status,
	}
	if workDir != "" {
		if info, err := os.Stat(filepath.Join(workDir, path)); err == nil {
			entry.Size = info.Size()
			entry.ModTime = info.ModTime()
			entry.IsDir = info.IsDir()
		}
	}
	return entry
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func isIgnorableLine(line string) bool {
	ignorePrefixes := []string{
		"cvs update: Updating",
		"cvs update: New directory",
	}
	for _, p := range ignorePrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

