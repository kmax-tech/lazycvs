package cvs

import (
	"fmt"
	"strconv"
	"strings"
)

// DiffLineType classifies a line in a unified diff.
type DiffLineType string

const (
	DiffLineAdded   DiffLineType = "added"
	DiffLineRemoved DiffLineType = "removed"
	DiffLineContext DiffLineType = "context"
)

// DiffResult holds a parsed unified diff.
type DiffResult struct {
	Path  string         `json:"path"`
	Hunks []DiffHunk     `json:"hunks"`
	Pairs []DiffLinePair `json:"pairs,omitempty"`
}

// DiffHunk represents a single hunk in a unified diff.
type DiffHunk struct {
	OldStart int        `json:"old_start"`
	OldCount int        `json:"old_count"`
	NewStart int        `json:"new_start"`
	NewCount int        `json:"new_count"`
	Header   string     `json:"header"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is a single line within a diff hunk.
type DiffLine struct {
	Type    DiffLineType `json:"type"`
	Content string       `json:"content"`
	OldNum  int          `json:"old_num,omitempty"`
	NewNum  int          `json:"new_num,omitempty"`
}

// DiffLinePair pairs left (removed/context) and right (added/context) lines for side-by-side view.
type DiffLinePair struct {
	Left  *DiffLine `json:"left"`
	Right *DiffLine `json:"right"`
}

// ParseDiff parses unified diff output into a DiffResult.
func ParseDiff(output string) *DiffResult {
	result := &DiffResult{}
	lines := strings.Split(output, "\n")

	var currentHunk *DiffHunk
	oldNum, newNum := 0, 0

	for _, line := range lines {
		// Hunk header: @@ -oldStart,oldCount +newStart,newCount @@
		if strings.HasPrefix(line, "@@") {
			hunk := parseHunkHeader(line)
			if hunk != nil {
				result.Hunks = append(result.Hunks, *hunk)
				currentHunk = &result.Hunks[len(result.Hunks)-1]
				oldNum = hunk.OldStart
				newNum = hunk.NewStart
			}
			continue
		}

		// Skip diff headers (---, +++, Index, etc.)
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "Index:") || strings.HasPrefix(line, "===") ||
			strings.HasPrefix(line, "diff") || strings.HasPrefix(line, "RCS file:") ||
			strings.HasPrefix(line, "retrieving") {
			continue
		}

		if currentHunk == nil {
			continue
		}

		var dl DiffLine
		switch {
		case strings.HasPrefix(line, "+"):
			dl = DiffLine{Type: DiffLineAdded, Content: line[1:], NewNum: newNum}
			newNum++
		case strings.HasPrefix(line, "-"):
			dl = DiffLine{Type: DiffLineRemoved, Content: line[1:], OldNum: oldNum}
			oldNum++
		default:
			// Context line (starts with space or is empty)
			content := line
			if len(content) > 0 && content[0] == ' ' {
				content = content[1:]
			}
			dl = DiffLine{Type: DiffLineContext, Content: content, OldNum: oldNum, NewNum: newNum}
			oldNum++
			newNum++
		}
		currentHunk.Lines = append(currentHunk.Lines, dl)
	}

	return result
}

// parseHunkHeader parses "@@ -X,Y +X,Y @@ optional context" into a DiffHunk.
func parseHunkHeader(line string) *DiffHunk {
	// Find the range parts between @@ markers
	parts := strings.SplitN(line, "@@", 3)
	if len(parts) < 2 {
		return nil
	}

	ranges := strings.TrimSpace(parts[1])
	rangeParts := strings.Fields(ranges)
	if len(rangeParts) < 2 {
		return nil
	}

	hunk := &DiffHunk{Header: line}

	// Parse -X,Y
	old := strings.TrimPrefix(rangeParts[0], "-")
	oldStart, oldCount := parseRange(old)
	hunk.OldStart = oldStart
	hunk.OldCount = oldCount

	// Parse +X,Y
	new := strings.TrimPrefix(rangeParts[1], "+")
	newStart, newCount := parseRange(new)
	hunk.NewStart = newStart
	hunk.NewCount = newCount

	return hunk
}

func parseRange(s string) (int, int) {
	parts := strings.SplitN(s, ",", 2)
	start, _ := strconv.Atoi(parts[0])
	count := 1
	if len(parts) == 2 {
		count, _ = strconv.Atoi(parts[1])
	}
	return start, count
}

// BuildSideBySide converts hunks into paired lines for side-by-side display.
// Uses a pending-removed buffer to align removed lines with subsequent added lines.
func BuildSideBySide(hunks []DiffHunk) []DiffLinePair {
	var pairs []DiffLinePair

	for _, hunk := range hunks {
		// Add hunk header as a separator
		headerLine := &DiffLine{Type: DiffLineContext, Content: hunk.Header}
		pairs = append(pairs, DiffLinePair{Left: headerLine, Right: headerLine})

		var pendingRemoved []DiffLine

		for _, line := range hunk.Lines {
			switch line.Type {
			case DiffLineRemoved:
				pendingRemoved = append(pendingRemoved, line)

			case DiffLineAdded:
				if len(pendingRemoved) > 0 {
					// Pair with pending removed line
					removed := pendingRemoved[0]
					pendingRemoved = pendingRemoved[1:]
					pairs = append(pairs, DiffLinePair{Left: &removed, Right: copyLine(line)})
				} else {
					// Added without a corresponding removed line
					pairs = append(pairs, DiffLinePair{Left: nil, Right: copyLine(line)})
				}

			case DiffLineContext:
				// Flush any remaining pending removed lines
				for _, r := range pendingRemoved {
					pairs = append(pairs, DiffLinePair{Left: copyLine(r), Right: nil})
				}
				pendingRemoved = nil

				pairs = append(pairs, DiffLinePair{Left: copyLine(line), Right: copyLine(line)})
			}
		}

		// Flush remaining removed lines at end of hunk
		for _, r := range pendingRemoved {
			pairs = append(pairs, DiffLinePair{Left: copyLine(r), Right: nil})
		}
	}

	return pairs
}

func copyLine(l DiffLine) *DiffLine {
	return &l
}

// FormatFileSize returns a human-readable file size.
func FormatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
