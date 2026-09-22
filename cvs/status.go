package cvs

import (
	"regexp"
	"strings"
)

// FileStatus represents the CVS status of a single file.
type FileStatus struct {
	Path          string `json:"path"`
	WorkingRev    string `json:"working_rev"`
	RepositoryRev string `json:"repository_rev"`
	Status        string `json:"status"` // "Up-to-date", "Locally Modified", etc.
}

var (
	// `cvs status` emits one line per file:
	//   File: <name>\tStatus: Up-to-date
	//   File: no file <name>\tStatus: Locally Removed
	//
	// "no file" is CVS' way of saying the working copy is gone; it is
	// matched as an optional prefix so the captured name is the
	// filename, not "no".
	//
	// The name is captured non-greedily up to the separator rather
	// than as \S+, because file names contain spaces and CVS's
	// separator is a tab: \S+ stopped at the first space, so
	// "Zusammenfassung Größe.txt" was parsed as "Zusammenfassung",
	// no status was ever recorded under the real path, and the file
	// showed as clean while cvs considered it modified.
	statusFileRe = regexp.MustCompile(`^File:\s+(?:no file\s+)?(.+?)\s+Status:\s+(.+)$`)
	workingRevRe = regexp.MustCompile(`^\s+Working revision:\s+(\S+)`)
	repoRevRe    = regexp.MustCompile(`^\s+Repository revision:\s+(\S+)`)
)

// ParseStatus parses the output of `cvs status` into file statuses.
// Paths are prefixed with the directory CVS reports via "Examining <dir>"
// lines as it walks the tree.
func ParseStatus(output string) []FileStatus {
	return ParseStatusInDir(output, "")
}

// ParseStatusInDir is ParseStatus with a fallback directory context.
// Use it when calling `cvs status -l <dir>` on a single directory:
// some CVS versions skip the "Examining <dir>" line in that case and
// emit bare basenames, which would otherwise be parsed as top-level
// paths and surface in the wrong directory in the UI. Passing the
// scope dir as defaultDir gives the parser a sensible starting context
// so removed/missing files keep their full relative path.
func ParseStatusInDir(output, defaultDir string) []FileStatus {
	var result []FileStatus
	var current *FileStatus
	currentDir := defaultDir

	for _, line := range strings.Split(output, "\n") {
		// Track directory context
		if strings.HasPrefix(line, "cvs status: Examining") {
			parts := strings.SplitN(line, "Examining ", 2)
			if len(parts) == 2 {
				currentDir = strings.TrimSpace(parts[1])
			}
			continue
		}

		if m := statusFileRe.FindStringSubmatch(line); m != nil {
			path := m[1]
			if currentDir != "" && currentDir != "." {
				path = currentDir + "/" + path
			}
			current = &FileStatus{
				Path:   path,
				Status: strings.TrimSpace(m[2]),
			}
			continue
		}

		if current != nil {
			if m := workingRevRe.FindStringSubmatch(line); m != nil {
				current.WorkingRev = m[1]
			}
			if m := repoRevRe.FindStringSubmatch(line); m != nil {
				current.RepositoryRev = m[1]
				// We have all fields, add to result
				result = append(result, *current)
				current = nil
			}
		}
	}

	return result
}
