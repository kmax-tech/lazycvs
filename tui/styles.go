package tui

import "github.com/charmbracelet/lipgloss"

// ANSI colors — respect user's terminal theme
var (
	colorMod      = lipgloss.Color("4") // Blue
	colorConflict = lipgloss.Color("1") // Red
	colorUpdated  = lipgloss.Color("2") // Green
	// Untracked files are the bulk of a typical listing, so they must
	// read as quieter than M/C — but ANSI 8 made them the exact colour
	// of the panel borders and every muted label, which buried the one
	// or two rows that needed attention in a field of identical grey.
	// 244 keeps them subdued while separating them from the chrome, and
	// leaves 239 (ignored) dimmer still: ignored < untracked < normal.
	colorUntracked = lipgloss.Color("244") // Medium gray
	colorStale     = lipgloss.Color("3")   // Yellow
	colorBorder    = lipgloss.Color("8")   // Gray
	colorActive    = lipgloss.Color("4")   // Blue
	colorMuted     = lipgloss.Color("8")   // Gray
	colorIgnored   = lipgloss.Color("239") // Dark gray (256-color)
)

// Status and keys
var (
	statusBadge  = lipgloss.NewStyle().Width(2).Align(lipgloss.Center)
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(colorActive)
	helpStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	titleStyle   = lipgloss.NewStyle().Bold(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	ignoredStyle = lipgloss.NewStyle().Foreground(colorIgnored).Strikethrough(true)
)

// Console
var (
	consoleCmd    = lipgloss.NewStyle().Bold(true)
	consoleOk     = lipgloss.NewStyle().Foreground(colorUpdated)
	consoleErr    = lipgloss.NewStyle().Foreground(colorConflict)
	consoleWarn   = lipgloss.NewStyle().Foreground(colorStale)
	consoleTime   = lipgloss.NewStyle().Foreground(colorMuted)
	consoleStatus = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
)

// Diff
var (
	diffAdd     = lipgloss.NewStyle().Foreground(colorUpdated)
	diffDel     = lipgloss.NewStyle().Foreground(colorConflict)
	diffContext = lipgloss.NewStyle().Foreground(colorMuted)
	diffHunk    = lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	diffLineNum = lipgloss.NewStyle().Foreground(colorMuted).Width(5).Align(lipgloss.Right)
)

func statusColor(status string) lipgloss.TerminalColor {
	switch status {
	case "M":
		return colorMod
	case "C":
		return colorConflict
	case "U", "P":
		return colorUpdated
	case "?":
		return colorUntracked
	case "!":
		return colorStale
	default:
		return lipgloss.NoColor{}
	}
}
