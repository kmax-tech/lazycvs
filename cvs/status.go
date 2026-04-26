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
	statusFileRe  = regexp.MustCompile(`^File:\s+(\S+)\s+Status:\s+(.+)$`)
	workingRevRe  = regexp.MustCompile(`^\s+Working revision:\s+(\S+)`)
	repoRevRe     = regexp.MustCompile(`^\s+Repository revision:\s+(\S+)`)
)

// ParseStatus parses the output of `cvs status -R` into file statuses.
func ParseStatus(output string) []FileStatus {
	var result []FileStatus
	var current *FileStatus
	currentDir := ""

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
