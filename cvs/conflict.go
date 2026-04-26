package cvs

import (
	"bytes"
	"os"
	"strings"
)

// HasConflictMarkers reads the file at absPath and reports whether it still
// contains any of CVS's three-way merge conflict markers (<<<<<<<, =======,
// >>>>>>>). Used to pre-validate a commit: `cvs commit` rejects files with
// these markers, so the TUI can block the dispatch and show a clearer hint
// ("press M to resolve") instead of letting CVS error out into the console.
//
// On read error the function returns false — the caller can let cvs report
// the actual problem in that case.
func HasConflictMarkers(absPath string) bool {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return false
	}
	// Both opening and closing markers should be present in a real conflict;
	// either alone could be a coincidence in unrelated content (e.g. some
	// templating language). Require at least one of each plus the divider.
	hasOpen := bytes.Contains(data, []byte("\n<<<<<<<")) || bytes.HasPrefix(data, []byte("<<<<<<<"))
	hasDiv := bytes.Contains(data, []byte("\n=======\n"))
	hasClose := bytes.Contains(data, []byte("\n>>>>>>>"))
	return hasOpen && hasDiv && hasClose
}

// ConflictFile holds all conflict regions in a file.
type ConflictFile struct {
	Path    string           `json:"path"`
	Regions []ConflictRegion `json:"regions"`
}

// ConflictRegion represents a single conflict between local and server versions.
type ConflictRegion struct {
	ID          int      `json:"id"`
	LocalLines  []string `json:"local_lines"`
	ServerLines []string `json:"server_lines"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
}

// ParseConflicts reads file content and extracts CVS conflict regions.
// Conflict markers: <<<<<<< filename / ======= / >>>>>>> revision
func ParseConflicts(content, path string) *ConflictFile {
	cf := &ConflictFile{Path: path}
	lines := strings.Split(content, "\n")

	var region *ConflictRegion
	inLocal := false
	inServer := false
	regionID := 0

	for i, line := range lines {
		lineNum := i + 1

		if strings.HasPrefix(line, "<<<<<<<") {
			regionID++
			region = &ConflictRegion{
				ID:        regionID,
				StartLine: lineNum,
			}
			inLocal = true
			inServer = false
			continue
		}

		if strings.HasPrefix(line, "=======") && region != nil {
			inLocal = false
			inServer = true
			continue
		}

		if strings.HasPrefix(line, ">>>>>>>") && region != nil {
			inServer = false
			region.EndLine = lineNum
			cf.Regions = append(cf.Regions, *region)
			region = nil
			continue
		}

		if region != nil {
			if inLocal {
				region.LocalLines = append(region.LocalLines, line)
			} else if inServer {
				region.ServerLines = append(region.ServerLines, line)
			}
		}
	}

	return cf
}

// ResolveConflict replaces conflict markers in content with the chosen side.
// choice is "local" or "server".
func ResolveConflict(content, choice string, regionID int) string {
	lines := strings.Split(content, "\n")
	var result []string

	var currentRegion int
	inLocal := false
	inServer := false
	skip := false

	for _, line := range lines {
		if strings.HasPrefix(line, "<<<<<<<") {
			currentRegion++
			if currentRegion == regionID || regionID == 0 {
				inLocal = true
				skip = true
				continue
			}
		}

		if strings.HasPrefix(line, "=======") && skip {
			inLocal = false
			inServer = true
			continue
		}

		if strings.HasPrefix(line, ">>>>>>>") && skip {
			inServer = false
			skip = false
			continue
		}

		if skip {
			if inLocal && choice == "local" {
				result = append(result, line)
			} else if inServer && choice == "server" {
				result = append(result, line)
			}
		} else {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}
