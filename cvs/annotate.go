package cvs

import (
	"regexp"
	"strings"
)

// AnnotateLine represents a single line from cvs annotate output.
type AnnotateLine struct {
	Revision string `json:"revision"`
	Author   string `json:"author"`
	Date     string `json:"date"`
	Content  string `json:"content"`
	LineNum  int    `json:"line_num"`
}

var annotateRe = regexp.MustCompile(`^(\S+)\s+\((\S+)\s+(\S+)\):\s?(.*)$`)

// ParseAnnotate parses the output of `cvs annotate` into annotated lines.
func ParseAnnotate(output string) []AnnotateLine {
	var result []AnnotateLine
	lineNum := 0

	for _, line := range strings.Split(output, "\n") {
		if line == "" && lineNum == 0 {
			continue
		}
		lineNum++

		if m := annotateRe.FindStringSubmatch(line); m != nil {
			result = append(result, AnnotateLine{
				Revision: m[1],
				Author:   m[2],
				Date:     m[3],
				Content:  m[4],
				LineNum:  lineNum,
			})
		} else {
			// Lines that don't match the pattern (shouldn't happen normally)
			result = append(result, AnnotateLine{
				Content: line,
				LineNum: lineNum,
			})
		}
	}

	return result
}
