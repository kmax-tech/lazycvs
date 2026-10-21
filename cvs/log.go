package cvs

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FileHistory holds the revision history of a single file.
type FileHistory struct {
	Path      string     `json:"path"`
	Revisions []Revision `json:"revisions"`
	// KeywordSubstitution is the file's `-k` mode from the log header
	// ("kv" for text, "b" for a binary added with -kb; "" when the header
	// had no such line). A binary stored in any mode but "b" comes back
	// from the repository with keywords expanded, i.e. damaged.
	KeywordSubstitution string `json:"keyword_substitution,omitempty"`
}

// Revision represents a single revision in a file's history.
type Revision struct {
	Number       string    `json:"number"`
	Author       string    `json:"author"`
	Date         time.Time `json:"date"`
	Message      string    `json:"message"`
	Tags         []string  `json:"tags,omitempty"`
	Branches     []string  `json:"branches,omitempty"`
	LinesAdded   int       `json:"lines_added"`
	LinesRemoved int       `json:"lines_removed"`
	PrevNumber   string    `json:"prev_number,omitempty"`
}

var (
	revisionRe = regexp.MustCompile(`^revision\s+(\S+)`)
	dateLineRe = regexp.MustCompile(`^date:\s+(.+?);\s+author:\s+(.+?);\s+state:\s+(.+?);(?:\s+lines:\s+\+(\d+)\s+-(\d+))?`)
	tagLineRe  = regexp.MustCompile(`^\s+(\S+):\s+(\S+)`)
)

// KeywordSubstitutionLine recognizes the `keyword substitution: <mode>`
// header line of `cvs log` and returns the mode. The one home for that
// line's format: both the batched parser (ParseLog) and the TUI's
// streaming parser use it.
func KeywordSubstitutionLine(line string) (mode string, ok bool) {
	const prefix = "keyword substitution:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}

// ParseLog parses the output of `cvs log` into a FileHistory.
func ParseLog(output string) *FileHistory {
	history := &FileHistory{}
	lines := strings.Split(output, "\n")

	// First pass: collect symbolic names (tags)
	tagMap := make(map[string][]string) // revision -> tags
	inSymbolic := false

	for _, line := range lines {
		if strings.HasPrefix(line, "symbolic names:") {
			inSymbolic = true
			continue
		}
		if mode, ok := KeywordSubstitutionLine(line); ok {
			history.KeywordSubstitution = mode
			continue
		}
		if inSymbolic {
			if m := tagLineRe.FindStringSubmatch(line); m != nil {
				tag, rev := m[1], m[2]
				tagMap[rev] = append(tagMap[rev], tag)
			} else {
				inSymbolic = false
			}
		}
	}

	// Second pass: parse revisions
	var current *Revision
	inMessage := false
	separator := "----------------------------"
	endSeparator := "============================================================================="

	for _, line := range lines {
		if line == separator || line == endSeparator {
			if current != nil {
				current.Message = strings.TrimSpace(current.Message)
				current.Tags = tagMap[current.Number]
				history.Revisions = append(history.Revisions, *current)
			}
			current = nil
			inMessage = false
			continue
		}

		if m := revisionRe.FindStringSubmatch(line); m != nil {
			current = &Revision{Number: m[1]}
			inMessage = false
			continue
		}

		if current != nil && !inMessage {
			if m := dateLineRe.FindStringSubmatch(line); m != nil {
				current.Date = parseCVSDate(strings.TrimSpace(m[1]))
				current.Author = m[2]
				if m[4] != "" {
					current.LinesAdded, _ = strconv.Atoi(m[4])
				}
				if m[5] != "" {
					current.LinesRemoved, _ = strconv.Atoi(m[5])
				}
				inMessage = true
				continue
			}
		}

		if inMessage && current != nil {
			if current.Message != "" {
				current.Message += "\n"
			}
			current.Message += line
		}
	}

	// Compute PrevNumber for each revision
	for i := range history.Revisions {
		if i < len(history.Revisions)-1 {
			history.Revisions[i].PrevNumber = history.Revisions[i+1].Number
		}
	}

	return history
}

// parseCVSDate tries common CVS date formats.
// CVS servers output varying formats: "2024/01/05 10:00:00",
// "2024-01-05 10:00:00 +0000", "2024/01/05 10:00:00 +0000", etc.
func parseCVSDate(s string) time.Time {
	formats := []string{
		"2006/01/02 15:04:05",
		"2006-01-02 15:04:05 -0700",
		"2006/01/02 15:04:05 -0700",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t
		}
	}
	// Fallback: try parsing just the date/time portion (first 19 chars)
	if len(s) > 19 {
		for _, f := range formats[:2] {
			if t, err := time.Parse(f, s[:19]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
