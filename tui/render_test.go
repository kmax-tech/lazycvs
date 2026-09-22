package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Columns are measured in display cells. fmt's %-*s pads by bytes, so
// every umlaut in a file name used to shift the size column one cell
// left — routine in a repo whose expected encoding is Latin-1.
func TestPadCellsCountsCellsNotBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
	}{
		{"ascii", "readme.txt", 20},
		{"umlauts", "Übersicht-Grün.txt", 20},
		{"sharp s", "Straßenbahn.md", 20},
		{"exactly full", "abcde", 5},
		{"needs truncation", "a-very-long-file-name.txt", 10},
		{"truncated with umlauts", "Ümlaut-Datei-mit-langem-Namen.txt", 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := padCells(c.in, c.w)
			if w := lipgloss.Width(got); w != c.w {
				t.Errorf("padCells(%q, %d) is %d cells wide: %q", c.in, c.w, w, got)
			}
		})
	}
}

// Two rows of the same width must line up whatever their names contain
// — the actual symptom: a size column that stepped left on every file
// with an umlaut.
func TestFileRowsAlignAcrossEncodings(t *testing.T) {
	const w = 24
	ascii := padCells("notes.txt", w) + " 1.3 KB"
	umlaut := padCells("Änderungen.txt", w) + " 1.3 KB"

	if lipgloss.Width(ascii) != lipgloss.Width(umlaut) {
		t.Errorf("rows differ in width: %d vs %d\n  %q\n  %q",
			lipgloss.Width(ascii), lipgloss.Width(umlaut), ascii, umlaut)
	}
}

// The selection bar spans the pane and carries no colour blocks: the
// tree used to reverse only as much as the text was long, and reversing
// a coloured count badge painted it as a solid block.
func TestCursorBarSpansPaneWithoutColourBlocks(t *testing.T) {
	colored := lipgloss.NewStyle().Foreground(colorMod).Render("1M") + " file.txt"
	bar := cursorBar(colored, 30)

	if w := lipgloss.Width(bar); w != 30 {
		t.Errorf("bar is %d cells wide, want 30: %q", w, ansi.Strip(bar))
	}
	if n := strings.Count(bar, "\x1b["); n > 2 {
		t.Errorf("bar carries %d escape sequences, want one reverse pair: %q", n, bar)
	}
	if !strings.Contains(ansi.Strip(bar), "1M file.txt") {
		t.Errorf("bar lost its text: %q", ansi.Strip(bar))
	}
}

// Untracked rows are the bulk of a listing; if they share the border's
// colour, the M and C rows that need attention disappear into them.
func TestUntrackedIsDistinctFromChrome(t *testing.T) {
	if colorUntracked == colorBorder || colorUntracked == colorMuted {
		t.Errorf("untracked (%v) must differ from border (%v) and muted (%v)",
			colorUntracked, colorBorder, colorMuted)
	}
	if colorUntracked == colorIgnored {
		t.Errorf("untracked (%v) must stay brighter than ignored (%v)",
			colorUntracked, colorIgnored)
	}
}
