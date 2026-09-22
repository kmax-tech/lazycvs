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
			if line.Type == cvs.DiffLineNote {
				// diff's annotation about the line above it. It has no
				// number because it is not a line of either file.
				b.WriteString(diffLineNum.Render("") + " " +
					diffContext.Render("  "+line.Content) + "\n")
				continue
			}
			num := diffLineNum.Render(fmt.Sprintf("%d", line.NewNum))
			if line.NewNum == 0 && line.OldNum > 0 {
				num = diffLineNum.Render(fmt.Sprintf("%d", line.OldNum))
			}
			switch line.Type {
			case cvs.DiffLineAdded:
				b.WriteString(num + " " + diffAdd.Render("+ "+line.Content) + "\n")
			case cvs.DiffLineRemoved:
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
	// Display cells, not bytes: slicing a string at a byte offset cuts
	// umlauts in half, and width-7 goes negative on a narrow pane.
	padded := padCells(line.Content, width)

	switch line.Type {
	case cvs.DiffLineAdded:
		return diffAdd.Render(padded)
	case cvs.DiffLineRemoved:
		return diffDel.Render(padded)
	default:
		return diffContext.Render(padded)
	}
}
