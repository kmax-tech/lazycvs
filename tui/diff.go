package tui

import (
	"fmt"
	"github.com/kmax-tech/lazycvs/cvs"
	"strings"
)

// renderUnifiedDiff renders hunks as a unified diff with colored +/- lines.
// Used by HistoryModel for both single-revision diffs and free compares.
func renderUnifiedDiff(hunks []cvs.DiffHunk) string {
	var b strings.Builder
	for _, hunk := range hunks {
		b.WriteString(diffHunk.Render(hunk.Header) + "\n")
		for _, line := range hunk.Lines {
			num := diffLineNum.Render(fmt.Sprintf("%d", line.NewNum))
			if line.NewNum == 0 && line.OldNum > 0 {
				num = diffLineNum.Render(fmt.Sprintf("%d", line.OldNum))
			}
			switch line.Type {
			case "added":
				b.WriteString(num + " " + diffAdd.Render("+ "+line.Content) + "\n")
			case "removed":
				b.WriteString(num + " " + diffDel.Render("- "+line.Content) + "\n")
			default:
				b.WriteString(num + " " + diffContext.Render("  "+line.Content) + "\n")
			}
		}
	}
	return b.String()
}

// renderSideBySideDiff renders a diff as two columns (removed | added).
func renderSideBySideDiff(diff *cvs.DiffResult, width int) string {
	if diff.Pairs == nil {
		diff.Pairs = cvs.BuildSideBySide(diff.Hunks)
	}

	colWidth := (width - 3) / 2 // 3 for " | " separator
	var b strings.Builder

	for _, pair := range diff.Pairs {
		var left, right string
		if pair.Left != nil {
			left = renderSBSLine(pair.Left, colWidth)
		} else {
			left = strings.Repeat(" ", colWidth)
		}
		if pair.Right != nil {
			right = renderSBSLine(pair.Right, colWidth)
		} else {
			right = strings.Repeat(" ", colWidth)
		}
		b.WriteString(left + " | " + right + "\n")
	}
	return b.String()
}

func renderSBSLine(line *cvs.DiffLine, width int) string {
	content := line.Content
	if len(content) > width-4 {
		content = content[:width-7] + "..."
	}
	padded := fmt.Sprintf("%-*s", width, content)

	switch line.Type {
	case "added":
		return diffAdd.Render(padded)
	case "removed":
		return diffDel.Render(padded)
	default:
		return diffContext.Render(padded)
	}
}
