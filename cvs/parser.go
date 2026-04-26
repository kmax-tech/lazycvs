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

// ErrorPatterns maps stderr substrings to human-readable error messages.
var ErrorPatterns = map[string]string{
	"failed to create lock directory": "Lock conflict — another user or process is using the repository. Wait a moment and try again.",
	"authorization failed":            "Authentication failed — run 'cvs login' to refresh your credentials.",
	"cannot open directory":           "Stale directory — the server directory is empty or missing.",
	"connection refused":              "CVS server unreachable — check your network connection.",
	"move away":                       "A local file conflicts with a new server file. Rename or remove the local file.",
	"up-to-date check failed":        "The file has been modified on the server since your last update. Run update first.",
}

// MatchErrorPattern returns a human-readable message for a CVS error, or empty string.
func MatchErrorPattern(stderr string) string {
	lower := strings.ToLower(stderr)
	for pattern, message := range ErrorPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return message
		}
	}
	return ""
}
